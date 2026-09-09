#!/usr/bin/env sh
# Checks duplication only in lines introduced after a verified merge base.
set -eu

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)

base_ref=${DUPLICATION_BASE:-origin/main}
max=${DUPLICATION_MAX:-3}
threshold=${DUPLICATION_TOKEN_THRESHOLD:-55}
artifact=${DUPLICATION_ARTIFACT:-.artifacts/ci-gates/changed-code-duplication.json}

if ! git rev-parse --verify -q "$base_ref^{commit}" >/dev/null; then
  echo "duplication base '$base_ref' is unavailable. Fetch the PR base or set DUPLICATION_BASE to an explicit local commit/ref."
  exit 1
fi
if ! base=$(git merge-base "$base_ref" HEAD); then
  echo "duplication base '$base_ref' has no merge-base with HEAD. Set DUPLICATION_BASE explicitly."
  exit 1
fi
"$script_dir/managed-output.sh" ensure "$(dirname "$artifact")"
added=$(mktemp)
dup=$(mktemp)
raw=$(mktemp)
diff=$(mktemp)
trap 'rm -f "$added" "$dup" "$raw" "$diff"' EXIT INT TERM
if ! git diff --unified=0 --no-color "$base" -- '*.go' > "$diff"; then
  echo "unable to inspect changed Go code; refusing to report a successful duplication result"
  exit 1
fi
awk '
  /^\+\+\+ b\// { file=substr($0,7); next }
  $1 == "@@" { line=$3; sub(/^\+/,"",line); split(line,p,","); start=p[1]+0; count=(p[2]=="" ? 1 : p[2]+0); for (i=0;i<count;i++) if(file!="") print file ":" start+i }
' "$diff" | sort -u > "$added"
# Untracked Go files are candidate code too. Account for every source line so
# an unstaged file cannot make a duplication result look harmless.
git ls-files --others --exclude-standard -- '*.go' | while IFS= read -r file; do
  [ -n "$file" ] || continue
  awk -v file="$file" '{ print file ":" NR }' "$file"
done | sort -u >> "$added"
sort -u -o "$added" "$added"
total=$(wc -l < "$added" | tr -d ' ')
if [ "$total" -eq 0 ]; then
  printf '{"base": "%s", "added_lines": 0, "duplicated_added_lines": 0, "percentage": 0}\n' "$base" > "$artifact"
  echo "Changed-code duplication: 0.00% (no changed Go lines vs $base)."
  exit 0
fi
if ! GOFLAGS=-buildvcs=false go run github.com/mibk/dupl@f008fcf5e62793d38bda510ee37aab8b0c68e76c -t "$threshold" -plumbing . > "$raw" 2>&1; then
  cat "$raw"
  echo "duplication detector failed; refusing to report a successful result"
  exit 1
fi
awk -F: '
  { n=split($2,r,"-"); if(n!=2 || r[1]!~/^[0-9]+$/ || r[2]!~/^[0-9]+$/) next; for(i=r[1]+0;i<=r[2]+0;i++) print $1 ":" i }
' "$raw" | sort -u > "$dup"
duplicated=$(comm -12 "$added" "$dup" | wc -l | tr -d ' ')
percentage=$(awk -v d="$duplicated" -v t="$total" 'BEGIN { printf "%.2f", 100*d/t }')
printf '{"base": "%s", "added_lines": %s, "duplicated_added_lines": %s, "percentage": %s}\n' "$base" "$total" "$duplicated" "$percentage" > "$artifact"
echo "Changed-code duplication: $percentage% ($duplicated/$total added Go lines; max $max%; base $base)."
awk -v actual="$percentage" -v allowed="$max" 'BEGIN { exit !(actual <= allowed) }' || { echo "duplication gate failed: $percentage% exceeds $max%"; exit 1; }
