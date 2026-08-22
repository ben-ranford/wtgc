# ♻️ wtgc

[![CI](https://github.com/ben-ranford/wtgc/actions/workflows/ci.yml/badge.svg)](https://github.com/ben-ranford/wtgc/actions/workflows/ci.yml)
[![MIT License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

> Find the worktrees eating your disk. Remove only the ones Git can prove you no longer need.

## 🌟 Highlights

- **Dry-run means dry-run.** `wtgc` shows its working and changes nothing until you explicitly choose `--yes` or `--interactive`.
- **Conservative on purpose.** Dirty, untracked, unmerged, locked, detached, primary, and ambiguous worktrees are kept.
- **Scan more than one repository.** Start at a common root and discover repositories with nested worktrees.
- **The disk bill is itemised.** See every worktree's status and size, plus the total space you could reclaim.
- **Works in scripts.** JSON output supports hooks and scheduled jobs without changing the cleanup rules.

## ℹ️ Overview

One-worktree-per-task workflows are excellent at creating isolated checkouts and
terrible at reminding anyone to remove them. The audit that prompted `wtgc`
found 350+ registered worktrees across five repositories and more than 30 GB
tied up in directories whose branches had already landed.

Git already knows most of what is needed to clean this up; it just makes you
assemble the evidence yourself. `wtgc` does that boring part. It checks the
registered worktree, local default-branch ancestry, remote reachability, and
working-tree state before calling anything removable.

### 🛡️ How eligibility is proved

```text
clean + merged locally + reachable remotely + not protected = eligible
```

For a live worktree, missing proof means it stays. False negatives waste disk;
false positives destroy work. Given that choice, `wtgc` leaves the worktree
alone. Live worktrees are removed through `git worktree remove`, never raw
`rm -rf`, and local branch deletion is a separate opt-in action.

Stale records are handled separately. If Git marks a missing worktree path as
prunable, `wtgc` may remove its registration with `git worktree prune
--expire=now`; it does not delete a directory. The complete
[safety contract](docs/safety.md) documents every refusal and revalidation rule.

## 🚀 Usage

Point `wtgc` at the directory containing your repositories:

```bash
wtgc clean --scan-root "$HOME/Projects"
```

Every run is a dry-run unless you request a removal mode. It explains each
decision without changing the filesystem. Abridged output:

```text
PATH                                  BRANCH      CLASSIFICATION  DIRTY  ACTION        SIZE     RECLAIMED  REASON
/Users/me/Projects/app-worktrees/42   feature/42  safe_to_remove  clean  would_remove  1.8 GiB  0 B        clean branch tip is reachable from both the default branch and a remote-tracking ref

Summary:
  dry run: true
  repositories: 12
  scanned: 38
  safe: 4
  removed: 0
  potential reclaimable: 6.3 GiB
  reclaimed: 0 B
```

When the preview looks right:

```bash
# Remove every worktree proven safe
wtgc clean --scan-root "$HOME/Projects" --yes

# Or confirm each action yourself
wtgc clean --scan-root "$HOME/Projects" --interactive

# Emit machine-readable inventory instead
wtgc clean --scan-root "$HOME/Projects" --json
```

Worktree removal keeps the local branch. Add `--delete-branch` only when you
also intend to delete branches after their worktrees are safely removed.

Git commands have no deadline by default. For hooks and scheduled jobs, set
`WTGC_GIT_TIMEOUT=2m` (or another positive Go duration) to apply a deadline to
each Git command.

## ⬇️ Installation

Install from source with Go:

```bash
go install github.com/ben-ranford/wtgc/cmd/wtgc@latest
```

Release automation is configured to publish prebuilt archives for Linux, macOS,
and Windows on amd64 and arm64. Checksums, an SPDX SBOM, and provenance will be
published beside them on [GitHub Releases](https://github.com/ben-ranford/wtgc/releases).

Runtime requirements:

- Git `2.36+`
- An unambiguous remote default branch, such as `origin/HEAD -> origin/main`
- Go `1.26.5+` only when installing from source

## 📖 The useful links

- [Why a worktree is kept or removed](docs/safety.md)
- [JSON inventory format](docs/inventory-schema.md)
- [Hooks and scheduled cleanup](docs/ci-usage.md)
- [How wtgc is structured](docs/architecture.md)

## 💭 Feedback and contributing

Questions and feature ideas belong in
[GitHub Discussions](https://github.com/ben-ranford/wtgc/discussions). Concrete
bugs and feature requests belong in [issues](https://github.com/ben-ranford/wtgc/issues/new/choose).

If `wtgc` calls a worktree safe and you think it is wrong, that is the important
bug. Please report it with the Git state that produced the classification.

Contributions are welcome: start with [CONTRIBUTING.md](CONTRIBUTING.md). Report
suspected data-loss or command-execution vulnerabilities through the private
process in [SECURITY.md](SECURITY.md), not a public issue.

The [support policy](SUPPORT.md) and [Code of Conduct](CODE_OF_CONDUCT.md) set
expectations for help and participation.

`wtgc` was created by [Ben Ranford](https://github.com/ben-ranford) and is
available under the [MIT License](LICENSE).
