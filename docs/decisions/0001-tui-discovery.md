# ADR 0001: defer a wtgc TUI

Date: 2026-09-09
Status: accepted for v1.1 discovery
Decision: do not add `wtgc tui` or a Stave dependency in v1.1.

## Context

Issue #81 asks whether a terminal UI materially improves review of large wtgc
inventories while keeping the CLI, its dry-run, and its cleanup decision engine
authoritative. A TUI must consume the published inventory and must not become a
worktree, branch, pull-request, file-explorer, or persistent-dashboard product.

The existing `--interactive` flow already offers serial confirmation. It is
adequate when the user has a small, already-understood set of candidates, but
it does not offer a repository/classification overview, filtering, a stable
skip-reason view, multi-selection, or a selected reclaimed-space total.

## Disposable interaction mock

A throwaway standard-library Go program generated CLI-shaped dry-run inventory
documents and consumed them through JSON decoding. It exercised a scripted
review flow: filter `safe_to_remove`, navigate (`j`/`k`), inspect a reason
(`enter`), select a safe row (`space`), open confirmation (`c`), and cancel
(`esc`). It did not invoke wtgc cleanup.

| Fixture | Repositories | Safe candidates | Visible page | Potential bytes | Result |
| --- | ---: | ---: | ---: | ---: | --- |
| 50 worktrees, 100 columns | 10 | 10 | 10 | 246,415,360 | selection allowed only for a safe row; cancel reported `mutation: none` |
| 300 worktrees, 80 columns | 10 | 60 | 12 | 9,342,812,160 | one partial scan error stayed visible; safe selection and cancel reported `mutation: none` |

The mock's in-memory render/model pass took 11,500 ns for 50 rows and 8,208 ns
for 300 rows on the discovery host. These figures are not a terminal-rendering
benchmark; they only show that bounded pages over the inventory model do not
need to process every row on each user action.

The prototype, fixture JSON, and executable were created under a `mktemp -d`
directory, then removed after validation. The recorded command shape was:

```sh
GO111MODULE=off go run . --generate 50 > inventory-50.json
GO111MODULE=off go run . --generate 300 > inventory-300.json
GO111MODULE=off go run . --mock --width 100 < inventory-50.json > result-50.json
GO111MODULE=off go run . --mock --width 80 < inventory-300.json > result-300.json
```

This is an interaction/state mock, not a real terminal renderer. It cannot
prove terminal compatibility, accessibility, focus visibility, key delivery,
or rendering performance; those remain implementation-gate requirements.

## Interaction comparison

| Need | Current `--interactive` | Proposed future TUI |
| --- | --- | --- |
| Small, single-candidate confirmation | Direct and lower complexity | No material advantage |
| 50–300 mixed candidates | Sequential prompts obscure the inventory | Group by repository/classification and filter before selecting |
| Why a row is kept | Present in output but separated from the prompt sequence | Master/detail reason panel, including provider, retention, and cache warnings |
| Reclaimed-space review | Available in the summary | Selected-candidate total beside full potential total |
| Keyboard and cancellation | Prompt-specific input and EOF refusal | Visible focus, `j`/`k`, filter, help, confirmation dialog, and `esc` cancel |
| Error or partial failure | Reported after a command run | Immutable result rows; keep failed and changed-since-scan rows selected only after re-review |

The comparison supports a TUI for occasional large manual review sessions, not
as a replacement for scripted JSON or the existing interactive path.

## Stave evaluation

The issue specifies `github.com/ben-ranford/stave`. Its [adoption guide](https://github.com/ben-ranford/stave/blob/main/docs/client-adoption.md)
requires an application-owned model/reducer, immutable semantic tree, typed
action registry, application-owned capability policy/keymap/effects, and
conformance fixtures. Its [primitives](https://github.com/ben-ranford/stave/blob/main/docs/primitives.md)
support bounded table windows, stable row IDs, and master/detail focus return.
Its [accessibility contract](https://github.com/ben-ranford/stave/blob/main/docs/accessibility-agent-parity.md)
requires keyboard parity, accessible names, non-colour state signals, and
explicit dialog cancellation. Its [security contract](https://github.com/ben-ranford/stave/blob/main/docs/security.md)
treats terminal/report text as untrusted data and binds consequential actions
to policy and confirmation grants.

Those contracts fit wtgc's safety boundary well. They also make a correct
integration substantially larger than rendering a table: the application would
need its own typed action policy, semantic fixtures, terminal capability modes,
and conformance coverage. At discovery time Stave's repository has no GitHub
release and reported zero stars, so its versioned API and release/support
surface are not yet a proportionate v1.1 dependency risk.

## Decision and future shape

Defer implementation. Keep `wtgc clean --json` and `--interactive` as the
supported review surfaces in v1.1. This preserves the stdlib-only dependency
policy and avoids adding an experimental UI to a data-deleting tool.

If a future issue reopens the decision after Stave has a supported release, the
smallest proposed command is `wtgc tui --inventory FILE` (with a separately
designed optional command that obtains a fresh dry-run inventory). It must:

1. render only the inventory and decision data from `internal/app`/the public
   model; it must not classify, fetch, or scan independently;
2. group and filter by repository/classification; use bounded rows and stable
   path/HEAD identity for detail and focus;
3. disable every non-`safe_to_remove` row, including dirty, ambiguous, and
   changed-since-scan rows;
4. show reason, provider/retention evidence, advisory caches, potential bytes,
   and selected potential bytes without treating advisory bytes as reclaimable;
5. require a final confirmation that binds the selected immutable identity to a
   fresh existing cleanup revalidation; cancellation and terminal loss perform
   no action; and
6. retain plain, narrow, no-colour, ASCII, non-TTY, and keyboard-complete
   paths, with no persistent state or agent-only authority.

Before implementation, create separate future issues for: (a) a Stave version
and license/support review; (b) an inventory-to-semantic-tree adapter with
50/300-row and narrow-terminal conformance fixtures; (c) a confirmation adapter
that invokes the existing revalidation/removal path; and (d) accessibility,
terminal compatibility, cancellation, and partial-failure end-to-end tests.

## Consequences

No production command, runtime dependency, or cleanup path changes in v1.1.
The discovery leaves a concrete architecture boundary and interaction model for
a later, separately reviewed implementation.
