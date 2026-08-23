#!/usr/bin/env sh
set -eu

test_root=".codex-artifacts/release-automation-check.$$"

cleanup() {
  if [ -d "$test_root" ]; then
    find "$test_root" -mindepth 1 -exec rm -rf {} +
    rmdir "$test_root"
  fi
}
trap cleanup EXIT INT TERM

fail() {
  echo "$*" >&2
  exit 1
}

expect_failure() {
  if "$@" >/dev/null 2>&1; then
    fail "expected failure: $*"
  fi
}

expect_ref_failure() {
  repo="$1"
  helper="$2"
  tag="$3"
  sha="$4"
  if (cd "$repo" && sh "$helper" "$tag" "$sha") >/dev/null 2>&1; then
    fail "expected release ref verification failure: $tag $sha"
  fi
}

expect_contains() {
  subject="$1"
  expected="$2"
  message="$3"
  printf '%s\n' "$subject" | grep -F -- "$expected" >/dev/null || fail "$message"
}

expect_matches() {
  subject="$1"
  pattern="$2"
  message="$3"
  printf '%s\n' "$subject" | grep -E -- "$pattern" >/dev/null || fail "$message"
}

workflow_job() {
  job="$1"
  awk -v job="$job" '
    $0 == "  " job ":" { found = 1 }
    found && $0 ~ /^  [[:alnum:]_-]+:$/ && $0 != "  " job ":" { exit }
    found { print }
  ' .github/workflows/release.yml
}

tap_gate="$(workflow_job homebrew-tap-token-gate)"
tap_validation="$(workflow_job validate-homebrew-tap)"
tap_publish="$(workflow_job update-homebrew-tap)"

[ -n "$tap_gate" ] || fail "release workflow is missing the Homebrew token gate job"
[ -n "$tap_validation" ] || fail "release workflow is missing the tokenless Homebrew validation job"
[ -n "$tap_publish" ] || fail "release workflow is missing the Homebrew tap publication job"

expect_contains "$tap_gate" "HOMEBREW_TAP_TOKEN" "Homebrew token gate must inspect HOMEBREW_TAP_TOKEN"
expect_contains "$tap_gate" "outputs:" "Homebrew token gate must expose token availability to downstream jobs"

expect_contains "$tap_validation" "needs:" "Homebrew validation must wait for release publication"
expect_contains "$tap_validation" "publish" "Homebrew validation must run after release publication"
expect_contains "$tap_validation" "brew audit" "Homebrew validation must audit the generated formula"
expect_contains "$tap_validation" "brew install --build-from-source" "Homebrew validation must source-build the formula"
expect_contains "$tap_validation" "brew test" "Homebrew validation must test the installed formula"
expect_contains "$tap_validation" "SOURCE_SHA" "Homebrew validation must bind its formula to the release SHA"
expect_contains "$tap_validation" "archive/\${SOURCE_SHA}.tar.gz" "Homebrew validation must use an immutable source archive"
expect_contains "$tap_validation" "sha256" "Homebrew validation must checksum the source archive"
case "$tap_validation" in
  *HOMEBREW_TAP_TOKEN*)
    fail "Homebrew validation must not receive the tap token"
    ;;
  *)
    ;;
esac

expect_contains "$tap_publish" "needs:" "Homebrew tap publication must wait for validation"
expect_contains "$tap_publish" "validate-homebrew-tap" "Homebrew tap publication must depend on validation"
expect_contains "$tap_publish" "HOMEBREW_TAP_TOKEN" "Homebrew tap publication must receive the tap token"
expect_contains "$tap_publish" "homebrew-tap-main" "Homebrew tap publication must serialize writes to tap main"
expect_matches "$tap_publish" "diff( --cached)? --quiet" "Homebrew tap publication must skip no-op updates"
expect_contains "$tap_publish" "rebase origin/main" "Homebrew tap publication must rebase before pushing"
expect_matches "$tap_publish" "(for attempt in 1 2 3|seq 1 3)" "Homebrew tap publication must retry conflicting pushes"
expect_contains "$tap_publish" "unset HOMEBREW_TAP_TOKEN" "Homebrew tap publication must clear the token after authentication"
expect_contains "$tap_publish" "Gem::Version" "Homebrew tap publication must refuse stale formula versions"
expect_contains "$tap_publish" "SOURCE_SHA" "Homebrew tap publication must bind its formula to the release SHA"
expect_contains "$tap_publish" "archive/\${SOURCE_SHA}.tar.gz" "Homebrew tap publication must use an immutable source archive"

mkdir -p "$test_root/repo"
git -C "$test_root/repo" init -q
git -C "$test_root/repo" config user.name "wtgc release test"
git -C "$test_root/repo" config user.email "wtgc-release-test@example.invalid"
printf 'first\n' > "$test_root/repo/file.txt"
git -C "$test_root/repo" add file.txt
git -C "$test_root/repo" commit -qm first
first_sha="$(git -C "$test_root/repo" rev-parse HEAD)"
git -C "$test_root/repo" tag v1.0.0

(
  cd "$test_root/repo"
  sh "$(cd ../../.. && pwd -P)/scripts/verify-release-ref.sh" v1.0.0 "$first_sha"
) >/dev/null

printf 'second\n' >> "$test_root/repo/file.txt"
git -C "$test_root/repo" commit -qam second
second_sha="$(git -C "$test_root/repo" rev-parse HEAD)"
expect_ref_failure "$test_root/repo" "$(pwd -P)/scripts/verify-release-ref.sh" v1.0.0 "$second_sha"
expect_ref_failure "$test_root/repo" "$(pwd -P)/scripts/verify-release-ref.sh" missing "$first_sha"

mkdir -p "$test_root/bin" "$test_root/assets" "$test_root/gh-state/assets"
printf 'archive\n' > "$test_root/assets/wtgc_v1.0.0_linux_amd64.tar.gz"
printf 'checksum\n' > "$test_root/assets/checksums.txt"
printf 'sbom\n' > "$test_root/assets/wtgc_v1.0.0_sbom.spdx.json"
printf 'internal\n' > "$test_root/assets/.wtgc-managed-output"
printf 'not a release asset\n' > "$test_root/assets/notes.txt"

cat > "$test_root/bin/gh" <<'EOF'
#!/usr/bin/env sh
set -eu

state="${WTGC_TEST_GH_STATE:?}"
command="${1:-} ${2:-}"

case "$command" in
  "release view")
    [ -f "$state/exists" ] || exit 1
    case "$*" in
      *"--json isDraft"*) cat "$state/draft" ;;
      *"--json assets"*) find "$state/assets" -type f -exec basename {} \; | sort ;;
      *) ;;
    esac
    ;;
  "release create")
    : > "$state/exists"
    printf 'true\n' > "$state/draft"
    ;;
  "release upload")
    cp "$4" "$state/assets/$(basename "$4")"
    ;;
  "release download")
    cp "$state/assets/$5" "$7/$5"
    ;;
  "release edit")
    printf 'false\n' > "$state/draft"
    ;;
  *)
    echo "unexpected gh invocation: $*" >&2
    exit 1
    ;;
esac
EOF
chmod +x "$test_root/bin/gh"

PATH="$(pwd -P)/$test_root/bin:$PATH" \
  WTGC_TEST_GH_STATE="$(pwd -P)/$test_root/gh-state" \
  sh ./scripts/publish-release-assets.sh v1.0.0 "$test_root/assets" >/dev/null

[ "$(cat "$test_root/gh-state/draft")" = "false" ] || fail "release was not published"
[ -f "$test_root/gh-state/assets/checksums.txt" ] || fail "checksums were not uploaded"
[ -f "$test_root/gh-state/assets/wtgc_v1.0.0_linux_amd64.tar.gz" ] || fail "archive was not uploaded"
[ -f "$test_root/gh-state/assets/wtgc_v1.0.0_sbom.spdx.json" ] || fail "SBOM was not uploaded"
[ ! -e "$test_root/gh-state/assets/.wtgc-managed-output" ] || fail "managed marker was uploaded"
[ ! -e "$test_root/gh-state/assets/notes.txt" ] || fail "unmanaged file was uploaded"

printf 'unexpected\n' > "$test_root/gh-state/assets/unexpected.txt"
expect_failure env \
  PATH="$(pwd -P)/$test_root/bin:$PATH" \
  WTGC_TEST_GH_STATE="$(pwd -P)/$test_root/gh-state" \
  sh ./scripts/publish-release-assets.sh v1.0.0 "$test_root/assets"

echo "Release automation integrity checks passed."
