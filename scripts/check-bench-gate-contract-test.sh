#!/bin/sh
# Exercise benchmark-gate rejection and signal cleanup without touching user worktrees.
set -eu

repo=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
before=$(mktemp)
after=$(mktemp)
trap 'rm -f "$before" "$after"' EXIT HUP INT TERM

git -C "$repo" worktree list --porcelain >"$before"

if make -C "$repo" bench-gate MEMORY_BENCH_BASE=missing-benchmark-base >/dev/null 2>&1; then
	echo "benchmark gate accepted an unavailable baseline" >&2
	exit 1
fi

set +e
perl -e 'alarm shift; exec @ARGV' 2 make -C "$repo" bench-gate MEMORY_BENCH_BASE=HEAD BENCH_COUNT=30 BENCH_TIME=1s >/dev/null 2>&1
status=$?
set -e
if [ "$status" -ne 142 ]; then
	echo "benchmark cancellation exit = $status, want 142" >&2
	exit 1
fi

deadline=$(( $(date +%s) + 5 ))
while :; do
	git -C "$repo" worktree list --porcelain >"$after"
	if cmp -s "$before" "$after"; then
		break
	fi
	if [ "$(date +%s)" -ge "$deadline" ]; then
		break
	fi
	sleep 1
done
if ! cmp -s "$before" "$after"; then
	echo "benchmark cancellation left a registered worktree" >&2
	diff -u "$before" "$after" >&2 || true
	exit 1
fi
if ps ax -o pid=,command= | awk '$2 ~ /app[.]test/ && /BenchmarkRunClassifies350Worktrees/ { found=1 } END { exit !found }'; then
	echo "benchmark cancellation left a benchmark process running" >&2
	exit 1
fi

echo "benchmark gate contract tests passed"
