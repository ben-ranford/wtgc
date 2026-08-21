package integration_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/testgit"
)

var (
	wtgcBuildOnce sync.Once
	wtgcBuildPath string
	wtgcBuildErr  error
)

func TestCleanDryRunDoesNotRemoveSafeMergedWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/dry-run")

	inv := runWTGC(t, repo.Root, "clean", "--json", "--scan-root", repo.Root)

	if !inv.DryRun {
		t.Fatal("DryRun = false, want true")
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("safe worktree was mutated during dry-run: %v", err)
	}
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.SafeToRemove {
		t.Fatalf("classification = %q, want %q", item.Classification, model.SafeToRemove)
	}
	if item.Removed {
		t.Fatal("Removed = true in dry-run, want false")
	}
}

func TestCleanYesRemovesCleanMergedWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/remove-clean")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree exists after cleanup or stat failed unexpectedly: %v", err)
	}
	item := requireWorktree(t, inv, worktree)
	if !item.Removed {
		t.Fatal("Removed = false, want true")
	}
}

func TestCleanYesRetainsBranchByDefault(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/retain-branch")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	item := requireWorktree(t, inv, worktree)
	if item.BranchDeleted {
		t.Fatal("BranchDeleted = true without --delete-branch, want false")
	}
	if !repo.BranchExists(t, "feature/retain-branch") {
		t.Fatal("branch was deleted without --delete-branch")
	}
}

func TestCleanYesDeletesBranchWhenRequested(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/delete-branch")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--delete-branch", "--json", "--scan-root", repo.Root)

	item := requireWorktree(t, inv, worktree)
	if !item.BranchDeleted {
		t.Fatal("BranchDeleted = false with --delete-branch, want true")
	}
	if repo.BranchExists(t, "feature/delete-branch") {
		t.Fatal("branch still exists after --delete-branch cleanup")
	}
}

func TestCleanYesKeepsTrackedDirtyMergedWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/tracked-dirty")
	testgit.WriteFile(t, filepath.Join(worktree, "feature-tracked-dirty.txt"), "changed\n")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("tracked dirty worktree was removed: %v", err)
	}
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.MergedButDirty {
		t.Fatalf("classification = %q, want %q", item.Classification, model.MergedButDirty)
	}
	if item.Removed {
		t.Fatal("Removed = true for tracked dirty worktree, want false")
	}
}

func TestCleanYesKeepsUntrackedDirtyMergedWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/untracked-dirty")
	testgit.WriteFile(t, filepath.Join(worktree, "untracked.txt"), "local\n")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("untracked dirty worktree was removed: %v", err)
	}
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.MergedButDirty {
		t.Fatalf("classification = %q, want %q", item.Classification, model.MergedButDirty)
	}
	if item.Removed {
		t.Fatal("Removed = true for untracked dirty worktree, want false")
	}
}

func TestCleanYesKeepsUnmergedWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateUnmergedWorktree(t, "feature/unmerged")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("unmerged worktree was removed: %v", err)
	}
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Unmerged {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Unmerged)
	}
	if item.Removed {
		t.Fatal("Removed = true for unmerged worktree, want false")
	}
}

func TestCleanJSONEmitsParseableInventory(t *testing.T) {
	repo := newRepository(t)
	repo.CreateMergedWorktree(t, "feature/json")

	inv := runWTGC(t, repo.Root, "clean", "--json", "--scan-root", repo.Root)

	if inv.SchemaVersion == "" {
		t.Fatal("SchemaVersion is empty")
	}
	if inv.GeneratedAt.IsZero() {
		t.Fatal("GeneratedAt is zero")
	}
	if inv.Summary.Repositories != 1 {
		t.Fatalf("repositories = %d, want 1", inv.Summary.Repositories)
	}
	if len(inv.Worktrees) == 0 {
		t.Fatal("Worktrees is empty")
	}
}

func TestCleanDeduplicatesNestedScanRoots(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/dedup")
	nested := filepath.Join(repo.Path, "nested")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("create nested scan root: %v", err)
	}

	inv := runWTGC(t, repo.Root, "clean", "--json", "--scan-root", repo.Root, "--scan-root", nested, "--scan-root", worktree)

	if inv.Summary.Repositories != 1 {
		t.Fatalf("repositories = %d, want 1", inv.Summary.Repositories)
	}
	seen := map[string]bool{}
	for _, item := range inv.Worktrees {
		path := canonicalPath(t, item.Path)
		if seen[path] {
			t.Fatalf("duplicate worktree in inventory: %s", item.Path)
		}
		seen[path] = true
	}
}

func TestCleanYesPrunesMissingWorktreeMetadata(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/prunable")
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatalf("remove worktree path to create prunable metadata: %v", err)
	}

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Prunable {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Prunable)
	}
	if !item.Removed {
		t.Fatal("Removed = false for prunable metadata, want true")
	}
	if strings.Contains(repo.RegisteredWorktrees(t), worktree) {
		t.Fatal("prunable worktree metadata remains registered after cleanup")
	}
}

func TestCleanYesKeepsDetachedWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateDetachedWorktree(t, "detached")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("detached worktree was removed: %v", err)
	}
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Kept {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Kept)
	}
	if !item.Detached {
		t.Fatal("Detached = false, want true")
	}
}

func TestCleanYesKeepsCurrentWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/current")

	inv := runWTGCInDir(t, worktree, "clean", "--yes", "--json", "--scan-root", repo.Root)

	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("current worktree was removed: %v", err)
	}
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Kept {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Kept)
	}
	if item.Removed {
		t.Fatal("Removed = true for current worktree, want false")
	}
}

func runWTGC(t *testing.T, scanDir string, args ...string) model.Inventory {
	t.Helper()
	return runWTGCInDir(t, scanDir, args...)
}

func newRepository(t *testing.T) *testgit.Repository {
	t.Helper()
	requireWTGCCommand(t)
	return testgit.NewRepository(t)
}

func runWTGCInDir(t *testing.T, dir string, args ...string) model.Inventory {
	t.Helper()
	cmd := exec.Command(wtgcBinary(t), args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			t.Fatalf("wtgc failed with exit %d\nstderr:\n%s\nstdout:\n%s", exitErr.ExitCode(), stderr.String(), string(out))
		}
		t.Fatalf("wtgc failed: %v\nstderr:\n%s", err, stderr.String())
	}

	var inv model.Inventory
	if err := json.Unmarshal(out, &inv); err != nil {
		t.Fatalf("json.Unmarshal CLI output failed: %v\nstdout:\n%s\nstderr:\n%s", err, string(out), stderr.String())
	}
	return inv
}

func requireWTGCCommand(t *testing.T) string {
	t.Helper()
	projectRoot := projectRoot(t)
	if _, err := os.Stat(filepath.Join(projectRoot, "cmd", "wtgc")); errors.Is(err, os.ErrNotExist) {
		t.Skip("cmd/wtgc is not present yet; black-box CLI integration tests activate when the command lands")
	} else if err != nil {
		t.Fatalf("stat cmd/wtgc: %v", err)
	}
	return projectRoot
}

func wtgcBinary(t *testing.T) string {
	t.Helper()
	projectRoot := requireWTGCCommand(t)
	wtgcBuildOnce.Do(func() {
		buildDir, err := os.MkdirTemp("", "wtgc-integration-*")
		if err != nil {
			wtgcBuildErr = err
			return
		}
		wtgcBuildPath = filepath.Join(buildDir, "wtgc")
		cmd := exec.Command("go", "build", "-o", wtgcBuildPath, "./cmd/wtgc")
		cmd.Dir = projectRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			wtgcBuildErr = errors.New(strings.TrimSpace(string(out)) + ": " + err.Error())
		}
	})
	if wtgcBuildErr != nil {
		t.Fatalf("build wtgc integration binary: %v", wtgcBuildErr)
	}
	return wtgcBuildPath
}

func projectRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve integration test file path")
	}
	return filepath.Dir(filepath.Dir(file))
}

func requireWorktree(t *testing.T, inv model.Inventory, path string) model.Worktree {
	t.Helper()
	want := canonicalPathAllowMissing(t, path)
	for _, item := range inv.Worktrees {
		if canonicalPathAllowMissing(t, item.Path) == want {
			return item
		}
	}
	t.Fatalf("inventory missing worktree %s; got %#v", path, inv.Worktrees)
	return model.Worktree{}
}

func canonicalPath(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs %s: %v", path, err)
	}
	evaluated, err := filepath.EvalSymlinks(abs)
	if err != nil {
		t.Fatalf("eval symlinks %s: %v", path, err)
	}
	return evaluated
}

func canonicalPathAllowMissing(t *testing.T, path string) string {
	t.Helper()
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs %s: %v", path, err)
	}
	evaluated, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return evaluated
	}
	existing := abs
	var missing []string
	for {
		parent := filepath.Dir(existing)
		if parent == existing {
			return abs
		}
		missing = append([]string{filepath.Base(existing)}, missing...)
		existing = parent
		evaluatedParent, err := filepath.EvalSymlinks(existing)
		if err == nil {
			parts := append([]string{evaluatedParent}, missing...)
			return filepath.Join(parts...)
		}
	}
}
