#!/usr/bin/env sh
set -eu

matches=$(git grep -nE '(#nosec|//[[:space:]]*nolint|NOSONAR)' -- '*.go' || true)
if [ -n "$matches" ]; then
  echo "Inline static-analysis suppressions are not allowed; use a reviewed central configuration exception."
  echo "$matches"
  exit 1
fi

echo "No inline static-analysis suppressions found."
