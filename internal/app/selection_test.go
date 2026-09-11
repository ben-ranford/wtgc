package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/cache"
	"github.com/ben-ranford/wtgc/internal/model"
)

func selectionFixture(t *testing.T) (*fakeGit, Options) {
	t.Helper()
	root := t.TempDir()
	common := filepath.Join(root, ".git")
	var records []model.RegisteredWorktree
	for _, name := range []string{"a", "b", "unselected", ".git"} {
		path := filepath.Join(root, name)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if name != ".git" {
			if err := os.WriteFile(filepath.Join(path, ".git"), []byte("gitdir: "+filepath.Join(common, "worktrees", name)), 0o600); err != nil {
				t.Fatal(err)
			}
			records = append(records, model.RegisteredWorktree{Path: path, Branch: name, Head: "abc123"})
		}
	}
	records = append(records, model.RegisteredWorktree{Path: filepath.Join(root, "missing"), Prunable: true})
	backend := newFakeGit(records...)
	backend.repository = model.Repository{PrimaryPath: root, CommonDir: common}
	backend.repositories = []model.Repository{backend.repository}
	backend.clean, backend.ancestor, backend.remote = []bool{true}, true, true
	opts := Options{Roots: []string{root}, SelectedPaths: []string{records[0].Path, records[1].Path}, Execute: true, CacheScanner: func(context.Context, string, int64) []cache.Warning { return nil }}
	return backend, opts
}

func TestSelectionRejectsWholeInvalidSet(t *testing.T) {
	for _, name := range []string{"empty", "missing", "unknown", "duplicate", "alias duplicate", "ambiguous", "prunable", "dirty", "locked", "no HEAD", "scan error", "common missing", "registration missing", "not directory"} {
		t.Run(name, func(t *testing.T) {
			backend, opts := selectionFixture(t)
			switch name {
			case "empty":
				opts.SelectedPaths[1] = ""
			case "missing":
				opts.SelectedPaths[1] = backend.records[3].Path
			case "unknown":
				opts.SelectedPaths[1] = t.TempDir()
			case "duplicate":
				opts.SelectedPaths[1] = opts.SelectedPaths[0]
			case "alias duplicate":
				alias := filepath.Join(t.TempDir(), "alias")
				selectionSymlink(t, opts.SelectedPaths[0], alias)
				opts.SelectedPaths[1] = alias
			case "ambiguous":
				backend.records = append(backend.records, backend.records[1])
			case "prunable":
				backend.records[1].Prunable = true
			case "dirty":
				backend.cleanByPath = map[string]bool{backend.records[0].Path: true}
			case "locked":
				backend.records[1].Locked = true
			case "no HEAD":
				backend.records[1].Head = ""
			case "scan error":
				backend.listErrs = []error{errors.New("unreadable registrations")}
			case "not directory":
				path := backend.records[1].Path
				if err := os.RemoveAll(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("not a worktree"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "registration missing":
				if err := os.Remove(filepath.Join(backend.records[1].Path, ".git")); err != nil {
					t.Fatal(err)
				}
			case "common missing":
				backend.repositories[0].CommonDir = filepath.Join(t.TempDir(), "missing")
			}
			calls := 0
			opts.Interactive, opts.DeleteBranch = true, true
			opts.ConfirmSelection = func(SelectionPreview) bool { calls++; return true }
			inv, err := New(backend).Run(context.Background(), opts)
			if err == nil || calls != 0 || backend.removeCalls != 0 || backend.pruneCalls != 0 || backend.deleteCalls != 0 {
				t.Fatalf("err=%v confirms=%d mutations=%d/%d/%d", err, calls, backend.removeCalls, backend.pruneCalls, backend.deleteCalls)
			}
			if inv.Summary.PotentialBytes != 0 {
				t.Fatalf("invalid set claims reclaimable bytes: %+v", inv.Summary)
			}
			for _, item := range inv.Worktrees {
				if item.Action != model.ActionKept {
					t.Fatalf("invalid set action=%+v", item)
				}
			}
		})
	}
}

func TestSelectionPreviewAndFrozenInputs(t *testing.T) {
	for _, execute := range []bool{false, true} {
		t.Run(map[bool]string{false: "dry", true: "execute"}[execute], func(t *testing.T) {
			backend, opts := selectionFixture(t)
			wantPaths := append([]string(nil), opts.SelectedPaths...)
			wantRoots := append([]string(nil), opts.Roots...)
			calls := 0
			now := time.Now().UTC()
			opts.Execute, opts.Interactive = execute, true
			opts.Now = func() time.Time {
				opts.Roots[0], opts.SelectedPaths[0] = "changed root", "changed selection"
				opts.Execute, opts.DeleteBranch, opts.Retention = false, true, time.Hour
				return now
			}
			opts.Confirm = func(model.Worktree) bool { t.Fatal("per-row confirmation called"); return false }
			opts.ConfirmSelection = func(p SelectionPreview) bool {
				calls++
				for i := range wantPaths {
					wantPaths[i] = canonicalPath(wantPaths[i])
				}
				if p.Count != 2 || p.ReclaimableBytes != 4096 || !reflect.DeepEqual(p.Paths, wantPaths) {
					t.Fatalf("preview=%+v", p)
				}
				p.Paths[0] = backend.records[2].Path
				return true
			}
			inv, err := New(backend).Run(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(inv.Roots, wantRoots) || !inv.GeneratedAt.Equal(now) {
				t.Fatalf("snapshot=%+v", inv)
			}
			expected := 0
			if execute {
				expected = 2
			}
			if backend.removeCalls != expected || backend.pruneCalls != 0 || backend.deleteCalls != 0 {
				t.Fatalf("mutations=%d/%d/%d", backend.removeCalls, backend.pruneCalls, backend.deleteCalls)
			}
			if calls != expected/2 || inv.Summary.PotentialBytes != 4096 || inv.Summary.Safe != 3 {
				t.Fatalf("calls=%d summary=%+v", calls, inv.Summary)
			}
			for _, index := range []int{2, 3} {
				if item := selectedItemByPath(t, inv, backend.records[index].Path); item.Action != model.ActionKept || item.Removed {
					t.Fatalf("unselected=%+v", item)
				}
			}
		})
	}
}

func TestSelectionCancellation(t *testing.T) {
	for _, stage := range []string{"before confirm", "declined", "nil confirmer", "confirm", "revalidation", "after remove"} {
		t.Run(stage, func(t *testing.T) {
			fake, opts := selectionFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			backend := &selectionHooks{Git: fake}
			opts.Interactive, opts.DeleteBranch = true, true
			confirms := 0
			opts.ConfirmSelection = func(SelectionPreview) bool {
				confirms++
				if stage == "confirm" {
					cancel()
				}
				return stage != "declined"
			}
			switch stage {
			case "before confirm":
				cancel()
			case "nil confirmer":
				opts.ConfirmSelection = nil
			case "revalidation":
				fake.cleanHook = func(call int) {
					if call > 3 {
						cancel()
					}
				}
			case "after remove":
				backend.afterRemove = cancel
			}
			inv, err := New(backend).Run(ctx, opts)
			want := 0
			if stage == "after remove" {
				want = 1
			}
			if fake.removeCalls != want || fake.deleteCalls != 0 || fake.pruneCalls != 0 || inv.Summary.Removed != want {
				t.Fatalf("removals=%d deletes=%d prunes=%d summary=%+v", fake.removeCalls, fake.deleteCalls, fake.pruneCalls, inv.Summary)
			}
			if stage == "before confirm" && confirms != 0 {
				t.Fatal("prompted canceled operation")
			}
			if stage != "declined" && stage != "nil confirmer" {
				if err == nil || !strings.Contains(strings.Join(inv.Errors, " "), "not rolled back") {
					t.Fatalf("cancellation not reported: err=%v inv=%+v", err, inv)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			for _, item := range inv.Worktrees {
				if !item.Removed && item.Action != model.ActionKept {
					t.Fatalf("pending action after cancellation: %+v", item)
				}
			}
		})
	}
}

type selectionHooks struct {
	Git
	afterRemove func()
	discover    func(context.Context, []string) ([]model.Repository, []error)
	list        func(context.Context, model.Repository) ([]model.RegisteredWorktree, error)
}

func (h *selectionHooks) Remove(ctx context.Context, repo model.Repository, path string) error {
	err := h.Git.Remove(ctx, repo, path)
	if h.afterRemove != nil {
		h.afterRemove()
	}
	return err
}
func (h *selectionHooks) Discover(ctx context.Context, roots []string) ([]model.Repository, []error) {
	if h.discover != nil {
		return h.discover(ctx, roots)
	}
	return h.Git.Discover(ctx, roots)
}

func TestSelectedIdentityAndSafetyDrift(t *testing.T) {
	for _, drift := range []string{"HEAD", "branch", "path", "duplicate", "missing", "prunable", "dirty", "locked", "default", "list error", "default error", "common", "repository", "discovery error", "symlink", "retention", "remove error"} {
		t.Run(drift, func(t *testing.T) {
			fake, opts := selectionFixture(t)
			opts.SelectedPaths = opts.SelectedPaths[:1]
			backend := &selectionHooks{Git: fake}
			opts.Interactive = true
			opts.ConfirmSelection = func(SelectionPreview) bool {
				switch drift {
				case "HEAD":
					fake.records[0].Head = "changed"
				case "branch":
					fake.records[0].Branch = "changed"
				case "path":
					alias := filepath.Join(t.TempDir(), "alias")
					selectionSymlink(t, fake.records[0].Path, alias)
					fake.records[0].Path = alias
				case "duplicate":
					fake.records = append(fake.records, fake.records[0])
				case "missing":
					fake.records = fake.records[1:]
				case "prunable":
					fake.records[0].Prunable = true
				case "dirty":
					fake.clean = []bool{false}
				case "locked":
					fake.records[0].Locked = true
				case "default":
					fake.defaultBranches = []string{"other"}
				case "list error":
					fake.listErrs = []error{errors.New("list failed")}
				case "default error":
					fake.defaultErr = errors.New("default failed")
				case "common":
					fake.repositories[0].CommonDir = t.TempDir()
				case "repository":
					fake.repositories[0].PrimaryPath = t.TempDir()
				case "discovery error":
					backend.discover = func(context.Context, []string) ([]model.Repository, []error) {
						return nil, []error{errors.New("discovery failed")}
					}
				case "symlink":
					path := fake.records[0].Path
					if err := os.RemoveAll(path); err != nil {
						t.Fatal(err)
					}
					selectionSymlink(t, fake.records[2].Path, path)
				case "retention":
					later := time.Now().Add(time.Minute)
					if err := os.Chtimes(fake.records[0].Path, later, later); err != nil {
						t.Fatal(err)
					}
				case "remove error":
					fake.removeErr = errors.New("remove failed")
				}
				return true
			}
			if drift == "retention" {
				old := time.Now().Add(-2 * time.Hour)
				if err := os.Chtimes(filepath.Join(fake.records[0].Path, ".git"), old, old); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(fake.records[0].Path, old, old); err != nil {
					t.Fatal(err)
				}
				opts.Retention = time.Hour
			}
			inv, err := New(backend).Run(context.Background(), opts)
			wantCalls := 0
			if drift == "remove error" {
				wantCalls = 1
			}
			if err == nil || fake.removeCalls != wantCalls || inv.Summary.Removed != 0 || inv.Summary.PotentialBytes != 0 || fake.pruneCalls != 0 {
				t.Fatalf("err=%v calls=%d inv=%+v", err, fake.removeCalls, inv)
			}
		})
	}
}

func TestBindSelectionRejectsAmbiguousRepository(t *testing.T) {
	backend, opts := selectionFixture(t)
	inv, err := New(backend).Run(context.Background(), Options{Roots: opts.Roots})
	if err != nil {
		t.Fatal(err)
	}
	for _, repos := range [][]model.Repository{nil, {backend.repository, backend.repository}} {
		if _, err := bindSelection(repos, opts.SelectedPaths, &inv); err == nil {
			t.Fatal("unbound repository accepted")
		}
	}
}

func TestSelectedAliasBindsToLiveCanonicalPath(t *testing.T) {
	backend, opts := selectionFixture(t)
	selected := backend.records[0].Path
	alias := filepath.Join(t.TempDir(), "alias")
	selectionSymlink(t, selected, alias)
	opts.SelectedPaths = []string{alias}
	inv, err := New(backend).Run(context.Background(), opts)
	if err != nil || backend.removeCalls != 1 || !selectedItemByPath(t, inv, selected).Removed || selectedItemByPath(t, inv, backend.records[1].Path).Removed {
		t.Fatalf("err=%v inv=%+v", err, inv)
	}
}

func selectedItemByPath(t *testing.T, inv model.Inventory, path string) model.Worktree {
	t.Helper()
	// Resolve the still-live parent: removed paths must not all compare equal
	// just because EvalSymlinks returns an empty string with an error.
	normalized := func(path string) string {
		parent, err := filepath.EvalSymlinks(filepath.Dir(path))
		if err != nil {
			t.Fatal(err)
		}
		return filepath.Join(parent, filepath.Base(path))
	}
	wanted := normalized(path)
	for _, item := range inv.Worktrees {
		if normalized(item.Path) == wanted {
			return item
		}
	}
	t.Fatalf("missing exact path %s in %+v", path, inv.Worktrees)
	return model.Worktree{}
}

func selectionSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink capability unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

func TestSelectedBranchDeletionUsesPostRemovalRegistrations(t *testing.T) {
	for _, state := range []string{"unused", "new checkout", "stale checkout", "list failure", "canceled list"} {
		t.Run(state, func(t *testing.T) {
			fake, opts := selectionFixture(t)
			opts.SelectedPaths, opts.DeleteBranch = opts.SelectedPaths[:1], true
			selected := fake.records[0]
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			afterRemoval := false
			backend := &selectionHooks{Git: fake, afterRemove: func() {
				afterRemoval = true
				fake.records = fake.records[1:] // Mirror successful removal from Git's live list.
				switch state {
				case "new checkout", "stale checkout":
					fake.records = append(fake.records, model.RegisteredWorktree{Path: "new checkout", Branch: selected.Branch, Head: selected.Head, Prunable: state == "stale checkout"})
				case "list failure":
					fake.listErrs = []error{errors.New("registration list unavailable")}
				}
			}}
			backend.list = func(ctx context.Context, repo model.Repository) ([]model.RegisteredWorktree, error) {
				records, err := fake.List(ctx, repo)
				if afterRemoval && state == "canceled list" {
					cancel()
				}
				return records, err
			}
			inv, err := New(backend).Run(ctx, opts)
			item := selectedItemByPath(t, inv, selected.Path)
			if !item.Removed || inv.Summary.Removed != 1 || fake.removeCalls != 1 || fake.pruneCalls != 0 {
				t.Fatalf("inv=%+v", inv)
			}
			if state == "unused" {
				if err != nil || !item.BranchDeleted || fake.deleteCalls != 1 {
					t.Fatalf("err=%v item=%+v deletes=%d", err, item, fake.deleteCalls)
				}
			} else {
				if err == nil || item.BranchDeleted || fake.deleteCalls != 0 || item.Action != model.ActionRemoved || !strings.Contains(item.Error, "branch retained") {
					t.Fatalf("err=%v item=%+v deletes=%d", err, item, fake.deleteCalls)
				}
				if state == "list failure" && !strings.Contains(item.Error, "registration list unavailable") {
					t.Fatalf("lost listing evidence: %+v", item)
				}
			}
		})
	}
}

func (h *selectionHooks) List(ctx context.Context, repo model.Repository) ([]model.RegisteredWorktree, error) {
	if h.list != nil {
		return h.list(ctx, repo)
	}
	return h.Git.List(ctx, repo)
}

func TestPickedSelectionKeepsDisplayedIdentityAndPreflightsWholeSet(t *testing.T) {
	for _, drift := range []string{"none", "second HEAD"} {
		t.Run(drift, func(t *testing.T) {
			backend, inv, err, seen := runPickedSelection(t, drift)
			if seen != 1 {
				t.Fatalf("picker calls=%d", seen)
			}
			assertPickedSelectionResult(t, drift, backend, inv, err)
		})
	}
}

func TestPickerFailureRejectsCleanupWithoutTreatingItAsEmptyChoice(t *testing.T) {
	backend, opts := selectionFixture(t)
	opts.SelectedPaths = nil
	opts.Pick = func(PickPreview) PickResult { return PickResult{Err: errors.New("input failed")} }
	inv, err := New(backend).Run(context.Background(), opts)
	if err == nil || backend.removeCalls != 0 || !strings.Contains(strings.Join(inv.Errors, " "), "picker: input failed") {
		t.Fatalf("err=%v removes=%d inventory=%+v", err, backend.removeCalls, inv)
	}
}

func TestPickerRejectsSelectionWhenAnotherRepositoryScanFails(t *testing.T) {
	backend, opts := selectionFixture(t)
	other := model.Repository{PrimaryPath: filepath.Join(t.TempDir(), "other"), CommonDir: filepath.Join(t.TempDir(), "other.git")}
	backend.repositories = append(backend.repositories, other)
	hooks := &selectionHooks{Git: backend, list: func(_ context.Context, repo model.Repository) ([]model.RegisteredWorktree, error) {
		if repo.PrimaryPath == other.PrimaryPath {
			return nil, errors.New("other repository scan failed")
		}
		return append([]model.RegisteredWorktree(nil), backend.records...), nil
	}}
	opts.SelectedPaths = nil
	opts.Pick = func(PickPreview) PickResult { return PickResult{Selected: []int{1}} }
	inv, err := New(hooks).Run(context.Background(), opts)
	if err == nil || backend.removeCalls != 0 || !strings.Contains(strings.Join(inv.Errors, " "), "picker selection rejected because the scan contains errors") {
		t.Fatalf("err=%v removes=%d inventory=%+v", err, backend.removeCalls, inv)
	}
}

func TestPickerEmptyAndInvalidSelectionsDoNotAuthorizeRemoval(t *testing.T) {
	for _, selected := range [][]int{nil, {0}, {1, 1}, {4}} {
		t.Run(fmt.Sprint(selected), func(t *testing.T) {
			backend, opts := selectionFixture(t)
			opts.SelectedPaths = nil
			opts.Pick = func(PickPreview) PickResult { return PickResult{Selected: selected} }
			inv, err := New(backend).Run(context.Background(), opts)
			if backend.removeCalls != 0 {
				t.Fatalf("picker selection removed worktrees: %d", backend.removeCalls)
			}
			if len(selected) == 0 {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(strings.Join(inv.Errors, " "), "picker returned an invalid selection") {
				t.Fatalf("err=%v inventory=%+v", err, inv)
			}
		})
	}
}

func TestPickerBindingAndPreflightRefusalsKeepWorktrees(t *testing.T) {
	t.Run("common directory unavailable", func(t *testing.T) {
		backend, opts := selectionFixture(t)
		inv, err := New(backend).Run(context.Background(), Options{Roots: opts.Roots})
		if err != nil {
			t.Fatal(err)
		}
		backend.repositories[0].CommonDir = filepath.Join(t.TempDir(), "missing")
		if _, err := bindSelectedWorktree(backend.repositories, inv.Worktrees[0], 0); err == nil || !strings.Contains(err.Error(), "common directory") {
			t.Fatalf("binding error=%v", err)
		}
		opts.SelectedPaths = nil
		opts.Pick = func(preview PickPreview) PickResult {
			if preview.Rows[0].Selectable {
				t.Fatalf("row remained selectable: %+v", preview.Rows[0])
			}
			return PickResult{Selected: []int{1}}
		}
		inv, err = New(backend).Run(context.Background(), opts)
		if err == nil || backend.removeCalls != 0 || !strings.Contains(strings.Join(inv.Errors, " "), "picker returned an invalid selection") {
			t.Fatalf("err=%v removes=%d inventory=%+v", err, backend.removeCalls, inv)
		}
	})

	t.Run("displayed path disappears before binding", func(t *testing.T) {
		backend, opts := selectionFixture(t)
		inv, err := New(backend).Run(context.Background(), Options{Roots: opts.Roots})
		if err != nil {
			t.Fatal(err)
		}
		item := inv.Worktrees[0]
		item.Path = filepath.Join(t.TempDir(), "missing")
		if _, err := bindSelectedWorktree(backend.repositories, item, 0); err == nil {
			t.Fatal("missing displayed path was bound")
		}
	})

	t.Run("preflight exclusion membership cannot be proven", func(t *testing.T) {
		backend, opts := selectionFixture(t)
		inv, err := New(backend).Run(context.Background(), Options{Roots: opts.Roots})
		if err != nil {
			t.Fatal(err)
		}
		bound, err := bindSelectedWorktree(backend.repositories, inv.Worktrees[0], 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.RemoveAll(inv.Worktrees[0].Path); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(inv.Worktrees[0].Path, []byte("no longer a worktree"), 0o600); err != nil {
			t.Fatal(err)
		}
		opts.exclusions = exclusionBoundary{values: []string{opts.Roots[0]}}
		if err := New(backend).preflightSelectedSet(context.Background(), []selectedWorktree{bound}, opts, &inv); err == nil || !strings.Contains(err.Error(), "selected exclusion membership") {
			t.Fatalf("preflight error=%v", err)
		}
	})

	t.Run("preflight excludes displayed path", func(t *testing.T) {
		backend, opts := selectionFixture(t)
		inv, err := New(backend).Run(context.Background(), Options{Roots: opts.Roots})
		if err != nil {
			t.Fatal(err)
		}
		bound, err := bindSelectedWorktree(backend.repositories, inv.Worktrees[0], 0)
		if err != nil {
			t.Fatal(err)
		}
		opts.exclusions, err = newExclusionBoundary([]string{inv.Worktrees[0].Path})
		if err != nil {
			t.Fatal(err)
		}
		if err := New(backend).preflightSelectedSet(context.Background(), []selectedWorktree{bound}, opts, &inv); err == nil || !strings.Contains(err.Error(), "selected path is excluded") {
			t.Fatalf("preflight error=%v", err)
		}
	})
}

func TestSelectedBranchRemainsWhenExcludedRegistrationStillExists(t *testing.T) {
	backend, _ := selectionFixture(t)
	exclusions, err := newExclusionBoundary([]string{backend.records[0].Path})
	if err != nil {
		t.Fatal(err)
	}
	err = New(backend).requireUnusedBranch(context.Background(), backend.repository, backend.records[0].Branch, exclusions)
	if err == nil || !strings.Contains(err.Error(), "excluded registration remains") {
		t.Fatalf("branch guard error=%v", err)
	}
}

func runPickedSelection(t *testing.T, drift string) (*fakeGit, model.Inventory, error, int) {
	t.Helper()
	backend, opts := selectionFixture(t)
	opts.SelectedPaths = nil
	opts.Execute, opts.Interactive = true, true
	seen := 0
	opts.Pick = func(preview PickPreview) PickResult {
		assertPickRows(t, preview.Rows)
		seen++
		return PickResult{Selected: []int{1, 2}}
	}
	opts.ConfirmSelection = func(SelectionPreview) bool {
		if drift == "second HEAD" {
			backend.records[1].Head = "changed"
		}
		return true
	}
	inv, err := New(backend).Run(context.Background(), opts)
	return backend, inv, err, seen
}

func assertPickRows(t *testing.T, rows []PickRow) {
	t.Helper()
	if len(rows) != 4 || !rows[0].Selectable || !rows[1].Selectable || rows[2].Selectable || !rows[3].Selectable {
		t.Fatalf("rows=%+v", rows)
	}
}

func assertPickedSelectionResult(t *testing.T, drift string, backend *fakeGit, inv model.Inventory, err error) {
	t.Helper()
	if drift == "second HEAD" {
		if err == nil || backend.removeCalls != 0 || inv.Summary.Removed != 0 {
			t.Fatalf("whole-set drift mutated: err=%v removes=%d inv=%+v", err, backend.removeCalls, inv)
		}
		return
	}
	if err != nil || backend.removeCalls != 2 || inv.Summary.Removed != 2 {
		t.Fatalf("picked cleanup err=%v removes=%d inv=%+v", err, backend.removeCalls, inv)
	}
}
