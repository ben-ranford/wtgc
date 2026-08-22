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
