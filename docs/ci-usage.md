# CI, Hooks, And Scheduled Runs

## Local Gates

`make ci` is the canonical local and CI validation target. It runs:

- `make format-check`
- `make automation-check`
- `make lint`
- `make security`
- `make vuln`
- `make suppressions`
- `make test`
- `make race`
- `make cov`
- `make build`
- `make perf-check`

GitHub Actions jobs run on GitHub-hosted runners. Pull-request and
default-branch CI includes native Linux, macOS, and Windows test verification.
The full automation gate and release packaging smoke check run on Ubuntu with
the tools supplied by the hosted image.

Tool versions are pinned in `Makefile`:

- `golangci-lint`: `v2.9.0`
- `gosec`: `v2.22.11`
- Go toolchain target: `go1.26.5`
- Coverage floor: `COVERAGE_MIN`; safety and failure-path tests keep it enforced.
- `GOSEC_FLAGS`: currently excludes `G204` because wtgc intentionally shells
  out to Git through argument-vector subprocess calls. Source changes to the
  command runner should still receive manual security review.
- `make vuln`: runs `govulncheck` against the configured package pattern.
- `make suppressions`: rejects inline static-analysis suppressions so
  exceptions stay central and reviewable.
- `make automation-check`: validates shell syntax, GitHub Actions pinning, and
  workflow and Lefthook YAML parsing, plus the queue-me workflow/controller
  contracts. Ruby and Node.js are required development tools so this validation
  cannot be silently skipped.
- `make release-check`: runs `automation-check`, builds a local release, writes
  checksums, and verifies one checksum entry per release asset.

## Pre-Commit Hook

Install:

```bash
make hooks-install
```

The managed hook runs the fast checks:

- format check
- inline-suppression policy check
- unit tests
- build

Run the full gate before pushing:

```bash
make ci
```

## Cron Example

Dry-run JSON report every weekday morning:

```cron
15 9 * * 1-5 /usr/local/bin/wtgc clean --json --scan-root "$HOME/Projects" > "$HOME/.cache/wtgc/latest.json" 2>> "$HOME/.cache/wtgc/wtgc.log"
```

Cleanup should be opt-in and reviewed before enabling:

```cron
30 9 * * 5 /usr/local/bin/wtgc clean --yes --json --scan-root "$HOME/Projects" > "$HOME/.cache/wtgc/cleanup.json" 2>> "$HOME/.cache/wtgc/wtgc.log"
```

Each report is one complete JSON document, rather than a line-delimited stream,
and is overwritten on the next run. See `examples/cron/wtgc.cron` for a
configurable template.

## Git Hook Examples

Dry-run `post-merge` and `post-checkout` examples live in `examples/hooks/`.
They write JSON reports under `WTGC_STATE_DIR` and default `WTGC_SCAN_ROOT` to
the current repository. Cleanup runs only when `WTGC_MUTATE=1` is set.

## Lefthook Example

`examples/lefthook.yml` provides dry-run `post-merge`, `post-checkout`, and
manual jobs. Its cleanup job is disabled unless `WTGC_MUTATE=1` is present in
the job environment.

## launchd Example

`examples/launchd/com.example.wtgc.plist` is a dry-run macOS launchd template.
Replace `/usr/local/bin/wtgc`, `/Users/example/Projects`, and the state paths
with local values before loading it. Create the parent directory for the output
and error paths before loading the job; launchd does not create it.

## GitHub Actions

`.github/workflows/ci.yml` runs pull-request and default-branch checks on
GitHub-hosted runners. It verifies the test suite natively on Linux, macOS, and
Windows, then runs the full automation gate and release packaging smoke check on
Ubuntu through `make ci` and `make release-check`.

`.github/workflows/release-please.yml` runs on default-branch pushes. It opens
or updates Release Please PRs and, when a release is created, calls the reusable
release workflow with the tag produced by Release Please.

`.github/workflows/release.yml` is the reusable and manual release workflow. It
runs on Ubuntu, cross-compiles the Linux, macOS, and Windows artifact matrix,
generates an SPDX JSON SBOM for the release asset set, rebuilds
`checksums.txt`, validates every release asset, and uploads assets with
`gh release upload` or creates the release if it has not been published yet.
Release packaging requires `./cmd/wtgc`. Artifact attestations are skipped while
the repository is private unless `ENABLE_PRIVATE_ATTESTATIONS=true` is
configured, because GitHub only enables private attestations on plans that
support them.

## Pull Request Queue

`.github/workflows/queue-me.yml` advances pull requests labeled `queue-me` in
ascending pull-request-number order. It only reads trusted workflow code and
never checks out or executes pull-request-head code. The queue is inactive,
green, and makes no repository changes until its GitHub App credentials are
configured.

### Setup

Create and install a GitHub App on this repository with these repository
permissions: Contents (read and write), Issues (read and write), Pull requests
(read and write), and Workflows (read and write). Then configure:

Enable **Allow auto-merge** in the repository's pull-request settings.

- Repository variable `QUEUE_APP_ID`: the GitHub App's numeric App ID.
- Repository secret `QUEUE_APP_PRIVATE_KEY`: the GitHub App private key in PEM
  format.

The workflow creates the `queue-me` label when it first runs with credentials.
Add that label to an open pull request targeting `main`; the oldest labeled
pull request becomes the leader. The leader is rebased onto current `main` when
needed, then squash-merged immediately when requirements are satisfied or
retried after trusted check or review completion events while required checks and approvals are pending.
Followers have auto-merge disabled and receive a status comment identifying
the pull request ahead of them.

### Pause and removal behavior

The leader pauses with a status comment when it is a draft, has a rebase
conflict, its base or head changes while the controller acts, or targets a
branch other than `main`. Current fork branches can wait in the queue, but a
stale fork is never rebased by the repository-scoped App; its contributor must
rebase and push it manually.

Remove `queue-me` to leave the queue. The controller disables that pull
request's auto-merge and updates its single marked status comment. Retargeting
a labeled pull request away from `main` has the same safe pause behavior.

For local validation, install Node.js and run `make queue-me-check` (or the
full `make automation-check` / `make ci` gates). The Node suite exercises the
controller behavior; the Go contract test checks the workflow's trusted-code,
least-privilege, and pinning design.
