#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT HUP INT TERM
mkdir -p "$fixture/.github/workflows"
sha=3cdb78d0f62ad29dd32de765782654f4eedea607

check() {
  expected=$1
  line=$2
  printf '%s\n' "$line" > "$fixture/.github/workflows/test.yml"
  if (cd "$fixture" && sh "$repo_root/scripts/check-github-actions-pinning.sh") > "$fixture/output" 2>&1; then
    actual=pass
  else
    actual=fail
  fi
  if [ "$actual" != "$expected" ]; then
    echo "expected $expected, got $actual: $line" >&2
    cat "$fixture/output" >&2
    exit 1
  fi
}

check pass "uses: Homebrew/actions/setup-homebrew@$sha # 2026.08.31.1"
check pass "uses: Homebrew/actions/setup-homebrew@$sha # v2026.08.31.1"
check pass "uses: actions/checkout@$sha # v4.2.2"
check fail "uses: Homebrew/actions/setup-homebrew@$sha # 2026.08.31"
check fail "uses: Homebrew/actions/setup-homebrew@$sha # v2026.08.31"
check fail "uses: Homebrew/actions/setup-homebrew@$sha # v2026.08.31.1garbage"
check fail "uses: Homebrew/actions/setup-homebrew@$sha # 2026.08.31.1.2"
check fail "uses: Homebrew/actions/setup-homebrew@$sha"
check fail "uses: actions/checkout@$sha # 2026.08.31.1"
check fail "uses: actions/checkout@$sha"
check fail 'uses: Homebrew/actions/setup-homebrew@3cdb78d # 2026.08.31.1'
check fail 'uses: actions/checkout@v4 # v4.2.2'
echo 'GitHub Actions pinning regression checks passed'
