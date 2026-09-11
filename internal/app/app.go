// Package app classifies worktrees and executes conservative cleanup plans.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ben-ranford/wtgc/internal/cache"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/provider"
)

const schemaVersion = "1.1.0"

// Git is the safety boundary around repository inspection and mutation.
type Git interface {
	Discover(context.Context, []string) ([]model.Repository, []error)
	List(context.Context, model.Repository) ([]model.RegisteredWorktree, error)
	DefaultBranch(context.Context, model.Repository) (string, error)
	IsClean(context.Context, string) (bool, error)
	IsAncestor(context.Context, model.Repository, string, string) (bool, error)
	RemoteContains(context.Context, model.Repository, string) (bool, error)
	DiskUsage(string) (int64, error)
	Remove(context.Context, model.Repository, string) error
	Prune(context.Context, model.Repository) error
	DeleteBranch(context.Context, model.Repository, string, string) error
	ProviderUpstream(context.Context, model.Repository, string) (remote, branch, remoteURL string, err error)
	ProviderDefaultTracking(context.Context, model.Repository, string, string) (remote, branch, remoteURL string, err error)
}

// Options controls one scan and optional cleanup pass.
type Options struct {
	Roots            []string
	SelectedPaths    []string
	ConfirmSelection func(SelectionPreview) bool
	selection        *selectionIdentity
	Execute          bool
	Interactive      bool
	DeleteBranch     bool
	Confirm          func(model.Worktree) bool
	ProtectedPath    string
	Now              func() time.Time
	Retention        time.Duration
	CacheThreshold   int64
	Provider         provider.MergeFinder
	ProviderRemote   string
	CacheScanner     func(context.Context, string, int64) []cache.Warning
	// ExplainEvidence requests classifier trace data for wtgc explain only.
	// Normal inventory/review scans retain their allocation and output contract.
	ExplainEvidence bool
}

// App coordinates Git inspection without weakening Git's own safety checks.
type App struct {
	git Git
}

// New constructs an application with an explicit Git boundary.
func New(git Git) *App { return &App{git: git} }

// Run builds an inventory and, when explicitly requested, executes safe actions.
func (a *App) Run(ctx context.Context, opts Options) (model.Inventory, error) {
	opts.Roots = slices.Clone(opts.Roots)
	opts.SelectedPaths = slices.Clone(opts.SelectedPaths)
	started := time.Now()
	if opts.Now == nil {
		opts.Now = time.Now
	}
	now := opts.Now().UTC()
	opts.Now = func() time.Time { return now }
	inv := model.Inventory{
		SchemaVersion: schemaVersion,
		GeneratedAt:   now,
		DryRun:        !opts.Execute,
		Roots:         append([]string(nil), opts.Roots...),
		Worktrees:     []model.Worktree{},
	}

	repositories, discoveryErrors := a.git.Discover(ctx, opts.Roots)
	for _, err := range discoveryErrors {
		inv.Errors = append(inv.Errors, err.Error())
	}
	inv.Summary.Repositories = len(repositories)
	if len(repositories) == 0 {
		inv.Summary.Duration = time.Since(started)
		return inv, errors.New("no Git repositories with registered worktrees found")
	}

	a.scanRepositories(ctx, repositories, scanOptions{protectedPath: opts.ProtectedPath, provider: opts.Provider, providerRemote: opts.ProviderRemote, now: now, explainEvidence: opts.ExplainEvidence}, &inv)
	a.addCacheWarnings(ctx, opts.CacheThreshold, opts.CacheScanner, &inv)
	a.applyRetention(now, opts.Retention, &inv)
	sort.Slice(inv.Worktrees, func(i, j int) bool {
		if inv.Worktrees[i].Repository == inv.Worktrees[j].Repository {
			return inv.Worktrees[i].Path < inv.Worktrees[j].Path
		}
		return inv.Worktrees[i].Repository < inv.Worktrees[j].Repository
	})
	a.summarize(&inv)

	if len(opts.SelectedPaths) > 0 {
		a.cleanSelection(ctx, repositories, opts, &inv)
		a.summarize(&inv)
	} else if opts.Execute {
		for _, repo := range repositories {
			a.cleanRepository(ctx, repo, opts, &inv)
		}
		a.summarize(&inv)
	}
	inv.Summary.Duration = time.Since(started)
	if len(inv.Errors) > 0 {
		return inv, fmt.Errorf("completed with %d error(s)", len(inv.Errors))
	}
	return inv, nil
}

func (a *App) addCacheWarnings(ctx context.Context, threshold int64, scanner func(context.Context, string, int64) []cache.Warning, inv *model.Inventory) {
	if scanner == nil {
		scanner = cache.Scan
	}
	for i := range inv.Worktrees {
		item := &inv.Worktrees[i]
		if item.Prunable || item.Path == "" {
			continue
		}
		for _, warning := range scanner(ctx, item.Path, threshold) {
			details := item.Details()
			details.CacheWarnings = append(details.CacheWarnings, model.CacheWarning{Path: warning.Path, Bytes: warning.Bytes, Kind: warning.Kind, Reason: warning.Reason, Error: warning.Error})
		}
	}
}

func (a *App) applyRetention(now time.Time, retention time.Duration, inv *model.Inventory) {
	if retention == 0 {
		return
	}
	for i := range inv.Worktrees {
		item := &inv.Worktrees[i]
		if item.Classification != model.SafeToRemove {
			continue
		}
		observed, basis, err := retentionObservedAt(item)
		if err != nil || observed.IsZero() || observed.After(now) {
			item.Classification, item.Reason = model.Kept, "kept because retention timestamp could not be proven"
			if err != nil {
				item.Error = fmt.Sprintf("retention timestamp: %v", err)
			}
			continue
		}
		eligible := observed.Add(retention)
		details := item.Details()
		details.RetentionBasis, details.ObservedAt, details.EligibleAt = basis, &observed, &eligible
		details.Remaining = eligible.Sub(now)
		if now.Before(eligible) {
			item.Classification, item.Reason = model.Kept, "kept until retention window elapses"
		}
	}
}

func retentionObservedAt(item *model.Worktree) (time.Time, string, error) {
	if item.WorktreeDetails != nil && item.MergedAt != nil {
		return item.MergedAt.UTC(), "provider_merged_at", nil
	}
	value, err := latestModTime(item.Path)
	return value, "worktree_mtime", err
}

func latestModTime(root string) (time.Time, error) {
	var latest time.Time
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.ModTime().After(latest) {
			latest = info.ModTime().UTC()
		}
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return latest, nil
}

type classificationJob struct {
	repo          model.Repository
	defaultBranch string
	protectedPath string
	record        model.RegisteredWorktree
}

type classifyOptions struct {
	provider        provider.MergeFinder
	providerRemote  string
	now             time.Time
	explainEvidence bool
}

type scanOptions struct {
	protectedPath   string
	provider        provider.MergeFinder
	providerRemote  string
	now             time.Time
	explainEvidence bool
}

func (a *App) scanRepositories(ctx context.Context, repositories []model.Repository, options scanOptions, inv *model.Inventory) {
	jobs := a.collectClassificationJobs(ctx, repositories, options.protectedPath, inv)
	if len(jobs) == 0 {
		return
	}
	a.classifyJobs(ctx, jobs, options.provider, options.providerRemote, options.now, options.explainEvidence, inv)
}

func (a *App) collectClassificationJobs(ctx context.Context, repositories []model.Repository, protectedPath string, inv *model.Inventory) []classificationJob {
	var jobs []classificationJob
	for _, repo := range repositories {
		records, err := a.git.List(ctx, repo)
		if err != nil {
			inv.Errors = append(inv.Errors, fmt.Sprintf("%s: list worktrees: %v", repo.CommonDir, err))
			continue
		}
		defaultBranch, err := a.git.DefaultBranch(ctx, repo)
		if err != nil {
			inv.Errors = append(inv.Errors, fmt.Sprintf("%s: default branch: %v", repo.CommonDir, err))
			for _, record := range records {
				item := a.classifyDefaultBranchError(ctx, repo, record, fmt.Sprintf("default branch: %v", err))
				inv.Worktrees = append(inv.Worktrees, item)
				if item.Error != "" {
					inv.Errors = append(inv.Errors, fmt.Sprintf("%s: %s: %s", repo.PrimaryPath, item.Path, item.Error))
				}
			}
			continue
		}
		for _, record := range records {
			jobs = append(jobs, classificationJob{
				repo:          repo,
				defaultBranch: defaultBranch,
				protectedPath: protectedPath,
				record:        record,
			})
		}
	}
	return jobs
}

func (a *App) classifyJobs(ctx context.Context, jobs []classificationJob, p provider.MergeFinder, providerRemote string, now time.Time, explainEvidence bool, inv *model.Inventory) {
	inv.Worktrees = slices.Grow(inv.Worktrees, len(jobs))
	jobCh := make(chan classificationJob)
	resultCh := make(chan model.Worktree, len(jobs))
	workerCount := 8
	if len(jobs) < workerCount {
		workerCount = len(jobs)
	}
	var group sync.WaitGroup
	for range workerCount {
		group.Add(1)
		go func() {
			defer group.Done()
			for job := range jobCh {
				resultCh <- a.classify(ctx, job, classifyOptions{provider: p, providerRemote: providerRemote, now: now, explainEvidence: explainEvidence})
			}
		}()
	}
	for _, job := range jobs {
		jobCh <- job
	}
	close(jobCh)
	group.Wait()
	close(resultCh)

	for item := range resultCh {
		inv.Worktrees = append(inv.Worktrees, item)
		if item.Error != "" {
			inv.Errors = append(inv.Errors, fmt.Sprintf("%s: %s: %s", item.Repository, item.Path, item.Error))
		}
	}
}

func (a *App) classify(ctx context.Context, job classificationJob, options classifyOptions) model.Worktree {
	repo, defaultBranch, protectedPath, record := job.repo, job.defaultBranch, job.protectedPath, job.record
	item := model.Worktree{
		Path:          record.Path,
		Branch:        record.Branch,
		Head:          record.Head,
		Repository:    repo.PrimaryPath,
		DefaultBranch: defaultBranch,
		Primary:       record.Primary,
		Detached:      record.Detached,
		Locked:        record.Locked,
		Prunable:      record.Prunable,
	}
	if record.Prunable {
		item.Classification, item.Reason = model.Prunable, "worktree path is missing and Git marks its metadata prunable"
		traceCheck(&item, options, "registration", model.ExplainBlocked, "Git marks the missing registration prunable")
		return item
	}
	traceCheck(&item, options, "registration", model.ExplainPassed, "Git registered a live worktree")
	if size, err := a.git.DiskUsage(record.Path); err == nil {
		item.DiskBytes = size
		traceCheck(&item, options, "disk_usage", model.ExplainPassed, "disk usage was measured")
	} else {
		traceCheck(&item, options, "disk_usage", model.ExplainUnavailable, fmt.Sprintf("measure disk usage: %v", err))
		return classificationError(item, fmt.Sprintf("measure disk usage: %v", err))
	}
	clean, err := a.git.IsClean(ctx, record.Path)
	if err != nil {
		traceCheck(&item, options, "working_tree", model.ExplainUnavailable, fmt.Sprintf("inspect working tree: %v", err))
		return classificationError(item, fmt.Sprintf("inspect working tree: %v", err))
	}
	item.Dirty = boolPtr(!clean)
	if clean {
		traceCheck(&item, options, "working_tree", model.ExplainPassed, "working tree is clean")
	} else {
		traceCheck(&item, options, "working_tree", model.ExplainBlocked, "tracked, staged, or untracked changes exist")
	}
	if classified, kept := keepProtectedWorktree(item, record, defaultBranch, protectedPath); kept {
		traceCheck(&classified, options, "protection", model.ExplainBlocked, classified.Reason)
		return classified
	}
	traceCheck(&item, options, "protection", model.ExplainPassed, "no protected-worktree condition was observed")
	merged, err := a.git.IsAncestor(ctx, repo, record.Head, defaultBranch)
	if err != nil {
		traceCheck(&item, options, "local_default_reachability", model.ExplainUnavailable, fmt.Sprintf("check merge ancestry: %v", err))
		return classificationError(item, fmt.Sprintf("check merge ancestry: %v", err))
	}
	if !merged {
		traceCheck(&item, options, "local_default_reachability", model.ExplainBlocked, "branch tip is not reachable from the local default branch")
		return a.classifyUnmerged(ctx, item, repo, record, defaultBranch, clean, options)
	}
	traceCheck(&item, options, "local_default_reachability", model.ExplainPassed, "branch tip is reachable from the local default branch")
	if !clean {
		item.Classification, item.Reason = model.MergedButDirty, "branch is merged but tracked, staged, or untracked changes exist"
		return item
	}
	remote, err := a.git.RemoteContains(ctx, repo, record.Head)
	if err != nil {
		traceCheck(&item, options, "remote_tracking_reachability", model.ExplainUnavailable, fmt.Sprintf("check remote reachability: %v", err))
		return classificationError(item, fmt.Sprintf("check remote reachability: %v", err))
	}
	if !remote {
		traceCheck(&item, options, "remote_tracking_reachability", model.ExplainBlocked, "branch tip is not reachable from local remote-tracking refs")
		item.Classification, item.Reason = model.Unmerged, "branch tip is not reachable from any local remote-tracking ref"
		return item
	}
	traceCheck(&item, options, "remote_tracking_reachability", model.ExplainPassed, "branch tip is reachable from local remote-tracking refs")
	item.Classification, item.Reason = model.SafeToRemove, "clean branch tip is reachable from both the default branch and a remote-tracking ref"
	return item
}

func (a *App) classifyUnmerged(ctx context.Context, item model.Worktree, repo model.Repository, record model.RegisteredWorktree, defaultBranch string, clean bool, options classifyOptions) model.Worktree {
	if !clean {
		item.Classification, item.Reason = model.Kept, "dirty worktree branch tip is not reachable from the local default branch"
		return item
	}
	if options.provider == nil {
		item.Classification, item.Reason = model.Unmerged, "branch tip is not reachable from the local default branch"
		return item
	}
	proof, ok, category := a.providerProof(ctx, repo, record, defaultBranch, options.provider, options.providerRemote, options.now)
	if !ok {
		traceCheck(&item, options, "provider_proof", model.ExplainBlocked, "provider confirmation "+category)
		item.Classification, item.Reason = model.Unmerged, "provider confirmation "+category+"; clean worktree retained because branch tip is not reachable from the local default branch"
		return item
	}
	traceCheck(&item, options, "provider_proof", model.ExplainPassed, "explicit provider merge proof was accepted")
	item.Classification, item.Reason = model.SafeToRemove, "clean squash-merged pull request has exact provider and selected-default reachability proof"
	details := item.Details()
	details.Provider, details.ProviderPR, details.ProviderURL, details.MergedAt = "github", proof.Number, proof.URL, &proof.MergedAt
	details.ProviderProof = providerProofIdentity(proof)
	return item
}

func traceCheck(item *model.Worktree, options classifyOptions, id string, status model.ExplainCheckStatus, detail string) {
	if !options.explainEvidence {
		return
	}
	details := item.Details()
	details.ExplainChecks = append(details.ExplainChecks, model.ExplainCheck{ID: id, Status: status, Detail: detail})
}

func providerProofIdentity(proof provider.PullRequest) model.ProviderProof {
	return model.ProviderProof{Kind: "github", HeadRemote: proof.HeadRemote, BaseRemote: proof.BaseRemote, Number: proof.Number, MergedAt: proof.MergedAt, MergeCommitSHA: proof.MergeCommitSHA, HeadSHA: proof.HeadSHA, HeadOwner: proof.HeadOwner, HeadRepo: proof.HeadRepo, HeadRef: proof.HeadRef, BaseOwner: proof.BaseOwner, BaseRepo: proof.BaseRepo, BaseRef: proof.BaseRef}
}

func (a *App) providerProof(ctx context.Context, repo model.Repository, record model.RegisteredWorktree, defaultBranch string, p provider.MergeFinder, selectedRemote string, now time.Time) (provider.PullRequest, bool, string) {
	headRemote, headRef, headURL, err := a.git.ProviderUpstream(ctx, repo, record.Branch)
	if err != nil {
		return provider.PullRequest{}, false, "mapping unavailable or ambiguous"
	}
	trackingRemote, baseRef, baseURL, err := a.git.ProviderDefaultTracking(ctx, repo, defaultBranch, selectedRemote)
	if err != nil {
		return provider.PullRequest{}, false, "mapping unavailable or ambiguous"
	}
	ho, hr, ok := githubRepo(headURL)
	if !ok || headRef == "" {
		return provider.PullRequest{}, false, "mapping identity is invalid"
	}
	bo, br, ok := githubRepo(baseURL)
	if !ok || baseRef != defaultBranch {
		return provider.PullRequest{}, false, "mapping identity is invalid"
	}
	proof, err := p.FindMerged(ctx, provider.Query{HeadOwner: ho, HeadRepo: hr, HeadRef: headRef, HeadSHA: record.Head, BaseOwner: bo, BaseRepo: br, BaseRef: baseRef})
	if err != nil {
		return provider.PullRequest{}, false, providerFailureCategory(err)
	}
	if proof.Number <= 0 || proof.MergedAt.IsZero() || proof.MergedAt.After(now) || proof.HeadSHA != record.Head || !sameGitHubRepository(proof.HeadOwner, proof.HeadRepo, ho, hr) || proof.HeadRef != headRef || !sameGitHubRepository(proof.BaseOwner, proof.BaseRepo, bo, br) || proof.BaseRef != baseRef || !fullOID(proof.MergeCommitSHA) {
		return provider.PullRequest{}, false, "proof identity is invalid"
	}
	proof.HeadRemote, proof.BaseRemote = headRemote, trackingRemote
	local, err := a.git.IsAncestor(ctx, repo, proof.MergeCommitSHA, defaultBranch)
	if err != nil || !local {
		return provider.PullRequest{}, false, "local default reachability failed"
	}
	remote, err := a.git.IsAncestor(ctx, repo, proof.MergeCommitSHA, "refs/remotes/"+trackingRemote+"/"+defaultBranch)
	if err != nil || !remote {
		return provider.PullRequest{}, false, "selected default reachability failed"
	}
	return proof, true, ""
}
func providerFailureCategory(err error) string {
	value := strings.ToLower(err.Error())
	switch {
	case strings.Contains(value, "http 401") || strings.Contains(value, "http 403"):
		return "authentication failed"
	case strings.Contains(value, "http 429"):
		return "rate limit failed"
	case strings.Contains(value, "deadline") || strings.Contains(value, "canceled") || strings.Contains(value, "query failed"):
		return "timeout, cancellation, or offline failure"
	case strings.Contains(value, "exceeds size") || strings.Contains(value, "decode"):
		return "malformed or oversized response"
	case strings.Contains(value, "no exact"):
		return "no exact merged pull request"
	default:
		return "provider response rejected"
	}
}
func fullOID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, r := range value {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f' || r >= 'A' && r <= 'F') {
			return false
		}
	}
	return true
}
func githubRepo(raw string) (string, string, bool) {
	raw = strings.TrimSuffix(raw, ".git")
	if strings.HasPrefix(raw, "git@github.com:") {
		raw = "https://github.com/" + strings.TrimPrefix(raw, "git@github.com:")
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Host, "github.com") {
		return "", "", false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func keepProtectedWorktree(item model.Worktree, record model.RegisteredWorktree, defaultBranch, protectedPath string) (model.Worktree, bool) {
	if record.Primary || record.Bare {
		item.Classification, item.Reason = model.Kept, "primary or bare worktree is never removable"
		return item, true
	}
	if protectedPath != "" && pathsOverlap(record.Path, protectedPath) {
		item.Classification, item.Reason = model.Kept, "worktree containing the running process is never removable"
		return item, true
	}
	if record.Locked {
		item.Classification, item.Reason = model.Kept, "worktree is locked"
		return item, true
	}
	if record.Detached || record.Branch == "" {
		item.Classification, item.Reason = model.Kept, "detached worktree has no local branch to prove merged"
		return item, true
	}
	if record.Branch == defaultBranch {
		item.Classification, item.Reason = model.Kept, "default-branch worktree is never removable"
		return item, true
	}
	return item, false
}

func (a *App) classifyDefaultBranchError(ctx context.Context, repo model.Repository, record model.RegisteredWorktree, message string) model.Worktree {
	item := model.Worktree{
		Path:           record.Path,
		Branch:         record.Branch,
		Head:           record.Head,
		Repository:     repo.PrimaryPath,
		Primary:        record.Primary,
		Detached:       record.Detached,
		Locked:         record.Locked,
		Prunable:       record.Prunable,
		Action:         model.ActionKept,
		ReclaimedBytes: 0,
		Error:          message,
	}
	if record.Prunable {
		item.Classification, item.Reason = model.Prunable, "worktree path is missing and Git marks its metadata prunable; repository kept because default branch could not be resolved"
		return item
	}
	if size, err := a.git.DiskUsage(record.Path); err == nil {
		item.DiskBytes = size
	} else {
		item.Error = strings.TrimSpace(item.Error + "; " + fmt.Sprintf("measure disk usage: %v", err))
	}
	if clean, err := a.git.IsClean(ctx, record.Path); err == nil {
		item.Dirty = boolPtr(!clean)
	} else {
		item.Error = strings.TrimSpace(item.Error + "; " + fmt.Sprintf("inspect working tree: %v", err))
	}
	item.Classification, item.Reason = model.Kept, "kept because default branch could not be resolved"
	return item
}

func classificationError(item model.Worktree, message string) model.Worktree {
	item.Classification = model.Error
	item.Reason = "kept because safety could not be proven"
	item.Error = message
	return item
}

func (a *App) cleanRepository(ctx context.Context, repo model.Repository, opts Options, inv *model.Inventory) {
	if repoDefaultBranchFailed(inv, repo) {
		return
	}
	var acceptedPrunable []int
	pruneBlocked := false
	for i := range inv.Worktrees {
		item := &inv.Worktrees[i]
		if item.Repository != repo.PrimaryPath || (item.Classification != model.SafeToRemove && item.Classification != model.Prunable) {
			continue
		}
		if opts.Interactive && (opts.Confirm == nil || !opts.Confirm(*item)) {
			item.Classification = model.Kept
			item.Reason = "kept by interactive choice"
			item.Action = model.ActionKept
			if item.Prunable {
				pruneBlocked = true
			}
			continue
		}
		if item.Classification == model.Prunable {
			acceptedPrunable = append(acceptedPrunable, i)
			continue
		}

		a.removeWorktree(ctx, repo, opts, item, inv)
	}
	a.pruneAcceptedWorktrees(ctx, repo, inv, acceptedPrunable, pruneBlocked)
}

func (a *App) removeWorktree(ctx context.Context, repo model.Repository, opts Options, item *model.Worktree, inv *model.Inventory) {
	fresh, ok := a.revalidate(ctx, repo, opts, *item)
	if !ok {
		*item = fresh
		return
	}
	if opts.selection != nil && ctx.Err() != nil {
		item.Action, item.Reason = model.ActionKept, "selected cleanup canceled before removal"
		return
	}
	if err := a.git.Remove(ctx, repo, fresh.Path); err != nil {
		item.Classification = model.Error
		item.Reason = "remove failed; worktree kept"
		if opts.selection != nil {
			item.Reason = "selected removal failed; completion could not be confirmed"
		}
		item.Error = err.Error()
		item.Action = model.ActionKept
		inv.Errors = append(inv.Errors, fmt.Sprintf("%s: remove %s: %v", repo.PrimaryPath, fresh.Path, err))
		return
	}
	item.Removed = true
	item.ReclaimedBytes = item.DiskBytes
	item.Action = model.ActionRemoved
	if opts.DeleteBranch && opts.selection == nil {
		a.deleteWorktreeBranch(ctx, repo, fresh, item, inv, false)
	}
}

func (a *App) deleteWorktreeBranch(ctx context.Context, repo model.Repository, fresh model.Worktree, item *model.Worktree, inv *model.Inventory, selected bool) {
	if fresh.WorktreeDetails != nil && fresh.ProviderProof.HeadSHA != "" {
		item.Error = "worktree removed; branch retained because provider squash proof does not authorize branch deletion"
		return
	}
	var err error
	if selected {
		err = a.requireUnusedSelectedBranch(ctx, repo, fresh.Branch)
	}
	if err == nil {
		err = a.git.DeleteBranch(ctx, repo, fresh.Branch, fresh.DefaultBranch)
	}
	if err != nil {
		item.Error = fmt.Sprintf("worktree removed; branch retained: %v", err)
		inv.Errors = append(inv.Errors, fmt.Sprintf("%s: delete branch %s: %v", repo.PrimaryPath, fresh.Branch, err))
		return
	}
	item.BranchDeleted = true
	item.Action = model.ActionRemovedBranchDeleted
}

func (a *App) pruneAcceptedWorktrees(ctx context.Context, repo model.Repository, inv *model.Inventory, acceptedPrunable []int, pruneBlocked bool) {
	if pruneBlocked || len(acceptedPrunable) == 0 {
		return
	}
	if err := a.requireAcceptedPrunableSet(ctx, repo, inv, acceptedPrunable); err != nil {
		inv.Errors = append(inv.Errors, fmt.Sprintf("%s: prune blocked: %v", repo.PrimaryPath, err))
		for _, index := range acceptedPrunable {
			inv.Worktrees[index].Classification = model.Error
			inv.Worktrees[index].Reason = "revalidation blocked prune because the prunable set changed"
			inv.Worktrees[index].Error = err.Error()
			inv.Worktrees[index].Action = model.ActionKept
		}
		return
	}
	if err := a.git.Prune(ctx, repo); err != nil {
		inv.Errors = append(inv.Errors, fmt.Sprintf("%s: prune: %v", repo.PrimaryPath, err))
		for _, index := range acceptedPrunable {
			inv.Worktrees[index].Error = err.Error()
			inv.Worktrees[index].Action = model.ActionKept
		}
		return
	}
	for _, index := range acceptedPrunable {
		inv.Worktrees[index].Removed = true
		inv.Worktrees[index].Action = model.ActionPruned
	}
}

func (a *App) requireAcceptedPrunableSet(ctx context.Context, repo model.Repository, inv *model.Inventory, accepted []int) error {
	records, err := a.git.List(ctx, repo)
	if err != nil {
		return err
	}
	want := make([]string, 0, len(accepted))
	for _, index := range accepted {
		want = append(want, canonicalPath(inv.Worktrees[index].Path))
	}
	got := make([]string, 0)
	for _, record := range records {
		if record.Prunable {
			got = append(got, canonicalPath(record.Path))
		}
	}
	sort.Strings(want)
	sort.Strings(got)
	if !equalStrings(want, got) {
		return fmt.Errorf("accepted prunable set %v changed to %v", want, got)
	}
	return nil
}

func (a *App) revalidate(ctx context.Context, repo model.Repository, opts Options, previous model.Worktree) (model.Worktree, bool) {
	if opts.selection != nil {
		if err := a.requireSelectedRepository(ctx, repo, previous.Path, *opts.selection); err != nil {
			return classificationError(previous, err.Error()), false
		}
	}
	defaultBranch, err := a.git.DefaultBranch(ctx, repo)
	if err != nil {
		return classificationError(previous, fmt.Sprintf("revalidate default branch: %v", err)), false
	}
	records, err := a.git.List(ctx, repo)
	if err != nil {
		return classificationError(previous, fmt.Sprintf("revalidate worktree list: %v", err)), false
	}
	if opts.selection != nil {
		if err := requireSelectedRecord(records, previous.Path, *opts.selection); err != nil {
			return classificationError(previous, err.Error()), false
		}
	}
	record, found := registeredRecord(records, previous.Path)
	if !found {
		previous.Classification, previous.Reason = model.Kept, "worktree registration changed after scan"
		return previous, false
	}
	if record.Head != previous.Head || record.Branch != previous.Branch {
		previous.Classification, previous.Reason = model.Kept, "worktree HEAD or branch changed after scan"
		return previous, false
	}
	fresh := a.classify(ctx, classificationJob{repo: repo, defaultBranch: defaultBranch, protectedPath: opts.ProtectedPath, record: record}, classifyOptions{provider: opts.Provider, providerRemote: opts.ProviderRemote, now: opts.Now().UTC()})
	return revalidationDecision(previous, fresh, opts.Now().UTC())
}

func registeredRecord(records []model.RegisteredWorktree, path string) (model.RegisteredWorktree, bool) {
	for _, record := range records {
		if record.Path == path {
			return record, true
		}
	}
	return model.RegisteredWorktree{}, false
}

func revalidationDecision(previous, fresh model.Worktree, now time.Time) (model.Worktree, bool) {
	if fresh.DefaultBranch != previous.DefaultBranch {
		fresh.Classification, fresh.Reason = model.Kept, "revalidation blocked removal: default branch changed after scan"
		return fresh, false
	}
	if providerProofChanged(previous, fresh) {
		fresh.Classification, fresh.Reason = model.Kept, "revalidation blocked removal: provider proof changed after scan"
		return fresh, false
	}
	if reason := retentionRevalidationFailure(previous, fresh, now); reason != "" {
		fresh.Classification, fresh.Reason = model.Kept, reason
		return fresh, false
	}
	if fresh.Classification != model.SafeToRemove {
		fresh.Reason = "revalidation blocked removal: " + fresh.Reason
		return fresh, false
	}
	return fresh, true
}

func providerProofChanged(previous, fresh model.Worktree) bool {
	old := previous.WorktreeDetails != nil && previous.ProviderProof.HeadSHA != ""
	current := fresh.WorktreeDetails != nil && fresh.ProviderProof.HeadSHA != ""
	return old != current || (old && !sameProviderProof(previous.ProviderProof, fresh.ProviderProof))
}

func sameProviderProof(previous, fresh model.ProviderProof) bool {
	return previous.Kind == fresh.Kind &&
		previous.HeadRemote == fresh.HeadRemote && previous.BaseRemote == fresh.BaseRemote &&
		previous.Number == fresh.Number && previous.MergedAt == fresh.MergedAt &&
		previous.MergeCommitSHA == fresh.MergeCommitSHA && previous.HeadSHA == fresh.HeadSHA &&
		sameGitHubRepository(previous.HeadOwner, previous.HeadRepo, fresh.HeadOwner, fresh.HeadRepo) && previous.HeadRef == fresh.HeadRef &&
		sameGitHubRepository(previous.BaseOwner, previous.BaseRepo, fresh.BaseOwner, fresh.BaseRepo) && previous.BaseRef == fresh.BaseRef
}

// GitHub canonicalizes repository owner and name casing, but refs and object
// IDs remain exact proof components.
func sameGitHubRepository(owner, repo, wantOwner, wantRepo string) bool {
	return strings.EqualFold(owner, wantOwner) && strings.EqualFold(repo, wantRepo)
}

func retentionRevalidationFailure(previous, fresh model.Worktree, now time.Time) string {
	if previous.WorktreeDetails == nil || previous.RetentionBasis == "" {
		return ""
	}
	if previous.ObservedAt == nil || previous.EligibleAt == nil {
		return "revalidation blocked removal: retention evidence is incomplete"
	}
	observed, basis, err := retentionObservedAt(&fresh)
	if err != nil || basis != previous.RetentionBasis || !observed.Equal(*previous.ObservedAt) || observed.IsZero() || observed.After(now) || now.Before(observed.Add(previous.EligibleAt.Sub(*previous.ObservedAt))) {
		return "revalidation blocked removal: retention window has not elapsed or timestamp changed"
	}
	return ""
}

func repoDefaultBranchFailed(inv *model.Inventory, repo model.Repository) bool {
	for _, item := range inv.Worktrees {
		if item.Repository == repo.PrimaryPath && strings.Contains(item.Error, "default branch:") {
			return true
		}
	}
	return false
}

func boolPtr(value bool) *bool {
	return &value
}

func pathsOverlap(worktree, protected string) bool {
	worktree = canonicalPath(worktree)
	protected = canonicalPath(protected)
	if worktree == "" || protected == "" {
		return false
	}
	if worktree == protected {
		return true
	}
	rel, err := filepath.Rel(worktree, protected)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func canonicalPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(abs)
}

func (a *App) summarize(inv *model.Inventory) {
	duration := inv.Summary.Duration
	inv.Summary = model.Summary{Repositories: inv.Summary.Repositories, Duration: duration}
	for i := range inv.Worktrees {
		finalizeAction(&inv.Worktrees[i])
		item := inv.Worktrees[i]
		inv.Summary.Scanned++
		if item.Classification == model.SafeToRemove {
			inv.Summary.Safe++
			if item.Action != model.ActionKept {
				inv.Summary.PotentialBytes += item.DiskBytes
			}
		}
		if item.Removed {
			inv.Summary.Removed++
			if item.Prunable {
				inv.Summary.Pruned++
			}
		} else if item.Classification != model.SafeToRemove {
			inv.Summary.Skipped++
		}
		inv.Summary.ReclaimedBytes += item.ReclaimedBytes
		if item.WorktreeDetails != nil {
			for _, warning := range item.CacheWarnings {
				inv.Summary.CacheWarningCount++
				inv.Summary.CacheWarningBytes += warning.Bytes
			}
		}
	}
	inv.Errors = uniqueStrings(inv.Errors)
}

func finalizeAction(item *model.Worktree) {
	switch {
	case item.Action != "" && item.Action != model.ActionWouldRemove && item.Action != model.ActionWouldPrune:
		return
	case item.Removed && item.Prunable:
		item.Action = model.ActionPruned
	case item.Removed && item.BranchDeleted:
		item.Action = model.ActionRemovedBranchDeleted
	case item.Removed:
		item.Action = model.ActionRemoved
	case item.Classification == model.SafeToRemove:
		item.Action = model.ActionWouldRemove
	case item.Classification == model.Prunable:
		item.Action = model.ActionWouldPrune
	default:
		item.Action = model.ActionKept
	}
	if item.Action == model.ActionRemoved || item.Action == model.ActionRemovedBranchDeleted {
		item.ReclaimedBytes = item.DiskBytes
	} else if item.Action != model.ActionPruned {
		item.ReclaimedBytes = 0
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := values[:0]
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
