#!/usr/bin/env sh
set -eu

if grep -REin 'jsonl' README.md docs examples; then
  echo "public documentation must not claim unsupported line-delimited JSON output" >&2
  exit 1
fi

if grep -REn -- '--json.*(^|[^0-9])>>[[:space:]]*[^[:space:]]*\.json' README.md docs examples; then
  echo "automation examples must not append complete JSON documents" >&2
  exit 1
fi

guard='if [ "${WTGC_MUTATE:-0}" != "1" ]; then exit 0; fi;'
if ! grep -F "$guard" examples/lefthook.yml >/dev/null 2>&1; then
  echo "Lefthook cleanup must exit before mutation unless WTGC_MUTATE=1" >&2
  exit 1
fi

echo "Automation examples preserve JSON and mutation-guard contracts."
