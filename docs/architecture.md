# Architecture

wtgc is a local command-line tool around Git worktree state. The core shape is:

1. Discover candidate repository roots.
2. Ask Git for registered worktrees.
3. Inspect each worktree state.
4. Classify cleanup eligibility.
5. Emit an inventory and, for explicit cleanup commands, apply removals.

## Boundaries

- Git is the source of truth for worktree registration, branch heads, ancestry,
  dirty state, locks, and pruning.
- The filesystem is used for path existence and disk-usage estimation.
- The JSON model in `internal/model` is the public reporting contract.
- The CLI layer should adapt user input into scanner/cleanup options without
  embedding safety decisions.
- There is no application UI or TUI in v1.

## Suggested Package Shape

- `cmd/wtgc`: CLI entrypoint.
- `internal/model`: stable inventory and reporting structs.
- `internal/gitx`: Git command wrapper and parsing helpers.
- `internal/app`: conservative classification, two-phase cleanup, and summary
  orchestration.
- `internal/cli`: standard-library flag parsing and usage errors.
- `internal/gitx`: repository discovery plus Git command/porcelain boundaries.
- `internal/report`: JSON and human output formatting.

## Data Flow

```text
roots -> git worktree list -> state inspection -> classification -> inventory
                                                       |
                                                       v
                                              explicit cleanup only
```

## Reliability Rules

- Pass Git arguments as argument vectors, not shell-concatenated strings.
- Keep deletion effects in one package with tests around every refusal path.
- Prefer explicit refusal reasons over silent skipping.
- Treat partial scan errors as data in the inventory.
- Keep classification deterministic and independent from terminal output.
- Bound concurrent read-only classification while keeping mutation serial.

## Dependency Policy

The project is stdlib-only for v1. New dependencies require a focused decision
record covering why the standard library is insufficient, maintenance risk,
license, and security posture.

If a future UI or TUI is approved, use `github.com/ben-ranford/stave` for that
surface. Do not add UI dependencies while v1 remains CLI-only.
