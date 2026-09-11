package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

// SelectionPreview is the complete validated set, not an executable plan.
// Paths is a copy: confirmation cannot change the application's allowlist.
type SelectionPreview struct {
	Count            int
	Paths            []string
	ReclaimableBytes int64
}

// PickRow is a displayed worktree row. Number is non-zero only for rows the
// application has bound as safe, live cleanup candidates.
type PickRow struct {
	Number      int
	Worktree    model.Worktree
	Selectable  bool
	Unavailable string
}

// PickPreview is an immutable display snapshot. The picker returns only row
// numbers; private selection identities stay in the application layer.
type PickPreview struct {
	Rows []PickRow
}

// PickResult separates a voluntary empty choice from a canceled or failed
// picker stream, so an I/O failure cannot silently become authorization.
type PickResult struct {
	Selected []int
	Err      error
}

type selectionIdentity struct {
	CommonDir     string
	CanonicalPath string
	Branch        string
	Head          string
	Registration  string
}

type selectedWorktree struct {
	index    int
	repo     model.Repository
	identity selectionIdentity
}

func strictCanonicalPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("empty selection path")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	return filepath.Abs(resolved)
}

func bindSelection(repositories []model.Repository, paths []string, inv *model.Inventory) ([]selectedWorktree, error) {
	if len(inv.Errors) > 0 {
		return nil, fmt.Errorf("selection rejected because the scan contains errors")
	}
	selected := make([]selectedWorktree, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		canonical, err := strictCanonicalPath(path)
		if err != nil {
			return nil, fmt.Errorf("select %q: %w", path, err)
		}
		if seen[canonical] {
			return nil, fmt.Errorf("duplicate selection %q", path)
		}
		seen[canonical] = true
		index := -1
		for i, item := range inv.Worktrees {
			candidate, err := strictCanonicalPath(item.Path)
			if err != nil || candidate != canonical {
				continue
			}
			if index >= 0 {
				return nil, fmt.Errorf("ambiguous selection %q", path)
			}
			index = i
		}
		if index < 0 {
			return nil, fmt.Errorf("unknown selection %q", path)
		}
		bound, err := bindSelectedWorktree(repositories, inv.Worktrees[index], index)
		if err != nil {
			return nil, fmt.Errorf("unsafe or non-live selection %q: %w", path, err)
		}
		selected = append(selected, bound)
	}
	return selected, nil
}

func bindSelectedWorktree(repositories []model.Repository, item model.Worktree, index int) (selectedWorktree, error) {
	if item.Prunable || item.Classification != model.SafeToRemove || item.Branch == "" || item.Head == "" {
		return selectedWorktree{}, fmt.Errorf("%s", item.Reason)
	}
	var matches []model.Repository
	for _, repo := range repositories {
		if repo.PrimaryPath == item.Repository {
			matches = append(matches, repo)
		}
	}
	if len(matches) != 1 {
		return selectedWorktree{}, fmt.Errorf("ambiguous repository")
	}
	canonical, err := strictCanonicalPath(item.Path)
	if err != nil {
		return selectedWorktree{}, err
	}
	common, err := strictCanonicalPath(matches[0].CommonDir)
	if err != nil {
		return selectedWorktree{}, fmt.Errorf("common directory: %w", err)
	}
	registration, err := readRegistration(item.Path)
	if err != nil {
		return selectedWorktree{}, fmt.Errorf("registration: %w", err)
	}
	return selectedWorktree{index: index, repo: matches[0], identity: selectionIdentity{CommonDir: common, CanonicalPath: canonical, Branch: item.Branch, Head: item.Head, Registration: registration}}, nil
}

func (a *App) cleanPicked(ctx context.Context, repositories []model.Repository, opts Options, inv *model.Inventory) {
	for i := range inv.Worktrees {
		inv.Worktrees[i].Action = model.ActionKept
	}
	rows := make([]PickRow, 0, len(inv.Worktrees))
	choices := make([]selectedWorktree, 0)
	for index, item := range inv.Worktrees {
		row := PickRow{Worktree: pickerPreviewWorktree(item), Unavailable: item.Reason}
		if bound, err := bindSelectedWorktree(repositories, item, index); err == nil {
			row.Number, row.Selectable = len(choices)+1, true
			row.Unavailable = ""
			choices = append(choices, bound)
		} else {
			row.Unavailable = err.Error()
		}
		rows = append(rows, row)
	}
	result := opts.Pick(PickPreview{Rows: rows})
	if result.Err != nil {
		inv.Errors = append(inv.Errors, "picker: "+result.Err.Error())
		return
	}
	selectedNumbers := result.Selected
	if len(selectedNumbers) == 0 {
		return
	}
	if len(inv.Errors) > 0 {
		inv.Errors = append(inv.Errors, "picker selection rejected because the scan contains errors")
		return
	}
	selected := make([]selectedWorktree, 0, len(selectedNumbers))
	seen := make(map[int]bool, len(selectedNumbers))
	for _, number := range selectedNumbers {
		if number < 1 || number > len(choices) || seen[number] {
			inv.Errors = append(inv.Errors, "picker returned an invalid selection")
			return
		}
		seen[number] = true
		selected = append(selected, choices[number-1])
	}
	a.applySelectedSet(ctx, selected, opts, inv)
}

func pickerPreviewWorktree(item model.Worktree) model.Worktree {
	preview := item
	preview.Dirty = cloneBool(item.Dirty)
	if item.WorktreeDetails == nil {
		return preview
	}
	details := *item.WorktreeDetails
	details.DiskBytesMeasured = cloneBool(item.DiskBytesMeasured)
	details.ObservedAt = cloneTime(item.ObservedAt)
	details.EligibleAt = cloneTime(item.EligibleAt)
	details.MergedAt = cloneTime(item.MergedAt)
	details.CacheWarnings = append([]model.CacheWarning(nil), item.CacheWarnings...)
	details.ExplainChecks = append([]model.ExplainCheck(nil), item.ExplainChecks...)
	preview.WorktreeDetails = &details
	return preview
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (a *App) cleanSelection(ctx context.Context, repositories []model.Repository, opts Options, inv *model.Inventory) {
	// No actions are authorized until every selection has passed validation.
	for i := range inv.Worktrees {
		inv.Worktrees[i].Action = model.ActionKept
	}
	if err := opts.exclusions.revalidate(); err != nil {
		inv.Errors = append(inv.Errors, fmt.Sprintf("selection rejected because excluded scope changed: %v", err))
		return
	}
	for _, path := range opts.SelectedPaths {
		excluded, err := opts.exclusions.contains(path)
		if err != nil {
			inv.Errors = append(inv.Errors, fmt.Sprintf("selection %q exclusion membership could not be proven: %v", path, err))
			return
		}
		if excluded {
			inv.Errors = append(inv.Errors, fmt.Sprintf("selection %q is excluded by invocation scope", path))
			return
		}
	}
	selected, err := bindSelection(repositories, opts.SelectedPaths, inv)
	if err != nil {
		inv.Errors = append(inv.Errors, err.Error())
		return
	}
	a.applySelectedSet(ctx, selected, opts, inv)
}

func (a *App) applySelectedSet(ctx context.Context, selected []selectedWorktree, opts Options, inv *model.Inventory) {
	preview := selectedPreview(selected, inv)
	if !opts.Execute {
		return
	}
	if !a.confirmAndPreflightSelectedSet(ctx, selected, opts, inv, preview) {
		reportSelectedCancellation(ctx, inv)
		return
	}
	a.removeSelectedSet(ctx, selected, opts, inv)
	reportSelectedCancellation(ctx, inv)
}

func reportSelectedCancellation(ctx context.Context, inv *model.Inventory) {
	if err := ctx.Err(); err != nil {
		inv.Errors = append(inv.Errors, fmt.Sprintf("selected cleanup stopped: %v; completed removals are not rolled back", err))
	}
}

func selectedPreview(selected []selectedWorktree, inv *model.Inventory) SelectionPreview {
	preview := SelectionPreview{Count: len(selected), Paths: make([]string, 0, len(selected))}
	for _, bound := range selected {
		item := &inv.Worktrees[bound.index]
		item.Action = model.ActionWouldRemove
		preview.Paths = append(preview.Paths, bound.identity.CanonicalPath)
		preview.ReclaimableBytes += item.DiskBytes
	}
	return preview
}

func (a *App) confirmAndPreflightSelectedSet(ctx context.Context, selected []selectedWorktree, opts Options, inv *model.Inventory, preview SelectionPreview) bool {
	if ctx.Err() != nil {
		keepSelection(selected, inv, "selected cleanup canceled before confirmation")
		return false
	}
	if opts.Interactive && (opts.ConfirmSelection == nil || !opts.ConfirmSelection(preview)) {
		keepSelection(selected, inv, "selected set kept by interactive choice")
		return false
	}
	if err := a.preflightSelectedSet(ctx, selected, opts, inv); err != nil {
		keepSelection(selected, inv, "selected cleanup blocked because the displayed set changed")
		inv.Errors = append(inv.Errors, "selected cleanup preflight: "+err.Error())
		return false
	}
	return true
}

func (a *App) removeSelectedSet(ctx context.Context, selected []selectedWorktree, opts Options, inv *model.Inventory) {
	for _, bound := range selected {
		a.removeSelectedWorktree(ctx, bound, opts, inv)
	}
	if opts.DeleteBranch {
		a.deleteSelectedBranches(ctx, selected, opts.exclusions, inv)
	}
}

func (a *App) removeSelectedWorktree(ctx context.Context, bound selectedWorktree, opts Options, inv *model.Inventory) {
	item := &inv.Worktrees[bound.index]
	if ctx.Err() != nil {
		item.Action, item.Reason = model.ActionKept, "selected cleanup canceled; no new action started"
		return
	}
	opts.selection = &bound.identity
	a.removeWorktree(ctx, bound.repo, opts, item, inv)
	if !item.Removed {
		item.Action = model.ActionKept
		inv.Errors = append(inv.Errors, fmt.Sprintf("%s: selected removal refused: %s %s", item.Path, item.Reason, item.Error))
	}
}

func (a *App) preflightSelectedSet(ctx context.Context, selected []selectedWorktree, opts Options, inv *model.Inventory) error {
	if err := opts.exclusions.revalidate(); err != nil {
		return fmt.Errorf("excluded scope changed: %w", err)
	}
	for _, bound := range selected {
		item := inv.Worktrees[bound.index]
		excluded, err := opts.exclusions.contains(item.Path)
		if err != nil || excluded {
			if err != nil {
				return fmt.Errorf("selected exclusion membership: %w", err)
			}
			return fmt.Errorf("selected path is excluded")
		}
		preflight := opts
		preflight.selection = &bound.identity
		if _, ok := a.revalidate(ctx, bound.repo, preflight, item); !ok {
			return fmt.Errorf("%s changed after display", item.Path)
		}
	}
	return ctx.Err()
}

func keepSelection(selected []selectedWorktree, inv *model.Inventory, reason string) {
	for _, bound := range selected {
		item := &inv.Worktrees[bound.index]
		item.Action, item.Reason = model.ActionKept, reason
	}
}

func (a *App) requireSelectedRepository(ctx context.Context, repo model.Repository, path string, identity selectionIdentity) error {
	canonical, err := strictCanonicalPath(path)
	if err != nil || canonical != identity.CanonicalPath {
		return fmt.Errorf("selected canonical path changed after scan")
	}
	// A same-repository .git swap is invisible to worktree list. Bind its
	// registration pointer too, so another checkout cannot lend its clean status.
	registration, err := readRegistration(path)
	if err != nil || registration != identity.Registration {
		return fmt.Errorf("selected Git registration pointer changed after scan")
	}
	// Rediscover from the selected worktree itself, not a saved repository handle:
	// a changed .git link must not borrow safety evidence from another repository.
	fresh, errs := a.git.Discover(ctx, []string{path})
	if len(errs) > 0 || len(fresh) != 1 {
		return fmt.Errorf("selected repository identity unavailable or ambiguous")
	}
	common, err := strictCanonicalPath(fresh[0].CommonDir)
	if err != nil || common != identity.CommonDir || fresh[0].PrimaryPath != repo.PrimaryPath {
		return fmt.Errorf("selected common directory or repository changed after scan")
	}
	return nil
}

func requireSelectedRecord(records []model.RegisteredWorktree, path string, identity selectionIdentity) error {
	matches := 0
	for _, record := range records {
		canonical, err := strictCanonicalPath(record.Path)
		if err != nil || canonical != identity.CanonicalPath {
			continue
		}
		matches++
		if record.Path != path || record.Head != identity.Head || record.Branch != identity.Branch || record.Prunable {
			return fmt.Errorf("selected path, HEAD, branch or live registration changed after scan")
		}
	}
	if matches != 1 {
		return fmt.Errorf("selected registration missing or ambiguous after scan")
	}
	return nil
}

func readRegistration(path string) (string, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return "", err
	}
	defer root.Close()
	data, err := root.ReadFile(".git")
	return string(data), err
}

// The allowlist authorizes removing selected checkouts, not invalidating a
// shared ref still used by any remaining checkout (including stale records).
func (a *App) requireUnusedBranch(ctx context.Context, repo model.Repository, branch string, exclusions exclusionBoundary) error {
	if err := exclusions.revalidate(); err != nil {
		return err
	}
	records, err := a.git.List(ctx, repo)
	if err != nil {
		return fmt.Errorf("inspect remaining branch checkouts: %w", err)
	}
	for _, record := range records {
		if record.Branch == branch {
			excluded, err := exclusions.contains(record.Path)
			if err != nil {
				return fmt.Errorf("evaluate excluded registration %q: %w", record.Path, err)
			}
			if excluded {
				return fmt.Errorf("branch is retained because excluded registration remains at %s", record.Path)
			}
			return fmt.Errorf("branch is still checked out at %s", record.Path)
		}
	}
	return ctx.Err()
}

// Branch actions follow the entire removal pass: another selected checkout is
// not a terminal retention failure while it is still waiting for removal.
func (a *App) deleteSelectedBranches(ctx context.Context, selected []selectedWorktree, exclusions exclusionBoundary, inv *model.Inventory) {
	attempted := make(map[[2]string]bool)
	for _, bound := range selected {
		if ctx.Err() != nil {
			return
		}
		item := &inv.Worktrees[bound.index]
		key := [2]string{bound.identity.CommonDir, bound.identity.Branch}
		if !item.Removed || attempted[key] {
			continue
		}
		attempted[key] = true
		// Successful removal revalidated these exact branch/default/proof fields.
		// The branch guard still freshly lists registrations before ancestry/CAS.
		a.deleteWorktreeBranch(ctx, bound.repo, *item, item, inv, exclusions, true)
	}
}
