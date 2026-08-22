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
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

const localBranchRefPrefix = "refs/heads/"

// Client executes Git commands for repository discovery and cleanup.
type Client struct {
	gitBinary      string
	commandTimeout time.Duration
}

// New returns a Git client. Empty gitBinary defaults to "git".
func New(gitBinary string) *Client {
	return NewWithTimeout(gitBinary, 0)
}

// NewWithTimeout returns a Git client with a per-command deadline. A non-positive
// timeout disables the client-level deadline and relies on the caller context.
func NewWithTimeout(gitBinary string, commandTimeout time.Duration) *Client {
	if gitBinary == "" {
		gitBinary = "git"
	}
	return &Client{gitBinary: gitBinary, commandTimeout: commandTimeout}
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
	for _, root := range roots {
		c.discoverRoot(ctx, root, seen, &errs)
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

func (c *Client) discoverRoot(ctx context.Context, root string, seen map[string]string, errs *[]error) {
	if strings.TrimSpace(root) == "" {
		*errs = append(*errs, errors.New("empty discovery root"))
		return
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("resolve root %q: %w", root, err))
		return
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("stat root %q: %w", absRoot, err))
		return
	}
	if !info.IsDir() {
		*errs = append(*errs, fmt.Errorf("root %q is not a directory", absRoot))
		return
	}
	resolvedRoot, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("resolve root symlinks %q: %w", absRoot, err))
		return
	}
	if err := c.walkRepositoryRoot(ctx, resolvedRoot, seen, errs); err != nil {
		*errs = append(*errs, fmt.Errorf("walk root %q: %w", resolvedRoot, err))
	}
}

func (c *Client) walkRepositoryRoot(ctx context.Context, root string, seen map[string]string, errs *[]error) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			*errs = append(*errs, fmt.Errorf("walk %q: %w", path, walkErr))
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != ".git" {
			return nil
		}
		worktreePath := filepath.Dir(path)
		commonGitDir, err := c.commonGitDir(ctx, worktreePath)
		if err != nil {
			*errs = append(*errs, fmt.Errorf("common git dir for %q: %w", worktreePath, err))
		} else if _, ok := seen[commonGitDir]; !ok {
			seen[commonGitDir] = worktreePath
		}
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
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
		if err := applyWorktreeField(current, key, value, hasValue); err != nil {
			errs = append(errs, err)
		}
	}
	finish()
	return records, errors.Join(errs...)
}

func applyWorktreeField(record *model.RegisteredWorktree, key, value string, hasValue bool) error {
	switch key {
	case "HEAD":
		if !hasValue || value == "" {
			return fmt.Errorf("malformed HEAD field for %q", record.Path)
		}
		record.Head = value
	case "branch":
		if !hasValue || value == "" {
			return fmt.Errorf("malformed branch field for %q", record.Path)
		}
		record.Branch = shortBranch(value)
	case "bare":
		record.Bare = true
	case "detached":
		record.Detached = true
	case "locked":
		record.Locked = true
	case "prunable":
		record.Prunable = true
	default:
		return fmt.Errorf("unknown worktree field %q for %q", key, record.Path)
	}
	return nil
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
	if len(resolved) > 1 {
		branches := make([]string, 0, len(resolved))
		for branch := range resolved {
			branches = append(branches, branch)
		}
		sort.Strings(branches)
		return "", fmt.Errorf("detect default branch: remote HEADs disagree: %s", strings.Join(branches, ", "))
	}
	if len(resolved) == 0 {
		return "", fmt.Errorf("detect default branch: no local remote HEAD is configured: %w", errors.Join(errs...))
	}
	if len(errs) > 0 {
		return "", fmt.Errorf("detect default branch: every remote must have a valid local remote HEAD: %w", errors.Join(errs...))
	}
	if len(resolved) == 1 {
		for branch := range resolved {
			return branch, nil
		}
	}
	return "", errors.New("detect default branch: no local remote HEAD is configured")
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
		descendant = localBranchRefPrefix + descendant
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
			if isTransientWalkNotExist(path, p, walkErr) {
				return nil
			}
			return fmt.Errorf("walk %q: %w", p, walkErr)
		}
		info, err := d.Info()
		if err != nil {
			if isTransientWalkNotExist(path, p, err) {
				return nil
			}
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

// isTransientWalkNotExist ignores files that disappear after WalkDir reads a
// directory entry. Git frequently creates and removes index locks while a
// worktree scan is in progress; a missing descendant cannot affect cleanup
// safety. A missing root remains an error.
func isTransientWalkNotExist(root, path string, err error) bool {
	return path != root && errors.Is(err, fs.ErrNotExist)
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

// DeleteBranch deletes a local branch only when its exact current tip is merged
// into the named default branch.
func (c *Client) DeleteBranch(ctx context.Context, repo model.Repository, shortBranch, defaultBranch string) error {
	if err := validateRepository(repo); err != nil {
		return err
	}
	if err := validateShortBranchName("branch", shortBranch); err != nil {
		return err
	}
	if err := validateShortBranchName("default branch", defaultBranch); err != nil {
		return err
	}
	if shortBranch == defaultBranch {
		return fmt.Errorf("refusing to delete default branch %q", defaultBranch)
	}

	branchRef := localBranchRefPrefix + shortBranch
	defaultRef := localBranchRefPrefix + defaultBranch
	out, err := c.run(ctx, repo.PrimaryPath, "rev-parse", "--verify", branchRef+"^{commit}")
	if err != nil {
		return fmt.Errorf("resolve branch %s: %w", branchRef, err)
	}
	branchOID := strings.TrimSpace(string(out))
	if !isFullObjectID(branchOID) {
		return fmt.Errorf("resolve branch %s: unexpected object id %q", branchRef, branchOID)
	}
	if _, err := c.run(ctx, repo.PrimaryPath, "rev-parse", "--verify", defaultRef+"^{commit}"); err != nil {
		return fmt.Errorf("resolve default branch %s: %w", defaultRef, err)
	}
	merged, err := c.IsAncestor(ctx, repo, branchOID, defaultRef)
	if err != nil {
		return fmt.Errorf("prove %s is merged into %s: %w", branchRef, defaultRef, err)
	}
	if !merged {
		return fmt.Errorf("refusing to delete unmerged branch %q", shortBranch)
	}
	_, err = c.run(ctx, repo.PrimaryPath, "update-ref", "-d", branchRef, branchOID)
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
	commandCtx := ctx
	cancel := func() {
		// No timeout is configured.
	}
	if c.commandTimeout > 0 {
		commandCtx, cancel = context.WithTimeout(ctx, c.commandTimeout)
	}
	defer cancel()

	cmd := exec.CommandContext(commandCtx, c.gitBinary, args...)
	cmd.Dir = dir
	configureCommand(cmd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if commandCtx.Err() != nil {
			if errors.Is(commandCtx.Err(), context.DeadlineExceeded) && c.commandTimeout > 0 {
				return nil, fmt.Errorf("%s %s in %q timed out after %s: %w: %s", c.gitBinary, strings.Join(args, " "), dir, c.commandTimeout, commandCtx.Err(), strings.TrimSpace(string(out)))
			}
			return nil, fmt.Errorf("%s %s in %q canceled: %w: %s", c.gitBinary, strings.Join(args, " "), dir, commandCtx.Err(), strings.TrimSpace(string(out)))
		}
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
	return strings.TrimPrefix(ref, localBranchRefPrefix)
}

func validateShortBranchName(label, branch string) error {
	if strings.TrimSpace(branch) == "" {
		return fmt.Errorf("%s is required", label)
	}
	if branch != strings.TrimSpace(branch) {
		return fmt.Errorf("%s has surrounding whitespace: %q", label, branch)
	}
	if strings.HasPrefix(branch, "-") {
		return fmt.Errorf("refusing %s name that looks like an option: %q", label, branch)
	}
	if strings.HasPrefix(branch, "refs/") {
		return fmt.Errorf("expected short %s name, got %q", label, branch)
	}
	if strings.Contains(branch, "\\") {
		return fmt.Errorf("%s contains invalid character: %q", label, branch)
	}
	if branch == "@" || strings.Contains(branch, "..") || strings.Contains(branch, "//") || strings.Contains(branch, "@{") {
		return fmt.Errorf("%s is not a valid short branch name: %q", label, branch)
	}
	if strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".") || strings.HasSuffix(branch, ".lock") {
		return fmt.Errorf("%s is not a valid short branch name: %q", label, branch)
	}
	for _, char := range branch {
		if char <= ' ' || strings.ContainsRune("~^:?*[", char) || char == 0x7f {
			return fmt.Errorf("%s contains invalid character: %q", label, branch)
		}
	}
	return nil
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
