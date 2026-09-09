#!/usr/bin/env sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
gate="$repo_root/scripts/check-duplication.sh"
temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/wtgc-duplication-test.XXXXXX")
cleanup() { find "$temporary_root" -type f -delete 2>/dev/null || true; find "$temporary_root" -depth -type d -exec rmdir {} + 2>/dev/null || true; }
trap cleanup EXIT HUP INT TERM
git init -q -b main "$temporary_root/repo"
git -C "$temporary_root/repo" config user.email wtgc-test@example.invalid
git -C "$temporary_root/repo" config user.name 'wtgc contract test'
printf '%s\n' 'package fixture' > "$temporary_root/repo/fixture.go"
git -C "$temporary_root/repo" add fixture.go
git -C "$temporary_root/repo" commit -qm baseline
(cd "$temporary_root/repo" && DUPLICATION_BASE=HEAD "$gate")
if (cd "$temporary_root/repo" && DUPLICATION_BASE=missing "$gate") >"$temporary_root/missing-base" 2>&1; then
  printf '%s\n' 'missing duplication base unexpectedly passed' >&2; exit 1
fi
printf '%s\n' 'package fixture // untracked candidate' > "$temporary_root/repo/untracked.go"
(cd "$temporary_root/repo" && DUPLICATION_BASE=HEAD "$gate")
mkdir -p "$temporary_root/bin"
cat > "$temporary_root/bin/go" <<'EOF'
#!/bin/sh
printf '%s\n' 'PASS'
exit 1
EOF
chmod +x "$temporary_root/bin/go"
misleading_artifact="$temporary_root/repo/.artifacts/ci-gates/misleading.json"
if (cd "$temporary_root/repo" && PATH="$temporary_root/bin:$PATH" DUPLICATION_BASE=HEAD DUPLICATION_ARTIFACT=.artifacts/ci-gates/misleading.json "$gate") >"$temporary_root/misleading-output" 2>&1; then
  printf '%s\n' 'duplication gate accepted a detector that printed success and failed' >&2; exit 1
fi
if ! grep -q '^PASS$' "$temporary_root/misleading-output"; then
  printf '%s\n' 'fake detector output was not exercised' >&2; exit 1
fi
if [ -e "$misleading_artifact" ]; then
  printf '%s\n' 'duplication gate wrote a success artifact after detector failure' >&2; exit 1
fi
printf '%s\n' 'duplication gate contract tests passed'
