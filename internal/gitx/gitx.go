// Package gitx provides a small, stdlib-only adapter around Git commands used
// by wtgc. It intentionally exposes raw Git facts and leaves product decisions
// to higher layers.
package gitx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ben-ranford/wtgc/internal/model"
)

// Client executes Git commands for repository discovery and cleanup.
type Client struct {
	gitBinary string
}

// New returns a Git client. Empty gitBinary defaults to "git".
func New(gitBinary string) *Client {
	if gitBinary == "" {
		gitBinary = "git"
	}
	return &Client{gitBinary: gitBinary}
}

// Discover recursively finds Git repositories and worktrees beneath roots,
// deduplicating by `git rev-parse --git-common-dir`. Recoverable discovery
// failures are returned in the error slice so callers can surface ambiguity.
func (c *Client) Discover(ctx context.Context, roots []string) ([]model.Repository, []error) {
	if len(roots) == 0 {
		return nil, []error{errors.New("no discovery roots provided")}
	}

	seen := map[string]string{}
	var errs []error
	skippedDirectories := map[string]struct{}{
		"node_modules": {},
		"vendor":       {},
		"target":       {},
		"dist":         {},
		".cache":       {},
	}
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			errs = append(errs, errors.New("empty discovery root"))
			continue
		}
		absRoot, err := filepath.Abs(root)
		if err != nil {
			errs = append(errs, fmt.Errorf("resolve root %q: %w", root, err))
			continue
		}
		info, err := os.Stat(absRoot)
		if err != nil {
			errs = append(errs, fmt.Errorf("stat root %q: %w", absRoot, err))
			continue
		}
		if !info.IsDir() {
			errs = append(errs, fmt.Errorf("root %q is not a directory", absRoot))
			continue
		}
		err = filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				errs = append(errs, fmt.Errorf("walk %q: %w", path, walkErr))
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() && path != absRoot {
				if _, skip := skippedDirectories[d.Name()]; skip {
					return filepath.SkipDir
				}
			}
			if d.Name() != ".git" {
				return nil
			}
			worktreePath := filepath.Dir(path)
			commonGitDir, err := c.commonGitDir(ctx, worktreePath)
			if err != nil {
				errs = append(errs, fmt.Errorf("common git dir for %q: %w", worktreePath, err))
			} else if _, ok := seen[commonGitDir]; !ok {
				seen[commonGitDir] = worktreePath
			}
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		})
		if err != nil {
			errs = append(errs, fmt.Errorf("walk root %q: %w", absRoot, err))
		}
	}

	commonDirs := make([]string, 0, len(seen))
	for commonDir := range seen {
		commonDirs = append(commonDirs, commonDir)
	}
	sort.Strings(commonDirs)

	repos := make([]model.Repository, 0, len(commonDirs))
	for _, commonDir := range commonDirs {
		repo := model.Repository{
			CommonDir:   commonDir,
			PrimaryPath: seen[commonDir],
		}
		worktrees, err := c.List(ctx, repo)
		if err != nil {
			errs = append(errs, fmt.Errorf("list worktrees for %q: %w", repo.PrimaryPath, err))
		} else if len(worktrees) > 0 {
			repo.PrimaryPath = worktrees[0].Path
		}
		repos = append(repos, repo)
	}
	return repos, errs
}

// List returns raw worktree records for a repository.
func (c *Client) List(ctx context.Context, repo model.Repository) ([]model.RegisteredWorktree, error) {
	if err := validateRepository(repo); err != nil {
		return nil, err
	}
	out, err := c.run(ctx, repo.PrimaryPath, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return nil, err
	}
	worktrees, err := ParseWorktreeListPorcelainZ(out)
	if err != nil {
		return nil, err
	}
	for i := range worktrees {
		worktrees[i].Primary = i == 0
	}
	return worktrees, nil
}

// ParseWorktreeListPorcelainZ parses `git worktree list --porcelain -z`.
func ParseWorktreeListPorcelainZ(data []byte) ([]model.RegisteredWorktree, error) {
	if len(data) == 0 {
		return nil, nil
	}
	fields := bytes.Split(data, []byte{0})
	var records []model.RegisteredWorktree
	var current *model.RegisteredWorktree
	var errs []error

	finish := func() {
		if current != nil {
			records = append(records, *current)
			current = nil
		}
	}

	for _, raw := range fields {
		if len(raw) == 0 {
			continue
		}
		line := string(raw)
		key, value, hasValue := strings.Cut(line, " ")
		if key == "worktree" {
			finish()
			if !hasValue || value == "" {
				errs = append(errs, fmt.Errorf("malformed worktree field %q", line))
				continue
			}
			current = &model.RegisteredWorktree{Path: value}
			continue
		}
		if current == nil {
			errs = append(errs, fmt.Errorf("field before worktree: %q", line))
			continue
		}
		switch key {
		case "HEAD":
			if !hasValue || value == "" {
				errs = append(errs, fmt.Errorf("malformed HEAD field for %q", current.Path))
			} else {
				current.Head = value
			}
		case "branch":
			if !hasValue || value == "" {
				errs = append(errs, fmt.Errorf("malformed branch field for %q", current.Path))
			} else {
				current.Branch = shortBranch(value)
			}
		case "bare":
			current.Bare = true
		case "detached":
			current.Detached = true
		case "locked":
			current.Locked = true
		case "prunable":
			current.Prunable = true
		default:
			errs = append(errs, fmt.Errorf("unknown worktree field %q for %q", key, current.Path))
		}
	}
	finish()
	return records, errors.Join(errs...)
}

// DefaultBranch detects a single unambiguous local remote HEAD and returns its
// short branch name. It deliberately does not guess from the checked-out branch.
func (c *Client) DefaultBranch(ctx context.Context, repo model.Repository) (string, error) {
	if err := validateRepository(repo); err != nil {
		return "", err
	}
	out, err := c.run(ctx, repo.PrimaryPath, "remote")
	if err != nil {
		return "", fmt.Errorf("list remotes: %w", err)
	}
	remotes := strings.Fields(string(out))
	if len(remotes) == 0 {
		return "", errors.New("detect default branch: repository has no remotes")
	}
	resolved := make(map[string]struct{})
	var errs []error
	for _, remote := range remotes {
		ref := "refs/remotes/" + remote + "/HEAD"
		out, err := c.run(ctx, repo.PrimaryPath, "symbolic-ref", "--quiet", "--short", ref)
		if err != nil {
			errs = append(errs, fmt.Errorf("resolve %s: %w", ref, err))
			continue
		}
		branch := strings.TrimSpace(string(out))
		prefix := remote + "/"
		if !strings.HasPrefix(branch, prefix) || strings.TrimPrefix(branch, prefix) == "" {
			errs = append(errs, fmt.Errorf("remote HEAD %s returned unexpected ref %q", ref, branch))
			continue
		}
		resolved[strings.TrimPrefix(branch, prefix)] = struct{}{}
	}
	if len(resolved) == 1 {
		for branch := range resolved {
			return branch, nil
		}
	}
	if len(resolved) > 1 {
		branches := make([]string, 0, len(resolved))
		for branch := range resolved {
			branches = append(branches, branch)
		}
		sort.Strings(branches)
		return "", fmt.Errorf("detect default branch: remote HEADs disagree: %s", strings.Join(branches, ", "))
	}
	return "", fmt.Errorf("detect default branch: no local remote HEAD is configured: %w", errors.Join(errs...))
}

// IsClean reports whether path has no tracked or untracked changes.
func (c *Client) IsClean(ctx context.Context, path string) (bool, error) {
	out, err := c.run(ctx, path, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) == "", nil
}

// IsAncestor reports whether ancestor is reachable from descendant.
func (c *Client) IsAncestor(ctx context.Context, repo model.Repository, ancestor, descendant string) (bool, error) {
	if err := validateRepository(repo); err != nil {
		return false, err
	}
	if strings.TrimSpace(ancestor) == "" || strings.TrimSpace(descendant) == "" {
		return false, errors.New("ancestor and descendant refs are required")
	}
	if !strings.HasPrefix(descendant, "refs/") && !isFullObjectID(descendant) {
		descendant = "refs/heads/" + descendant
	}
	_, err := c.run(ctx, repo.PrimaryPath, "merge-base", "--is-ancestor", ancestor, descendant)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// RemoteContains reports whether any remote branch contains commit.
func (c *Client) RemoteContains(ctx context.Context, repo model.Repository, commit string) (bool, error) {
	if err := validateRepository(repo); err != nil {
		return false, err
	}
	if strings.TrimSpace(commit) == "" {
		return false, errors.New("commit is required")
	}
	out, err := c.run(ctx, repo.PrimaryPath, "branch", "-r", "--contains", commit, "--format=%(refname)")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasSuffix(line, "/HEAD") {
			return true, nil
		}
	}
	return false, nil
}

// DiskUsage returns a recursive size in bytes for path.
func DiskUsage(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("walk %q: %w", p, walkErr)
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %q: %w", p, err)
		}
		total += info.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

// DiskUsage returns a recursive size in bytes for path.
func (c *Client) DiskUsage(path string) (int64, error) {
	return DiskUsage(path)
}

// Remove removes a registered worktree.
func (c *Client) Remove(ctx context.Context, repo model.Repository, path string) error {
	if err := validateRepository(repo); err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return errors.New("worktree path is required")
	}
	_, err := c.run(ctx, repo.PrimaryPath, "worktree", "remove", "--", path)
	return err
}

// Prune prunes stale Git worktree metadata.
func (c *Client) Prune(ctx context.Context, repo model.Repository) error {
	if err := validateRepository(repo); err != nil {
		return err
	}
	_, err := c.run(ctx, repo.PrimaryPath, "worktree", "prune", "--expire=now")
	return err
}

// DeleteBranch deletes a local branch with Git's safe `-d` behavior.
func (c *Client) DeleteBranch(ctx context.Context, repo model.Repository, shortBranch string) error {
	if err := validateRepository(repo); err != nil {
		return err
	}
	if strings.TrimSpace(shortBranch) == "" {
		return errors.New("branch is required")
	}
	if strings.HasPrefix(shortBranch, "-") {
		return fmt.Errorf("refusing branch name that looks like an option: %q", shortBranch)
	}
	if strings.HasPrefix(shortBranch, "refs/") {
		return fmt.Errorf("expected short branch name, got %q", shortBranch)
	}
	_, err := c.run(ctx, repo.PrimaryPath, "branch", "-d", "--", shortBranch)
	return err
}

func (c *Client) commonGitDir(ctx context.Context, repoPath string) (string, error) {
	out, err := c.run(ctx, repoPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common := strings.TrimSpace(string(out))
	if common == "" {
		return "", errors.New("git returned empty common git dir")
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(repoPath, common)
	}
	abs, err := filepath.Abs(common)
	if err != nil {
		return "", err
	}
	if evaluated, err := filepath.EvalSymlinks(abs); err == nil {
		return evaluated, nil
	}
	return abs, nil
}

func (c *Client) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, errors.New("git command directory is required")
	}
	cmd := exec.CommandContext(ctx, c.gitBinary, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s %s in %q failed: %w: %s", c.gitBinary, strings.Join(args, " "), dir, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func validateRepository(repo model.Repository) error {
	if strings.TrimSpace(repo.PrimaryPath) == "" {
		return errors.New("repository path is required")
	}
	if strings.TrimSpace(repo.CommonDir) == "" {
		return errors.New("repository common git dir is required")
	}
	return nil
}

func shortBranch(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

func isFullObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}
