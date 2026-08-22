package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunAcceptsTemplateFromEnvironment(t *testing.T) {
	var stderr bytes.Buffer
	code := run(nil, mapEnv(map[string]string{"PR_TITLE": "fix(cli): enforce PR metadata", "PR_HEAD_REF": "bug/42-pr-metadata", "PR_BODY": validBody()}), &stderr)
	if code != 0 {
		t.Fatalf("run exited with %d, stderr: %s", code, stderr.String())
	}
}

func TestRunReadsBodyFileAndRejectsOversizedInput(t *testing.T) {
	bodyFile := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyFile, []byte(validBody()), 0o600); err != nil {
		t.Fatalf("write body file: %v", err)
	}
	var stderr bytes.Buffer
	args := []string{"--title", "fix(cli): enforce PR metadata", "--body-file", bodyFile}
	if code := run(args, mapEnv(nil), &stderr); code != 0 {
		t.Fatalf("run exited with %d, stderr: %s", code, stderr.String())
	}
	if err := os.WriteFile(bodyFile, []byte(strings.Repeat("x", maxBodyBytes+1)), 0o600); err != nil {
		t.Fatalf("write oversized body file: %v", err)
	}
	stderr.Reset()
	if code := run(args, mapEnv(nil), &stderr); code != 1 || !strings.Contains(stderr.String(), "exceeds") {
		t.Fatalf("oversized body result = (%d, %q), want a readable failure", code, stderr.String())
	}
}

func TestReadBodyRejectsDirectories(t *testing.T) {
	_, err := readBody(t.TempDir(), mapEnv(nil))
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory read error = %v", err)
	}
}

func TestRunRejectsMissingBodyFileAndInvalidFlags(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"--body-file", filepath.Join(t.TempDir(), "missing.md")}, mapEnv(nil), &stderr); code != 1 || !strings.Contains(stderr.String(), "read PR body") {
		t.Fatalf("missing file result = (%d, %q)", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"--unknown"}, mapEnv(nil), &stderr); code != 2 {
		t.Fatalf("invalid flag exit code = %d, want 2", code)
	}
}

func TestValidateRejectsInvalidTitleAndIncompleteMetadata(t *testing.T) {
	if err := validate("bug: wrong type", "bug/42", validBody(), releasePleaseIdentity{}); err == nil || !strings.Contains(err.Error(), "use fix(scope)") {
		t.Fatalf("invalid title error = %v", err)
	}
	body := strings.Replace(validBody(), "- Migration or release impact: None", "", 1)
	body = strings.Replace(body, "Changes made in this pull request.", "<!-- TODO -->", 1)
	err := validate("fix(cli): enforce PR metadata", "bug/42", body, releasePleaseIdentity{})
	if err == nil || !strings.Contains(err.Error(), `section "Changes" must be completed`) || !strings.Contains(err.Error(), `field "Migration or release impact"`) {
		t.Fatalf("incomplete metadata error = %v", err)
	}
}

func TestValidateRequiresAllHeadingsButAllowsBlankNotes(t *testing.T) {
	body := strings.Replace(validBody(), "## Notes\n\nNo additional reviewer notes.\n", "## Notes\n", 1)
	if err := validate("docs: clarify metadata policy", "docs/metadata", body, releasePleaseIdentity{}); err != nil {
		t.Fatalf("blank notes rejected: %v", err)
	}
	body = strings.Replace(validBody(), "## Notes", "## Reviewer notes", 1)
	err := validate("docs: clarify metadata policy", "docs/metadata", body, releasePleaseIdentity{})
	if err == nil || !strings.Contains(err.Error(), `missing required template section "Notes"`) {
		t.Fatalf("missing heading error = %v", err)
	}
}

func TestValidateOnlyExemptsTrustedReleasePleasePRs(t *testing.T) {
	identity := releasePleaseIdentity{headRepositoryFullName: "ben-ranford/wtgc", repositoryFullName: "ben-ranford/wtgc", releasePleaseAuthor: "ben-ranford", pullRequestAuthor: "ben-ranford"}
	if err := validate("chore: release 1.2.3", "release-please--branches--main--components--wtgc", "## Changelog\n", identity); err != nil {
		t.Fatalf("trusted release PR rejected: %v", err)
	}
	for _, candidate := range []releasePleaseIdentity{{}, {headRepositoryFullName: "fork/wtgc", repositoryFullName: "ben-ranford/wtgc", releasePleaseAuthor: "ben-ranford", pullRequestAuthor: "ben-ranford"}, {headRepositoryFullName: "ben-ranford/wtgc", repositoryFullName: "ben-ranford/wtgc", releasePleaseAuthor: "ben-ranford", pullRequestAuthor: "other-user"}} {
		err := validate("chore: release 1.2.3", "release-please--branches--main--components--wtgc", "## Changelog\n", candidate)
		if err == nil || !strings.Contains(err.Error(), `missing required template section`) {
			t.Fatalf("untrusted release PR error = %v", err)
		}
	}
}

func TestRunValidatesRepositoryMergePolicy(t *testing.T) {
	values := map[string]string{
		"PR_TITLE": "fix(cli): enforce PR metadata", "PR_HEAD_REF": "bug/42", "PR_BODY": validBody(),
		"REPO_ALLOW_MERGE_COMMIT": "false", "REPO_ALLOW_REBASE_MERGE": "false", "REPO_ALLOW_SQUASH_MERGE": "true", "REPO_SQUASH_MERGE_COMMIT_TITLE": "PR_TITLE",
	}
	var stderr bytes.Buffer
	if run([]string{"--check-repo-policy"}, mapEnv(values), &stderr) != 0 {
		t.Fatalf("valid policy rejected: %s", stderr.String())
	}
	values["REPO_SQUASH_MERGE_COMMIT_TITLE"] = "COMMIT_OR_PR_TITLE"
	stderr.Reset()
	if code := run([]string{"--check-repo-policy"}, mapEnv(values), &stderr); code != 1 || !strings.Contains(stderr.String(), "default to the PR title") {
		t.Fatalf("invalid policy result = (%d, %q)", code, stderr.String())
	}
}

func TestHelpers(t *testing.T) {
	if heading, ok := parseH2("### Nested"); ok || heading != "" {
		t.Fatalf("parseH2 accepted nested heading")
	}
	if heading, ok := parseH2("## Heading"); !ok || heading != "Heading" {
		t.Fatalf("parseH2 = (%q, %t)", heading, ok)
	}
	if hasMeaningfulContent("<!-- comment -->\n```\nmake ci\n```") {
		t.Fatal("comments and code fences counted as meaningful content")
	}
	if hasMeaningfulContent("<!--\nhidden placeholder\n-->") {
		t.Fatal("multiline HTML comments counted as meaningful content")
	}
	if !fieldHasValue("- Safety: None", "Safety") || fieldHasValue("- Safety:  ", "Safety") || fieldHasValue("- Safety: <!-- not filled -->", "Safety") || fieldHasValue("```yaml\n- Safety: None\n```", "Safety") {
		t.Fatal("fieldHasValue did not distinguish filled and empty fields")
	}
	if code := writeError(&errWriter{}, "failure"); code != 1 {
		t.Fatalf("writeError = %d", code)
	}
}

func TestValidateIgnoresHeadingsInsideCodeFences(t *testing.T) {
	body := validBody() + "\n```markdown\n## Changes\n\n```\n"
	if err := validate("docs: show template examples", "docs/template", body, releasePleaseIdentity{}); err != nil {
		t.Fatalf("fenced heading invalidated template: %v", err)
	}
}

func validBody() string {
	return `## Problem

Metadata validation keeps release notes and safety review information complete.

## Changes

Changes made in this pull request.

## Validation

Commands and checks run:

- go test ./tools/prcheck

## Compatibility And Safety

- Worktree deletion behavior: None
- JSON schema or output changes: None
- Migration or release impact: None

## Notes

No additional reviewer notes.
`
}

func mapEnv(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

type errWriter struct{}

func (*errWriter) Write([]byte) (int, error) { return 0, os.ErrClosed }
