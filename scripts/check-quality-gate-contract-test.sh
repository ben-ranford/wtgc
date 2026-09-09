#!/bin/sh
# Prove Make targets fail closed when their tool fails and reject malformed input.
set -eu

repo=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/wtgc-quality-contract.XXXXXX")
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT HUP INT TERM

mkdir -p "$tmp/bin"
cat > "$tmp/bin/go" <<'EOF'
#!/bin/sh
printf '%s\n' 'PASS'
exit 1
EOF
chmod +x "$tmp/bin/go"

for target in mod-check actionlint shellcheck; do
	if PATH="$tmp/bin:$PATH" make -C "$repo" "$target" >"$tmp/$target-fake.out" 2>&1; then
		echo "$target accepted a tool that printed success and exited nonzero" >&2
		exit 1
	fi
	if ! grep -qx PASS "$tmp/$target-fake.out"; then
		echo "$target did not invoke the injected failing tool" >&2
		exit 1
	fi
done

cat > "$tmp/bin/go" <<'EOF'
#!/bin/sh
case "${1-}" in
	env)
		case "${2-}" in
			GOOS) printf '%s\n' darwin ;;
			GOARCH) printf '%s\n' arm64 ;;
		esac
		exit 0
		;;
	mod)
		case "${2-}" in
			tidy) printf '%s\n' MOD_TIDY; exit 0 ;;
			verify) printf '%s\n' MOD_VERIFY; exit 1 ;;
		esac
		;;
esac
exit 2
EOF
chmod +x "$tmp/bin/go"

if PATH="$tmp/bin:$PATH" make -C "$repo" mod-check >"$tmp/mod-verify-fake.out" 2>&1; then
	echo "mod-check accepted a failing mod verify after tidy succeeded" >&2
	exit 1
fi
for marker in MOD_TIDY MOD_VERIFY; do
	if ! grep -qx "$marker" "$tmp/mod-verify-fake.out"; then
		echo "mod-check did not invoke $marker" >&2
		exit 1
	fi
done

cat > "$tmp/invalid-workflow.yml" <<'EOF'
name: malformed
on: [push
EOF
if make -C "$repo" actionlint ACTIONLINT_FILES="$tmp/invalid-workflow.yml" >"$tmp/actionlint-invalid.out" 2>&1; then
	echo "actionlint accepted malformed workflow input" >&2
	exit 1
fi

cat > "$tmp/invalid.sh" <<'EOF'
if then
EOF
if make -C "$repo" shellcheck SHELLCHECK_FILES="$tmp/invalid.sh" >"$tmp/shellcheck-invalid.out" 2>&1; then
	echo "shellcheck accepted malformed shell input" >&2
	exit 1
fi

for name in one two three; do
	printf '%s\n' '#!/bin/sh' 'true' > "$tmp/$name.sh"
done
cat > "$tmp/bin/go" <<'EOF'
#!/bin/sh
set -eu

[ "$1" = run ]
[ "$2" = github.com/wasilibs/go-shellcheck/cmd/shellcheck@v0.10.0 ]
[ "$3" = --shell=sh ]
[ "$#" = 4 ]
printf '%s\n' "$4" >> "${WTGC_SHELLCHECK_CALL_LOG:?}"
EOF
chmod +x "$tmp/bin/go"

shellcheck_files="$tmp/one.sh $tmp/two.sh $tmp/three.sh"
if ! PATH="$tmp/bin:$PATH" WTGC_SHELLCHECK_CALL_LOG="$tmp/shellcheck-calls" make -C "$repo" shellcheck SHELLCHECK_FILES="$shellcheck_files" >"$tmp/shellcheck-files.out" 2>&1; then
	echo "shellcheck rejected the per-file fake tool invocation" >&2
	exit 1
fi
printf '%s\n' "$tmp/one.sh" "$tmp/two.sh" "$tmp/three.sh" | LC_ALL=C sort > "$tmp/shellcheck-expected"
LC_ALL=C sort "$tmp/shellcheck-calls" > "$tmp/shellcheck-actual"
if ! cmp -s "$tmp/shellcheck-expected" "$tmp/shellcheck-actual"; then
	echo "shellcheck did not invoke the pinned tool exactly once per file" >&2
	exit 1
fi

echo "quality gate contract tests passed"
