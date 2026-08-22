package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

func TestRunHelpAndVersion(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{name: "help", args: []string{"--help"}, want: "Usage:"},
		{name: "version", args: []string{"--version"}, want: "wtgc dev"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr, nil, func() (string, error) { return "/tmp", nil })
			if code != 0 {
				t.Fatalf("exit = %d, stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.want) {
				t.Fatalf("stdout = %q, want %q", stdout.String(), test.want)
			}
		})
	}
}

func TestRunUsageError(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"clean", "--yes", "--interactive"}, strings.NewReader(""), &stdout, &stderr, nil, func() (string, error) { return "/tmp", nil })
	if code != 2 || !strings.Contains(stderr.String(), "mutually exclusive") || !strings.Contains(stderr.String(), "Usage:") {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", code, stdout.String(), stderr.String())
	}
}

func TestRunWritesJSONReportFromInjectedBackend(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	backend := newMainFakeGit(mainRecord("main"))

	code := run(context.Background(), []string{"--json"}, strings.NewReader(""), &stdout, &stderr, backend, func() (string, error) { return "/repo", nil })
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

func TestRunReturnsOneWhenGetwdFails(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer

	code := run(context.Background(), nil, strings.NewReader(""), &stdout, &stderr, newMainFakeGit(mainRecord("main")), func() (string, error) {
		return "", errors.New("cwd unavailable")
	})
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

	code := run(context.Background(), nil, strings.NewReader(""), &stdout, &stderr, backend, func() (string, error) { return "/repo", nil })
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

	code := run(context.Background(), nil, strings.NewReader(""), failingWriter{}, &stderr, newMainFakeGit(mainRecord("main")), func() (string, error) { return "/repo", nil })
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

func mainRecord(branch string) model.RegisteredWorktree {
	return model.RegisteredWorktree{Path: "/repo", Branch: branch, Head: "abc123", Primary: true}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

var _ io.Writer = failingWriter{}
