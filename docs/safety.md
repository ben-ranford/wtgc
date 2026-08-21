# Safety Contract

wtgc must prefer false negatives over false positives. A worktree that might
contain unrecovered work is kept.

## Removal Eligibility

A worktree can be classified as `safe_to_remove` only when all conditions are
true:

- It is not the repository's primary worktree.
- It is not locked.
- It is not detached.
- Its working tree and index are clean.
- Its branch head is reachable from the locally resolved default branch.
- Its branch head is reachable from at least one remote ref.
- Its path can be resolved and inspected.
- No Git command needed for the decision failed.

## Immediate Eligibility

V1 does not impose an age threshold. Once every safety condition passes, a
worktree can be reported as eligible immediately.

Users who want a delay should schedule cleanup with their own cadence, for
example daily or weekly dry-runs before cleanup.

## Classification Meanings

- `safe_to_remove`: all safety conditions pass.
- `merged_but_dirty`: ancestry checks pass but local modifications exist.
- `unmerged`: local ancestor or remote reachability is not proven.
- `stale_orphaned`: Git reports stale worktree administrative data.
- `kept`: intentionally retained for a non-error reason.
- `error`: state could not be determined safely.

## Remote Reachability

Remote reachability is a conservative v1 requirement. A local branch that is
merged only into a local base ref is not enough. The head must also be reachable
from a remote ref so local-only work is not mistaken for landed work.

wtgc does not fetch. If remote-tracking refs or remote `HEAD` metadata are stale
or ambiguous, it keeps the worktree and reports why.

## Revalidation

Classification and mutation are separate phases. Immediately before removing a
live worktree, wtgc re-lists registered worktrees and verifies that the path,
branch, HEAD, cleanliness, ancestry, remote reachability, and protection state
still pass. A stale record is likewise rechecked before `git worktree prune`.
Any changed or unreadable state blocks that action.

`git worktree remove` updates metadata for a live worktree itself. Because
`git worktree prune --expire=now` is repository-wide, wtgc invokes it only when
at least one stale record from that repository was explicitly accepted and all
stale records considered in the run still pass revalidation.

## Branch Deletion

Branch deletion is separate from worktree removal. It is opt-in through
`--delete-branch`, reported through `branch_deleted`, and uses Git's safe
`branch -d` behavior after the same local ancestry and remote-reachability
proof.

## Cache Warnings

Cache warnings are intentionally deferred. Until wtgc has cache behavior, it
should not warn about stale cache data or cache invalidation.
