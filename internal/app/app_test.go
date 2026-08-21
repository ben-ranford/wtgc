package app

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/ben-ranford/wtgc/internal/model"
)

func TestClassificationsFailClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		record     model.RegisteredWorktree
		clean      bool
		ancestor   bool
		remote     bool
		want       model.Classification
		wantReason string
	}{
		{name: "primary", record: record("main"), clean: true, ancestor: true, remote: true, want: model.Kept, wantReason: "primary"},
		{name: "prunable", record: model.RegisteredWorktree{Path: "/missing", Prunable: true}, want: model.Prunable, wantReason: "prunable"},
		{name: "detached", record: detachedRecord(), clean: true, ancestor: true, remote: true, want: model.Kept, wantReason: "detached"},
		{name: "locked", record: lockedRecord(), clean: true, ancestor: true, remote: true, want: model.Kept, wantReason: "locked"},
		{name: "default branch", record: branchRecord("main"), clean: true, ancestor: true, remote: true, want: model.Kept, wantReason: "default-branch"},
		{name: "unmerged", record: branchRecord("feature"), clean: true, ancestor: false, remote: true, want: model.Unmerged, wantReason: "not reachable"},
		{name: "merged dirty", record: branchRecord("feature"), clean: false, ancestor: true, remote: true, want: model.MergedButDirty, wantReason: "changes exist"},
		{name: "not pushed", record: branchRecord("feature"), clean: true, ancestor: true, remote: false, want: model.Unmerged, wantReason: "remote-tracking"},
		{name: "safe", record: branchRecord("feature"), clean: true, ancestor: true, remote: true, want: model.SafeToRemove, wantReason: "reachable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newFakeGit(tt.record)
			backend.clean = []bool{tt.clean}
			backend.ancestor = tt.ancestor
			backend.remote = tt.remote
			inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got := inventory.Worktrees[0].Classification; got != tt.want {
				t.Fatalf("classification = %q, want %q", got, tt.want)
			}
			if got := inventory.Worktrees[0].Reason; !contains(got, tt.wantReason) {
				t.Fatalf("reason = %q, want substring %q", got, tt.wantReason)
			}
			if backend.removeCalls != 0 || backend.pruneCalls != 0 {
				t.Fatal("dry-run mutated repository")
			}
		})
	}
}

func TestExecuteRevalidatesBeforeRemoval(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, false}
	backend.ancestor = true
	backend.remote = true

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.removeCalls != 0 {
		t.Fatalf("Remove() calls = %d, want 0", backend.removeCalls)
	}
	if got := inventory.Worktrees[0].Classification; got != model.MergedButDirty {
		t.Fatalf("classification = %q, want %q", got, model.MergedButDirty)
	}
	if !contains(inventory.Worktrees[0].Reason, "revalidation blocked") {
		t.Fatalf("reason = %q, want revalidation block", inventory.Worktrees[0].Reason)
	}
}

func TestExecuteRemovesThenOptionallyDeletesBranch(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, true}
	backend.ancestor = true
	backend.remote = true

	inventory, err := New(backend).Run(context.Background(), Options{
		Roots:        []string{"/scan"},
		Execute:      true,
		DeleteBranch: true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.removeCalls != 1 || backend.deleteCalls != 1 {
		t.Fatalf("remove/delete calls = %d/%d, want 1/1", backend.removeCalls, backend.deleteCalls)
	}
	if !inventory.Worktrees[0].Removed || !inventory.Worktrees[0].BranchDeleted {
		t.Fatalf("result = %+v, want removed branch", inventory.Worktrees[0])
	}
	if backend.pruneCalls != 0 {
		t.Fatalf("Prune() calls = %d, want 0 without an accepted stale record", backend.pruneCalls)
	}
}

func TestErrorsKeepWorktree(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.cleanErr = errors.New("status failed")
	backend.ancestor = true
	backend.remote = true

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}})
	if err == nil {
		t.Fatal("classification command failure should produce a non-zero run result")
	}
	if got := inventory.Worktrees[0].Classification; got != model.Error {
		t.Fatalf("classification = %q, want %q", got, model.Error)
	}
	if backend.removeCalls != 0 {
		t.Fatal("ambiguous worktree was removed")
	}
}

func TestNoRepositoriesReturnsDiscoveryError(t *testing.T) {
	t.Parallel()
	backend := newFakeGit()
	backend.repositories = nil

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}})
	if err == nil {
		t.Fatal("Run() error = nil, want no repositories error")
	}
	if inventory.Summary.Repositories != 0 || len(inventory.Worktrees) != 0 {
		t.Fatalf("inventory = %+v, want no repositories or worktrees", inventory)
	}
}

func TestDefaultBranchErrorPreventsUnsafeClassification(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.defaultErr = errors.New("remote head missing")

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want default branch error")
	}
	if len(inventory.Worktrees) != 0 {
		t.Fatalf("worktrees = %#v, want scan to stop before classification", inventory.Worktrees)
	}
	if backend.removeCalls != 0 || backend.pruneCalls != 0 {
		t.Fatalf("mutating calls remove/prune = %d/%d, want 0/0", backend.removeCalls, backend.pruneCalls)
	}
}

func TestListErrorPreventsUnsafeClassification(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.listErrs = []error{errors.New("worktree list failed")}

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want list error")
	}
	if len(inventory.Worktrees) != 0 {
		t.Fatalf("worktrees = %#v, want no classifications from failed list", inventory.Worktrees)
	}
	if backend.removeCalls != 0 || backend.pruneCalls != 0 {
		t.Fatalf("mutating calls remove/prune = %d/%d, want 0/0", backend.removeCalls, backend.pruneCalls)
	}
}

func TestDiskUsageErrorKeepsWorktree(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.diskErr = errors.New("permission denied")

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want disk usage error")
	}
	if got := inventory.Worktrees[0].Classification; got != model.Error {
		t.Fatalf("classification = %q, want %q", got, model.Error)
	}
	if backend.removeCalls != 0 {
		t.Fatal("worktree with unknown disk state was removed")
	}
}

func TestProtectedPathKeepsContainingWorktree(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))

	inventory, err := New(backend).Run(context.Background(), Options{
		Roots:         []string{"/scan"},
		Execute:       true,
		ProtectedPath: "/repo-worktrees/feature/subdir/process",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := inventory.Worktrees[0].Classification; got != model.Kept {
		t.Fatalf("classification = %q, want %q", got, model.Kept)
	}
	if backend.removeCalls != 0 {
		t.Fatal("protected worktree was removed")
	}
}

func TestInteractiveRejectionKeepsSafeWorktree(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true}
	backend.ancestor = true
	backend.remote = true

	inventory, err := New(backend).Run(context.Background(), Options{
		Roots:       []string{"/scan"},
		Execute:     true,
		Interactive: true,
		Confirm:     func(model.Worktree) bool { return false },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := inventory.Worktrees[0].Classification; got != model.Kept {
		t.Fatalf("classification = %q, want %q", got, model.Kept)
	}
	if backend.removeCalls != 0 {
		t.Fatal("interactively rejected worktree was removed")
	}
}

func TestExecutePrunesAcceptedPrunableMetadata(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(model.RegisteredWorktree{Path: "/repo-worktrees/missing", Prunable: true})
	backend.listResults = [][]model.RegisteredWorktree{
		{{Path: "/repo-worktrees/missing", Prunable: true}},
		{{Path: "/repo-worktrees/missing", Prunable: true}},
	}

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.pruneCalls != 1 {
		t.Fatalf("Prune() calls = %d, want 1", backend.pruneCalls)
	}
	if !inventory.Worktrees[0].Removed {
		t.Fatalf("removed = false, want prunable metadata marked removed")
	}
}

func TestPrunableRevalidationBlocksWhenMetadataChanges(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(model.RegisteredWorktree{Path: "/repo-worktrees/missing", Prunable: true})
	backend.listResults = [][]model.RegisteredWorktree{
		{{Path: "/repo-worktrees/missing", Prunable: true}},
		{{Path: "/repo-worktrees/missing"}},
	}

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.pruneCalls != 0 {
		t.Fatal("prune ran after prunable metadata changed")
	}
	if got := inventory.Worktrees[0].Classification; got != model.Kept {
		t.Fatalf("classification = %q, want %q", got, model.Kept)
	}
	if !contains(inventory.Worktrees[0].Reason, "revalidation blocked prune") {
		t.Fatalf("reason = %q, want revalidation block", inventory.Worktrees[0].Reason)
	}
}

func TestPrunableRevalidationErrorFailsClosed(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(model.RegisteredWorktree{Path: "/repo-worktrees/missing", Prunable: true})
	backend.listErrs = []error{nil, errors.New("list disappeared")}

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want revalidation error")
	}
	if got := inventory.Worktrees[0].Classification; got != model.Error {
		t.Fatalf("classification = %q, want %q", got, model.Error)
	}
	if backend.pruneCalls != 0 {
		t.Fatal("prune ran after prunable revalidation error")
	}
}

func TestPruneErrorMarksAcceptedPrunable(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(model.RegisteredWorktree{Path: "/repo-worktrees/missing", Prunable: true})
	backend.pruneErr = errors.New("prune failed")

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want prune error")
	}
	if inventory.Worktrees[0].Removed {
		t.Fatal("prunable metadata marked removed after prune failed")
	}
	if inventory.Worktrees[0].Error != "prune failed" {
		t.Fatalf("worktree error = %q, want prune failed", inventory.Worktrees[0].Error)
	}
}

func TestRemoveErrorSkipsBranchDeletion(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, true}
	backend.ancestor = true
	backend.remote = true
	backend.removeErr = errors.New("remove failed")

	inventory, err := New(backend).Run(context.Background(), Options{
		Roots:        []string{"/scan"},
		Execute:      true,
		DeleteBranch: true,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want remove error")
	}
	if inventory.Worktrees[0].Removed {
		t.Fatal("worktree marked removed after remove failed")
	}
	if backend.deleteCalls != 0 {
		t.Fatal("branch delete ran after remove failed")
	}
}

func TestDeleteBranchErrorRetainsBranch(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, true}
	backend.ancestor = true
	backend.remote = true
	backend.deleteErr = errors.New("branch not merged")

	inventory, err := New(backend).Run(context.Background(), Options{
		Roots:        []string{"/scan"},
		Execute:      true,
		DeleteBranch: true,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want branch delete error")
	}
	if !inventory.Worktrees[0].Removed {
		t.Fatal("worktree should remain marked removed after branch delete failure")
	}
	if inventory.Worktrees[0].BranchDeleted {
		t.Fatal("branch marked deleted after delete failure")
	}
	if !contains(inventory.Worktrees[0].Error, "branch retained") {
		t.Fatalf("worktree error = %q, want branch retained", inventory.Worktrees[0].Error)
	}
}

type fakeGit struct {
	mu           sync.Mutex
	repository   model.Repository
	repositories []model.Repository
	records      []model.RegisteredWorktree
	listResults  [][]model.RegisteredWorktree
	listErrs     []error
	clean        []bool
	cleanErr     error
	defaultErr   error
	ancestor     bool
	remote       bool
	diskErr      error
	removeErr    error
	pruneErr     error
	deleteErr    error
	removeCalls  int
	pruneCalls   int
	deleteCalls  int
}

func newFakeGit(records ...model.RegisteredWorktree) *fakeGit {
	return &fakeGit{
		repository:   model.Repository{CommonDir: "/repo/.git", PrimaryPath: "/repo"},
		repositories: []model.Repository{{CommonDir: "/repo/.git", PrimaryPath: "/repo"}},
		records:      records,
	}
}

func (f *fakeGit) Discover(context.Context, []string) ([]model.Repository, []error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.Repository(nil), f.repositories...), nil
}
func (f *fakeGit) List(context.Context, model.Repository) ([]model.RegisteredWorktree, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var err error
	if len(f.listErrs) > 0 {
		err = f.listErrs[0]
		f.listErrs = f.listErrs[1:]
	}
	if err != nil {
		return nil, err
	}
	if len(f.listResults) > 0 {
		records := append([]model.RegisteredWorktree(nil), f.listResults[0]...)
		f.listResults = f.listResults[1:]
		return records, nil
	}
	return append([]model.RegisteredWorktree(nil), f.records...), nil
}
func (f *fakeGit) DefaultBranch(context.Context, model.Repository) (string, error) {
	if f.defaultErr != nil {
		return "", f.defaultErr
	}
	return "main", nil
}
func (f *fakeGit) IsClean(context.Context, string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cleanErr != nil {
		return false, f.cleanErr
	}
	if len(f.clean) == 0 {
		return false, nil
	}
	value := f.clean[0]
	if len(f.clean) > 1 {
		f.clean = f.clean[1:]
	}
	return value, nil
}
func (f *fakeGit) IsAncestor(context.Context, model.Repository, string, string) (bool, error) {
	return f.ancestor, nil
}
func (f *fakeGit) RemoteContains(context.Context, model.Repository, string) (bool, error) {
	return f.remote, nil
}
func (f *fakeGit) DiskUsage(string) (int64, error) {
	if f.diskErr != nil {
		return 0, f.diskErr
	}
	return 2048, nil
}
func (f *fakeGit) Remove(context.Context, model.Repository, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeCalls++
	return f.removeErr
}
func (f *fakeGit) Prune(context.Context, model.Repository) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneCalls++
	return f.pruneErr
}
func (f *fakeGit) DeleteBranch(context.Context, model.Repository, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	return f.deleteErr
}

func record(branch string) model.RegisteredWorktree {
	r := branchRecord(branch)
	r.Primary = true
	return r
}

func branchRecord(branch string) model.RegisteredWorktree {
	return model.RegisteredWorktree{Path: "/repo-worktrees/" + branch, Head: "abc123", Branch: branch}
}

func detachedRecord() model.RegisteredWorktree {
	return model.RegisteredWorktree{Path: "/repo-worktrees/detached", Head: "abc123", Detached: true}
}

func lockedRecord() model.RegisteredWorktree {
	r := branchRecord("feature")
	r.Locked = true
	return r
}

func contains(value, substring string) bool {
	return len(substring) == 0 || (len(value) >= len(substring) && find(value, substring))
}

func find(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
