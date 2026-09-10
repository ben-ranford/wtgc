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

Squash merges can be proven with explicit `--provider github` confirmation,
which binds the pull request to the exact branch SHA and mapped upstream.

For a live worktree, missing proof means it stays. False negatives waste disk;
false positives destroy work. Given that choice, `wtgc` leaves the worktree
alone. Live worktrees are removed through `git worktree remove`, never raw
`rm -rf`, and local branch deletion is a separate opt-in action.

Stale records are handled separately. If Git marks a missing worktree path as
prunable, `wtgc` may remove its registration with `git worktree prune
--expire=now`; it does not delete a directory. The complete
[safety contract](docs/safety.md) documents every refusal and revalidation rule.
For unattended dry-run guidance, see the [harness documentation](docs/harness.md).

## 🚀 Usage

Point `wtgc` at the directory containing your repositories:

```bash
wtgc clean --scan-root "$HOME/Projects"
```

Running `wtgc` without arguments prints this usage and exits. Use the explicit
`clean` command when you want to start a scan; flags may also be supplied
without the command token for compatibility. The bare invocation never
recursively scans the directory where the shell happens to be running.

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
Provider-confirmed squash candidates always retain their local branch, so an
interactive prompt states that outcome before removing the worktree.

Git commands have no deadline by default. For hooks and scheduled jobs, set
`WTGC_GIT_TIMEOUT=2m` (or another positive Go duration) to apply a deadline to
each Git command.

### First-run options and kept worktrees

The default scan uses local Git data only and does not fetch. Start with one
repository to check its remote/default-branch setup:

```bash
wtgc --version
wtgc clean --scan-root "$HOME/Projects/my-repo"

# Keep otherwise safe worktrees for seven days; still a dry-run
wtgc clean --scan-root "$HOME/Projects/my-repo" --retention 168h
```

`--retention` takes a non-negative Go duration such as `30m`, `24h`, or `168h`
(use hours for days; `7d` is not supported). The default `0` adds no waiting
period. Age uses the provider merge time when available, otherwise the newest
worktree modification time; missing or unreliable timestamps keep the worktree.

For squash-merge proof, explicitly opt into GitHub access. Supply `GH_TOKEN`
or `GITHUB_TOKEN` through your shell or secret manager; `GH_TOKEN` takes
precedence. A login stored by `gh` is not read automatically. If you already
use an authenticated GitHub CLI, this passes its token only via the environment:

```bash
GH_TOKEN="$(gh auth token)" wtgc clean --scan-root "$HOME/Projects/my-repo" --provider github

# When multiple remotes track the default branch, select the base remote
GH_TOKEN="$(gh auth token)" wtgc clean --scan-root "$HOME/Projects/my-repo" --provider github --provider-remote origin
```

Use the actual base remote name in place of `origin`. The feature branch also
needs an unambiguous configured upstream; selecting the base remote does not
replace that mapping. Authentication, mapping, or proof failures retain the
candidate. See the [provider safety contract](docs/safety.md#provider-confirmation-and-cache-warnings).

Retained work is an expected safety outcome, not a cleanup failure:

| Reason | What to check |
| --- | --- |
| Primary, current, default-branch, locked, or detached worktree | These worktrees are protected. |
| Tracked, staged, or untracked changes | Review and preserve the local work. |
| Branch tip is not reachable locally or remotely | Check merge status and remote-tracking refs; wtgc does not fetch for you. |
| Default branch or provider mapping cannot be resolved | Check remote `HEAD` and branch upstream configuration. |
| Retention window has not elapsed | Wait until the reported eligibility time. |

Cache warnings identify recognized dependency/build directories at or above
`--cache-threshold` (bytes; default `104857600`, or 100 MiB). `0` reports all
recognized cache directories. Warnings, including cache scan errors, are
advisory: they do not change eligibility or authorize cache deletion. wtgc has
no separate cache-deletion action; removing an eligible worktree removes its
contents with it.

## ⬇️ Installation

### Homebrew (recommended)

Install the latest stable release from the `wtgc` formula:

```bash
brew tap ben-ranford/tap
brew install wtgc
wtgc --version
wtgc clean --scan-root "$HOME/Projects/my-repo"
```

### Optional: install from source

With Go `1.26.6+`, install from source. Set an explicit binary directory so the
next commands can find it:

```bash
export GOBIN="$HOME/.local/bin"
export PATH="$GOBIN:$PATH"
go install github.com/ben-ranford/wtgc/cmd/wtgc@latest
wtgc --version
wtgc clean --scan-root "$HOME/Projects/my-repo"
```

Replace `my-repo` with an existing repository path. `@latest` follows Go module
version resolution and may include unreleased changes from the default branch;
use Homebrew for the latest stable release. To build a specific local checkout,
see [development setup](CONTRIBUTING.md#development-setup). Source installations
may report `dev` because release version metadata is supplied by release builds.

Release automation is configured to publish prebuilt archives for Linux, macOS,
and Windows on amd64 and arm64. Checksums and an SPDX SBOM will be published
beside them on [GitHub Releases](https://github.com/ben-ranford/wtgc/releases).
Stable releases also update the `wtgc` formula in
[`ben-ranford/tap`](https://github.com/ben-ranford/homebrew-tap).
GitHub records provenance as an artifact attestation. Verify a downloaded
archive with:

```bash
gh attestation verify PATH/TO/WTGC_ARCHIVE -R ben-ranford/wtgc
```

See GitHub's [attestation verification guide](https://docs.github.com/en/actions/how-tos/secure-your-work/use-artifact-attestations/use-artifact-attestations#verifying-an-artifact-attestation-for-binaries)
for details.

Runtime requirements:

- Git `2.36+`
- An unambiguous remote default branch, such as `origin/HEAD -> origin/main`
- Go `1.26.6+` only when installing from source

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
