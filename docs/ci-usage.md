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

GitHub Actions jobs run on the `wtgc-arc` self-hosted scale set. Each job
bootstraps only the missing Debian packages it needs; production runner images
should preinstall those tools to avoid package installation on every cold start.

Tool versions are pinned in `Makefile`:

- `golangci-lint`: `v2.9.0`
- `gosec`: `v2.22.11`
- Go toolchain target: `go1.26.1`
- Coverage floor: `COVERAGE_MIN`; safety and failure-path tests keep it enforced.
- `GOSEC_FLAGS`: currently excludes `G204` because wtgc intentionally shells
  out to Git through argument-vector subprocess calls. Source changes to the
  command runner should still receive manual security review.
- `make vuln`: runs `govulncheck` against the configured package pattern.
- `make suppressions`: rejects inline static-analysis suppressions so
  exceptions stay central and reviewable.
- `make automation-check`: validates shell syntax, GitHub Actions pinning, and
  workflow and Lefthook YAML parsing. Ruby is a required development tool so
  this validation cannot be silently skipped.
- `make release-check`: runs `automation-check`, builds a local release, writes
  checksums, and verifies one checksum entry per archive.

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

`.github/workflows/ci.yml` runs pull-request and default-branch checks across:

- macOS
- Linux
- Windows

`.github/workflows/release.yml` builds tagged release artifacts and publishes
checksums. Release packaging requires `./cmd/wtgc`. Release jobs pin all
remaining GitHub actions to full upstream commit SHAs with version comments,
flatten downloaded artifacts into one release directory, regenerate one
`checksums.txt`, validate it, and publish with `gh release create` using the
workflow `GH_TOKEN`. Rerunning an existing tag uploads the verified assets with
`gh release upload --clobber`, so a partially completed publish can recover.
