# Inventory Schema

`wtgc` JSON output is the inventory document described by
`docs/inventory.schema.json`.

## Generate

The CLI emits JSON with a command shaped like:

```bash
wtgc clean --json --scan-root "$HOME/Projects" > inventory.json
```

Validate with any JSON Schema 2020-12 validator:

```bash
jsonschema -i inventory.json docs/inventory.schema.json
```

## Top-Level Fields

- `schema_version`: currently `1.0.0`.
- `generated_at`: RFC 3339 timestamp.
- `dry_run`: whether this run reported only.
- `roots`: requested repository roots.
- `worktrees`: one row per inspected worktree.
- `summary`: aggregate scan and cleanup counters.
- `errors`: optional scan-level errors.

## Worktree Fields

- `path`: worktree path.
- `branch`: branch name when known.
- `head`: commit hash when known.
- `repository`: owning repository path.
- `default_branch`: resolved default branch when known.
- `disk_bytes`: estimated disk usage.
- `classification`: cleanup decision.
- `reason`: human-readable decision reason.
- `primary`, `detached`, `locked`, `prunable`: state flags.
- `removed`, `branch_deleted`: cleanup effects.
- `error`: row-specific error detail.

## Notes

Summary counters use snake_case JSON names. `duration_ns` is encoded as an
integer nanosecond duration.
