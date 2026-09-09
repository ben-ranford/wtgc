#!/usr/bin/env sh
# Validate the portable wtgc Agent Skill and install it into disposable projects.
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
skill_dir="$repo_root/skills/wtgc-harness"
skills_cli='npx --yes skills@1.5.25'
skills_ref='git+https://github.com/agentskills/agentskills.git@69ef37e9424c0a7ea9dd2293b559e43ec8176379#subdirectory=skills-ref'

if ! command -v node >/dev/null 2>&1; then

    printf '%s\n' 'node >=22.20.0 is required for skills@1.5.25' >&2
    exit 1
fi

node -e 'const [major, minor] = process.versions.node.split(".").map(Number); process.exit(major > 22 || (major === 22 && minor >= 20) ? 0 : 1)' || {
    printf '%s\n' "node $(node --version) is too old; skills@1.5.25 requires >=22.20.0" >&2
    exit 1
}

if ! command -v uv >/dev/null 2>&1; then
    printf '%s\n' 'uv is required to run the pinned Agent Skills reference validator' >&2
    exit 1
fi

uv tool run --from "$skills_ref" skills-ref validate "$skill_dir"

# Discovery is an actual Skills CLI operation; a successful validator alone
# cannot prove that the package layout is discoverable by the installer.
sh -c "$skills_cli add \"$repo_root\" --list"

temporary_root=$(mktemp -d "${TMPDIR:-/tmp}/wtgc-harness-skill.XXXXXX")
cleanup() {
    if [ -n "${temporary_root:-}" ] && [ -d "$temporary_root" ]; then
        find "$temporary_root" -type l -delete 2>/dev/null || true
        find "$temporary_root" -type f -delete 2>/dev/null || true
        find "$temporary_root" -depth -type d -exec rmdir {} + 2>/dev/null || true
    fi
}
trap cleanup EXIT HUP INT TERM

for agent in codex claude-code; do
    project="$temporary_root/$agent"
    mkdir "$project"
    (
        cd "$project"
        sh -c "$skills_cli add \"$repo_root\" --skill wtgc-harness --agent \"$agent\" --yes"
        sh -c "$skills_cli list --agent \"$agent\" --json"
    )
    installed=$(find "$project" -type f -path '*/wtgc-harness/SKILL.md' -print -quit)
    if [ -z "$installed" ]; then
        printf 'skills install did not create wtgc-harness for %s\n' "$agent" >&2
        exit 1
    fi
    uv tool run --from "$skills_ref" skills-ref validate "$(dirname "$installed")"
done
