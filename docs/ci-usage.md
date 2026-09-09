# CI, Hooks, And Scheduled Runs

## Local Gates

`make ci` is the canonical local and CI validation target. It runs:

- `make format-check`
- `make mod-check` verifies a tidy module graph and module checksums.
- `make feature-flag-check` validates `.ci/feature-flags.json`; an empty `flags`
  array is the valid no-feature-flag state.
- `make gostyle`, `make actionlint`, and `make shellcheck` run the pinned Go
  tools declared in `Makefile`. Shell quality gates run on POSIX/Linux CI;
  Windows keeps its native Go tests.
- `make dup-check` requires a fetched `DUPLICATION_BASE` (default
  `origin/main`) and writes `.artifacts/changed-code-duplication.json`. Set an
  explicit local commit/ref when working offline; missing base evidence fails.
- `make bench-gate` compares `BenchmarkRunClassifies350Worktrees` at the
  merge-base and candidate, enforcing allocations and bytes/op only. It writes
  raw outputs and a Markdown comparison under `.artifacts` and removes its
  temporary baseline worktree on exit.
- `make fuzz-corpus-check` executes each committed porcelain corpus seed once;
  the scheduled fuzz workflow runs a bounded 30-second campaign.
- `make ci-tools-cov` covers the repository-owned CI validators. `make cov`
  enforces its preserved 85% total floor and checked-in individual package
  floors from `.ci/coverage-ratchet.json`
  and emits JSON results in `.artifacts`.
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
- Go toolchain target: `go1.26.6`
- Coverage floors: `.ci/coverage-ratchet.json`; safety and failure-path tests keep the checked-in total and package ratchets enforced.
- `GOSEC_FLAGS`: currently excludes `G204` because wtgc intentionally shells
  out to Git through argument-vector subprocess calls. Source changes to the
  command runner should still receive manual security review.
- `make vuln`: runs `govulncheck` against the configured package pattern.
- `make suppressions`: compares new suppressions with `SUPPRESSION_BASE` and
  requires `.ci/static-suppressions.json` entries with location, rationale,
  owner, removal condition, and an existing GitHub issue URL. The trusted
  suppression workflow can create a missing tracking issue but deliberately
  fails until its URL is committed; local/offline checks never claim an issue
  exists. The central `G204` Gosec policy remains reviewed in `Makefile`.
  Its contract tests require POSIX `sh`, Git, Python 3, and Node.js.
- `make harness-check` validates and smoke-installs the pinned harness skill;
  it requires Node.js `>=22.20` and `uv`. CI supplies Node 24 and installs a
  pinned, checksum-verified `uv` wheel into an isolated runner-temp virtual
  environment before invoking `make ci`.
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
For stable releases, it also generates, audits, source-builds, and tests the
`wtgc` Homebrew formula before updating
[`ben-ranford/homebrew-tap`](https://github.com/ben-ranford/homebrew-tap).
Release packaging requires `./cmd/wtgc`. Artifact attestations are skipped while
the repository is private unless `ENABLE_PRIVATE_ATTESTATIONS=true` is
configured, because GitHub only enables private attestations on plans that
support them.

### Homebrew tap publishing

Homebrew formula publication requires the `HOMEBREW_TAP_TOKEN` repository
secret. Configure it as a fine-grained personal access token or GitHub App
installation token that can write **Contents** only to
`ben-ranford/homebrew-tap`; do not grant it access to this repository or other
repositories. The workflow uses the token only to fetch and push the tap after
the tokenless formula validation succeeds. When the secret is absent, releases
still publish normally and skip the tap update.

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

The next queued pull request pauses with a status comment when it is a draft,
has a rebase conflict, its base or head changes while the controller acts, or
targets a branch other than `main`. A rebase conflict does not block later
queued pull requests: the controller checks the next item and returns to the
conflicted pull request when its branch is updated. Current fork branches can
wait in the queue, but a stale fork is never rebased by the repository-scoped
App; its contributor must rebase and push it manually.

Remove `queue-me` to leave the queue. The controller disables that pull
request's auto-merge and updates its single marked status comment. Retargeting
a labeled pull request away from `main` has the same safe pause behavior.

For local validation, install Node.js and run `make queue-me-check` (or the
full `make automation-check` / `make ci` gates). The Node suite exercises the
controller behavior; the Go contract test checks the workflow's trusted-code,
least-privilege, and pinning design.
