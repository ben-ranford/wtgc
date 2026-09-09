#!/usr/bin/env sh
# Enforces accountable declarations only for suppressions newly added after base.
set -eu
base_ref=${SUPPRESSION_BASE:-origin/main}
manifest=${SUPPRESSION_MANIFEST:-.ci/static-suppressions.json}
if ! git rev-parse --verify -q "$base_ref^{commit}" >/dev/null; then
  echo "suppression base '$base_ref' is unavailable. Fetch the PR base or set SUPPRESSION_BASE to an explicit local commit/ref."
  exit 1
fi
base=$(git merge-base "$base_ref" HEAD) || { echo "suppression base has no merge-base"; exit 1; }
if [ ! -f "$manifest" ]; then
  echo "suppression manifest '$manifest' is required (use {\"suppressions\":[]} when no new markers exist)."
  exit 1
fi
python3 - "$base" "$manifest" <<'PY'
import json, os, re, subprocess, sys
base, path = sys.argv[1:]
try:
    with open(path, encoding="utf-8") as source:
        doc = json.load(source)
    if not isinstance(doc, dict) or set(doc) != {"suppressions"}:
        raise ValueError("manifest must contain only a suppressions array")
    entries = doc["suppressions"]
    if not isinstance(entries, list):
        raise ValueError("suppressions must be an array")
except Exception as e: raise SystemExit(f"invalid suppression manifest: {e}")
try:
    diff = subprocess.check_output(["git", "diff", "--no-ext-diff", "--unified=0", "--no-color", base, "--", "*.go"], text=True, stderr=subprocess.PIPE)
except subprocess.CalledProcessError as e:
    raise SystemExit(f"unable to inspect working-tree suppression diff: {e.stderr.strip()}")
found=[]; file=None; line=0
marker=re.compile(r"(?:#|//)\s*nosec\b|//\s*nolint\b|//\s*NOSONAR\b", re.I)
for raw in diff.splitlines():
    if raw.startswith("+++ b/"): file=raw[6:]
    elif raw == "+++ /dev/null": file=None
    elif raw.startswith("@@"):
        m=re.search(r"\+(\d+)(?:,\d+)?", raw); line=int(m.group(1)) if m else 0
    elif raw.startswith("+") and not raw.startswith("+++"):
        if file and marker.search(raw): found.append((file,line))
        line += 1
    elif raw.startswith(" "): line += 1
# git diff cannot represent an untracked path. Read every untracked Go file so
# a local pre-commit or CI checkout cannot hide a marker by leaving it unstaged.
untracked = subprocess.check_output(["git", "ls-files", "--others", "--exclude-standard", "--", "*.go"], text=True)
for name in untracked.splitlines():
    try:
        with open(name, encoding="utf-8") as source:
            for number, text in enumerate(source, 1):
                if marker.search(text): found.append((name, number))
    except OSError as e:
        raise SystemExit(f"unable to inspect untracked Go file {name!r}: {e}")

entries_by_loc={}
for index, entry in enumerate(entries):
    if not isinstance(entry, dict):
        raise SystemExit(f"suppression entry {index} must be an object")
    extra_fields=set(entry)-{"location","rationale","owner","removal_condition","issue"}
    if extra_fields:
        raise SystemExit(f"suppression entry {index} has unexpected fields: {', '.join(sorted(extra_fields))}")
    missing=[key for key in ("location","rationale","owner","removal_condition","issue") if not isinstance(entry.get(key), str) or not entry[key].strip()]
    if missing:
        raise SystemExit(f"suppression entry {index} lacks: {', '.join(missing)}")
    location=entry["location"]
    if not re.fullmatch(r"[^\n:]+\.go:\d+", location):
        raise SystemExit(f"suppression {location!r} location must be path.go:line")
    if location in entries_by_loc:
        raise SystemExit(f"duplicate suppression manifest location: {location}")
    issue_match=re.fullmatch(r"https://github\.com/([^/]+)/([^/]+)/issues/([1-9]\d*)", entry["issue"])
    if not issue_match:
        raise SystemExit(f"suppression {location!r} issue must be a GitHub issue URL; local validation cannot verify it exists")
    if os.environ.get("SUPPRESSION_VERIFY_ISSUES") == "1":
        repository=os.environ.get("GITHUB_REPOSITORY", "")
        if repository != f"{issue_match.group(1)}/{issue_match.group(2)}":
            raise SystemExit(f"suppression {location!r} issue must belong to {repository}")
        try:
            subprocess.check_output(["gh", "api", f"repos/{repository}/issues/{issue_match.group(3)}"], stderr=subprocess.STDOUT, text=True)
        except (OSError, subprocess.CalledProcessError) as e:
            raise SystemExit(f"suppression {location!r} linked issue cannot be verified: {e}")
    entries_by_loc[location]=entry
needed={f"{f}:{n}" for f,n in found}; missing=needed-set(entries_by_loc)
if missing: raise SystemExit("untracked new static-analysis suppressions: "+", ".join(sorted(missing))+"; add an accountable manifest entry and linked issue")
for location in entries_by_loc:
    source_path, source_line = location.rsplit(":", 1)
    try:
        with open(source_path, encoding="utf-8") as source:
            lines = source.readlines()
        if int(source_line) < 1 or int(source_line) > len(lines) or not marker.search(lines[int(source_line)-1]):
            raise SystemExit(f"stale suppression manifest entry: {location}")
    except OSError as e:
        raise SystemExit(f"stale suppression manifest entry: {location} ({e})")
print("Static suppression accountability contract valid.")
PY
