# Jobs To Be Done

This decomposes the initial PRD into implementation jobs, acceptance criteria,
and v1 decisions.

## Primary Job

When I have many Git worktrees left behind after feature and bug branches land,
I want a local tool to identify and remove the worktrees that are demonstrably
safe to delete, so that disk space and branch clutter are reclaimed without
losing unmerged work.

## User Jobs

### Inventory worktrees

As a developer, I can scan one or more repository roots and receive an inventory
of registered worktrees.

Acceptance criteria:

- Primary worktrees are identified and never classified as removable.
- Linked worktrees include path, repository, branch, head, dirty/locked/detached
  state, default branch, disk usage, classification, and reason.
- Scan errors are returned in the JSON output without aborting unrelated roots.

### Classify cleanup safety

As a developer, I can see why each worktree is safe, unsafe, stale, or blocked.

Acceptance criteria:

- Dirty worktrees are classified as `merged_but_dirty` or `kept`, not removable.
- Worktrees whose branch head is not proven merged are classified as `unmerged`.
- Stale or missing worktree administrative records are classified separately as
  `stale_orphaned`.
- Ambiguous Git state is classified as `error` or `kept` with a reason.

### Remove eligible worktrees

As a developer, I can run cleanup and remove only worktrees that meet the safety
contract.

Acceptance criteria:

- Dry-run is the default reporting posture unless cleanup is explicitly invoked.
- Cleanup removes only worktrees classified as `safe_to_remove`.
- Removed worktrees report `removed: true`.
- Branch deletion is explicit through `--delete-branch` and reported separately
  through `branch_deleted`.

### Automate local hygiene

As a developer, I can run wtgc from a local hook, cron, launchd, or CI-style
script without scraping terminal output.

Acceptance criteria:

- JSON output follows `docs/inventory.schema.json`.
- Exit behavior distinguishes successful scans from scan failures.
- Hook and cron examples default to dry-run/report mode.

## Explicit V1 Resolutions

- Local ancestor and remote reachability are both required for conservative v1
  removal eligibility.
- Eligibility is immediate by default after safety checks pass; there is no
  default age delay.
- Cache warnings are deferred until a cache exists.
- The implementation remains standard-library Go unless a future PRD explicitly
  accepts a dependency.

## Delivery Jobs

The primary job is delivered through these independently verifiable slices:

1. Discover repository families under one or more nested scan roots and dedupe
   them by the shared Git directory.
2. Parse stable, NUL-delimited Git worktree records into a flat inventory.
3. Resolve one unambiguous default branch from local remote `HEAD` refs.
4. Classify primary, locked, detached, dirty, unmerged, unpushed-only, and stale
   records conservatively.
5. Estimate disk usage and report potential or reclaimed bytes.
6. Default every run to dry-run and require `--yes` or `--interactive` for
   mutation.
7. Re-list and reclassify immediately before removal or metadata pruning.
8. Remove only through `git worktree remove`, then prune accepted stale metadata
   through Git.
9. Keep local branches unless `--delete-branch` is explicitly supplied.
10. Produce stable human and JSON reports for hooks and scheduled jobs.
11. Ship cross-platform static binaries behind local, CI, security, race, and
    coverage gates.

## Non-Goals For V1

- Cloud synchronization.
- Remote branch deletion by default.
- Force-removing dirty, locked, primary, or ambiguous worktrees.
- Cache invalidation warnings before cache behavior is introduced.
