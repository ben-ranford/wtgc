#!/usr/bin/env sh
# Contract coverage for the diff-scoped static-suppression gate.
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
gate="$repo_root/scripts/check-suppressions.sh"
temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/wtgc-suppressions-test.XXXXXX")
cleanup() {
    find "$temporary_root" -type f -delete 2>/dev/null || true
    find "$temporary_root" -depth -type d -exec rmdir {} + 2>/dev/null || true
}
trap cleanup EXIT HUP INT TERM

fail() { printf '%s\n' "$*" >&2; exit 1; }
expect_failure() {
    if "$@" >"$temporary_root/output" 2>&1; then
        fail "expected failure: $*"
    fi
}
fixture_gate() {
    env -u GITHUB_REPOSITORY -u SUPPRESSION_VERIFY_ISSUES -u SUPPRESSION_MANIFEST -u GH_TOKEN -u GITHUB_TOKEN "$@"
}
new_repo() {
    case_root="$temporary_root/$1"
    git init -q -b main "$case_root"
    git -C "$case_root" config user.email wtgc-test@example.invalid
    git -C "$case_root" config user.name 'wtgc contract test'
    mkdir "$case_root/.ci"
    printf '%s\n' '{"suppressions":[]}' > "$case_root/.ci/static-suppressions.json"
    printf '%s\n' 'package fixture' > "$case_root/fixture.go"
    git -C "$case_root" add .
    git -C "$case_root" commit -qm baseline
    printf '%s\n' "$case_root"
}

positive=$(new_repo positive)
printf '%s\n' 'package fixture //nosec G204 -- argument vector reviewed' > "$positive/fixture.go"
printf '%s\n' '{"suppressions":[{"location":"fixture.go:1","rationale":"fixture","owner":"security","removal_condition":"replace fixture","issue":"https://github.com/example/repo/issues/1"}]}' > "$positive/.ci/static-suppressions.json"
(cd "$positive" && fixture_gate SUPPRESSION_BASE=HEAD "$gate")
(cd "$positive" && expect_failure env -u SUPPRESSION_MANIFEST -u GH_TOKEN -u GITHUB_TOKEN SUPPRESSION_BASE=HEAD SUPPRESSION_VERIFY_ISSUES=1 GITHUB_REPOSITORY=example/repo "$gate")
git -C "$positive" add .
git -C "$positive" commit -qm 'add tracked suppression'
printf '%s\n' '// unrelated change' >> "$positive/fixture.go"
(cd "$positive" && fixture_gate SUPPRESSION_BASE=HEAD "$gate")

missing_base=$(new_repo missing-base)
(cd "$missing_base" && expect_failure fixture_gate SUPPRESSION_BASE=missing "$gate")

untracked=$(new_repo untracked)
printf '%s\n' 'package fixture // NOSONAR' > "$untracked/untracked.go"
(cd "$untracked" && expect_failure fixture_gate SUPPRESSION_BASE=HEAD "$gate")

staged=$(new_repo staged)
printf '%s\n' 'package fixture //nolint:fixture' > "$staged/fixture.go"
git -C "$staged" add fixture.go
(cd "$staged" && expect_failure fixture_gate SUPPRESSION_BASE=HEAD "$gate")

malformed=$(new_repo malformed)
printf '%s\n' '{"suppressions":"not-an-array"}' > "$malformed/.ci/static-suppressions.json"
(cd "$malformed" && expect_failure fixture_gate SUPPRESSION_BASE=HEAD "$gate")

duplicate=$(new_repo duplicate)
printf '%s\n' '{"suppressions":[{"location":"fixture.go:1","rationale":"a","owner":"security","removal_condition":"x","issue":"https://github.com/example/repo/issues/1"},{"location":"fixture.go:1","rationale":"b","owner":"security","removal_condition":"x","issue":"https://github.com/example/repo/issues/2"}]}' > "$duplicate/.ci/static-suppressions.json"
(cd "$duplicate" && expect_failure fixture_gate SUPPRESSION_BASE=HEAD "$gate")

misleading_helper() { printf '%s\n' PASS; return 1; }
if misleading_helper >"$temporary_root/misleading-output" 2>&1; then
    fail 'misleading non-zero helper was accepted'
fi

printf '%s\n' 'suppression gate contract tests passed'
