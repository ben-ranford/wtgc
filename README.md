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
provided.

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

The schema version is currently `1.0.0`.

## Development

Requirements:

- Go `1.26.x` or newer
- POSIX shell for local hooks and release packaging

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

## Release

Tag releases build cross-platform artifacts in GitHub Actions and publish
checksums. Local release packaging uses the same static binary entrypoint:

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
