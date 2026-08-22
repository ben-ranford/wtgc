package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const maxBodyBytes = 1 << 20

var (
	titlePattern        = regexp.MustCompile(`^(feat|fix|perf|docs|refactor|revert|test|ci|build|chore)(\([a-z0-9][a-z0-9._/-]*\))?!?: [^\s].+$`)
	releaseTitlePattern = regexp.MustCompile(`^chore: release [0-9]+\.[0-9]+\.[0-9]+$`)

	placeholderTexts = []string{
		"What user-visible problem or engineering risk does this address?",
		"Commands and checks run:",
		"Anything reviewers should pay special attention to?",
	}
	requiredHeadings     = []string{"Problem", "Changes", "Validation", "Compatibility And Safety", "Notes"}
	requiredSafetyFields = []string{"Worktree deletion behavior", "JSON schema or output changes", "Migration or release impact"}
)

type repoMergePolicy struct {
	allowMergeCommit       string
	allowRebaseMerge       string
	allowSquashMerge       string
	squashMergeCommitTitle string
}

type releasePleaseIdentity struct {
	headRepositoryFullName string
	repositoryFullName     string
}

func main() {
	os.Exit(run(os.Args[1:], os.Getenv, os.Stderr))
}

func run(args []string, getenv func(string) string, stderr io.Writer) int {
	fs := flag.NewFlagSet("prcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	title := fs.String("title", strings.TrimSpace(getenv("PR_TITLE")), "pull request title")
	headRef := fs.String("head-ref", strings.TrimSpace(getenv("PR_HEAD_REF")), "pull request head ref")
	headRepositoryFullName := fs.String("head-repo-full-name", strings.TrimSpace(getenv("PR_HEAD_REPO_FULL_NAME")), "pull request head repository full name")
	repositoryFullName := fs.String("repo-full-name", strings.TrimSpace(getenv("REPOSITORY_FULL_NAME")), "repository full name")
	bodyFile := fs.String("body-file", "", "path to a file containing the pull request body")
	checkRepoPolicy := fs.Bool("check-repo-policy", false, "validate repository merge policy")
	allowMergeCommit := fs.String("allow-merge-commit", strings.TrimSpace(getenv("REPO_ALLOW_MERGE_COMMIT")), "whether the repository allows merge commits")
	allowRebaseMerge := fs.String("allow-rebase-merge", strings.TrimSpace(getenv("REPO_ALLOW_REBASE_MERGE")), "whether the repository allows rebase merges")
	allowSquashMerge := fs.String("allow-squash-merge", strings.TrimSpace(getenv("REPO_ALLOW_SQUASH_MERGE")), "whether the repository allows squash merges")
	squashMergeCommitTitle := fs.String("squash-merge-commit-title", strings.TrimSpace(getenv("REPO_SQUASH_MERGE_COMMIT_TITLE")), "repository default squash merge title mode")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	body, err := readBody(*bodyFile, getenv)
	if err != nil {
		return writeError(stderr, "read PR body: %v\n", err)
	}
	identity := releasePleaseIdentity{*headRepositoryFullName, *repositoryFullName}
	if err := validate(*title, *headRef, body, identity); err != nil {
		return writeError(stderr, "%v\n", err)
	}
	if !*checkRepoPolicy {
		return 0
	}
	policy := repoMergePolicy{*allowMergeCommit, *allowRebaseMerge, *allowSquashMerge, *squashMergeCommitTitle}
	if err := validateRepoMergePolicy(policy); err != nil {
		return writeError(stderr, "%v\n", err)
	}
	return 0
}

func readBody(path string, getenv func(string) string) (string, error) {
	if path == "" {
		return strings.TrimSpace(getenv("PR_BODY")), nil
	}
	path = filepath.Clean(path)
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	defer root.Close()
	name := filepath.Base(path)
	info, err := root.Stat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("body file is not a regular file")
	}
	if info.Size() > maxBodyBytes {
		return "", fmt.Errorf("body exceeds %d byte limit", maxBodyBytes)
	}
	body, err := root.ReadFile(name)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func writeError(stderr io.Writer, format string, args ...any) int {
	if _, err := fmt.Fprintf(stderr, format, args...); err != nil {
		return 1
	}
	return 1
}

func validate(title, headRef, body string, identity releasePleaseIdentity) error {
	title = strings.TrimSpace(title)
	if !titlePattern.MatchString(title) {
		return errors.New("PR title must be a Conventional Commit title using one of feat, fix, perf, docs, refactor, revert, test, ci, build, or chore; use fix(scope): ... for bug fixes instead of bug: ...")
	}
	if isTrustedReleasePleasePR(headRef, title, identity) {
		return nil
	}

	sections := parseSections(body)
	var failures []string
	for _, heading := range requiredHeadings {
		content, ok := sections[heading]
		if !ok {
			failures = append(failures, fmt.Sprintf("PR body is missing required template section %q", heading))
			continue
		}
		if heading != "Notes" && !hasMeaningfulContent(content) {
			failures = append(failures, fmt.Sprintf("PR section %q must be completed; keep the heading and replace placeholder text with real content or N/A", heading))
		}
	}
	if safety, ok := sections["Compatibility And Safety"]; ok {
		for _, field := range requiredSafetyFields {
			if !fieldHasValue(safety, field) {
				failures = append(failures, fmt.Sprintf("Compatibility And Safety field %q must be present and filled, using None or N/A when there is no impact", field))
			}
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}

func isTrustedReleasePleasePR(headRef, title string, identity releasePleaseIdentity) bool {
	return strings.TrimSpace(headRef) == "release-please--branches--main" &&
		releaseTitlePattern.MatchString(strings.TrimSpace(title)) &&
		strings.TrimSpace(identity.headRepositoryFullName) != "" &&
		strings.TrimSpace(identity.headRepositoryFullName) == strings.TrimSpace(identity.repositoryFullName)
}

func parseSections(body string) map[string]string {
	sections := make(map[string]string)
	var current string
	var content strings.Builder
	inFence := false
	flush := func() {
		if current != "" {
			sections[current] = strings.TrimSpace(content.String())
			content.Reset()
		}
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		} else if !inFence {
			if heading, ok := parseH2(line); ok {
				flush()
				current = heading
				continue
			}
		}
		if current != "" {
			content.WriteString(line)
			content.WriteByte('\n')
		}
	}
	flush()
	return sections
}

func parseH2(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
		return "", false
	}
	return strings.TrimSpace(strings.TrimPrefix(line, "## ")), true
}

func hasMeaningfulContent(content string) bool {
	content = stripCodeFences(content)
	for _, placeholder := range placeholderTexts {
		content = strings.ReplaceAll(content, placeholder, "")
	}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && line != "-" && !(strings.HasPrefix(line, "<!--") && strings.HasSuffix(line, "-->")) {
			return true
		}
	}
	return false
}

func stripCodeFences(content string) string {
	var kept []string
	inFence := false
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
		} else if !inFence {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func fieldHasValue(content, field string) bool {
	pattern := regexp.MustCompile(`(?m)^-\s*` + regexp.QuoteMeta(field) + `:\s*(.+)$`)
	match := pattern.FindStringSubmatch(content)
	return len(match) == 2 && strings.TrimSpace(match[1]) != ""
}

func validateRepoMergePolicy(policy repoMergePolicy) error {
	expectations := []struct {
		actual, expected, message string
	}{
		{policy.allowSquashMerge, "true", "Repository must allow squash merges so release-please sees a single PR title-derived commit on main"},
		{policy.allowMergeCommit, "false", "Repository must disable merge commits so merged PRs cannot bypass the squash-title release policy"},
		{policy.allowRebaseMerge, "false", "Repository must disable rebase merges so merged PRs cannot bypass the squash-title release policy"},
		{policy.squashMergeCommitTitle, "PR_TITLE", "Repository squash merge titles must default to the PR title so release-please sees the validated Conventional Commit title"},
	}
	var failures []string
	for _, expectation := range expectations {
		if actual := strings.TrimSpace(expectation.actual); actual != expectation.expected {
			if actual == "" {
				failures = append(failures, fmt.Sprintf("%s (expected %q, got empty value)", expectation.message, expectation.expected))
			} else {
				failures = append(failures, fmt.Sprintf("%s (expected %q, got %q)", expectation.message, expectation.expected, actual))
			}
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "\n"))
	}
	return nil
}
