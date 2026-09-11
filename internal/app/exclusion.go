package app

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/ben-ranford/wtgc/internal/model"
)

// exclusionBoundary is an invocation-scoped, canonical path allow/deny boundary.
// It deliberately retains a missing suffix below a resolved existing parent so stale
// worktree registrations can be protected without inspecting their working files.
type exclusionBoundary struct {
	values       []string
	sources      []string
	sourceValues []string
}

func newExclusionBoundary(paths []string) (exclusionBoundary, error) {
	result := exclusionBoundary{values: make([]string, 0, len(paths))}
	for _, path := range paths {
		source, err := absolutePath(path)
		if err != nil {
			return exclusionBoundary{}, fmt.Errorf("exclude %q: %w", path, err)
		}
		canonical, err := canonicalExclusionPath(source)
		if err != nil {
			return exclusionBoundary{}, fmt.Errorf("exclude %q: %w", path, err)
		}
		duplicate := false
		for _, existing := range result.values {
			if samePath(existing, canonical) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			result.values = append(result.values, canonical)
		}
		result.sources = append(result.sources, source)
		result.sourceValues = append(result.sourceValues, canonical)
	}
	return result, nil
}

func absolutePath(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func canonicalExclusionPath(path string) (string, error) {
	abs, err := absolutePath(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	var suffix []string
	for {
		_, statErr := os.Lstat(abs)
		if statErr == nil {
			resolved, resolveErr := filepath.EvalSymlinks(abs)
			if resolveErr != nil {
				return "", resolveErr
			}
			info, infoErr := os.Stat(resolved)
			if infoErr != nil || !info.IsDir() {
				if infoErr != nil {
					return "", infoErr
				}
				return "", fmt.Errorf("path %q is not a directory", abs)
			}
			return filepath.Join(append([]string{filepath.Clean(resolved)}, suffix...)...), nil
		}
		if !os.IsNotExist(statErr) {
			return "", statErr
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", fmt.Errorf("no existing ancestor")
		}
		suffix = append([]string{filepath.Base(abs)}, suffix...)
		abs = parent
	}
}

func (b exclusionBoundary) paths() []string { return slices.Clone(b.values) }

func (b exclusionBoundary) contains(path string) (bool, error) {
	if len(b.values) == 0 {
		return false, nil
	}
	canonical, err := canonicalExclusionPath(path)
	if err != nil {
		return false, err
	}
	for _, excluded := range b.values {
		if pathContains(excluded, canonical) || pathContains(canonical, excluded) {
			return true, nil
		}
	}
	return false, nil
}

func (b exclusionBoundary) revalidate() error {
	for i, source := range b.sources {
		current, err := canonicalExclusionPath(source)
		if err != nil || !samePath(current, b.sourceValues[i]) {
			if err != nil {
				return fmt.Errorf("excluded scope changed: %w", err)
			}
			return fmt.Errorf("excluded scope changed from %q", b.sourceValues[i])
		}
	}
	return nil
}

func pathContains(parent, candidate string) bool {
	if samePath(parent, candidate) {
		return true
	}
	if runtime.GOOS == "windows" {
		parent, candidate = strings.ToLower(parent), strings.ToLower(candidate)
	}
	rel, err := filepath.Rel(parent, candidate)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func samePath(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func excludedWorktree(repo model.Repository, record model.RegisteredWorktree, defaultBranch string) model.Worktree {
	measured := false
	return model.Worktree{
		Path: record.Path, Branch: record.Branch, Head: record.Head, Repository: repo.PrimaryPath,
		DefaultBranch: defaultBranch, Primary: record.Primary, Detached: record.Detached, Locked: record.Locked,
		Prunable: record.Prunable, Classification: model.Kept, Action: model.ActionKept,
		Reason:          "excluded by invocation scope",
		WorktreeDetails: &model.WorktreeDetails{Excluded: true, DiskBytesMeasured: &measured},
	}
}

func exclusionMembershipError(repo model.Repository, record model.RegisteredWorktree, err error) model.Worktree {
	item := excludedWorktree(repo, record, "")
	item.Classification = model.Error
	item.Reason = "kept because exclusion membership could not be proven"
	item.Error = fmt.Sprintf("evaluate exclusion membership: %v", err)
	return item
}
