package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/testgit"
)

var (
	wtgcBuildOnce sync.Once
	wtgcBuildPath string
	wtgcBuildErr  error
)

func TestNoArgsPrintsUsageAndExits(t *testing.T) {
	binary := wtgcBinary(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, binary)
	cmd.Dir = t.TempDir()
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		if ctx.Err() != nil {
			t.Fatalf("wtgc with no args did not exit within timeout: %v", ctx.Err())
		}
		t.Fatalf("wtgc with no args failed: %v\nstderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(string(stdout), "Usage:") {
		t.Fatalf("stdout = %q, want usage", stdout)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

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

func TestCleanYesKeepsLocallyMergedUnpushedWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateLocallyMergedWorktree(t, "feature/local-only")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	requireExists(t, worktree, "locally merged unpushed worktree was removed")
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Unmerged {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Unmerged)
	}
	if !strings.Contains(item.Reason, "remote") {
		t.Fatalf("reason = %q, want remote reachability explanation", item.Reason)
	}
	requireDirty(t, item, false)
	requireKept(t, item)
	if inv.Summary.ReclaimedBytes != 0 {
		t.Fatalf("reclaimed_bytes = %d, want 0", inv.Summary.ReclaimedBytes)
	}
}

func TestCleanMissingRemoteHEADReturnsNonzeroJSONAndKeepsInventory(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/missing-remote-head")
	testgit.Run(t, repo.Path, "remote", "set-head", "origin", "-d")

	inv := runWTGCExpectExit(t, repo.Root, 1, "clean", "--yes", "--json", "--scan-root", repo.Root)

	requireExists(t, worktree, "worktree was removed when remote HEAD was missing")
	if len(inv.Errors) == 0 {
		t.Fatal("errors is empty, want missing remote HEAD error")
	}
	if !containsString(inv.Errors, "remote HEAD") {
		t.Fatalf("errors = %#v, want missing remote HEAD error", inv.Errors)
	}
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Kept {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Kept)
	}
	if item.Error == "" {
		t.Fatal("item.Error is empty, want default branch error")
	}
	requireDirty(t, item, false)
	requireKept(t, item)
	if inv.Summary.ReclaimedBytes != 0 {
		t.Fatalf("reclaimed_bytes = %d, want 0", inv.Summary.ReclaimedBytes)
	}
}

func TestCleanAmbiguousRemoteHEADReturnsNonzeroJSONAndKeepsInventory(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/ambiguous-remote-head")
	testgit.Run(t, repo.Path, "checkout", "-b", "trunk", "main")
	testgit.Run(t, repo.Path, "push", "origin", "trunk")
	testgit.Run(t, repo.Path, "checkout", "main")
	testgit.Run(t, repo.Path, "remote", "add", "backup", repo.Origin)
	testgit.Run(t, repo.Path, "fetch", "backup")
	testgit.Run(t, repo.Path, "symbolic-ref", "refs/remotes/backup/HEAD", "refs/remotes/backup/trunk")

	inv := runWTGCExpectExit(t, repo.Root, 1, "clean", "--yes", "--json", "--scan-root", repo.Root)

	requireExists(t, worktree, "worktree was removed when remote HEAD was ambiguous")
	if len(inv.Errors) == 0 {
		t.Fatal("errors is empty, want ambiguous remote HEAD error")
	}
	if !containsString(inv.Errors, "remote HEADs disagree") {
		t.Fatalf("errors = %#v, want ambiguous remote HEAD error", inv.Errors)
	}
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Kept {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Kept)
	}
	if item.Error == "" {
		t.Fatal("item.Error is empty, want default branch error")
	}
	requireDirty(t, item, false)
	requireKept(t, item)
	if inv.Summary.ReclaimedBytes != 0 {
		t.Fatalf("reclaimed_bytes = %d, want 0", inv.Summary.ReclaimedBytes)
	}
}

func TestCleanOneBadScanRootReturnsNonzeroJSONAndKeepsGoodInventory(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/good-root")
	badRoot := filepath.Join(repo.Root, "missing root")

	inv := runWTGCExpectExit(t, repo.Root, 1, "clean", "--json", "--scan-root", badRoot, "--scan-root", repo.Root)

	if len(inv.Errors) == 0 {
		t.Fatal("errors is empty, want bad scan root error")
	}
	if !containsString(inv.Errors, "stat root") {
		t.Fatalf("errors = %#v, want stat root error", inv.Errors)
	}
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.SafeToRemove {
		t.Fatalf("classification = %q, want %q", item.Classification, model.SafeToRemove)
	}
	if item.Action != model.ActionWouldRemove {
		t.Fatalf("action = %q, want %q", item.Action, model.ActionWouldRemove)
	}
	if item.ReclaimedBytes != 0 {
		t.Fatalf("reclaimed_bytes = %d, want 0 in dry-run", item.ReclaimedBytes)
	}
}

func TestCleanYesKeepsStagedDirtyMergedWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/staged-dirty")
	testgit.WriteFile(t, filepath.Join(worktree, "staged.txt"), "staged\n")
	testgit.Run(t, worktree, "add", "staged.txt")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	requireExists(t, worktree, "staged dirty worktree was removed")
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.MergedButDirty {
		t.Fatalf("classification = %q, want %q", item.Classification, model.MergedButDirty)
	}
	requireDirty(t, item, true)
	requireKept(t, item)
}

func TestCleanYesKeepsDirtyUnmergedWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateUnmergedWorktree(t, "feature/dirty-unmerged")
	testgit.WriteFile(t, filepath.Join(worktree, "local.txt"), "local\n")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	requireExists(t, worktree, "dirty unmerged worktree was removed")
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Kept {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Kept)
	}
	if !strings.Contains(item.Reason, "dirty") {
		t.Fatalf("reason = %q, want dirty unmerged explanation", item.Reason)
	}
	requireDirty(t, item, true)
	requireKept(t, item)
}

func TestCleanYesKeepsDirtySubmoduleMergedWorktree(t *testing.T) {
	repo := newRepository(t)
	submoduleOrigin := newSubmoduleOrigin(t)
	testgit.Run(t, repo.Path, "-c", "protocol.file.allow=always", "submodule", "add", submoduleOrigin, "modules/lib")
	testgit.Run(t, repo.Path, "commit", "-m", "add submodule")
	testgit.Run(t, repo.Path, "push", "origin", "main")
	worktree := repo.CreateMergedWorktree(t, "feature/dirty-submodule")
	testgit.Run(t, worktree, "-c", "protocol.file.allow=always", "submodule", "update", "--init", "modules/lib")
	testgit.WriteFile(t, filepath.Join(worktree, "modules", "lib", "README.md"), "changed in submodule\n")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--json", "--scan-root", repo.Root)

	requireExists(t, worktree, "dirty submodule worktree was removed")
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.MergedButDirty {
		t.Fatalf("classification = %q, want %q", item.Classification, model.MergedButDirty)
	}
	requireDirty(t, item, true)
	requireKept(t, item)
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

func TestCleanHandlesPathsWithSpacesAcrossScanRemoveAndDelete(t *testing.T) {
	repo := newRepository(t)
	spacedRoot := filepath.Join(repo.Root, "scan root with spaces")
	if err := os.MkdirAll(spacedRoot, 0o750); err != nil {
		t.Fatalf("create spaced scan root: %v", err)
	}
	spacedPath := filepath.Join(spacedRoot, "worktree name with spaces")
	worktree := repo.CreateMergedWorktreeAt(t, "feature/spaced-path", spacedPath)

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--delete-branch", "--json", "--scan-root", spacedRoot)

	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spaced worktree exists after cleanup or stat failed unexpectedly: %v", err)
	}
	item := requireWorktree(t, inv, worktree)
	if !item.Removed {
		t.Fatal("Removed = false for spaced path worktree, want true")
	}
	if !item.BranchDeleted {
		t.Fatal("BranchDeleted = false for spaced path worktree, want true")
	}
	if item.Action != model.ActionRemovedBranchDeleted {
		t.Fatalf("action = %q, want %q", item.Action, model.ActionRemovedBranchDeleted)
	}
	if item.ReclaimedBytes <= 0 {
		t.Fatalf("reclaimed_bytes = %d, want positive reclaimed bytes", item.ReclaimedBytes)
	}
	if inv.Summary.Removed != 1 {
		t.Fatalf("summary.removed = %d, want 1", inv.Summary.Removed)
	}
	if inv.Summary.ReclaimedBytes <= 0 {
		t.Fatalf("summary.reclaimed_bytes = %d, want positive reclaimed bytes", inv.Summary.ReclaimedBytes)
	}
	if repo.BranchExists(t, "feature/spaced-path") {
		t.Fatal("branch still exists after spaced path --delete-branch cleanup")
	}
}

func TestCleanTreatsShellMetacharactersAndUnicodeAsData(t *testing.T) {
	repo := newRepository(t)
	branch := "feature/inject-;echo-PWNED-\u4e2d"
	marker := filepath.Join(repo.Root, "PWNED")
	worktree := repo.CreateMergedWorktreeAt(t, branch, filepath.Join(repo.Worktrees, "$(touch PWNED)-\u4e2d"))

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--delete-branch", "--json", "--scan-root", repo.Root)

	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("shell metacharacter marker exists or stat failed unexpectedly: %v", err)
	}
	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("metacharacter worktree exists after cleanup or stat failed unexpectedly: %v", err)
	}
	item := requireWorktree(t, inv, worktree)
	if !item.Removed {
		t.Fatal("Removed = false for metacharacter worktree, want true")
	}
	if !item.BranchDeleted {
		t.Fatal("BranchDeleted = false for unicode branch, want true")
	}
	if repo.BranchExists(t, branch) {
		t.Fatal("unicode branch still exists after --delete-branch cleanup")
	}
}

func TestCleanInteractiveNoKeepsSafeWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/interactive-no")

	cmd := exec.Command(wtgcBinary(t), "clean", "--interactive", "--json", "--scan-root", repo.Root)
	cmd.Dir = repo.Root
	cmd.Stdin = strings.NewReader("n\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("wtgc interactive failed: %v\nstderr:\n%s\nstdout:\n%s", err, stderr.String(), string(out))
	}
	if !strings.Contains(stderr.String(), "? [y/N]") {
		t.Fatalf("stderr = %q, want confirmation prompt", stderr.String())
	}

	var inv model.Inventory
	if err := json.Unmarshal(out, &inv); err != nil {
		t.Fatalf("json.Unmarshal CLI output failed: %v\nstdout:\n%s\nstderr:\n%s", err, string(out), stderr.String())
	}
	requireExists(t, worktree, "interactively rejected worktree was removed")
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Kept {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Kept)
	}
	if item.Action != model.ActionKept {
		t.Fatalf("action = %q, want %q", item.Action, model.ActionKept)
	}
	if !strings.Contains(item.Reason, "interactive choice") {
		t.Fatalf("reason = %q, want interactive choice evidence", item.Reason)
	}
}

func TestCleanInteractiveEOFKeepsSafeWorktree(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/interactive-eof")

	cmd := exec.Command(wtgcBinary(t), "clean", "--interactive", "--json", "--scan-root", repo.Root)
	cmd.Dir = repo.Root
	cmd.Stdin = strings.NewReader("")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("wtgc interactive EOF failed: %v\nstderr:\n%s\nstdout:\n%s", err, stderr.String(), string(out))
	}

	var inv model.Inventory
	if err := json.Unmarshal(out, &inv); err != nil {
		t.Fatalf("json.Unmarshal CLI output failed: %v\nstdout:\n%s\nstderr:\n%s", err, string(out), stderr.String())
	}
	requireExists(t, worktree, "interactive EOF removed safe worktree")
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Kept {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Kept)
	}
	if !strings.Contains(item.Reason, "interactive choice") {
		t.Fatalf("reason = %q, want interactive choice evidence", item.Reason)
	}
	requireKept(t, item)
}

func TestCleanSymlinkedProtectedCurrentWorktree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows environments")
	}
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/symlink-protected")
	linkWorktree := filepath.Join(repo.Root, "linked current worktree")
	if err := os.Symlink(worktree, linkWorktree); err != nil {
		t.Fatalf("create symlinked worktree cwd: %v", err)
	}

	inv := runWTGCInDir(t, linkWorktree, "clean", "--yes", "--json", "--scan-root", repo.Root)

	requireExists(t, worktree, "protected current worktree under symlinked scan root was removed")
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.Kept {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Kept)
	}
	if !strings.Contains(item.Reason, "running process") {
		t.Fatalf("reason = %q, want protected cwd explanation", item.Reason)
	}
	requireDirty(t, item, false)
	requireKept(t, item)
}

func TestCleanDeleteBranchSucceedsWhenPrimaryHeadIsUnrelatedButBranchMergedToDefault(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "feature/delete-from-unrelated-primary")
	testgit.Run(t, repo.Path, "checkout", "--orphan", "scratch")
	testgit.Run(t, repo.Path, "rm", "-rf", ".")
	testgit.WriteFile(t, filepath.Join(repo.Path, "scratch.txt"), "scratch\n")
	testgit.Run(t, repo.Path, "add", ".")
	testgit.Run(t, repo.Path, "commit", "-m", "scratch")

	inv := runWTGC(t, repo.Root, "clean", "--yes", "--delete-branch", "--json", "--scan-root", repo.Root)

	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree exists after cleanup or stat failed unexpectedly: %v", err)
	}
	item := requireWorktree(t, inv, worktree)
	if !item.Removed {
		t.Fatal("Removed = false, want true")
	}
	if !item.BranchDeleted {
		t.Fatal("BranchDeleted = false, want true")
	}
	if item.Action != model.ActionRemovedBranchDeleted {
		t.Fatalf("action = %q, want %q", item.Action, model.ActionRemovedBranchDeleted)
	}
	if item.ReclaimedBytes <= 0 {
		t.Fatalf("reclaimed_bytes = %d, want positive reclaimed bytes", item.ReclaimedBytes)
	}
	if repo.BranchExists(t, "feature/delete-from-unrelated-primary") {
		t.Fatal("branch still exists after --delete-branch cleanup from unrelated primary HEAD")
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
	return runWTGCExpectExit(t, dir, 0, args...)
}

func runWTGCExpectExit(t *testing.T, dir string, wantExit int, args ...string) model.Inventory {
	t.Helper()
	cmd := exec.Command(wtgcBinary(t), args...)
	cmd.Dir = dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	gotExit := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			gotExit = exitErr.ExitCode()
		} else {
			t.Fatalf("wtgc failed: %v\nstderr:\n%s", err, stderr.String())
		}
	}
	if gotExit != wantExit {
		t.Fatalf("wtgc exit = %d, want %d\nstderr:\n%s\nstdout:\n%s", gotExit, wantExit, stderr.String(), string(out))
	}

	var inv model.Inventory
	if err := json.Unmarshal(out, &inv); err != nil {
		t.Fatalf("json.Unmarshal CLI output failed: %v\nstdout:\n%s\nstderr:\n%s", err, string(out), stderr.String())
	}
	return inv
}

func newSubmoduleOrigin(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "submodule.git")
	work := filepath.Join(root, "submodule")
	testgit.Run(t, root, "init", "--bare", "--initial-branch=main", origin)
	testgit.Run(t, root, "clone", origin, work)
	testgit.Run(t, work, "config", "user.email", "test@example.com")
	testgit.Run(t, work, "config", "user.name", "Test User")
	testgit.WriteFile(t, filepath.Join(work, "README.md"), "submodule\n")
	testgit.Run(t, work, "add", "README.md")
	testgit.Run(t, work, "commit", "-m", "initial")
	testgit.Run(t, work, "push", "-u", "origin", "main")
	return origin
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
		binaryName := "wtgc"
		if runtime.GOOS == "windows" {
			binaryName += ".exe"
		}
		wtgcBuildPath = filepath.Join(buildDir, binaryName)
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

func requireExists(t *testing.T, path, message string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s: %v", message, err)
	}
}

func requireKept(t *testing.T, item model.Worktree) {
	t.Helper()
	if item.Removed {
		t.Fatal("Removed = true, want false")
	}
	if item.BranchDeleted {
		t.Fatal("BranchDeleted = true for kept worktree, want false")
	}
	if item.Action != model.ActionKept {
		t.Fatalf("action = %q, want %q", item.Action, model.ActionKept)
	}
	if item.ReclaimedBytes != 0 {
		t.Fatalf("reclaimed_bytes = %d, want 0", item.ReclaimedBytes)
	}
}

func requireDirty(t *testing.T, item model.Worktree, want bool) {
	t.Helper()
	if item.Dirty == nil {
		t.Fatalf("dirty = nil, want %v", want)
	}
	if *item.Dirty != want {
		t.Fatalf("dirty = %v, want %v", *item.Dirty, want)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
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
