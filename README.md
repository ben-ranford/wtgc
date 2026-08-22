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
provided. V1 has no application UI or TUI.

Release artifacts target Linux, macOS, and Windows on amd64 and arm64. CI uses
GitHub-hosted Linux, macOS, and Windows runners for native test verification,
then cross-compiles the release artifact matrix from Ubuntu.

## Install

Download a release binary and verify its checksum:

```bash
WTGC_VERSION=v1.0.0
WTGC_OS=darwin
WTGC_ARCH=arm64
curl -LO "https://github.com/ben-ranford/wtgc/releases/download/${WTGC_VERSION}/wtgc_${WTGC_VERSION}_${WTGC_OS}_${WTGC_ARCH}.tar.gz"
curl -LO "https://github.com/ben-ranford/wtgc/releases/download/${WTGC_VERSION}/checksums.txt"
shasum -a 256 -c checksums.txt --ignore-missing
tar -xzf "wtgc_${WTGC_VERSION}_${WTGC_OS}_${WTGC_ARCH}.tar.gz"
install "wtgc_${WTGC_VERSION}_${WTGC_OS}_${WTGC_ARCH}/wtgc" /usr/local/bin/wtgc
```

Windows artifacts are `.zip` archives and contain `wtgc.exe`.

Install from source:

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

The preview names every decision and leaves the filesystem unchanged:

```text
PATH                                  BRANCH       CLASSIFICATION  DIRTY  ACTION        SIZE     RECLAIMED  REASON
/Users/me/Projects/app-worktrees/123  feature/123  safe_to_remove  clean  would_remove  1.8 GiB  0 B        clean branch tip is reachable from both the default branch and a remote-tracking ref

Summary:
  dry run: true
  safe: 1
  removed: 0
  potential reclaimable: 1.8 GiB
  reclaimed: 0 B
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

Git commands inherit cancellation from `SIGINT`/`SIGTERM` and have no arbitrary
deadline by default. For unattended environments, set an explicit backstop such
as `WTGC_GIT_TIMEOUT=2m`.

## Requirements

- Git `2.36+` is the documented runtime floor. wtgc depends on modern
  `git worktree list --porcelain -z`, `git worktree remove`, and
  `git worktree prune` behavior.
- A local remote `HEAD` symbolic ref must be configured, for example
  `origin/HEAD -> origin/main`.
- Source builds and development require Go `1.26.5` or newer.

## Safety Model

Version 1 cleanup eligibility is intentionally narrow:

- A worktree is eligible immediately after it satisfies all safety checks.
- Local ancestry is checked against the default branch resolved from an
  unambiguous local remote `HEAD` symbolic ref.
- Remote reachability must also prove the worktree head is present on a
  reachable remote ref.
- Squash merges can remain classified as `unmerged` because the original branch
  tip is not reachable from the default branch. GitHub/GitLab PR-state checks
  are intentionally deferred to a follow-up release.
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

- Go `1.26.5` or newer
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

Release Please manages release PRs from Conventional Commits. After a release PR
is merged to `main`, the Release Please workflow tags the release, calls the
reusable release workflow, creates cross-platform archives, generates an SPDX
SBOM, rebuilds checksums, and uploads release assets through GitHub Actions.

Local packaging uses the same static binary entrypoint and validates that every
release asset has exactly one checksum entry:

```bash
make release VERSION=v1.0.0 PLATFORMS="darwin/arm64 linux/amd64 windows/amd64"
```

## Docs

- [Architecture](docs/architecture.md)
- [Safety contract](docs/safety.md)
- [CI and hooks](docs/ci-usage.md)
- [JSON schema](docs/inventory-schema.md)
- [Contribution guide](CONTRIBUTING.md)
- [Support](SUPPORT.md)
- [Security policy](SECURITY.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
