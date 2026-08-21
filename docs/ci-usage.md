# CI, Hooks, And Scheduled Runs

## Local Gates

`make ci` is the canonical local and CI validation target. It runs:

- `make format-check`
- `make lint`
- `make security`
- `make test`
- `make race`
- `make cov`
- `make build`

Tool versions are pinned in `Makefile`:

- `golangci-lint`: `v2.9.0`
- `gosec`: `v2.22.11`
- Go toolchain target: `go1.26.1`
- Coverage floor: `COVERAGE_MIN`; safety and failure-path tests keep it enforced.
- `GOSEC_FLAGS`: currently excludes `G204` because wtgc intentionally shells
  out to Git through argument-vector subprocess calls. Source changes to the
  command runner should still receive manual security review.

## Pre-Commit Hook

Install:

```bash
make hooks-install
```

The managed hook runs the fast checks:

- format check
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
30 9 * * 5 /usr/local/bin/wtgc clean --yes --json --scan-root "$HOME/Projects" >> "$HOME/.cache/wtgc/cleanup.jsonl" 2>> "$HOME/.cache/wtgc/wtgc.log"
```

## GitHub Actions

`.github/workflows/ci.yml` runs pull-request and default-branch checks across:

- macOS
- Linux
- Windows

`.github/workflows/release.yml` builds tagged release artifacts and publishes
checksums. Release packaging requires `./cmd/wtgc`.
