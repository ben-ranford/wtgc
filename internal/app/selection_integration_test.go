package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ben-ranford/wtgc/internal/gitx"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/testgit"
)

func TestSelectedRealGitMixedRepositoriesLeaveUnselectedAndStale(t *testing.T) {
	first, second := testgit.NewRepository(t), testgit.NewRepository(t)
	a := first.CreateMergedWorktree(t, "selected-a")
	b := second.CreateMergedWorktree(t, "selected-b")
	other := first.CreateMergedWorktree(t, "unselected")
	stale := first.CreateMergedWorktree(t, "stale")
	if err := os.RemoveAll(stale); err != nil {
		t.Fatal(err)
	}
	opts := Options{Roots: []string{first.Root, second.Root}, SelectedPaths: []string{a, b}, DeleteBranch: true}
	application := New(gitx.New("git"))
	dry, err := application.Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if dry.SchemaVersion != "1.1.0" || dry.Summary.PotentialBytes != selectedItemByPath(t, dry, a).DiskBytes+selectedItemByPath(t, dry, b).DiskBytes || selectedItemByPath(t, dry, other).Action != model.ActionKept || selectedItemByPath(t, dry, stale).Action != model.ActionKept {
		t.Fatalf("dry=%+v", dry)
	}
	opts.Execute = true
	inv, err := application.Run(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if inv.Summary.Removed != 2 || inv.Summary.Pruned != 0 || !selectedItemByPath(t, inv, a).BranchDeleted || !selectedItemByPath(t, inv, b).BranchDeleted {
		t.Fatalf("inv=%+v", inv)
	}
	for _, path := range []string{a, b} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("selected path survived: %s %v", path, err)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatal(err)
	}
	if !first.BranchExists(t, "unselected") || !first.BranchExists(t, "stale") || first.BranchExists(t, "selected-a") || second.BranchExists(t, "selected-b") {
		t.Fatal("branch allowlist violated")
	}
	if !strings.Contains(first.RegisteredWorktrees(t), selectedItemByPath(t, dry, stale).Path) {
		t.Fatal("unselected stale registration pruned")
	}
}

func TestSelectedRealGitAdversarialRevalidation(t *testing.T) {
	for _, drift := range []string{"dirty", "lock", "HEAD", "branch", "git link", "protection"} {
		t.Run(drift, func(t *testing.T) {
			repo := testgit.NewRepository(t)
			path := repo.CreateMergedWorktree(t, "selected")
			other := repo.CreateMergedWorktree(t, "unselected")
			protected := repo.Path
			if drift == "protection" {
				protected = filepath.Join(t.TempDir(), "protected")
				selectionSymlink(t, repo.Path, protected)
			}
			opts := Options{Roots: []string{repo.Root}, SelectedPaths: []string{path}, Execute: true, Interactive: true, ProtectedPath: protected}
			opts.ConfirmSelection = func(SelectionPreview) bool {
				switch drift {
				case "dirty":
					testgit.WriteFile(t, filepath.Join(path, "untracked"), "keep me")
				case "lock":
					testgit.Run(t, repo.Path, "worktree", "lock", path)
				case "HEAD":
					testgit.Run(t, path, "reset", "--hard", "main")
				case "branch":
					testgit.Run(t, path, "switch", "-c", "replacement")
				case "git link":
					data, err := os.ReadFile(filepath.Join(other, ".git"))
					if err != nil {
						t.Fatal(err)
					}
					testgit.WriteFile(t, filepath.Join(path, ".git"), string(data))
				case "protection":
					if err := os.Remove(protected); err != nil {
						t.Fatal(err)
					}
					selectionSymlink(t, path, protected)
				}
				return true
			}
			inv, err := New(gitx.New("git")).Run(context.Background(), opts)
			if err == nil || inv.Summary.Removed != 0 || selectedItemByPath(t, inv, path).Action != model.ActionKept {
				t.Fatalf("err=%v inv=%+v", err, inv)
			}
			for _, remaining := range []string{path, other} {
				if _, err := os.Stat(remaining); err != nil {
					t.Fatalf("lost worktree: %v", err)
				}
			}
		})
	}
}

func TestSelectedRealGitPartialCancellation(t *testing.T) {
	repo := testgit.NewRepository(t)
	a, b := repo.CreateMergedWorktree(t, "a"), repo.CreateMergedWorktree(t, "b")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	backend := &selectionHooks{Git: gitx.New("git"), afterRemove: cancel}
	inv, err := New(backend).Run(ctx, Options{Roots: []string{repo.Root}, SelectedPaths: []string{a, b}, Execute: true, DeleteBranch: true})
	if err == nil || inv.Summary.Removed != 1 || !selectedItemByPath(t, inv, a).Removed || selectedItemByPath(t, inv, b).Action != model.ActionKept {
		t.Fatalf("err=%v inv=%+v", err, inv)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Fatalf("completed removal not reflected on disk: %v", err)
	}
	if _, err := os.Stat(b); err != nil {
		t.Fatal(err)
	}
	if !repo.BranchExists(t, "a") || !repo.BranchExists(t, "b") {
		t.Fatal("started branch deletion after cancellation")
	}
}

func TestSelectedRealGitProviderSquashRetainsBranch(t *testing.T) {
	repo, path, head, merge := squashFixture(t, "selected-squash")
	inv, err := New(gitx.New("git")).Run(context.Background(), Options{Roots: []string{repo.Root}, SelectedPaths: []string{path}, Provider: githubProofClient(head, merge, head), Execute: true, DeleteBranch: true})
	if err != nil {
		t.Fatal(err)
	}
	item := selectedItemByPath(t, inv, path)
	if !item.Removed || item.BranchDeleted || !repo.BranchExists(t, "selected-squash") {
		t.Fatalf("item=%+v", item)
	}
}

func TestSelectedRealGitProviderProofDrift(t *testing.T) {
	repo, path, head, merge := squashFixture(t, "selected-drift")
	inv, err := New(gitx.New("git")).Run(context.Background(), Options{Roots: []string{repo.Root}, SelectedPaths: []string{path}, Provider: githubProofClient(head, merge, "changed"), Execute: true, DeleteBranch: true})
	if err == nil || selectedItemByPath(t, inv, path).Removed || selectedItemByPath(t, inv, path).Action != model.ActionKept {
		t.Fatalf("err=%v inv=%+v", err, inv)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if !repo.BranchExists(t, "selected-drift") {
		t.Fatal("branch deleted without fresh proof")
	}
}
