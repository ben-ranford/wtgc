package gitx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/model"
)

var _ app.Git = New("git")

func TestParseWorktreeListPorcelainZ(t *testing.T) {
	input := []byte("worktree /repo\x00HEAD abc123\x00branch refs/heads/main\x00\x00worktree /repo-wt\x00HEAD def456\x00detached\x00locked maintenance\x00prunable gitdir file points to non-existent location\x00")

	records, err := ParseWorktreeListPorcelainZ(input)
	if err != nil {
		t.Fatalf("ParseWorktreeListPorcelainZ returned error: %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("got %d records, want 2", len(records))
	}
	if records[0].Path != "/repo" || records[0].Head != "abc123" || records[0].Branch != "main" {
		t.Fatalf("unexpected first record: %#v", records[0])
	}
	if !records[1].Detached || !records[1].Locked || !records[1].Prunable {
		t.Fatalf("unexpected second record: %#v", records[1])
	}
}

func TestParseWorktreeListPorcelainZSurfacesAmbiguity(t *testing.T) {
	_, err := ParseWorktreeListPorcelainZ([]byte("HEAD abc123\x00worktree /repo\x00unknown value\x00"))
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestNewDefaultsToGitBinary(t *testing.T) {
	t.Parallel()
	client := New("")
	if client.gitBinary != "git" {
		t.Fatalf("gitBinary = %q, want git", client.gitBinary)
	}
}

func TestParseWorktreeListPorcelainZRejectsMalformedFields(t *testing.T) {
	t.Parallel()
	_, err := ParseWorktreeListPorcelainZ([]byte("worktree\x00HEAD\x00branch\x00"))
	if err == nil {
		t.Fatal("expected parse error for malformed worktree, HEAD, and branch fields")
	}
}

func TestClientMethodsRejectInvalidInputsBeforeGitRuns(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	client := New("git")
	validRepo := model.Repository{PrimaryPath: "/repo", CommonDir: "/repo/.git"}

	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{name: "list missing repository", run: func() error {
			_, err := client.List(ctx, model.Repository{})
			return err
		}, want: "repository path is required"},
		{name: "default branch missing common dir", run: func() error {
			_, err := client.DefaultBranch(ctx, model.Repository{PrimaryPath: "/repo"})
			return err
		}, want: "repository common git dir is required"},
		{name: "is ancestor missing ref", run: func() error {
			_, err := client.IsAncestor(ctx, validRepo, "", "main")
			return err
		}, want: "ancestor and descendant refs are required"},
		{name: "remote contains missing commit", run: func() error {
			_, err := client.RemoteContains(ctx, validRepo, "")
			return err
		}, want: "commit is required"},
		{name: "remove missing path", run: func() error {
			return client.Remove(ctx, validRepo, " ")
		}, want: "worktree path is required"},
		{name: "delete branch missing branch", run: func() error {
			return client.DeleteBranch(ctx, validRepo, "")
		}, want: "branch is required"},
		{name: "delete branch option-like branch", run: func() error {
			return client.DeleteBranch(ctx, validRepo, "-danger")
		}, want: "looks like an option"},
		{name: "delete branch full ref", run: func() error {
			return client.DeleteBranch(ctx, validRepo, "refs/heads/feature")
		}, want: "expected short branch name"},
		{name: "prune missing repository", run: func() error {
			return client.Prune(ctx, model.Repository{})
		}, want: "repository path is required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatal("error = nil, want validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %q, want substring %q", err.Error(), tt.want)
			}
		})
	}
}

func TestDefaultBranchReportsDisagreeingRemoteHeads(t *testing.T) {
	t.Parallel()
	client := New(fakeGitBinary(t, map[string]string{
		"remote": "origin\nupstream\n",
		"symbolic-ref --quiet --short refs/remotes/origin/HEAD":   "origin/main\n",
		"symbolic-ref --quiet --short refs/remotes/upstream/HEAD": "upstream/trunk\n",
	}))

	_, err := client.DefaultBranch(context.Background(), model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"})
	if err == nil {
		t.Fatal("DefaultBranch error = nil, want disagreement")
	}
	if !strings.Contains(err.Error(), "remote HEADs disagree: main, trunk") {
		t.Fatalf("error = %q, want remote disagreement", err.Error())
	}
}

func TestDefaultBranchReportsMissingLocalRemoteHead(t *testing.T) {
	t.Parallel()
	client := New(fakeGitBinary(t, map[string]string{
		"remote": "origin\n",
	}))

	_, err := client.DefaultBranch(context.Background(), model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"})
	if err == nil {
		t.Fatal("DefaultBranch error = nil, want missing remote HEAD")
	}
	if !strings.Contains(err.Error(), "no local remote HEAD is configured") {
		t.Fatalf("error = %q, want missing remote HEAD", err.Error())
	}
}

func TestRemoteContainsIgnoresRemoteHeadRefs(t *testing.T) {
	t.Parallel()
	client := New(fakeGitBinary(t, map[string]string{
		"branch -r --contains abc123 --format=%(refname)": "refs/remotes/origin/HEAD\n",
	}))

	contains, err := client.RemoteContains(context.Background(), model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"}, "abc123")
	if err != nil {
		t.Fatalf("RemoteContains error = %v", err)
	}
	if contains {
		t.Fatal("RemoteContains = true for only remote HEAD ref")
	}
}

func TestDiskUsageMethodSurfacesWalkErrors(t *testing.T) {
	t.Parallel()
	_, err := New("git").DiskUsage(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("DiskUsage error = nil, want missing path error")
	}
}

func TestRealGitClient(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	repo := filepath.Join(root, "repo")
	run(t, root, "git", "init", "--bare", "--initial-branch=main", origin)
	run(t, root, "git", "clone", origin, repo)
	run(t, repo, "git", "config", "user.email", "test@example.com")
	run(t, repo, "git", "config", "user.name", "Test User")
	writeFile(t, filepath.Join(repo, "file.txt"), "base\n")
	run(t, repo, "git", "add", "file.txt")
	run(t, repo, "git", "commit", "-m", "initial")
	run(t, repo, "git", "push", "-u", "origin", "main")
	run(t, repo, "git", "remote", "set-head", "origin", "-a")
	run(t, repo, "git", "checkout", "-b", "feature")
	writeFile(t, filepath.Join(repo, "feature.txt"), "feature\n")
	run(t, repo, "git", "add", "feature.txt")
	run(t, repo, "git", "commit", "-m", "feature")
	run(t, repo, "git", "push", "-u", "origin", "feature")
	run(t, repo, "git", "checkout", "main")
	wt := filepath.Join(root, "repo-wt")
	run(t, repo, "git", "worktree", "add", wt, "feature")

	client := New("git")
	repos, errs := client.Discover(ctx, []string{root})
	if len(errs) > 0 {
		t.Fatalf("Discover errors: %v", errs)
	}
	if len(repos) != 1 {
		t.Fatalf("got %d repos, want 1 clone repo: %#v", len(repos), repos)
	}
	repoRecord := findRepoByPath(repos, repo)
	if repoRecord.PrimaryPath == "" {
		t.Fatalf("did not discover clone repository: %#v", repos)
	}

	worktrees, err := client.List(ctx, repoRecord)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(worktrees) != 2 {
		t.Fatalf("got %d worktrees, want 2: %#v", len(worktrees), worktrees)
	}
	if !hasPrimary(worktrees, repo) {
		t.Fatalf("primary worktree not marked: %#v", worktrees)
	}

	defaultBranch, err := client.DefaultBranch(ctx, repoRecord)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if defaultBranch != "main" {
		t.Fatalf("DefaultBranch = %q, want main", defaultBranch)
	}

	clean, err := client.IsClean(ctx, wt)
	if err != nil {
		t.Fatalf("IsClean clean: %v", err)
	}
	if !clean {
		t.Fatal("worktree should be clean")
	}
	writeFile(t, filepath.Join(wt, "untracked.txt"), "untracked\n")
	clean, err = client.IsClean(ctx, wt)
	if err != nil {
		t.Fatalf("IsClean dirty: %v", err)
	}
	if clean {
		t.Fatal("worktree with untracked file should be dirty")
	}
	if _, err := DiskUsage(wt); err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}

	ancestor, err := client.IsAncestor(ctx, repoRecord, "main", "feature")
	if err != nil {
		t.Fatalf("IsAncestor: %v", err)
	}
	if !ancestor {
		t.Fatal("main should be ancestor of feature")
	}
	contains, err := client.RemoteContains(ctx, repoRecord, "feature")
	if err != nil {
		t.Fatalf("RemoteContains: %v", err)
	}
	if !contains {
		t.Fatal("remote should contain feature tip")
	}

	if err := os.Remove(filepath.Join(wt, "untracked.txt")); err != nil {
		t.Fatalf("remove untracked: %v", err)
	}
	if err := client.Remove(ctx, repoRecord, wt); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(wt); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still exists or stat failed unexpectedly: %v", err)
	}
	if err := client.DeleteBranch(ctx, repoRecord, "feature"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if err := client.Prune(ctx, repoRecord); err != nil {
		t.Fatalf("Prune: %v", err)
	}
}

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func findRepoByPath(repos []model.Repository, path string) model.Repository {
	path = canonicalTestPath(path)
	for _, repo := range repos {
		if canonicalTestPath(repo.PrimaryPath) == path {
			return repo
		}
	}
	return model.Repository{}
}

func hasPrimary(worktrees []model.RegisteredWorktree, path string) bool {
	path = canonicalTestPath(path)
	for _, worktree := range worktrees {
		if canonicalTestPath(worktree.Path) == path && worktree.Primary {
			return true
		}
	}
	return false
}

func canonicalTestPath(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	evaluated, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return evaluated
}

func fakeGitBinary(t *testing.T, outputs map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "git")
	content := "#!/bin/sh\n"
	for args, output := range outputs {
		content += "if [ \"$*\" = " + quoteShell(args) + " ]; then\n" +
			"printf '%s' " + quoteShell(output) + "\n" +
			"exit 0\n" +
			"fi\n"
	}
	content += "printf '%s' \"unexpected git args: $*\" >&2\n" +
		"exit 1\n" +
		"\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	return script
}

func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
