package gitx

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

var gitScriptTestMu sync.Mutex

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
	if client.commandTimeout != 0 {
		t.Fatalf("commandTimeout = %s, want disabled default", client.commandTimeout)
	}
}

func TestRunTimesOutHungGitCommand(t *testing.T) {
	client := NewWithTimeout(hangingGitBinary(t), 25*time.Millisecond)

	_, err := client.List(context.Background(), model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"})
	if err == nil {
		t.Fatal("List error = nil, want timeout")
	}
	if !strings.Contains(err.Error(), "timed out after") || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("error = %q, want timeout evidence", err.Error())
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
			return client.DeleteBranch(ctx, validRepo, "", "main")
		}, want: "branch is required"},
		{name: "delete branch option-like branch", run: func() error {
			return client.DeleteBranch(ctx, validRepo, "-danger", "main")
		}, want: "looks like an option"},
		{name: "delete branch full ref", run: func() error {
			return client.DeleteBranch(ctx, validRepo, "refs/heads/feature", "main")
		}, want: "expected short branch name"},
		{name: "delete branch missing default branch", run: func() error {
			return client.DeleteBranch(ctx, validRepo, "feature", "")
		}, want: "default branch is required"},
		{name: "delete branch option-like default branch", run: func() error {
			return client.DeleteBranch(ctx, validRepo, "feature", "-main")
		}, want: "looks like an option"},
		{name: "delete branch full default ref", run: func() error {
			return client.DeleteBranch(ctx, validRepo, "feature", "refs/heads/main")
		}, want: "expected short default branch name"},
		{name: "delete default branch", run: func() error {
			return client.DeleteBranch(ctx, validRepo, "main", "main")
		}, want: "refusing to delete default branch"},
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

func TestDefaultBranchFailsWhenAnyRemoteHeadIsMissing(t *testing.T) {
	t.Parallel()
	client := New(fakeGitBinary(t, map[string]string{
		"remote": "origin\nupstream\n",
		"symbolic-ref --quiet --short refs/remotes/origin/HEAD": "origin/main\n",
	}))

	_, err := client.DefaultBranch(context.Background(), model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"})
	if err == nil {
		t.Fatal("DefaultBranch error = nil, want missing remote HEAD")
	}
	if !strings.Contains(err.Error(), "every remote must have a valid local remote HEAD") {
		t.Fatalf("error = %q, want fail-closed remote HEAD error", err.Error())
	}
}

func TestDefaultBranchFailsWhenAnyRemoteHeadIsUnexpected(t *testing.T) {
	t.Parallel()
	client := New(fakeGitBinary(t, map[string]string{
		"remote": "origin\nupstream\n",
		"symbolic-ref --quiet --short refs/remotes/origin/HEAD":   "origin/main\n",
		"symbolic-ref --quiet --short refs/remotes/upstream/HEAD": "origin/main\n",
	}))

	_, err := client.DefaultBranch(context.Background(), model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"})
	if err == nil {
		t.Fatal("DefaultBranch error = nil, want unexpected remote HEAD")
	}
	if !strings.Contains(err.Error(), "unexpected ref") {
		t.Fatalf("error = %q, want unexpected ref", err.Error())
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

func TestTransientWalkNotExist(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	child := filepath.Join(root, ".git", "worktrees", "feature", "index.lock")

	if !isTransientWalkNotExist(root, child, fs.ErrNotExist) {
		t.Fatal("missing descendant should be treated as transient")
	}
	if isTransientWalkNotExist(root, root, fs.ErrNotExist) {
		t.Fatal("missing root must remain an error")
	}
	if isTransientWalkNotExist(root, child, fs.ErrPermission) {
		t.Fatal("non-missing errors must remain errors")
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
	run(t, repo, "git", "merge", "--ff-only", "feature")
	run(t, repo, "git", "push", "origin", "main")
	wt := filepath.Join(root, "repo-wt")
	run(t, repo, "git", "worktree", "add", wt, "feature")

	client := New("git")
	repoRecord := discoverRealGitRepository(t, client, ctx, root, repo)
	assertRealGitWorktrees(t, client, ctx, repoRecord, repo)
	assertRealGitDefaultBranch(t, client, ctx, repoRecord)
	assertRealGitWorktreeState(t, client, ctx, wt)
	assertRealGitAncestry(t, client, ctx, repoRecord)
	removeRealGitWorktree(t, client, ctx, repoRecord, wt)
}

func discoverRealGitRepository(t *testing.T, client *Client, ctx context.Context, root, repo string) model.Repository {
	t.Helper()
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
	return repoRecord
}

func assertRealGitWorktrees(t *testing.T, client *Client, ctx context.Context, repoRecord model.Repository, repo string) {
	t.Helper()
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
}

func assertRealGitDefaultBranch(t *testing.T, client *Client, ctx context.Context, repoRecord model.Repository) {
	t.Helper()
	defaultBranch, err := client.DefaultBranch(ctx, repoRecord)
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if defaultBranch != "main" {
		t.Fatalf("DefaultBranch = %q, want main", defaultBranch)
	}
}

func assertRealGitWorktreeState(t *testing.T, client *Client, ctx context.Context, worktree string) {
	t.Helper()
	clean, err := client.IsClean(ctx, worktree)
	if err != nil {
		t.Fatalf("IsClean clean: %v", err)
	}
	if !clean {
		t.Fatal("worktree should be clean")
	}
	writeFile(t, filepath.Join(worktree, "untracked.txt"), "untracked\n")
	clean, err = client.IsClean(ctx, worktree)
	if err != nil {
		t.Fatalf("IsClean dirty: %v", err)
	}
	if clean {
		t.Fatal("worktree with untracked file should be dirty")
	}
	if _, err := DiskUsage(worktree); err != nil {
		t.Fatalf("DiskUsage: %v", err)
	}
}

func assertRealGitAncestry(t *testing.T, client *Client, ctx context.Context, repoRecord model.Repository) {
	t.Helper()
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
}

func removeRealGitWorktree(t *testing.T, client *Client, ctx context.Context, repoRecord model.Repository, worktree string) {
	t.Helper()
	if err := os.Remove(filepath.Join(worktree, "untracked.txt")); err != nil {
		t.Fatalf("remove untracked: %v", err)
	}
	if err := client.Remove(ctx, repoRecord, worktree); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(worktree); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("worktree still exists or stat failed unexpectedly: %v", err)
	}
	if err := client.DeleteBranch(ctx, repoRecord, "feature", "main"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if err := client.Prune(ctx, repoRecord); err != nil {
		t.Fatalf("Prune: %v", err)
	}
}

func TestDiscoverFindsNestedRepositoriesInBuildAndDependencyDirs(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	root := t.TempDir()
	repo := filepath.Join(root, "node_modules", "pkg")
	wt := filepath.Join(root, "vendor", "pkg-wt")
	run(t, root, "git", "init", "--initial-branch=main", repo)
	configureTestUser(t, repo)
	writeFile(t, filepath.Join(repo, "file.txt"), "base\n")
	run(t, repo, "git", "add", "file.txt")
	run(t, repo, "git", "commit", "-m", "initial")
	run(t, repo, "git", "branch", "feature")
	run(t, repo, "git", "worktree", "add", wt, "feature")

	repos, errs := New("git").Discover(ctx, []string{root})
	if len(errs) > 0 {
		t.Fatalf("Discover errors: %v", errs)
	}
	if len(repos) != 1 {
		t.Fatalf("got %d repos, want deduped nested repo: %#v", len(repos), repos)
	}
	if findRepoByPath(repos, repo).PrimaryPath == "" {
		t.Fatalf("did not discover nested repo under node_modules/vendor: %#v", repos)
	}
}

func TestDiscoverFollowsSymlinkedScanRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows environments")
	}
	realRoot := t.TempDir()
	repo := initRepoWithCommit(t, realRoot)
	link := filepath.Join(t.TempDir(), "projects-link")
	if err := os.Symlink(realRoot, link); err != nil {
		t.Fatalf("create scan-root symlink: %v", err)
	}

	repos, errs := New("git").Discover(context.Background(), []string{link})
	if len(errs) > 0 {
		t.Fatalf("Discover errors: %v", errs)
	}
	if len(repos) != 1 || findRepoByPath(repos, repo).PrimaryPath == "" {
		t.Fatalf("Discover through symlink = %#v, want repository %q", repos, repo)
	}
}

func TestDeleteBranchDeletesMergedBranchWhenPrimaryOnUnrelatedBranch(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	root := t.TempDir()
	repo := initRepoWithCommit(t, root)
	run(t, repo, "git", "branch", "feature")
	run(t, repo, "git", "checkout", "-b", "unrelated")

	client := New("git")
	repoRecord := repositoryRecord(t, repo)
	if err := client.DeleteBranch(ctx, repoRecord, "feature", "main"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if branchExists(t, repo, "feature") {
		t.Fatal("feature branch still exists")
	}
}

func TestDeleteBranchRejectsUnmergedBranch(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	root := t.TempDir()
	repo := initRepoWithCommit(t, root)
	run(t, repo, "git", "checkout", "-b", "feature")
	writeFile(t, filepath.Join(repo, "feature.txt"), "feature\n")
	run(t, repo, "git", "add", "feature.txt")
	run(t, repo, "git", "commit", "-m", "feature")
	run(t, repo, "git", "checkout", "main")

	err := New("git").DeleteBranch(ctx, repositoryRecord(t, repo), "feature", "main")
	if err == nil {
		t.Fatal("DeleteBranch error = nil, want unmerged rejection")
	}
	if !strings.Contains(err.Error(), "unmerged branch") {
		t.Fatalf("error = %q, want unmerged rejection", err.Error())
	}
	if !branchExists(t, repo, "feature") {
		t.Fatal("feature branch was deleted")
	}
}

func TestDeleteBranchRejectsChangedBranchAfterProof(t *testing.T) {
	requireGit(t)
	ctx := context.Background()
	root := t.TempDir()
	repo := initRepoWithCommit(t, root)
	run(t, repo, "git", "branch", "feature")
	run(t, repo, "git", "checkout", "-b", "race")
	writeFile(t, filepath.Join(repo, "race.txt"), "race\n")
	run(t, repo, "git", "add", "race.txt")
	run(t, repo, "git", "commit", "-m", "race")
	raceOID := strings.TrimSpace(string(runOutput(t, repo, "git", "rev-parse", "HEAD")))
	run(t, repo, "git", "checkout", "main")

	wrapper := gitRaceWrapper(t, repo, raceOID)
	err := New(wrapper).DeleteBranch(ctx, repositoryRecord(t, repo), "feature", "main")
	if err == nil {
		t.Fatal("DeleteBranch error = nil, want update-ref old-oid rejection")
	}
	if !branchExists(t, repo, "feature") {
		t.Fatal("feature branch was deleted")
	}
	got := strings.TrimSpace(string(runOutput(t, repo, "git", "rev-parse", "refs/heads/feature")))
	if got != raceOID {
		t.Fatalf("feature oid = %s, want raced oid %s", got, raceOID)
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

func runOutput(t *testing.T, dir string, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, string(out))
	}
	return out
}

func configureTestUser(t *testing.T, repo string) {
	t.Helper()
	run(t, repo, "git", "config", "user.email", "test@example.com")
	run(t, repo, "git", "config", "user.name", "Test User")
}

func initRepoWithCommit(t *testing.T, root string) string {
	t.Helper()
	repo := filepath.Join(root, "repo")
	run(t, root, "git", "init", "--initial-branch=main", repo)
	configureTestUser(t, repo)
	writeFile(t, filepath.Join(repo, "file.txt"), "base\n")
	run(t, repo, "git", "add", "file.txt")
	run(t, repo, "git", "commit", "-m", "initial")
	return repo
}

func repositoryRecord(t *testing.T, repo string) model.Repository {
	t.Helper()
	common := strings.TrimSpace(string(runOutput(t, repo, "git", "rev-parse", "--git-common-dir")))
	if !filepath.IsAbs(common) {
		common = filepath.Join(repo, common)
	}
	return model.Repository{PrimaryPath: repo, CommonDir: canonicalTestPath(common)}
}

func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repo
	err := cmd.Run()
	if err == nil {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false
	}
	t.Fatalf("git show-ref failed: %v", err)
	return false
}

func gitRaceWrapper(t *testing.T, repo, raceOID string) string {
	t.Helper()
	lockGitScriptTest(t)
	if runtime.GOOS == "windows" {
		t.Skip("Git race wrapper requires a Unix-like executable format")
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("find git: %v", err)
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "git")
	content := "#!/bin/sh\n" +
		"real_git=" + quoteShell(realGit) + "\n" +
		"repo=" + quoteShell(repo) + "\n" +
		"race_oid=" + quoteShell(raceOID) + "\n" +
		"if [ \"$1\" = merge-base ] && [ \"$2\" = --is-ancestor ]; then\n" +
		"  \"$real_git\" \"$@\"\n" +
		"  status=$?\n" +
		"  if [ $status -eq 0 ]; then\n" +
		"    \"$real_git\" -C \"$repo\" update-ref refs/heads/feature \"$race_oid\"\n" +
		"  fi\n" +
		"  exit $status\n" +
		"fi\n" +
		"exec \"$real_git\" \"$@\"\n"
	writeTestExecutable(t, script, content)
	return script
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
	lockGitScriptTest(t)
	if runtime.GOOS == "windows" {
		t.Skip("fake Git shell scripts require a Unix-like executable format")
	}
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
	writeTestExecutable(t, script, content)
	return script
}

func hangingGitBinary(t *testing.T) string {
	t.Helper()
	lockGitScriptTest(t)
	if runtime.GOOS == "windows" {
		t.Skip("fake Git shell scripts require a Unix-like executable format")
	}
	dir := t.TempDir()
	script := filepath.Join(dir, "git")
	content := "#!/bin/sh\n" +
		"sleep 60\n"
	writeTestExecutable(t, script, content)
	return script
}

func writeTestExecutable(t *testing.T, path, content string) {
	t.Helper()
	temporary, err := os.CreateTemp(filepath.Dir(path), ".git-test-*")
	if err != nil {
		t.Fatalf("create executable %s: %v", path, err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if _, err := temporary.WriteString(content); err != nil {
		_ = temporary.Close()
		t.Fatalf("write executable %s: %v", path, err)
	}
	if err := temporary.Chmod(0o755); err != nil {
		_ = temporary.Close()
		t.Fatalf("chmod executable %s: %v", path, err)
	}
	if err := temporary.Close(); err != nil {
		t.Fatalf("close executable %s: %v", path, err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		t.Fatalf("install executable %s: %v", path, err)
	}
}

func lockGitScriptTest(t *testing.T) {
	t.Helper()
	gitScriptTestMu.Lock()
	t.Cleanup(gitScriptTestMu.Unlock)
}

func quoteShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
