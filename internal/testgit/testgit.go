package testgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type Repository struct {
	Root      string
	Origin    string
	Path      string
	Worktrees string
}

func NewRepository(t *testing.T) *Repository {
	t.Helper()
	RequireGit(t)

	root := t.TempDir()
	repo := &Repository{
		Root:      root,
		Origin:    filepath.Join(root, "origin.git"),
		Path:      filepath.Join(root, "repo"),
		Worktrees: filepath.Join(root, "worktrees"),
	}
	if err := os.MkdirAll(repo.Worktrees, 0o750); err != nil {
		t.Fatalf("create worktree parent: %v", err)
	}

	Run(t, root, "init", "--bare", "--initial-branch=main", repo.Origin)
	Run(t, root, "clone", repo.Origin, repo.Path)
	Run(t, repo.Path, "config", "user.email", "test@example.com")
	Run(t, repo.Path, "config", "user.name", "Test User")
	WriteFile(t, filepath.Join(repo.Path, "README.md"), "base\n")
	Run(t, repo.Path, "add", "README.md")
	Run(t, repo.Path, "commit", "-m", "initial")
	Run(t, repo.Path, "push", "-u", "origin", "main")
	Run(t, repo.Path, "remote", "set-head", "origin", "-a")

	return repo
}

func RequireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
}

func Run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, string(out))
	}
	return string(out)
}

func WriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func (r *Repository) CreateMergedWorktree(t *testing.T, branch string) string {
	t.Helper()
	Run(t, r.Path, "checkout", "main")
	Run(t, r.Path, "checkout", "-b", branch)
	WriteFile(t, filepath.Join(r.Path, branchFileName(branch)), branch+"\n")
	Run(t, r.Path, "add", ".")
	Run(t, r.Path, "commit", "-m", "commit "+branch)
	Run(t, r.Path, "push", "-u", "origin", branch)
	Run(t, r.Path, "checkout", "main")
	Run(t, r.Path, "merge", "--no-ff", "--no-edit", branch)
	Run(t, r.Path, "push", "origin", "main")

	path := filepath.Join(r.Worktrees, safePathName(branch))
	Run(t, r.Path, "worktree", "add", path, branch)
	return path
}

func (r *Repository) CreateUnmergedWorktree(t *testing.T, branch string) string {
	t.Helper()
	Run(t, r.Path, "checkout", "main")
	Run(t, r.Path, "checkout", "-b", branch)
	WriteFile(t, filepath.Join(r.Path, branchFileName(branch)), branch+"\n")
	Run(t, r.Path, "add", ".")
	Run(t, r.Path, "commit", "-m", "commit "+branch)
	Run(t, r.Path, "push", "-u", "origin", branch)
	Run(t, r.Path, "checkout", "main")

	path := filepath.Join(r.Worktrees, safePathName(branch))
	Run(t, r.Path, "worktree", "add", path, branch)
	return path
}

func (r *Repository) CreateDetachedWorktree(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(r.Worktrees, safePathName(name))
	Run(t, r.Path, "worktree", "add", "--detach", path, "main")
	return path
}

func (r *Repository) BranchExists(t *testing.T, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = r.Path
	err := cmd.Run()
	if err == nil {
		return true
	}
	if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
		return false
	}
	t.Fatalf("git show-ref for %s failed: %v", branch, err)
	return false
}

func (r *Repository) RegisteredWorktrees(t *testing.T) string {
	t.Helper()
	return Run(t, r.Path, "worktree", "list", "--porcelain")
}

func branchFileName(branch string) string {
	return safePathName(branch) + ".txt"
}

func safePathName(name string) string {
	replacer := strings.NewReplacer("/", "-", "\\", "-", " ", "-")
	return replacer.Replace(name)
}
