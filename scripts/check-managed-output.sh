#!/usr/bin/env sh
set -eu

test_root=".codex-artifacts/managed-output-check.$$"
helper="./scripts/managed-output.sh"

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

mkdir -p "$test_root"

"$helper" ensure "$test_root/managed"
[ -f "$test_root/managed/.wtgc-managed-output" ] || fail "managed marker was not created"
printf 'old\n' > "$test_root/managed/old.txt"
"$helper" reset "$test_root/managed"
[ -f "$test_root/managed/.wtgc-managed-output" ] || fail "managed marker was not recreated after reset"
[ ! -e "$test_root/managed/old.txt" ] || fail "managed reset preserved stale file"
"$helper" remove "$test_root/managed"
[ ! -e "$test_root/managed" ] || fail "managed remove left directory behind"

mkdir -p "$test_root/foreign"
printf 'keep\n' > "$test_root/foreign/keep.txt"
expect_failure "$helper" reset "$test_root/foreign"
expect_failure "$helper" remove "$test_root/foreign"
[ -f "$test_root/foreign/keep.txt" ] || fail "foreign directory was mutated"

mkdir -p "$test_root/forged-marker"
printf 'not owned\n' > "$test_root/forged-marker/.wtgc-managed-output"
printf 'keep\n' > "$test_root/forged-marker/keep.txt"
expect_failure "$helper" reset "$test_root/forged-marker"
expect_failure "$helper" remove "$test_root/forged-marker"
[ -f "$test_root/forged-marker/keep.txt" ] || fail "invalid marker authorized deletion"

mkdir -p "$test_root/foreign-release"
printf 'keep\n' > "$test_root/foreign-release/keep.txt"
expect_failure "${MAKE:-make}" --no-print-directory release VERSION=managed-check PLATFORMS="$(go env GOOS)/$(go env GOARCH)" DIST_DIR="$test_root/foreign-release"
[ -f "$test_root/foreign-release/keep.txt" ] || fail "foreign release directory was mutated"

mkdir -p "$test_root/sentinel"
printf 'keep\n' > "$test_root/sentinel/keep.txt"
expect_failure "$helper" reset "$test_root/ok/../../sentinel"
[ -f "$test_root/sentinel/keep.txt" ] || fail "traversal target was mutated"

ln -s sentinel "$test_root/link"
expect_failure "$helper" reset "$test_root/link"
[ -f "$test_root/sentinel/keep.txt" ] || fail "symlink target was mutated"

"$helper" reset "$test_root/source"
mkdir -p "$test_root/source/pkg"
printf 'archive\n' > "$test_root/source/pkg/wtgc-test.tar.gz"
expect_failure ./scripts/flatten-release-artifacts.sh "$test_root/source" "$test_root/ok/../../sentinel"
[ -f "$test_root/sentinel/keep.txt" ] || fail "flatten traversal target was mutated"
expect_failure ./scripts/flatten-release-artifacts.sh "$test_root/source" "$test_root/source"
expect_failure ./scripts/flatten-release-artifacts.sh "$test_root/source" "$test_root/source/child"
expect_failure ./scripts/flatten-release-artifacts.sh "$test_root/source" "$test_root/source/missing/release"
expect_failure ./scripts/flatten-release-artifacts.sh "$test_root/source" "$test_root"
"$helper" reset "$test_root/flattened"
./scripts/flatten-release-artifacts.sh "$test_root/source" "$test_root/flattened" >/dev/null
[ -f "$test_root/flattened/wtgc-test.tar.gz" ] || fail "flatten did not copy archive"
[ -f "$test_root/flattened/checksums.txt" ] || fail "flatten did not write checksums"

release_dist="$test_root/release-dist"
"${MAKE:-make}" --no-print-directory release VERSION=managed-check PLATFORMS="$(go env GOOS)/$(go env GOARCH)" DIST_DIR="$release_dist" >/dev/null
[ -f "$release_dist/checksums.txt" ] || fail "release did not write checksums"
archive_count="$(find "$release_dist" -type f \( -name '*.tar.gz' -o -name '*.zip' \) | wc -l | tr -d ' ')"
[ "$archive_count" -eq 1 ] || fail "release wrote $archive_count archives; expected 1"
if find "$release_dist" -name .wtgc-managed-output | grep . >/dev/null 2>&1; then
  :
else
  fail "release dist is not marked as managed"
fi

echo "Managed output checks passed"
