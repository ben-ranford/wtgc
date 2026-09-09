# Inventory Schema

`wtgc` JSON output is the inventory document described by
`docs/inventory.schema.json`. For automation, the document is also the complete
action log: every inspected worktree row records the action that was taken or
would be taken, and the summary records totals.

## Generate

The CLI emits JSON with a command shaped like:

```bash
wtgc clean --json --scan-root "$HOME/Projects" > inventory.json
```

The repository test suite validates the Go inventory/report contract:

```bash
go test ./internal/model ./internal/report ./internal/app
```

To validate an emitted file against the JSON Schema document, install an
explicit validator instead of assuming a global `jsonschema` command exists:

```bash
python3 -m pip install --user check-jsonschema
python3 -m check_jsonschema --schemafile docs/inventory.schema.json inventory.json
```

## Top-Level Fields

- `schema_version`: currently `1.1.0`.
- `generated_at`: RFC 3339 timestamp.
- `dry_run`: whether this run reported only.
- `roots`: requested repository roots.
- `worktrees`: one row per inspected worktree and action.
- `summary`: aggregate scan, cleanup, prune, and byte counters.
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
- `dirty`: optional cleanliness state, present when wtgc inspected it.
- `action`: required action log value. Possible values are `kept`,
  `would_remove`, `would_prune`, `removed`, `pruned`, and
  `removed_branch_deleted`.
- `reclaimed_bytes`: required per-row reclaimed bytes. It is `0` for dry-runs,
  kept rows, and failed mutations.
- `removed`, `branch_deleted`: cleanup effects retained for compatibility and
  quick filtering.
- `error`: row-specific error detail.
- `retention_basis`, `observed_at`, `eligible_at`, `retention_remaining_ns`: present for otherwise safe rows when a positive retention window is configured.
- `cache_warnings`: advisory cache rows, each with path, estimated bytes, kind, and reason. These rows never change cleanup classification or reclaimed-byte totals.
- `provider`, `provider_pr`, `provider_url`, `merged_at`: present for an exact GitHub squash-merge proof. They contain no credential or raw API data.

## Notes

Summary counters use snake_case JSON names. `potential_bytes` is the dry-run
reclaimable estimate; `reclaimed_bytes` is the total actually reclaimed during
mutating runs. `cache_warning_count` and `cache_warning_bytes` are advisory
totals and remain separate from cleanup totals. `duration_ns` is encoded as an
integer nanosecond duration.
