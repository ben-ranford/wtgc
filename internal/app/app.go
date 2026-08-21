// Package app classifies worktrees and executes conservative cleanup plans.
package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

const schemaVersion = "1.0.0"

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
	DeleteBranch(context.Context, model.Repository, string) error
}

// Options controls one scan and optional cleanup pass.
type Options struct {
	Roots         []string
	Execute       bool
	Interactive   bool
	DeleteBranch  bool
	Confirm       func(model.Worktree) bool
	ProtectedPath string
	Now           func() time.Time
}

// App coordinates Git inspection without weakening Git's own safety checks.
type App struct {
	git Git
}

// New constructs an application with an explicit Git boundary.
func New(git Git) *App { return &App{git: git} }

// Run builds an inventory and, when explicitly requested, executes safe actions.
func (a *App) Run(ctx context.Context, opts Options) (model.Inventory, error) {
	started := time.Now()
	if opts.Now == nil {
		opts.Now = time.Now
	}
	inv := model.Inventory{
		SchemaVersion: schemaVersion,
		GeneratedAt:   opts.Now().UTC(),
		DryRun:        !opts.Execute,
		Roots:         append([]string(nil), opts.Roots...),
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

	for _, repo := range repositories {
		a.scanRepository(ctx, repo, opts.ProtectedPath, &inv)
	}
	sort.Slice(inv.Worktrees, func(i, j int) bool {
		if inv.Worktrees[i].Repository == inv.Worktrees[j].Repository {
			return inv.Worktrees[i].Path < inv.Worktrees[j].Path
		}
		return inv.Worktrees[i].Repository < inv.Worktrees[j].Repository
	})
	a.summarize(&inv)

	if opts.Execute {
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

func (a *App) scanRepository(ctx context.Context, repo model.Repository, protectedPath string, inv *model.Inventory) {
	defaultBranch, err := a.git.DefaultBranch(ctx, repo)
	if err != nil {
		inv.Errors = append(inv.Errors, fmt.Sprintf("%s: default branch: %v", repo.CommonDir, err))
		return
	}
	records, err := a.git.List(ctx, repo)
	if err != nil {
		inv.Errors = append(inv.Errors, fmt.Sprintf("%s: list worktrees: %v", repo.CommonDir, err))
		return
	}
	classified := make([]model.Worktree, len(records))
	workers := make(chan struct{}, 8)
	var group sync.WaitGroup
	for i, record := range records {
		group.Add(1)
		go func() {
			defer group.Done()
			workers <- struct{}{}
			defer func() { <-workers }()
			classified[i] = a.classify(ctx, repo, defaultBranch, protectedPath, record)
		}()
	}
	group.Wait()
	for _, item := range classified {
		inv.Worktrees = append(inv.Worktrees, item)
		if item.Error != "" {
			inv.Errors = append(inv.Errors, fmt.Sprintf("%s: %s: %s", repo.PrimaryPath, item.Path, item.Error))
		}
	}
}

func (a *App) classify(ctx context.Context, repo model.Repository, defaultBranch, protectedPath string, record model.RegisteredWorktree) model.Worktree {
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
		return item
	}
	if size, err := a.git.DiskUsage(record.Path); err == nil {
		item.DiskBytes = size
	} else {
		return classificationError(item, fmt.Sprintf("measure disk usage: %v", err))
	}
	if record.Primary || record.Bare {
		item.Classification, item.Reason = model.Kept, "primary or bare worktree is never removable"
		return item
	}
	if protectedPath != "" && pathsOverlap(record.Path, protectedPath) {
		item.Classification, item.Reason = model.Kept, "worktree containing the running process is never removable"
		return item
	}
	if record.Locked {
		item.Classification, item.Reason = model.Kept, "worktree is locked"
		return item
	}
	if record.Detached || record.Branch == "" {
		item.Classification, item.Reason = model.Kept, "detached worktree has no local branch to prove merged"
		return item
	}
	if record.Branch == defaultBranch {
		item.Classification, item.Reason = model.Kept, "default-branch worktree is never removable"
		return item
	}
	clean, err := a.git.IsClean(ctx, record.Path)
	if err != nil {
		return classificationError(item, fmt.Sprintf("inspect working tree: %v", err))
	}
	merged, err := a.git.IsAncestor(ctx, repo, record.Head, defaultBranch)
	if err != nil {
		return classificationError(item, fmt.Sprintf("check merge ancestry: %v", err))
	}
	if !merged {
		item.Classification, item.Reason = model.Unmerged, "branch tip is not reachable from the local default branch"
		return item
	}
	if !clean {
		item.Classification, item.Reason = model.MergedButDirty, "branch is merged but tracked, staged, or untracked changes exist"
		return item
	}
	remote, err := a.git.RemoteContains(ctx, repo, record.Head)
	if err != nil {
		return classificationError(item, fmt.Sprintf("check remote reachability: %v", err))
	}
	if !remote {
		item.Classification, item.Reason = model.Unmerged, "branch tip is not reachable from any local remote-tracking ref"
		return item
	}
	item.Classification, item.Reason = model.SafeToRemove, "clean branch tip is reachable from both the default branch and a remote-tracking ref"
	return item
}

func classificationError(item model.Worktree, message string) model.Worktree {
	item.Classification = model.Error
	item.Reason = "kept because safety could not be proven"
	item.Error = message
	return item
}

func (a *App) cleanRepository(ctx context.Context, repo model.Repository, opts Options, inv *model.Inventory) {
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
			if item.Prunable {
				pruneBlocked = true
			}
			continue
		}
		if item.Classification == model.Prunable {
			stillPrunable, err := a.stillPrunable(ctx, repo, item.Path)
			if err != nil {
				item.Classification = model.Error
				item.Reason = "revalidation blocked prune because state could not be proven"
				item.Error = err.Error()
				inv.Errors = append(inv.Errors, fmt.Sprintf("%s: revalidate prunable %s: %v", repo.PrimaryPath, item.Path, err))
				pruneBlocked = true
				continue
			}
			if !stillPrunable {
				item.Classification = model.Kept
				item.Reason = "revalidation blocked prune: worktree is no longer marked prunable"
				pruneBlocked = true
				continue
			}
			acceptedPrunable = append(acceptedPrunable, i)
			continue
		}

		fresh, ok := a.revalidate(ctx, repo, opts.ProtectedPath, *item)
		if !ok {
			*item = fresh
			continue
		}
		if err := a.git.Remove(ctx, repo, fresh.Path); err != nil {
			item.Error = err.Error()
			inv.Errors = append(inv.Errors, fmt.Sprintf("%s: remove %s: %v", repo.PrimaryPath, fresh.Path, err))
			continue
		}
		item.Removed = true
		if opts.DeleteBranch {
			if err := a.git.DeleteBranch(ctx, repo, fresh.Branch); err != nil {
				item.Error = fmt.Sprintf("worktree removed; branch retained: %v", err)
				inv.Errors = append(inv.Errors, fmt.Sprintf("%s: delete branch %s: %v", repo.PrimaryPath, fresh.Branch, err))
			} else {
				item.BranchDeleted = true
			}
		}
	}
	if pruneBlocked || len(acceptedPrunable) == 0 {
		return
	}
	if err := a.git.Prune(ctx, repo); err != nil {
		inv.Errors = append(inv.Errors, fmt.Sprintf("%s: prune: %v", repo.PrimaryPath, err))
		for _, index := range acceptedPrunable {
			inv.Worktrees[index].Error = err.Error()
		}
		return
	}
	for _, index := range acceptedPrunable {
		inv.Worktrees[index].Removed = true
	}
}

func (a *App) stillPrunable(ctx context.Context, repo model.Repository, path string) (bool, error) {
	records, err := a.git.List(ctx, repo)
	if err != nil {
		return false, err
	}
	for _, record := range records {
		if canonicalPath(record.Path) == canonicalPath(path) {
			return record.Prunable, nil
		}
	}
	return false, nil
}

func (a *App) revalidate(ctx context.Context, repo model.Repository, protectedPath string, previous model.Worktree) (model.Worktree, bool) {
	defaultBranch, err := a.git.DefaultBranch(ctx, repo)
	if err != nil {
		return classificationError(previous, fmt.Sprintf("revalidate default branch: %v", err)), false
	}
	records, err := a.git.List(ctx, repo)
	if err != nil {
		return classificationError(previous, fmt.Sprintf("revalidate worktree list: %v", err)), false
	}
	for _, record := range records {
		if record.Path != previous.Path {
			continue
		}
		if record.Head != previous.Head || record.Branch != previous.Branch {
			previous.Classification = model.Kept
			previous.Reason = "worktree HEAD or branch changed after scan"
			return previous, false
		}
		fresh := a.classify(ctx, repo, defaultBranch, protectedPath, record)
		if fresh.Classification != model.SafeToRemove {
			fresh.Reason = "revalidation blocked removal: " + fresh.Reason
			return fresh, false
		}
		return fresh, true
	}
	previous.Classification = model.Kept
	previous.Reason = "worktree registration changed after scan"
	return previous, false
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
	for _, item := range inv.Worktrees {
		inv.Summary.Scanned++
		if item.Classification == model.SafeToRemove {
			inv.Summary.Safe++
			inv.Summary.PotentialBytes += item.DiskBytes
		}
		if item.Removed {
			inv.Summary.Removed++
			if item.Prunable {
				inv.Summary.Pruned++
			} else {
				inv.Summary.ReclaimedBytes += item.DiskBytes
			}
		} else if item.Classification != model.SafeToRemove {
			inv.Summary.Skipped++
		}
	}
	inv.Errors = uniqueStrings(inv.Errors)
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
