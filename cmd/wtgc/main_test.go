package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/cli"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/provider"
	"github.com/ben-ranford/wtgc/internal/report"
	"github.com/ben-ranford/wtgc/internal/review"
)

func TestRunHelpAndVersion(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "no args", want: "Usage:"},
		{name: "help", args: []string{"--help"}, want: "Usage:"},
		{name: "short help", args: []string{"-h"}, want: "Usage:"},
		{name: "clean help", args: []string{"clean", "--help"}, want: "Usage:"},
		{name: "version", args: []string{"--version"}, want: "wtgc dev"},
		{name: "clean version", args: []string{"clean", "--version"}, want: "wtgc dev"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), test.args, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(nil, func() (string, error) { return "/tmp", nil }))
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.want)
			}
		})
	}
}

func TestRunNoArgsDoesNotResolveWorkingDirectory(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), nil, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(nil, func() (string, error) {
		return "", errors.New("getwd must not be called")
	}))
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("stdout = %q, want usage", stdout.String())
	}
}

func TestRunFlagOutputContracts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name              string
		args              []string
		stdin             string
		wantUsage         bool
		wantJSON          bool
		wantDryRun        bool
		wantRoot          string
		wantClass         model.Classification
		wantAction        model.Action
		wantRemoved       bool
		wantBranchDeleted bool
		wantStderr        string
	}{
		{name: "no args", wantUsage: true},
		{name: "dry run", args: []string{"clean", "--dry-run", "--json"}, wantJSON: true, wantDryRun: true, wantRoot: ".", wantClass: model.SafeToRemove, wantAction: model.ActionWouldRemove},
		{name: "yes", args: []string{"clean", "--yes", "--json"}, wantJSON: true, wantRoot: ".", wantClass: model.SafeToRemove, wantAction: model.ActionRemoved, wantRemoved: true},
		{name: "short yes", args: []string{"clean", "-y", "--json"}, wantJSON: true, wantRoot: ".", wantClass: model.SafeToRemove, wantAction: model.ActionRemoved, wantRemoved: true},
		{name: "interactive", args: []string{"clean", "--interactive", "--json"}, stdin: "n\n", wantJSON: true, wantRoot: ".", wantClass: model.Kept, wantAction: model.ActionKept, wantStderr: "remove /worktree? [y/N]"},
		{name: "delete branch", args: []string{"clean", "--yes", "--delete-branch", "--json"}, wantJSON: true, wantRoot: ".", wantClass: model.SafeToRemove, wantAction: model.ActionRemovedBranchDeleted, wantRemoved: true, wantBranchDeleted: true},
		{name: "scan root", args: []string{"clean", "--scan-root", "/repo", "--json"}, wantJSON: true, wantDryRun: true, wantRoot: "/repo", wantClass: model.SafeToRemove, wantAction: model.ActionWouldRemove},
		{name: "json", args: []string{"clean", "--json"}, wantJSON: true, wantDryRun: true, wantRoot: ".", wantClass: model.SafeToRemove, wantAction: model.ActionWouldRemove},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			var backend app.Git
			if len(test.args) > 0 {
				backend = newMainFakeGit(mainRecord("main"), mainRemovableRecord("feature"))
			}

			code := run(context.Background(), test.args, processIO{stdin: strings.NewReader(test.stdin), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return "/repo", nil }))
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
			if test.wantUsage {
				if !strings.Contains(stdout.String(), "Usage:") {
					t.Fatalf("stdout = %q, want usage", stdout.String())
				}
			} else if test.wantJSON {
				var inventory struct {
					SchemaVersion string           `json:"schema_version"`
					DryRun        bool             `json:"dry_run"`
					Roots         []string         `json:"roots"`
					Worktrees     []model.Worktree `json:"worktrees"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &inventory); err != nil {
					t.Fatalf("JSON output: %v\n%s", err, stdout.String())
				}
				if inventory.SchemaVersion == "" {
					t.Fatal("schema_version is empty")
				}
				if inventory.DryRun != test.wantDryRun {
					t.Fatalf("dry_run = %t, want %t", inventory.DryRun, test.wantDryRun)
				}
				if len(inventory.Roots) != 1 || inventory.Roots[0] != test.wantRoot {
					t.Fatalf("roots = %v, want [%q]", inventory.Roots, test.wantRoot)
				}
				var item model.Worktree
				for _, candidate := range inventory.Worktrees {
					if candidate.Path == "/worktree" {
						item = candidate
						break
					}
				}
				if item.Path == "" {
					t.Fatalf("JSON output omitted removable worktree: %s", stdout.String())
				}
				if item.Classification != test.wantClass || item.Action != test.wantAction || item.Removed != test.wantRemoved || item.BranchDeleted != test.wantBranchDeleted {
					t.Fatalf("worktree = %+v, want classification=%q action=%q removed=%t branch-deleted=%t", item, test.wantClass, test.wantAction, test.wantRemoved, test.wantBranchDeleted)
				}
			}
			if test.wantStderr == "" && stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want empty", stderr.String())
			}
			if test.wantStderr != "" && !strings.Contains(stderr.String(), test.wantStderr) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

func TestRunUsageError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"clean", "--yes", "--interactive"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(nil, func() (string, error) { return "/tmp", nil }))
	if code != 2 || !strings.Contains(stderr.String(), "mutually exclusive") || !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestConfirmerDescribesBranchOutcomeAccurately(t *testing.T) {
	tests := []struct {
		name     string
		worktree model.Worktree
		want     string
	}{
		{name: "local ancestry", worktree: model.Worktree{Path: "/local", Branch: "feature"}, want: "remove /local and delete its local branch? [y/N] "},
		{name: "provider squash", worktree: model.Worktree{Path: "/provider", Branch: "feature", WorktreeDetails: &model.WorktreeDetails{ProviderProof: model.ProviderProof{HeadSHA: "abc123"}}}, want: "remove /provider and retain its local branch (provider squash proof)? [y/N] "},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if !confirmer(strings.NewReader("yes\n"), &output, true)(test.worktree) {
				t.Fatal("confirmation was rejected")
			}
			if got := output.String(); got != test.want {
				t.Fatalf("prompt = %q, want %q", got, test.want)
			}
		})
	}
}

func TestRunWritesJSONReportFromInjectedBackend(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	backend := newMainFakeGit(mainRecord("main"))

	code := run(context.Background(), []string{"--json"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return "/repo", nil }))
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"schema_version":`) || !strings.Contains(stdout.String(), `"classification": "kept"`) {
		t.Fatalf("stdout = %q, want JSON inventory", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunReviewIsReadOnlyAndWritesSeparateDocument(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	realWorktree := filepath.Join(realRoot, "worktree")
	backend := &reviewNoMutationGit{mainFakeGit: newMainFakeGit(model.RegisteredWorktree{Path: realWorktree, Branch: "feature", Head: "def456"})}
	backend.repositories[0].PrimaryPath = realRoot
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"review", "--json", "--group-by", "classification", "--select", realWorktree}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return root, nil }))
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if backend.removes != 0 || backend.prunes != 0 || backend.deletes != 0 {
		t.Fatalf("review mutated repository: %+v", backend)
	}
	var document struct {
		ReviewSchemaVersion string `json:"review_schema_version"`
		Inventory           struct {
			SchemaVersion string `json:"schema_version"`
		} `json:"inventory"`
		View struct {
			Totals struct {
				Full struct {
					Count int `json:"count"`
				} `json:"full"`
				Visible struct {
					Count int `json:"count"`
				} `json:"visible"`
				Selected struct {
					Count int `json:"count"`
				} `json:"selected"`
			} `json:"totals"`
		} `json:"view"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("review JSON: %v\n%s", err, stdout.String())
	}
	if document.ReviewSchemaVersion != "1.0.0" || document.Inventory.SchemaVersion != "1.1.0" || document.View.Totals.Full.Count != 1 || document.View.Totals.Visible.Count != 1 || document.View.Totals.Selected.Count != 1 {
		t.Fatalf("document=%+v", document)
	}
}

func TestRunExplainIsReadOnlyAndEmitsStructuredTrace(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	backend := &reviewNoMutationGit{mainFakeGit: newMainFakeGit(model.RegisteredWorktree{Path: worktree, Branch: "feature", Head: "def456"})}
	backend.repositories[0].PrimaryPath = root
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"explain", "--json", worktree}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return root, nil }))
	if code != 0 || stderr.Len() != 0 || backend.removes != 0 || backend.prunes != 0 || backend.deletes != 0 {
		t.Fatalf("code=%d stderr=%q backend=%+v", code, stderr.String(), backend)
	}
	var document struct {
		ExplainSchemaVersion string                        `json:"explain_schema_version"`
		Checks               []struct{ ID, Status string } `json:"checks"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatalf("explain JSON: %v\n%s", err, stdout.String())
	}
	if document.ExplainSchemaVersion != "1.0.0" {
		t.Fatalf("schema=%q", document.ExplainSchemaVersion)
	}
	checks := map[string]string{}
	for _, check := range document.Checks {
		checks[check.ID] = check.Status
	}
	if checks["local_default_reachability"] != "passed" || checks["remote_tracking_reachability"] != "passed" || checks["provider_proof"] != "not_evaluated" {
		t.Fatalf("checks=%v", checks)
	}
}

func TestRunExplainRejectsUnknownPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"explain", "/missing"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(newMainFakeGit(mainRecord("main")), func() (string, error) { return "/repo", nil }))
	if code != 2 || !strings.Contains(stderr.String(), "unknown registered worktree") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunExplainReturnsStructuredOperationalFailure(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "target inspection", err: errors.New("target list failed"), want: "target list failed"},
		{name: "canceled target inspection", err: context.Canceled, want: "context canceled"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := &explainFailureGit{mainFakeGit: newMainFakeGit(mainRecord("main")), err: test.err}
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"explain", "--json", "/repo"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return "/repo", nil }))
			if code != 1 || !strings.Contains(stdout.String(), `"operational_errors"`) || !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunExplainReturnsUsageForAmbiguousResolverResult(t *testing.T) {
	backend := &explainFailureGit{mainFakeGit: newMainFakeGit(mainRecord("main")), err: explainUsageFailure{message: "ambiguous registered worktree /repo/wt"}}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"explain", "--json", "/repo/wt"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return "/repo", nil }))
	if code != 2 || !strings.Contains(stderr.String(), "ambiguous registered worktree") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestExplainHelpersResolveMissingTarget(t *testing.T) {
	root := t.TempDir()
	if got := explainScanRoot(filepath.Join(root, "missing", "worktree")); got != root {
		t.Fatalf("scan root=%q, want %q", got, root)
	}
	if got := explainScanRoot(string(filepath.Separator)); got != string(filepath.Separator) {
		t.Fatalf("filesystem root=%q", got)
	}
	options := cli.Options{ExplainPath: "/missing", Retention: time.Hour}
	var stderr bytes.Buffer
	if code := writeExplain(processIO{stdout: io.Discard, stderr: &stderr}, model.Inventory{}, options, report.FormatJSON, nil); code != 2 || !strings.Contains(stderr.String(), "unknown registered worktree") {
		t.Fatalf("unknown path code=%d stderr=%q", code, stderr.String())
	}
}

func TestExplainHelpersReportWriterAndDuplicateFailures(t *testing.T) {
	options := cli.Options{ExplainPath: "/repo/wt", Retention: time.Hour}
	worktree := model.Worktree{Path: "/repo/wt", Repository: "/repo", Classification: model.Kept}
	var stderr bytes.Buffer
	stderr.Reset()
	if code := writeExplain(processIO{stdout: failingWriter{}, stderr: &stderr}, model.Inventory{Worktrees: []model.Worktree{worktree}}, options, report.FormatHuman, nil); code != 1 || !strings.Contains(stderr.String(), "write report") {
		t.Fatalf("writer failure code=%d stderr=%q", code, stderr.String())
	}
	if _, err := findExplainedWorktree([]model.Worktree{worktree, worktree}, worktree.Path, worktree.Path); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous worktree error=%v", err)
	}
	stderr.Reset()
	if code := writeExplain(processIO{stdout: io.Discard, stderr: &stderr}, model.Inventory{Worktrees: []model.Worktree{worktree, {Path: worktree.Path + "/", Repository: "/repo", Classification: model.Kept}}}, options, report.FormatJSON, nil); code != 2 || !strings.Contains(stderr.String(), "ambiguous review path") {
		t.Fatalf("ambiguous match code=%d stderr=%q", code, stderr.String())
	}
}

func TestExplainHelpersWriteOperationalResults(t *testing.T) {
	options := cli.Options{ExplainPath: "/repo/wt", Retention: time.Hour}
	worktree := model.Worktree{Path: "/repo/wt", Repository: "/repo", Classification: model.Kept}
	operational := model.Inventory{Worktrees: []model.Worktree{worktree, {Path: worktree.Path + "/", Repository: "/repo", Classification: model.Kept}}, Errors: []string{"target inspection failed"}}
	stdout := &bytes.Buffer{}
	var stderr bytes.Buffer
	stderr.Reset()
	if code := writeExplain(processIO{stdout: stdout, stderr: &stderr}, operational, options, report.FormatJSON, errors.New("target inspection failed")); code != 1 || !strings.Contains(stdout.String(), `"operational_errors"`) || !strings.Contains(stdout.String(), "target inspection failed") {
		t.Fatalf("operational ambiguity code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := writeExplain(processIO{stdout: stdout, stderr: &stderr}, operational, options, report.FormatJSON, explainUsageFailure{message: "ambiguous registered worktree"}); code != 2 || !strings.Contains(stderr.String(), "ambiguous registered worktree") || stdout.Len() != 0 {
		t.Fatalf("usage ambiguity code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestExplainHelpersReportOperationalWriterFailure(t *testing.T) {
	var stderr bytes.Buffer
	stderr.Reset()
	if code := writeOperationalExplain(processIO{stdout: failingWriter{}, stderr: &stderr}, "/repo/wt", []string{"target inspection failed"}, report.FormatJSON); code != 1 || !strings.Contains(stderr.String(), "write report") {
		t.Fatalf("operational writer failure code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunReviewRejectsDestructiveFlagsWithStaticInformation(t *testing.T) {
	for _, args := range [][]string{{"review", "--yes", "--help"}, {"review", "--delete-branch=false", "--version"}} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(nil, func() (string, error) { return "/repo", nil })); code != 2 || !strings.Contains(stderr.String(), "review is read-only") {
			t.Fatalf("run %v code=%d stderr=%q", args, code, stderr.String())
		}
	}
}

func TestRunReviewOnlyCreatesProviderWhenExplicitlyRequested(t *testing.T) {
	backend := newMainFakeGit(mainRecord("main"), mainRemovableRecord("feature"))
	providerCalls := 0
	dependencies := commandDependencies{backend: backend, getwd: func() (string, error) { return "/repo", nil }, newProvider: func() provider.MergeFinder { providerCalls++; return mainProofFinder{} }}
	for _, args := range [][]string{{"review", "--json"}, {"review", "--provider", "github", "--json"}} {
		var stdout, stderr bytes.Buffer
		if code := run(context.Background(), args, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, dependencies); code != 0 {
			t.Fatalf("run %v code=%d stderr=%q", args, code, stderr.String())
		}
	}
	if providerCalls != 1 {
		t.Fatalf("provider calls=%d, want one explicit opt-in", providerCalls)
	}
}

func TestRunReviewNormalizesRelativeAndSymlinkPaths(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, "worktree")
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(worktree, filepath.Join(root, "selected-link")); err != nil {
		t.Fatal(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	backend := newMainFakeGit(model.RegisteredWorktree{Path: filepath.Join(realRoot, "worktree"), Head: "abc", Branch: "feature"})
	backend.repositories[0].PrimaryPath = realRoot
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"review", "--json", "--repository", ".", "--select", "selected-link"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return root, nil }))
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var document struct {
		View struct {
			Totals struct {
				Visible  struct{ Count int } `json:"visible"`
				Selected struct{ Count int } `json:"selected"`
			} `json:"totals"`
		} `json:"view"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.View.Totals.Visible.Count != 1 || document.View.Totals.Selected.Count != 1 {
		t.Fatalf("totals=%+v", document.View.Totals)
	}
}

func TestRunReviewReturnsSelectionAndWriteErrors(t *testing.T) {
	backend := newMainFakeGit(mainRecord("main"))
	for _, test := range []struct {
		name       string
		args       []string
		stdout     io.Writer
		wantCode   int
		wantStderr string
	}{
		{name: "unknown selection", args: []string{"review", "--select", "/missing"}, stdout: &bytes.Buffer{}, wantCode: 2, wantStderr: "unknown advisory selection"},
		{name: "writer failure", args: []string{"review"}, stdout: failingWriter{}, wantCode: 1, wantStderr: "write report: write failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := run(context.Background(), test.args, processIO{stdin: strings.NewReader(""), stdout: test.stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return "/repo", nil }))
			if code != test.wantCode || !strings.Contains(stderr.String(), test.wantStderr) {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
		})
	}
}

func TestRunReviewPreservesPartialScanWhenSelectionsAreMissing(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	worktree := filepath.Join(root, "worktree")
	lostRepository := filepath.Join(root, "lost")
	if err := os.MkdirAll(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	failed := model.Repository{CommonDir: filepath.Join(lostRepository, ".git"), PrimaryPath: lostRepository}
	mainBackend := newMainFakeGit(model.RegisteredWorktree{Path: worktree, Branch: "feature", Head: "def456"})
	mainBackend.repositories[0] = model.Repository{CommonDir: filepath.Join(repository, ".git"), PrimaryPath: repository}
	backend := &partialReviewGit{mainFakeGit: mainBackend, failed: failed}
	for _, test := range []struct {
		name                 string
		args                 []string
		wantSelectionError   bool
		wantSelectedWorktree int
	}{
		{name: "known selection", args: []string{"review", "--json", "--select", worktree}, wantSelectedWorktree: 1},
		{name: "known and missing selections", args: []string{"review", "--json", "--select", worktree, "--select", filepath.Join(lostRepository, "worktree")}, wantSelectionError: true, wantSelectedWorktree: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), test.args, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return repository, nil }))
			if code != 1 || !strings.Contains(stderr.String(), "completed with 1 error") {
				t.Fatalf("code=%d stderr=%q", code, stderr.String())
			}
			if test.wantSelectionError != strings.Contains(stderr.String(), "unknown advisory selection") {
				t.Fatalf("selection diagnostic=%q", stderr.String())
			}
			var document struct {
				Inventory struct{ Errors []string } `json:"inventory"`
				View      struct {
					Totals struct {
						Selected struct{ Count int } `json:"selected"`
					} `json:"totals"`
				} `json:"view"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &document); err != nil {
				t.Fatalf("partial review JSON: %v\n%s", err, stdout.String())
			}
			if len(document.Inventory.Errors) != 1 || document.View.Totals.Selected.Count != test.wantSelectedWorktree {
				t.Fatalf("document=%+v", document)
			}
		})
	}
}

func TestNormalizeReviewPathsRejectsBrokenSymlink(t *testing.T) {
	root := t.TempDir()
	link := filepath.Join(root, "broken")
	if err := os.Symlink(filepath.Join(root, "missing"), link); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeReviewPaths([]string{"broken"}, root); err == nil || !strings.Contains(err.Error(), "resolve review path") {
		t.Fatalf("error=%v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"review", "--select", "broken"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(newMainFakeGit(mainRecord("main")), func() (string, error) { return root, nil }))
	if code != 2 || !strings.Contains(stderr.String(), "resolve review path") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if _, err := normalizeReviewPaths([]string{"\x00"}, root); err == nil || !strings.Contains(err.Error(), "inspect review path") {
		t.Fatalf("invalid path error=%v", err)
	}
}

func TestCanonicalReviewPathResolvesMissingSuffixBelowSymlink(t *testing.T) {
	root := t.TempDir()
	actual := filepath.Join(root, "actual")
	if err := os.Mkdir(actual, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	got, err := canonicalReviewPath(filepath.Join(alias, "missing", "worktree"), "")
	if err != nil {
		t.Fatal(err)
	}
	wantRoot, err := filepath.EvalSymlinks(actual)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(wantRoot, "missing", "worktree"); got != want {
		t.Fatalf("canonical missing path=%q, want %q", got, want)
	}
}

func TestReviewPathParentRejectsPathWithoutAncestor(t *testing.T) {
	if _, err := reviewPathParent("."); err == nil || !strings.Contains(err.Error(), "no existing ancestor") {
		t.Fatalf("error=%v", err)
	}
}

func TestReviewOptionsForInventoryMatchesCanonicalIdentityWithoutChangingPaths(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	worktrees := filepath.Join(root, "worktrees")
	if err := os.MkdirAll(worktrees, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(worktrees, alias); err != nil {
		t.Fatal(err)
	}
	canonicalRepository, err := canonicalReviewPath(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorktree, err := canonicalReviewPath(filepath.Join(alias, "stale"), "")
	if err != nil {
		t.Fatal(err)
	}
	options, err := reviewOptionsForInventory(cli.Options{Repositories: []string{canonicalRepository}, SelectedPaths: []string{canonicalWorktree}}, model.Inventory{Worktrees: []model.Worktree{{Repository: repository, Path: filepath.Join(worktrees, "stale"), Classification: model.Prunable}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := options.Repositories[0]; got != repository {
		t.Fatalf("repository identity=%q, want raw inventory %q", got, repository)
	}
	if got, want := options.SelectedPaths[0], filepath.Join(worktrees, "stale"); got != want {
		t.Fatalf("selection identity=%q, want raw inventory %q", got, want)
	}
}

func TestReviewOptionsForInventoryKeepsUnresolvedErrorRowsVisible(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(root, "broken")
	if err := os.Symlink(filepath.Join(root, "missing-target"), broken); err != nil {
		t.Fatal(err)
	}
	canonicalSelected, err := canonicalReviewPath(selected, "")
	if err != nil {
		t.Fatal(err)
	}
	inventory := model.Inventory{Worktrees: []model.Worktree{
		{Path: selected, Repository: root, Classification: model.SafeToRemove},
		{Path: filepath.Join(broken, "stale"), Repository: root, Classification: model.Error, Error: "inspect stale worktree failed"},
	}}
	options, err := reviewOptionsForInventory(cli.Options{SelectedPaths: []string{canonicalSelected}}, inventory)
	if err != nil {
		t.Fatalf("review options rejected unrelated error row: %v", err)
	}
	document, err := review.Build(inventory, options)
	if err != nil {
		t.Fatal(err)
	}
	if document.View.Totals.Selected.Count != 1 || document.View.Totals.Visible.Count != 2 {
		t.Fatalf("totals=%+v", document.View.Totals)
	}
}

func TestRunReviewRejectsAmbiguousCanonicalSelection(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	backend := newMainFakeGit(
		model.RegisteredWorktree{Path: real, Branch: "one", Head: "one"},
		model.RegisteredWorktree{Path: alias, Branch: "two", Head: "two"},
	)
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"review", "--select", real}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return root, nil }))
	if code != 2 || !strings.Contains(stderr.String(), "ambiguous review path") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestReviewOptionsRejectsAmbiguousCanonicalRepository(t *testing.T) {
	root := t.TempDir()
	repository := filepath.Join(root, "repository")
	if err := os.Mkdir(repository, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(repository, alias); err != nil {
		t.Fatal(err)
	}
	canonicalRepository, err := canonicalReviewPath(repository, "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = reviewOptionsForInventory(cli.Options{Repositories: []string{canonicalRepository}}, model.Inventory{Worktrees: []model.Worktree{
		{Repository: repository, Path: filepath.Join(root, "first"), Classification: model.SafeToRemove},
		{Repository: alias, Path: filepath.Join(root, "second"), Classification: model.SafeToRemove},
	}})
	if err == nil || !strings.Contains(err.Error(), "ambiguous review path") {
		t.Fatalf("error=%v", err)
	}
}

func TestReviewOptionsKeepErrorRowsWhenWindowsVolumeIsUnavailable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("requires Windows volume semantics")
	}
	missingRoot := ""
	for drive := 'Z'; drive >= 'D'; drive-- {
		candidate := string(drive) + `:\`
		if _, err := os.Lstat(candidate); os.IsNotExist(err) {
			missingRoot = candidate
			break
		}
	}
	if missingRoot == "" {
		t.Skip("no unused drive letter")
	}
	if _, err := canonicalReviewPath(filepath.Join(missingRoot, "stale"), ""); err == nil || !strings.Contains(err.Error(), "no existing ancestor") {
		t.Fatalf("missing volume error=%v", err)
	}
	root := t.TempDir()
	selected := filepath.Join(root, "selected")
	if err := os.Mkdir(selected, 0o755); err != nil {
		t.Fatal(err)
	}
	canonicalSelected, err := canonicalReviewPath(selected, "")
	if err != nil {
		t.Fatal(err)
	}
	inventory := model.Inventory{Worktrees: []model.Worktree{
		{Path: selected, Repository: root, Classification: model.SafeToRemove},
		{Path: filepath.Join(missingRoot, "stale"), Repository: root, Classification: model.Error, Error: "inspect stale worktree failed"},
	}}
	options, err := reviewOptionsForInventory(cli.Options{SelectedPaths: []string{canonicalSelected}}, inventory)
	if err != nil {
		t.Fatalf("review options rejected unrelated unavailable volume: %v", err)
	}
	document, err := review.Build(inventory, options)
	if err != nil {
		t.Fatal(err)
	}
	if document.View.Totals.Selected.Count != 1 || document.View.Totals.Visible.Count != 2 {
		t.Fatalf("totals=%+v", document.View.Totals)
	}
}

func TestReviewOptionsMatchMissingSuffixCaseOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("requires Windows path-case semantics")
	}
	root := t.TempDir()
	candidate := filepath.Join(root, "OldWT")
	selection, err := canonicalReviewPath(filepath.Join(root, "oldwt"), "")
	if err != nil {
		t.Fatal(err)
	}
	options, err := reviewOptionsForInventory(cli.Options{SelectedPaths: []string{selection}}, model.Inventory{Worktrees: []model.Worktree{{Path: candidate, Repository: root, Classification: model.Prunable}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(options.SelectedPaths) != 1 || options.SelectedPaths[0] != candidate {
		t.Fatalf("selected paths=%q, want %q", options.SelectedPaths, candidate)
	}
}

func TestRunReturnsOneWhenGetwdFails(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), []string{"clean"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(newMainFakeGit(mainRecord("main")), func() (string, error) {
		return "", errors.New("cwd unavailable")
	}))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "resolve current directory: cwd unavailable") {
		t.Fatalf("stderr = %q, want cwd error", stderr.String())
	}
}

func TestRunReturnsOneAfterWritingReportForAppError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	backend := newMainFakeGit()
	backend.repositories = nil

	code := run(context.Background(), []string{"clean"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return "/repo", nil }))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stdout.String(), "Summary:") {
		t.Fatalf("stdout = %q, want report despite app error", stdout.String())
	}
	if !strings.Contains(stderr.String(), "wtgc: no Git repositories") {
		t.Fatalf("stderr = %q, want app error", stderr.String())
	}
}

func TestRunReturnsOneWhenReportWriteFails(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer

	code := run(context.Background(), []string{"clean"}, processIO{stdin: strings.NewReader(""), stdout: failingWriter{}, stderr: &stderr}, mainCommandDependencies(newMainFakeGit(mainRecord("main")), func() (string, error) { return "/repo", nil }))
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "write report: write failed") {
		t.Fatalf("stderr = %q, want report write error", stderr.String())
	}
}

func TestGitCommandTimeoutFromEnv(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		value     string
		want      time.Duration
		wantError string
	}{
		{name: "default disabled", want: 0},
		{name: "explicit", value: "150ms", want: 150 * time.Millisecond},
		{name: "invalid", value: "soon", wantError: "invalid duration"},
		{name: "zero", value: "0s", wantError: "greater than zero"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := gitCommandTimeoutFromEnv(test.value)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want substring %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("gitCommandTimeoutFromEnv error = %v", err)
			}
			if got != test.want {
				t.Fatalf("timeout = %s, want %s", got, test.want)
			}
		})
	}
}

func TestStaticInfoRequest(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{name: "help", args: []string{"--help"}, want: true},
		{name: "clean help", args: []string{"clean", "--help"}, want: true},
		{name: "version", args: []string{"--version"}, want: true},
		{name: "no args", want: true},
		{name: "normal command", args: []string{"clean"}},
		{name: "invalid command", args: []string{"unknown"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := staticInfoRequest(test.args); got != test.want {
				t.Fatalf("staticInfoRequest(%q) = %t, want %t", test.args, got, test.want)
			}
		})
	}
}

func TestRunMainUsesStaticBackendAndRejectsInvalidTimeout(t *testing.T) {
	t.Parallel()
	backend := newMainFakeGit(mainRecord("main"))
	newGitCalls, timedCalls := 0, 0
	newGit := func(string) app.Git {
		newGitCalls++
		return backend
	}
	newTimedGit := func(_ string, timeout time.Duration) app.Git {
		timedCalls++
		if timeout != 150*time.Millisecond {
			t.Fatalf("timeout = %s", timeout)
		}
		return backend
	}

	var stdout, stderr bytes.Buffer
	if code := runMain([]string{"--help"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, startupDependencies{getenv: func(string) string { return "invalid" }, getwd: func() (string, error) { return "/repo", nil }, newGit: newGit, newGitWithTimeout: newTimedGit}); code != 0 {
		t.Fatalf("static info exit = %d, stderr=%q", code, stderr.String())
	}
	if newGitCalls != 1 || timedCalls != 0 || !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("static startup new=%d timed=%d stdout=%q", newGitCalls, timedCalls, stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := runMain([]string{"clean"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, startupDependencies{getenv: func(string) string { return "bad" }, getwd: func() (string, error) { return "/repo", nil }, newGit: newGit, newGitWithTimeout: newTimedGit}); code != 2 {
		t.Fatalf("invalid timeout exit = %d", code)
	}
	if !strings.Contains(stderr.String(), "WTGC_GIT_TIMEOUT") || timedCalls != 0 {
		t.Fatalf("stderr=%q timed=%d", stderr.String(), timedCalls)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runMain([]string{"clean", "--json"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, startupDependencies{getenv: func(string) string { return "150ms" }, getwd: func() (string, error) { return "/repo", nil }, newGit: newGit, newGitWithTimeout: newTimedGit}); code != 0 {
		t.Fatalf("timed startup exit = %d stderr=%q", code, stderr.String())
	}
	if timedCalls != 1 || !strings.Contains(stdout.String(), `"schema_version"`) {
		t.Fatalf("timed startup calls=%d stdout=%q", timedCalls, stdout.String())
	}
}

func TestConfirmerPrunableAndEOFDefaultToNo(t *testing.T) {
	var output bytes.Buffer
	if confirmer(strings.NewReader(""), &output, false)(model.Worktree{Path: "/stale", Prunable: true}) {
		t.Fatal("EOF must not confirm")
	}
	if !strings.Contains(output.String(), "prune stale metadata for /stale? [y/N] \n") {
		t.Fatalf("prompt=%q", output.String())
	}
}

func TestRunWithProviderBuildsProofBoundary(t *testing.T) {
	t.Parallel()
	backend := newMainFakeGit(mainRecord("main"), mainRemovableRecord("feature"))
	proofCalls := 0
	finder := mainProofFinder{proof: provider.PullRequest{
		Number:         1,
		URL:            "https://github.com/owner/repo/pull/1",
		MergedAt:       time.Now().Add(-time.Minute),
		HeadSHA:        "def456",
		HeadOwner:      "owner",
		HeadRepo:       "repo",
		HeadRef:        "feature",
		BaseOwner:      "owner",
		BaseRepo:       "repo",
		BaseRef:        "main",
		MergeCommitSHA: strings.Repeat("a", 40),
	}}
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"clean", "--provider", "github", "--json"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, commandDependencies{
		backend: backend,
		getwd:   func() (string, error) { return "/repo", nil },
		newProvider: func() provider.MergeFinder {
			proofCalls++
			return finder
		},
	})
	if code != 0 || proofCalls != 1 || !strings.Contains(stdout.String(), `"schema_version"`) {
		t.Fatalf("code=%d calls=%d stdout=%q stderr=%q", code, proofCalls, stdout.String(), stderr.String())
	}
}

func TestRunCreatesDefaultGitHubProviderWithoutNetworkWhenGitProofIsSufficient(t *testing.T) {
	t.Parallel()
	backend := newMainFakeGit(mainRecord("main"), mainRemovableRecord("feature"))
	var stdout, stderr bytes.Buffer
	code := runMain([]string{"clean", "--provider", "github", "--json"}, processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}, startupDependencies{
		getenv:            func(string) string { return "" },
		getwd:             func() (string, error) { return "/repo", nil },
		newGit:            func(string) app.Git { return backend },
		newGitWithTimeout: func(string, time.Duration) app.Git { return backend },
	})
	if code != 0 || !strings.Contains(stdout.String(), `"schema_version"`) || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestProductionGitFactoriesCreateBoundaries(t *testing.T) {
	if newGitBackend("git") == nil || newTimedGitBackend("git", time.Second) == nil {
		t.Fatal("production Git factories returned nil")
	}
}

func TestConfirmerDefaultsToNo(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	confirm := confirmer(strings.NewReader("\nyes\n"), &output, true)
	worktree := model.Worktree{Path: "/tmp/wt\n\x1b[2J", Branch: "feature"}
	if confirm(worktree) {
		t.Fatal("empty answer should not confirm")
	}
	if !confirm(worktree) {
		t.Fatal("yes should confirm")
	}
	if !strings.Contains(output.String(), "delete its local branch") {
		t.Fatalf("prompt = %q", output.String())
	}
	if strings.Contains(output.String(), "\x1b") || strings.Contains(output.String(), "/tmp/wt\n") {
		t.Fatalf("prompt contains raw terminal control characters: %q", output.String())
	}
	if !strings.Contains(output.String(), `/tmp/wt\n\x1b[2J`) {
		t.Fatalf("prompt = %q, want escaped worktree path", output.String())
	}
}

type mainFakeGit struct {
	repositories []model.Repository
	records      []model.RegisteredWorktree
}

type reviewNoMutationGit struct {
	*mainFakeGit
	removes, prunes, deletes int
}

type partialReviewGit struct {
	*mainFakeGit
	failed model.Repository
}

type explainFailureGit struct {
	*mainFakeGit
	err error
}

type explainUsageFailure struct{ message string }

func (e explainUsageFailure) Error() string      { return e.message }
func (e explainUsageFailure) ExplainUsage() bool { return true }

func (f *explainFailureGit) ResolveExplain(context.Context, string) (model.Repository, model.RegisteredWorktree, bool, error) {
	return model.Repository{}, model.RegisteredWorktree{}, false, f.err
}

func (f *partialReviewGit) Discover(context.Context, []string) ([]model.Repository, []error) {
	return []model.Repository{f.repositories[0], f.failed}, nil
}

func (f *partialReviewGit) List(ctx context.Context, repo model.Repository) ([]model.RegisteredWorktree, error) {
	if repo.CommonDir == f.failed.CommonDir {
		return nil, errors.New("lost repository")
	}
	return f.mainFakeGit.List(ctx, repo)
}

func (f *reviewNoMutationGit) Remove(context.Context, model.Repository, string) error {
	f.removes++
	return nil
}
func (f *reviewNoMutationGit) Prune(context.Context, model.Repository) error { f.prunes++; return nil }
func (f *reviewNoMutationGit) DeleteBranch(context.Context, model.Repository, string, string) error {
	f.deletes++
	return nil
}

func mainCommandDependencies(backend app.Git, getwd func() (string, error)) commandDependencies {
	return commandDependencies{
		backend:     backend,
		getwd:       getwd,
		newProvider: func() provider.MergeFinder { return provider.NewGitHub(nil) },
	}
}

func newMainFakeGit(records ...model.RegisteredWorktree) *mainFakeGit {
	return &mainFakeGit{
		repositories: []model.Repository{{CommonDir: "/repo/.git", PrimaryPath: "/repo"}},
		records:      records,
	}
}

func (f *mainFakeGit) Discover(context.Context, []string) ([]model.Repository, []error) {
	return append([]model.Repository(nil), f.repositories...), nil
}

func (f *mainFakeGit) List(context.Context, model.Repository) ([]model.RegisteredWorktree, error) {
	return append([]model.RegisteredWorktree(nil), f.records...), nil
}

func (f *mainFakeGit) ResolveExplain(_ context.Context, path string) (model.Repository, model.RegisteredWorktree, bool, error) {
	for _, record := range f.records {
		if sameMainPath(record.Path, path) {
			return f.repositories[0], record, true, nil
		}
	}
	return f.repositories[0], model.RegisteredWorktree{}, false, nil
}

func sameMainPath(left, right string) bool {
	leftResolved, leftErr := filepath.EvalSymlinks(left)
	rightResolved, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil && rightErr == nil {
		return filepath.Clean(leftResolved) == filepath.Clean(rightResolved)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}

func (*mainFakeGit) DefaultBranch(context.Context, model.Repository) (string, error) {
	return "main", nil
}

func (*mainFakeGit) IsClean(context.Context, string) (bool, error) {
	return true, nil
}

func (*mainFakeGit) IsAncestor(context.Context, model.Repository, string, string) (bool, error) {
	return true, nil
}

func (*mainFakeGit) RemoteContains(context.Context, model.Repository, string) (bool, error) {
	return true, nil
}

func (*mainFakeGit) DiskUsage(string) (int64, error) {
	return 1024, nil
}

func (*mainFakeGit) Remove(context.Context, model.Repository, string) error {
	return nil
}

func (*mainFakeGit) Prune(context.Context, model.Repository) error {
	return nil
}

func (*mainFakeGit) DeleteBranch(context.Context, model.Repository, string, string) error {
	return nil
}
func (*mainFakeGit) ProviderUpstream(context.Context, model.Repository, string) (string, string, string, error) {
	return "origin", "feature", "https://github.com/owner/repo.git", nil
}
func (*mainFakeGit) ProviderDefaultTracking(context.Context, model.Repository, string, string) (string, string, string, error) {
	return "origin", "main", "https://github.com/owner/repo.git", nil
}

func mainRecord(branch string) model.RegisteredWorktree {
	return model.RegisteredWorktree{Path: "/repo", Branch: branch, Head: "abc123", Primary: true}
}

func mainRemovableRecord(branch string) model.RegisteredWorktree {
	return model.RegisteredWorktree{Path: "/worktree", Branch: branch, Head: "def456"}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

var _ io.Writer = failingWriter{}

type mainProofFinder struct{ proof provider.PullRequest }

func (p mainProofFinder) FindMerged(context.Context, provider.Query) (provider.PullRequest, error) {
	return p.proof, nil
}
