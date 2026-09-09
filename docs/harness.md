# wtgc harness skill

`wtgc-harness` gives supported coding-agent harnesses portable instructions for
auditing wtgc safely. It is discoverable at `skills/wtgc-harness/SKILL.md`.

The examples use the pinned open Skills CLI release `1.5.25`
([source commit `80feb48868972d518436f26711509bc78595b5cb`](https://github.com/vercel-labs/skills/commit/80feb48868972d518436f26711509bc78595b5cb)).
It requires Node.js `>=22.20.0`.

## Install

From a project directory, install the skill for the harnesses you use:

```sh
npx --yes skills@1.5.25 add ben-ranford/wtgc --skill wtgc-harness
```

Target Codex and Claude Code non-interactively with explicit agent names:

```sh
npx --yes skills@1.5.25 add ben-ranford/wtgc --skill wtgc-harness --agent codex claude-code --yes
```

The default scope is the current project. For a user-level installation, add
`--global`:

```sh
npx --yes skills@1.5.25 add ben-ranford/wtgc --skill wtgc-harness --global --yes
```

The CLI supports the `codex` and `claude-code` agent targets used above. Check
the installed CLI's help before naming another target:

```sh
npx --yes skills@1.5.25 add --help
```

## Update, remove, and one-shot use

Update the project-installed skill, or use `--global` for the user-level copy:

```sh
npx --yes skills@1.5.25 update wtgc-harness --yes
npx --yes skills@1.5.25 update wtgc-harness --global --yes
```

Remove it from the current project or the global scope:

```sh
npx --yes skills@1.5.25 remove wtgc-harness --yes
npx --yes skills@1.5.25 remove wtgc-harness --global --yes
```

For a one-shot prompt instead of installation, the current CLI supports the
package-at-skill form:

```sh
npx --yes skills@1.5.25 use ben-ranford/wtgc@wtgc-harness
```

`skills use` generates a prompt. It does not install the skill or authorize a
wtgc mutation.

## Local validation

The repository checks the skill against `skills-ref` `0.1.0` at commit
`69ef37e9424c0a7ea9dd2293b559e43ec8176379` and runs real disposable project
installs for both supported targets. The validator is a reference
implementation, so the smoke also uses the production Skills CLI.

```sh
scripts/check-harness-skill.sh
scripts/harness-skill-safety-smoke.sh
```

Both scripts use a local repository source before merge. After publication, run
the same discovery and install forms above with `ben-ranford/wtgc` to verify the
published package. The checks create temporary directories only and remove them
on exit; they never install a skill into your global harness settings.

The installed skill starts with a `wtgc` dry-run. It treats dirty, ambiguous,
or otherwise unproven worktrees as kept and requires a separate explicit user
request before `--yes`, `--interactive`, or `--delete-branch`.
