package integration_test

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/review"
	"github.com/ben-ranford/wtgc/internal/testgit"
)

func TestReviewProjectsLargeRealRepositoryAndRetainsPartialScanErrors(t *testing.T) {
	repo := newRepository(t)
	var selected string
	for i := 49; i >= 0; i-- {
		branch := "review/row-" + threeDigits(i)
		testgit.Run(t, repo.Path, "branch", branch, "main")
		path := filepath.Join(repo.Worktrees, "row-"+threeDigits(i))
		testgit.Run(t, repo.Path, "worktree", "add", path, branch)
		if i == 17 {
			selected = canonicalPath(t, path)
		}
	}

	doc, stderr := runReviewJSON(t, repo.Root, 1,
		"review", "--json",
		"--scan-root", filepath.Join(repo.Root, "missing scan root"),
		"--scan-root", repo.Root,
		"--repository", repo.Path,
		"--classification", string(model.SafeToRemove),
		"--group-by", "classification",
		"--sort-by", "size",
		"--select", selected,
	)

	if doc.ReviewSchemaVersion != review.SchemaVersion || doc.Inventory.SchemaVersion != "1.1.0" {
		t.Fatalf("schema versions = review %q inventory %q", doc.ReviewSchemaVersion, doc.Inventory.SchemaVersion)
	}
	if len(doc.Inventory.Errors) == 0 || !strings.Contains(strings.Join(doc.Inventory.Errors, "\n"), "missing scan root") {
		t.Fatalf("partial scan error missing from complete inventory: %#v", doc.Inventory.Errors)
	}
	if !strings.Contains(stderr, "completed with") {
		t.Fatalf("stderr = %q, want partial scan failure", stderr)
	}
	if doc.View.Totals.Full.Count != 51 || doc.View.Totals.Visible.Count != 50 || doc.View.Totals.Selected.Count != 1 {
		t.Fatalf("totals = %+v", doc.View.Totals)
	}
	if len(doc.View.Groups) != 1 || doc.View.Groups[0].Key != string(model.SafeToRemove) || len(doc.View.Groups[0].Worktrees) != 50 {
		t.Fatalf("groups = %+v", doc.View.Groups)
	}
	rows := doc.View.Groups[0].Worktrees
	for i, row := range rows {
		if row.Worktree.Classification != model.SafeToRemove {
			t.Fatalf("row %d classification = %q", i, row.Worktree.Classification)
		}
		if i > 0 && rows[i-1].Worktree.Path > row.Worktree.Path {
			t.Fatalf("size tie was not resolved by path: %q before %q", rows[i-1].Worktree.Path, row.Worktree.Path)
		}
	}
	if !hasSelectedPath(rows, selected) {
		t.Fatalf("selected path %q was not retained in the projected view", selected)
	}
}

func TestReviewHumanOutputIsPlainCompleteAndTerminalSafe(t *testing.T) {
	repo := newRepository(t)
	name := "review-unicode-über"
	if runtime.GOOS != "windows" {
		name += "-\x1b[2J"
	}
	branch := "review/terminal"
	path := filepath.Join(repo.Worktrees, name)
	testgit.Run(t, repo.Path, "branch", branch, "main")
	testgit.Run(t, repo.Path, "worktree", "add", path, branch)

	output, stderr := runReviewHuman(t, repo.Root, 1, []string{"NO_COLOR=1", "COLUMNS=80"},
		"review",
		"--scan-root", filepath.Join(repo.Root, "missing scan root"),
		"--scan-root", repo.Root,
		"--repository", repo.Path,
		"--select", path,
	)
	for _, want := range []string{"GROUP", "SELECTED", "Totals:", "full:", "visible:", "selected:", "Errors:", "missing scan root", "über"} {
		if !strings.Contains(output, want) {
			t.Fatalf("human output missing %q:\n%s", want, output)
		}
	}
	if !strings.Contains(stderr, "completed with") {
		t.Fatalf("stderr = %q, want partial scan failure", stderr)
	}
	if runtime.GOOS != "windows" {
		if strings.Contains(output, "\x1b") || !strings.Contains(output, `\x1b[2J`) {
			t.Fatalf("hostile path was not safely escaped:\n%s", output)
		}
	}
}

func TestReviewLeavesLegacyCleanJSONContractUntouched(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "review/legacy-contract")

	inv := runWTGC(t, repo.Root, "clean", "--json", "--scan-root", repo.Root)
	if inv.SchemaVersion != "1.1.0" || inv.DryRun != true {
		t.Fatalf("legacy inventory = %+v", inv)
	}
	item := requireWorktree(t, inv, worktree)
	if item.Classification != model.SafeToRemove || item.Action != model.ActionWouldRemove {
		t.Fatalf("legacy clean worktree = %+v", item)
	}

	output, _ := runReviewHuman(t, repo.Root, 0, nil, "review", "--scan-root", repo.Root)
	if !strings.Contains(output, "Totals:") {
		t.Fatalf("review output missing its view totals:\n%s", output)
	}
	if strings.Contains(output, "Summary:") {
		t.Fatalf("review reused clean's legacy human report:\n%s", output)
	}
}

func runReviewJSON(t *testing.T, dir string, wantExit int, args ...string) (review.Document, string) {
	t.Helper()
	output, stderr := runReviewCommand(t, dir, wantExit, nil, args...)
	var document review.Document
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode review JSON: %v\nstdout:\n%s\nstderr:\n%s", err, output, stderr)
	}
	return document, stderr
}

func runReviewHuman(t *testing.T, dir string, wantExit int, environment []string, args ...string) (string, string) {
	t.Helper()
	return runReviewCommand(t, dir, wantExit, environment, args...)
}

func runReviewCommand(t *testing.T, dir string, wantExit int, environment []string, args ...string) (string, string) {
	t.Helper()
	cmd := exec.Command(wtgcBinary(t), args...)
	cmd.Dir = dir
	if environment != nil {
		cmd.Env = append(os.Environ(), environment...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	gotExit := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run review: %v", err)
		}
		gotExit = exitErr.ExitCode()
	}
	if gotExit != wantExit {
		t.Fatalf("review exit = %d, want %d\nstdout:\n%s\nstderr:\n%s", gotExit, wantExit, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

func hasSelectedPath(rows []review.Row, path string) bool {
	for _, row := range rows {
		if row.Worktree.Path == path && row.Selected {
			return true
		}
	}
	return false
}

func threeDigits(value int) string {
	return string([]byte{'0' + byte(value/100), '0' + byte(value/10%10), '0' + byte(value%10)})
}
