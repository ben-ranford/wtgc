# wtgc

Worktree Garbage Collector safely reclaims Git worktrees that are no longer
useful after their changes have landed.

`wtgc` is local-first and conservative by default. It inventories registered
Git worktrees, classifies each one, and only treats a worktree as removable when
the branch is reachable from the configured local ancestor and from a reachable
remote ref. Dirty, locked, primary, detached, or ambiguous worktrees stay kept.

## Status

The v1 CLI is implemented as a single, dependency-free Go binary. It is safe by
default: every invocation is a dry-run unless `--yes` or `--interactive` is
provided. V1 has no application UI or TUI. If a future UI/TUI is approved, it
must use `github.com/ben-ranford/stave`; Stave is not included in v1 because
the current product is CLI-only and standard-library-only.

## Install

```bash
go install github.com/ben-ranford/wtgc/cmd/wtgc@latest
```

Or build locally:

```bash
make build
./bin/wtgc --help
```

## Quick Start

Preview cleanup across a projects directory:

```bash
wtgc clean --scan-root "$HOME/Projects"
```

Remove every worktree classified `safe_to_remove`:

```bash
wtgc clean --scan-root "$HOME/Projects" --yes
```

Confirm each action interactively:

```bash
wtgc clean --scan-root "$HOME/Projects" --interactive
```

Emit JSON for a hook or scheduled job:

```bash
wtgc clean --scan-root "$HOME/Projects" --json
```

Local branches are retained by default. Delete them only with the separate,
explicit `--delete-branch` flag.

## Safety Model

Version 1 cleanup eligibility is intentionally narrow:

- A worktree is eligible immediately after it satisfies all safety checks.
- Local ancestry is checked against the default branch resolved from an
  unambiguous local remote `HEAD` symbolic ref.
- Remote reachability must also prove the worktree head is present on a
  reachable remote ref.
- Dirty, locked, primary, detached, or unreadable worktrees are not removed.
- Stale Git administrative records can be reported as `stale_orphaned`, but
  removal remains explicit and auditable.
- Cache warnings are deferred until cache behavior exists.

See `docs/safety.md` for the full decision contract.

## JSON Output

Machine-readable inventory output is documented in:

- `docs/inventory-schema.md`
- `docs/inventory.schema.json`

The schema version is currently `1.0.0`. Each JSON document is a complete action
log: every row has an `action` and `reclaimed_bytes`, `dirty` is included when
inspected, and the summary includes potential and actual reclaimed byte totals.

## Development

Requirements:

- Go `1.26.x` or newer
- POSIX shell for local hooks and release packaging
- Ruby with its standard YAML library for automation configuration validation

Run the normal checks:

```bash
make format-check
make lint
make security
make test
make race
make cov
make build
```

Run the same gate used by CI:

```bash
make ci
```

Install fast local pre-commit hooks:

```bash
make hooks-install
```

Automation examples for dry-run hooks, Lefthook manual jobs, cron, and launchd
live under `examples/`. Mutation examples are opt-in through explicit flags or
environment variables.

## Release

Tag releases build cross-platform artifacts in GitHub Actions and publish
checksums. Release publishing uses the GitHub CLI and the workflow
`GH_TOKEN`; third-party release write actions are not used. Local release
packaging uses the same static binary entrypoint and validates that every
archive has exactly one checksum entry:

```bash
make release VERSION=v0.1.0 PLATFORMS="darwin/arm64 linux/amd64 windows/amd64"
```

## Docs

- Product decomposition: `docs/jtbd.md`
- Architecture: `docs/architecture.md`
- Safety contract: `docs/safety.md`
- CI and hooks: `docs/ci-usage.md`
- JSON schema: `docs/inventory-schema.md`
- Contribution guide: `CONTRIBUTING.md`
