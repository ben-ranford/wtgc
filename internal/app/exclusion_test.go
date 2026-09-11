package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ben-ranford/wtgc/internal/cache"
	"github.com/ben-ranford/wtgc/internal/gitx"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/provider"
	"github.com/ben-ranford/wtgc/internal/testgit"
)

func TestExcludedLinkedWorktreeIsUnmeasuredAndNeverMutated(t *testing.T) {
	repo := testgit.NewRepository(t)
	included := repo.CreateMergedWorktree(t, "included")
	excluded := repo.CreateMergedWorktree(t, "excluded")
	stale := repo.CreateMergedWorktree(t, "stale")
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
	}
	before := registeredWorktreePaths(t, repo)
	if !before[canonicalPathAllowMissing(t, stale)] {
		t.Fatal("stale registration missing before cleanup")
	}

	inv, err := New(gitx.New("git")).Run(context.Background(), Options{
		Roots: []string{repo.Root}, ExcludePaths: []string{excluded, stale}, Execute: true, DeleteBranch: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	excludedItem := selectedItemByPath(t, inv, excluded)
	if excludedItem.WorktreeDetails == nil || !excludedItem.Excluded || excludedItem.DiskBytesMeasured == nil || *excludedItem.DiskBytesMeasured || excludedItem.DiskBytes != 0 || excludedItem.Action != model.ActionKept || !strings.Contains(excludedItem.Reason, "excluded") {
		t.Fatalf("excluded item=%+v", excludedItem)
	}
	if staleItem := selectedItemByPath(t, inv, stale); staleItem.WorktreeDetails == nil || !staleItem.Excluded || staleItem.Action != model.ActionKept {
		t.Fatalf("stale item=%+v", staleItem)
	}
	if _, statErr := os.Stat(excluded); statErr != nil {
		t.Fatalf("excluded worktree was changed: %v", statErr)
	}
	if !repo.BranchExists(t, "excluded") || !repo.BranchExists(t, "stale") {
		t.Fatal("excluded registration did not protect its branch")
	}
	if _, statErr := os.Stat(included); !os.IsNotExist(statErr) {
		t.Fatalf("included candidate was not removed: %v", statErr)
	}
	if includedItem := selectedItemByPath(t, inv, included); !includedItem.Removed {
		t.Fatalf("included item=%+v", includedItem)
	}
	after := registeredWorktreePaths(t, repo)
	if !after[canonicalPathAllowMissing(t, stale)] {
		t.Fatalf("excluded stale registration was pruned: before=%v after=%v", before, after)
	}
}

func TestExplainExcludedTargetRetainsOnlyRegistrationEvidence(t *testing.T) {
	repo := testgit.NewRepository(t)
	excluded := repo.CreateMergedWorktree(t, "excluded-explain")

	inv, err := New(gitx.New("git")).Run(context.Background(), Options{
		ExplainPath: excluded, ExcludePaths: []string{excluded}, ExplainEvidence: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Worktrees) != 1 {
		t.Fatalf("worktrees=%+v", inv.Worktrees)
	}
	item := inv.Worktrees[0]
	if item.Classification != model.Kept || item.WorktreeDetails == nil || !item.Excluded || item.DiskBytesMeasured == nil || *item.DiskBytesMeasured {
		t.Fatalf("excluded explain item=%+v", item)
	}
	if !hasExplainCheck(item, "actual", model.ExplainNotEvaluated) {
		t.Fatalf("checks=%+v, want actual not_evaluated", item.ExplainChecks)
	}
	if hasExplainCheck(item, "working_tree", model.ExplainPassed) || hasExplainCheck(item, "local_default_reachability", model.ExplainPassed) {
		t.Fatalf("excluded target was classified: checks=%+v", item.ExplainChecks)
	}
}

func hasExplainCheck(item model.Worktree, id string, status model.ExplainCheckStatus) bool {
	for _, check := range item.ExplainChecks {
		if check.ID == id && check.Status == status {
			return true
		}
	}
	return false
}

func registeredWorktreePaths(t *testing.T, repo *testgit.Repository) map[string]bool {
	t.Helper()
	records, err := gitx.ParseWorktreeListPorcelainZ([]byte(testgit.Run(t, repo.Path, "worktree", "list", "--porcelain", "-z")))
	if err != nil {
		t.Fatal(err)
	}
	paths := make(map[string]bool, len(records))
	for _, record := range records {
		paths[canonicalPathAllowMissing(t, record.Path)] = true
	}
	return paths
}

func canonicalPathAllowMissing(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	missing := []string(nil)
	for {
		if _, err := os.Lstat(abs); err == nil {
			resolved, err := filepath.EvalSymlinks(abs)
			if err != nil {
				t.Fatal(err)
			}
			return filepath.Join(append([]string{resolved}, missing...)...)
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			t.Fatalf("no existing parent for %q", path)
		}
		missing = append([]string{filepath.Base(abs)}, missing...)
		abs = parent
	}
}

func TestExcludedPrimaryDirectoryIsNotDiscoveredWhileSiblingRemains(t *testing.T) {
	first, second := testgit.NewRepository(t), testgit.NewRepository(t)
	firstCandidate := first.CreateMergedWorktree(t, "excluded-repository")
	secondCandidate := second.CreateMergedWorktree(t, "included-repository")
	inv, err := New(gitx.New("git")).Run(context.Background(), Options{Roots: []string{filepath.Dir(first.Root)}, ExcludePaths: []string{first.Root}})
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range inv.Worktrees {
		if item.Path == firstCandidate || item.Repository == first.Path {
			t.Fatalf("excluded primary repository leaked into inventory: %+v", item)
		}
	}
	if item := selectedItemByPath(t, inv, secondCandidate); item.WorktreeDetails != nil && item.Excluded {
		t.Fatal("included sibling was excluded")
	}
}

func TestExcludedSelectionRejectsWholeSetBeforeMutation(t *testing.T) {
	repo := testgit.NewRepository(t)
	allowed := repo.CreateMergedWorktree(t, "allowed")
	excluded := repo.CreateMergedWorktree(t, "excluded")
	inv, err := New(gitx.New("git")).Run(context.Background(), Options{
		Roots: []string{repo.Root}, ExcludePaths: []string{excluded}, SelectedPaths: []string{allowed, excluded}, Execute: true,
	})
	if err == nil || !strings.Contains(strings.Join(inv.Errors, "\n"), "excluded by invocation scope") {
		t.Fatalf("err=%v inventory=%+v", err, inv)
	}
	for _, path := range []string{allowed, excluded} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("whole selection mutated %q: %v", path, statErr)
		}
	}
}

func TestExcludedDescendantProtectsAncestorAndLeavesSiblingUsable(t *testing.T) {
	root := t.TempDir()
	ancestor := filepath.Join(root, "ancestor")
	protectedChild := filepath.Join(ancestor, "cache")
	included := filepath.Join(root, "included")
	for _, path := range []string{ancestor, protectedChild, included} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	backend := &exclusionSpy{fakeGit: newFakeGit(
		model.RegisteredWorktree{Path: ancestor, Branch: "ancestor", Head: "ancestor-head"},
		model.RegisteredWorktree{Path: included, Branch: "included", Head: "included-head"},
	)}
	backend.repository = model.Repository{PrimaryPath: root, CommonDir: filepath.Join(root, ".git")}
	backend.repositories = []model.Repository{backend.repository}
	backend.clean, backend.ancestor, backend.remote = []bool{true}, true, true
	var cachePaths []string

	inv, err := New(backend).Run(context.Background(), Options{
		Roots: []string{root}, ExcludePaths: []string{protectedChild}, Execute: true,
		CacheScanner: func(_ context.Context, path string, _ int64) []cache.Warning {
			cachePaths = append(cachePaths, path)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	protected := selectedItemByPath(t, inv, ancestor)
	if protected.WorktreeDetails == nil || !protected.Excluded || protected.DiskBytesMeasured == nil || *protected.DiskBytesMeasured || protected.Action != model.ActionKept {
		t.Fatalf("protected ancestor=%+v", protected)
	}
	if item := selectedItemByPath(t, inv, included); !item.Removed {
		t.Fatalf("included sibling=%+v", item)
	}
	if len(backend.diskPaths) == 0 {
		t.Fatal("included sibling was not measured")
	}
	for _, path := range backend.diskPaths {
		if path != included {
			t.Fatalf("disk usage path=%q, want only %q", path, included)
		}
	}
	if len(cachePaths) != 1 || cachePaths[0] != included {
		t.Fatalf("cache paths=%q, want only %q", cachePaths, included)
	}
	if len(backend.removePaths) != 1 || backend.removePaths[0] != included {
		t.Fatalf("removal paths=%q, want only %q", backend.removePaths, included)
	}
	if _, statErr := os.Stat(ancestor); statErr != nil {
		t.Fatalf("protected ancestor was changed: %v", statErr)
	}

	boundary, err := newExclusionBoundary([]string{protectedChild})
	if err != nil {
		t.Fatal(err)
	}
	backend.removePaths = nil
	item := model.Worktree{Path: ancestor, Branch: "ancestor", Head: "ancestor-head", Repository: root, Classification: model.SafeToRemove}
	New(backend).removeWorktree(context.Background(), backend.repository, Options{exclusions: boundary}, &item, &model.Inventory{})
	if item.Action != model.ActionKept || !strings.Contains(item.Reason, "path is excluded") || len(backend.removePaths) != 0 {
		t.Fatalf("protected removal item=%+v calls=%q", item, backend.removePaths)
	}
}

func TestExclusionsRetainUnrelatedStaleRegistrationWithoutPruning(t *testing.T) {
	root := t.TempDir()
	protected := filepath.Join(root, "protected")
	if err := os.Mkdir(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(root, "stale")
	backend := &exclusionSpy{fakeGit: newFakeGit(model.RegisteredWorktree{Path: stale, Prunable: true})}
	backend.repository = model.Repository{PrimaryPath: root, CommonDir: filepath.Join(root, ".git")}
	backend.repositories = []model.Repository{backend.repository}

	inv, err := New(backend).Run(context.Background(), Options{Roots: []string{root}, ExcludePaths: []string{protected}, Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	item := selectedItemByPath(t, inv, stale)
	if item.Action != model.ActionKept || !strings.Contains(item.Reason, "exclusions disable repository-wide prune") || backend.pruneCalls != 0 {
		t.Fatalf("stale item=%+v prune calls=%d", item, backend.pruneCalls)
	}
}

func TestExclusionUsesComponentBoundariesAndMissingParent(t *testing.T) {
	root := t.TempDir()
	appPath := filepath.Join(root, "app")
	applePath := filepath.Join(root, "apple")
	for _, path := range []string{appPath, applePath} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	boundary, err := newExclusionBoundary([]string{appPath, filepath.Join(root, "missing", "stale")})
	appExcluded, appErr := boundary.contains(appPath)
	appleExcluded, appleErr := boundary.contains(applePath)
	missingExcluded, missingErr := boundary.contains(filepath.Join(root, "missing", "stale"))
	childExcluded, childErr := boundary.contains(filepath.Join(appPath, "nested"))
	if err != nil || appErr != nil || appleErr != nil || missingErr != nil || childErr != nil || !appExcluded || appleExcluded || !missingExcluded || !childExcluded {
		t.Fatalf("boundary=%+v err=%v", boundary, err)
	}
}

func TestDuplicateExclusionAliasDriftIsRejected(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	firstAlias, secondAlias := filepath.Join(root, "first-alias"), filepath.Join(root, "second-alias")
	if err := os.Symlink(first, firstAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(first, secondAlias); err != nil {
		t.Fatal(err)
	}
	boundary, err := newExclusionBoundary([]string{firstAlias, secondAlias})
	if err != nil || len(boundary.values) != 1 || boundary.revalidate() != nil {
		t.Fatalf("boundary=%+v err=%v", boundary, err)
	}
	if err := os.Remove(secondAlias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, secondAlias); err != nil {
		t.Fatal(err)
	}
	if err := boundary.revalidate(); err == nil {
		t.Fatal("duplicate alias drift accepted")
	}
}

func TestExclusionBoundaryRejectsInvalidAndRevalidatesAlias(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, paths := range [][]string{{""}, {file}} {
		if _, err := newExclusionBoundary(paths); err == nil {
			t.Fatalf("paths %q accepted", paths)
		}
	}
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(first, alias); err != nil {
		t.Fatal(err)
	}
	boundary, err := newExclusionBoundary([]string{alias})
	if err != nil || len(boundary.values) != 1 || boundary.revalidate() != nil {
		t.Fatalf("boundary=%+v err=%v", boundary, err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, alias); err != nil {
		t.Fatal(err)
	}
	if err := boundary.revalidate(); err == nil {
		t.Fatal("alias drift accepted")
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(alias, alias); err != nil {
		t.Fatal(err)
	}
	if err := boundary.revalidate(); err == nil {
		t.Fatal("broken alias accepted")
	}
}

func TestRunRejectsInvalidExcludedPathBeforeDiscovery(t *testing.T) {
	inv, err := New(newFakeGit()).Run(context.Background(), Options{ExcludePaths: []string{""}})
	if err == nil || inv.SchemaVersion != schemaVersion || len(inv.Worktrees) != 0 {
		t.Fatalf("err=%v inventory=%+v", err, inv)
	}
}

func TestEmptyExclusionBoundaryDoesNotResolveCandidatePath(t *testing.T) {
	loop := filepath.Join(t.TempDir(), "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("create symlink loop: %v", err)
	}
	excluded, err := (exclusionBoundary{}).contains(loop)
	if err != nil || excluded {
		t.Fatalf("excluded=%t err=%v", excluded, err)
	}
}

func TestExclusionDiscoveryErrorsRemainVisible(t *testing.T) {
	root := t.TempDir()
	protected := filepath.Join(root, "protected")
	if err := os.Mkdir(protected, 0o700); err != nil {
		t.Fatal(err)
	}
	backend := &exclusionSpy{fakeGit: newFakeGit(), discoveryErrors: []error{errors.New("cannot inspect one root")}}
	backend.repository = model.Repository{PrimaryPath: root, CommonDir: filepath.Join(root, ".git")}
	backend.repositories = []model.Repository{backend.repository}
	inv, err := New(backend).Run(context.Background(), Options{Roots: []string{root}, ExcludePaths: []string{protected}})
	if err == nil || !strings.Contains(strings.Join(inv.Errors, "\n"), "cannot inspect one root") {
		t.Fatalf("err=%v inventory=%+v", err, inv)
	}
}

func TestSelectedCleanupRejectsExclusionScopeDriftBeforeMutation(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, path := range []string{first, second} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(root, "protected-alias")
	if err := os.Symlink(first, alias); err != nil {
		t.Fatal(err)
	}
	boundary, err := newExclusionBoundary([]string{alias})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, alias); err != nil {
		t.Fatal(err)
	}
	backend := &exclusionSpy{fakeGit: newFakeGit()}
	inv := model.Inventory{Worktrees: []model.Worktree{{Path: filepath.Join(root, "selected"), Action: model.ActionWouldRemove}}}
	New(backend).cleanSelection(context.Background(), nil, Options{SelectedPaths: []string{inv.Worktrees[0].Path}, exclusions: boundary}, &inv)
	if len(inv.Errors) != 1 || !strings.Contains(inv.Errors[0], "excluded scope changed") || inv.Worktrees[0].Action != model.ActionKept || len(backend.removePaths) != 0 {
		t.Fatalf("inventory=%+v removals=%q", inv, backend.removePaths)
	}
}

func TestExcludeRequiresDiscoveryBoundary(t *testing.T) {
	backend, opts := selectionFixture(t)
	opts.ExcludePaths = []string{opts.Roots[0]}
	inv, err := New(backend).Run(context.Background(), opts)
	if err == nil || !strings.Contains(strings.Join(inv.Errors, "\n"), "discovery backend") {
		t.Fatalf("err=%v inv=%+v", err, inv)
	}
}

func TestExclusionSymlinkDriftBlocksSelectedRemoval(t *testing.T) {
	repo := testgit.NewRepository(t)
	selected := repo.CreateMergedWorktree(t, "selected")
	excluded := repo.CreateMergedWorktree(t, "excluded")
	other := filepath.Join(repo.Worktrees, "other-scope")
	if err := os.Mkdir(other, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(repo.Root, "protected-alias")
	if err := os.Symlink(excluded, alias); err != nil {
		t.Fatal(err)
	}
	opts := Options{Roots: []string{repo.Root}, ExcludePaths: []string{alias}, SelectedPaths: []string{selected}, Execute: true, Interactive: true}
	opts.ConfirmSelection = func(SelectionPreview) bool {
		if err := os.Remove(alias); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(other, alias); err != nil {
			t.Fatal(err)
		}
		return true
	}
	inv, err := New(gitx.New("git")).Run(context.Background(), opts)
	if err == nil || inv.Summary.Removed != 0 || !strings.Contains(strings.Join(inv.Errors, "\n"), "excluded scope changed") {
		t.Fatalf("err=%v inv=%+v", err, inv)
	}
	if _, statErr := os.Stat(selected); statErr != nil {
		t.Fatalf("scope drift removed selected worktree: %v", statErr)
	}
}

func TestUnprovableExclusionMembershipBlocksInspectionAndCleanup(t *testing.T) {
	root := t.TempDir()
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("create symlink loop: %v", err)
	}
	backend := &exclusionSpy{fakeGit: newFakeGit(model.RegisteredWorktree{Path: loop, Branch: "feature", Head: "abc"})}
	backend.repository = model.Repository{PrimaryPath: root, CommonDir: filepath.Join(root, ".git")}
	backend.repositories = []model.Repository{backend.repository}
	cacheCalls := 0
	mergeFinder := &countingMergeFinder{}

	inv, err := New(backend).Run(context.Background(), Options{
		Roots: []string{root}, ExcludePaths: []string{root}, Execute: true, DeleteBranch: true,
		Provider: mergeFinder,
		CacheScanner: func(context.Context, string, int64) []cache.Warning {
			cacheCalls++
			return nil
		},
	})
	if err == nil || !strings.Contains(strings.Join(inv.Errors, "\n"), "exclusion membership") {
		t.Fatalf("err=%v inventory=%+v", err, inv)
	}
	if len(inv.Worktrees) != 1 {
		t.Fatalf("worktrees=%+v", inv.Worktrees)
	}
	item := inv.Worktrees[0]
	if item.Classification != model.Error || item.WorktreeDetails == nil || !item.Excluded || item.DiskBytesMeasured == nil || *item.DiskBytesMeasured || !strings.Contains(item.Reason, "could not be proven") {
		t.Fatalf("membership failure item=%+v", item)
	}
	if backend.cleanCalls != 0 || backend.diskCalls != 0 || mergeFinder.calls != 0 || cacheCalls != 0 || backend.removeCalls != 0 {
		t.Fatalf("unprovable membership performed inspection or mutation: clean=%d disk=%d provider=%d cache=%d remove=%d", backend.cleanCalls, backend.diskCalls, mergeFinder.calls, cacheCalls, backend.removeCalls)
	}
}

func TestUnprovableExclusionMembershipRefusesRemovalAndBranchDeletion(t *testing.T) {
	root := t.TempDir()
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("create symlink loop: %v", err)
	}
	boundary, err := newExclusionBoundary([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	backend := newFakeGit(model.RegisteredWorktree{Path: loop, Branch: "feature", Head: "abc"})
	repo := model.Repository{PrimaryPath: root, CommonDir: filepath.Join(root, ".git")}
	backend.repository, backend.repositories = repo, []model.Repository{repo}
	item := model.Worktree{Path: loop, Branch: "feature", Head: "abc", Repository: root, Classification: model.SafeToRemove}
	New(backend).removeWorktree(context.Background(), repo, Options{exclusions: boundary}, &item, &model.Inventory{})
	if backend.removeCalls != 0 || !strings.Contains(item.Reason, "could not be proven") || item.Action != model.ActionKept {
		t.Fatalf("removal=%d item=%+v", backend.removeCalls, item)
	}
	if err := New(backend).requireUnusedBranch(context.Background(), repo, "feature", boundary); err == nil || !strings.Contains(err.Error(), "evaluate excluded registration") {
		t.Fatalf("branch guard err=%v", err)
	}
}

func TestExclusionBranchGuardFailsClosedForUnlistedAndUnreadableRegistrations(t *testing.T) {
	root := t.TempDir()
	boundary, err := newExclusionBoundary([]string{root})
	if err != nil {
		t.Fatal(err)
	}
	repo := model.Repository{PrimaryPath: root, CommonDir: filepath.Join(root, ".git")}
	backend := newFakeGit()
	backend.repository, backend.repositories, backend.records = repo, []model.Repository{repo}, nil
	if err := New(backend).requireUnusedBranch(context.Background(), repo, "feature", boundary); err != nil {
		t.Fatalf("unlisted branch guard err=%v", err)
	}
	backend.listErrs = []error{os.ErrPermission}
	if err := New(backend).requireUnusedBranch(context.Background(), repo, "feature", boundary); err == nil || !strings.Contains(err.Error(), "inspect remaining") {
		t.Fatalf("unreadable branch guard err=%v", err)
	}
}

type exclusionSpy struct {
	*fakeGit
	diskCalls       int
	diskPaths       []string
	removePaths     []string
	discoveryErrors []error
}

func (f *exclusionSpy) DiscoverExcluding(ctx context.Context, roots, _ []string) ([]model.Repository, []error) {
	repositories, _ := f.Discover(ctx, roots)
	return repositories, f.discoveryErrors
}

func (f *exclusionSpy) DiskUsage(path string) (int64, error) {
	f.mu.Lock()
	f.diskCalls++
	f.diskPaths = append(f.diskPaths, path)
	f.mu.Unlock()
	return f.fakeGit.DiskUsage(path)
}

func (f *exclusionSpy) Remove(ctx context.Context, repo model.Repository, path string) error {
	f.mu.Lock()
	f.removePaths = append(f.removePaths, path)
	f.mu.Unlock()
	return f.fakeGit.Remove(ctx, repo, path)
}

type countingMergeFinder struct{ calls int }

func (f *countingMergeFinder) FindMerged(context.Context, provider.Query) (provider.PullRequest, error) {
	f.calls++
	return provider.PullRequest{}, nil
}
