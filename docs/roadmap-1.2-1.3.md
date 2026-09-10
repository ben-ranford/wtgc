# wtgc roadmap: 1.2.0 and 1.3.0

Planning baseline: 2026-09-10, `origin/main` at `0bccc1e`.
Status: proposed delivery plan. Milestones describe intended outcomes; dates and implementation are not yet committed.

## Product direction

Help developers using one worktree per task reclaim disk space with evidence they can understand and control. Optimize for a successful, confident cleanup session and preservation of useful work. Downloads, coverage percentages, and UI feature counts are not substitutes for that outcome.

Primary user: a developer or agent-workflow maintainer reviewing 50–300 worktrees across multiple repositories. Secondary user: an operator consuming unattended dry-run JSON. The latter's existing contract must remain stable.

Use value versus effort, with evidence confidence and a safety override. We lack measured adoption, representative interviews, and support-volume data, so numerical RICE scores would imply precision we do not have. Sizes below are relative engineering estimates, not elapsed-time promises: S = a focused change, M = several coordinated changes, L = a cross-layer capability requiring adversarial validation.

## Evidence and assumptions

| Evidence | Planning implication |
|---|---|
| [1.1.0 is published](https://github.com/ben-ranford/wtgc/releases/tag/wtgc-v1.1.0); provider confirmation, retention, cache warnings, harness integration and native CI are delivered | Do not reopen these as new feature epics |
| [Coverage work #72](https://github.com/ben-ranford/wtgc/issues/72) is closed and [PR #103](https://github.com/ben-ranford/wtgc/pull/103) merged; [release PR #104](https://github.com/ben-ranford/wtgc/pull/104) is open with passing checks at planning time | Keep 1.2 small; coverage enforcement is completed scope, not remaining implementation |
| [README](../README.md) records the original audit of 350+ worktrees and 30+ GB | Large inventories are a credible use case, but this is one origin story, not broad demand validation |
| [ADR 0001](decisions/0001-tui-discovery.md) tested scripted 50/300-row interactions and deferred production TUI implementation | Test the review problem with people before funding a new interface |
| Current CLI offers full text/JSON output and serial interactive confirmation; no built-in grouping/filtering/selection contract | There is a bounded opportunity to reduce review effort while keeping the existing engine |
| [Dependency dashboard #17](https://github.com/ben-ranford/wtgc/issues/17) reports a Homebrew action digest lookup warning | Repair dependency monitoring; do not equate the warning with a proven release failure |
| README says source builds need Go 1.26.5+ while `go.mod` requires 1.26.6 | Reconcile installation guidance against the actual build contract |

Assumptions to validate: one maintainer owns delivery; large-inventory manual review recurs often enough to justify work; users prefer selecting a subset before cleanup; no commercial launch date is imposed. User research and performance numbers below are proposed targets, not observed results. Do not add background telemetry; collect consented session measurements and reproducible local fixtures.

## Milestone overview

| Release | Theme and intended outcome | Scope boundary | Exit evidence |
|---|---|---|---|
| 1.2.0 — Release confidence | Users can install the exact release and complete a trustworthy first preview | Finish the ready release, align documentation, repair the known dependency-monitoring warning | Exact-version install/upgrade and release-asset checks; documented dry-run and disposable cleanup results; current required CI passes |
| 1.3.0 — Review at scale | Users can identify and deliberately clean a chosen subset of large inventories with less effort | CLI review and explicit selection over the existing engine; TUI decision only | Measured review improvement, complete selection safety tests, compatible JSON and native-platform verification |

1.2 is the near-term delivery commitment proposed by this plan. 1.3 is the next investment, subject to its discovery gate. Its core workflow ships through the CLI. A production TUI remains outside both milestones; a positive decision produces a separately estimated future milestone rather than silently enlarging 1.3.

## 1.2.0: release confidence

Hypothesis: if installation guidance and release validation agree on the exact binary being delivered, developers will reach a useful first preview without manual repair or uncertainty about what version they installed.

| ID | Work item | Priority / size / confidence | Acceptance and evidence |
|---|---|---|---|
| R12-1 | Verify and publish the exact 1.2.0 release through existing PR #104 | Must / S / high | Required checks pass on the final release head. Verify all six OS/architecture archives, checksums, SBOM and configured attestations. Native Linux/macOS/Windows smoke checks identify the exact version. Preflight Homebrew token/configuration availability without exposing credentials. With publication configured, public-tap clean install and an actual 1.1.0 → 1.2.0 upgrade both resolve 1.2.0 and its formula test passes. Source-installing a candidate formula alone is not upgrade evidence. A disposable fixture proves dry-run preservation, safe removal and dirty refusal. Preserve logs and asset URLs; close only after publication is verified. |
| R12-2 | Make first-run documentation match shipped behavior | Must / S / high | Source Go requirement matches `go.mod`; document retention units/default, GitHub token/environment setup and remote selection, cache warning semantics, and common kept reasons. A person can follow source/Homebrew instructions to `--version` and a useful dry-run. Examples do not mutate by default and explain that retained work is not a cleanup failure. |
| R12-3 | Restore Homebrew action dependency monitoring | Should / S / high | Diagnose #17's specific digest lookup warning, retain immutable action pinning, demonstrate that the configured lookup resolves the intended upstream version/digest, and capture a successful Renovate lookup. Do not close the evergreen dependency dashboard. |

Completed baseline: 98% global and per-package coverage enforcement from #72/#103. Preserve historical issue placement; link that work rather than reopening or duplicating it. Attach the existing release PR to 1.2.0. Release numbering follows the existing Release Please proposal; this plan does not invent a breaking change to justify the old v2 milestone.

Sequence: R12-2 before the final release-PR refresh; R12-3 in parallel. R12-1 runs pre-publication checks, then publication, then installed-release checks. If dependency-monitoring repair is blocked externally while the pinned release workflow passes, defer R12-3 explicitly to a maintenance milestone and record the outstanding warning; it is not a reason to claim the repair complete or block a usable release indefinitely. If Homebrew publication credentials are unavailable, record the skipped checks and an open release follow-up; a maintainer must explicitly decide whether to defer the advertised Homebrew update before publishing archive-only. Never report skipped Homebrew steps as passed. Do not hold 1.2 for 1.3 discovery.

Success: all required platform/install checks identify the intended version; the first-preview walkthrough needs no undocumented steps; zero unexpected mutations in the release fixtures. These are release acceptance measures, not a claim that testing proves absence of every possible defect.

## 1.3.0: review at scale

Hypothesis: if users can group and filter a large inventory, inspect why each item is kept, and select exact eligible worktrees, they will finish review faster and make fewer selection mistakes than with today's full table and serial prompts.

The intended minimum product is a CLI review workflow with explicit selection and fresh cleanup revalidation. Command names and flags must be finalized in R13-1/R13-2 before implementation. A saved inventory is untrusted advisory data and never authorizes deletion.

| ID | Work item | Priority / size / confidence | Acceptance and evidence |
|---|---|---|---|
| R13-1 | Validate large-inventory review tasks and lock the command contract | Must, first / S / medium | Recruit five relevant developers where possible, including at least three beyond the maintainer. Compare current CLI with a disposable review mock on balanced 50/300-row fixtures. Counterbalance task order. Record completion time, incorrect selections, explanation comprehension and cancellations. Freeze proposed flags/commands, separately versioned review output, selected-set and policy-context contracts, and golden examples before production work. |
| R13-2 | Add grouped, filtered CLI inventory review | Must, after R13-1 / M / medium | Group by repository/classification; filter by repository/classification; stable sort by size/path. Show HEAD/path identity, full reason, retention/provider evidence, advisory cache bytes and full/visible/selected totals with clear labels. Partial errors remain visible even when filters hide affected rows. No external fetch or mutation. Preserve existing default output and JSON schema 1.1.0; define any new review output separately. |
| R13-3 | Clean only explicitly selected live worktrees | Must, after R13-2 / L / medium | Selection binds repository, canonical path, branch and expected HEAD; duplicate/unknown/unsafe selections are refused before mutation. Preview and confirmation name the exact set and selected reclaimable bytes. Reuse the existing revalidation/removal path with current invocation policy, and revalidate provider/retention/protection/dirty state immediately before each action. Identity or state drift refuses the affected action and is reported. Unselected live worktrees stay untouched. Branch deletion remains separately opt-in and squash-confirmed branches remain retained. |
| R13-4 | Prove review compatibility, safety and user benefit | Must, alongside R13-2/3 / M / high for safety, medium for benefit | Exercise 50/300-row fixtures, 80-column/plain/non-colour/Unicode and non-TTY output on supported platforms. Test hostile terminal text, malformed inventories, schema mismatch, symlinks/path ambiguity, changed HEAD, dirty/untracked/locked state, policy drift, interrupted input, cancellation and partial failure. Validate golden 1.1.0 inventories against the existing schema and assert unchanged semantics for existing invocations; normalize timestamps and fixture paths explicitly. Deterministic text fixtures retain byte compatibility. New review output has a separate versioned contract; do not add fields to the existing closed JSON schema. Preserve the existing 350-worktree benchmark gate and 98% coverage floors. Record the task-study comparison and release installation smoke results. |
| R13-5 | Reassess the TUI dependency and product case | Could, two-day timebox / S / low-to-medium | Recheck Stave release/support, license and pinned API contract; compare a real terminal prototype with the improved CLI using the same tasks. Document keyboard/accessibility, terminal cancellation and semantic-action conformance evidence. Update ADR 0001 with go/no-go and an estimate. A go produces future implementation issues; no runtime TUI dependency or production TUI ships in 1.3. |

### Discovery and release gates

R13-1 is capped at one working week of active discovery. Recruit developers who have used Git worktrees in the preceding month and have reviewed at least 20 worktrees; the maintainer recruits consenting contributors or existing developer contacts. This planning task sends no invitations. Use two fixed tasks on equivalent fixtures: (A) explain why a specified dirty, retention-held, and provider-confirmed row is kept/removable and find the largest eligible rows; (B) identify three specified eligible live worktrees and report their combined reclaimable bytes. For the current-CLI baseline, use dry-run output only: record the three identities and manually calculate/report the sum on a worksheet; do not accept interactive prompts. For the mock/built CLI, use its selection controls to report the same identities and sum. Time only the common identification-and-total task and score the same answer key in both conditions. Separately test the new confirmation/cancel flow without including that additional task in the 30% timing comparison; never invoke cleanup in a user-study fixture. An answer key defines the allowed row identities and explanations; selecting an unsafe or unintended identity, misreading cache bytes, or failing to cancel is an error. Store consented raw notes privately under maintainer control; commit only anonymized aggregates, fixture definitions, task scripts, and the decision under docs/research/. Proposed investment gate: at least four of five participants complete the review/selection task correctly without coaching; median completion time improves by at least 30% against the current CLI; no participant mistakes cache bytes for removable bytes or retained work for safe work. Capture the reasons for failure, not just the aggregate. This sample is directional evidence, not statistical proof.

If recruitment is unavailable, maintainer-only dogfooding is a labeled fallback and cannot be reported as validated demand. Keep R13-2/3 conditional until the maintainer explicitly accepts the evidence gap. If results fail the gate, revise the mock once within the timebox or stop the feature investment and re-scope the milestone to the specific explanation problems observed. Do not ship selection complexity merely to hit a version label.

R13-4 repeats the same comparison against the built CLI. Target: at least 30% median review-time reduction and four of five correct completions, with no unsafe row accepted in automated fixtures. Report small-sample limits and any discrepancy from the mock. Safety failures block release; missed benefit targets trigger scope review before release. Do not relax mutation checks to meet a speed target.

### Selection and stale-record boundary

Filtering is presentation only and cannot widen eligibility. Selection cannot infer consent from an old JSON `safe_to_remove` value or execute serialized commands. The initial implementation passes the selected set directly in-process to the authoritative application engine. Do not add an executable selection-plan file in this release. The app layer needs an explicit allowlist checked before each live mutation; a CLI display filter or per-row prompt hook is insufficient. Bind repository/common-directory identity, canonical path, branch and expected HEAD. Freeze the effective scan roots and provider/remote/retention/branch-deletion options for the review session; changing them requires a new review. Primary/locked/protection state is re-derived from Git during validation, not trusted from the snapshot. The engine performs a fresh scan and binds the reviewed identities to the existing revalidation path.

Selective cleanup in 1.3 applies to live worktrees only. Display stale records and explain that pruning remains a separate existing cleanup action. Do not trigger repository-wide `git worktree prune` from individual-row selection: its effects can exceed the chosen row. A future selective-prune design requires its own explicit scope and consent contract.

Cancellation before confirmation performs no mutation. During execution, cancellation stops new actions; already completed removals remain recorded and are not described as rolled back. Partial failure reports each succeeded/refused/failed action, preserves the existing exit-code contract, and requires a fresh review before retrying changed worktrees.

## Prioritization and delivery ownership

| Rank | Investment | Value / effort rationale | Decision |
|---|---|---|---|
| 1 | Exact release and accurate first run | High confidence, small remaining effort, every installer benefits | Finish 1.2 first |
| 2 | Review discovery | Cheap way to test the largest documented manual-workflow gap | Start 1.3 with evidence |
| 3 | Grouping, explanations and subset selection | High potential user value; selection has cross-layer safety cost | Build only after discovery; prioritize correctness over shortcuts |
| 4 | Dependency warning repair | Small maintenance task with a concrete observed failure | Parallel to 1.2, explicit deferral if externally blocked |
| 5 | Production TUI | Potential benefit, substantial support and conformance work, uncertain incremental value over improved CLI | Decision work only; production deferred |

Proposed accountable owner: project maintainer for scope, release decisions and research interpretation. Engineering owns the engine/CLI contract and implementation. A reviewer separate from the author owns safety and compatibility assessment. No contributor is assigned or contacted by this planning task.

Suggested sequence: 1.2 documentation/monitoring in parallel → release validation/publication → 1.3 discovery → review output and selection contract → implementation with adversarial verification → user-benefit comparison → release. TUI research may run alongside CLI work but cannot block it. Preserve time for review and release verification instead of filling the milestone with stretch features. Calendar due dates require actual capacity and participant availability; none are invented here.

## Outside these milestones

- GitLab/provider expansion without evidenced user demand.
- Automatic fetching, autonomous scheduled deletion, cache deletion or relaxed cleanup eligibility.
- A persistent dashboard, general Git client, web UI or background telemetry.
- Breaking changes to existing JSON, default CLI behavior or safety guarantees.
- New runtime dependencies as part of roadmap planning; a future Stave adoption needs its own decision.

## Planning completion versus delivery completion

This document and its GitHub issues complete planning only. All new delivery issues remain open until their acceptance evidence exists. Release PR #104 is not merged by the planning task. Old v1.0/v2.0 milestones remain historical metadata; cleanup of those milestones is not required to plan these releases.

GitHub milestone descriptions use the outcomes and release gates above. Each issue owns the matching acceptance criteria. R13-2 and R13-3 are blocked by the recorded R13-1 investment decision; R13-4 designs tests in parallel and verifies completed implementation. R13-5 is non-blocking. Proposed accountable roles are recorded without assigning or contacting contributors. Publication links follow below.


## GitHub delivery map

- [v1.2.0](https://github.com/ben-ranford/wtgc/milestone/5): Release confidence: users can install the exact release and complete a trustworthy first preview. Must: verify existing release PR #104, exact-version assets/native smoke checks and configured Homebrew install plus real upgrade; align first-run docs. Should: repair the Homebrew action lookup warning from #17. Completed scope: 98% coverage #72/#103. No new cleanup features. Missing Homebrew publication configuration requires an explicit maintainer deferral and tracked follow-up, never a claimed pass. No due date until capacity is known.
- [v1.3.0](https://github.com/ben-ranford/wtgc/milestone/6): Review at scale: help developers review 50–300 worktrees and clean an explicitly chosen safe subset. Start with timeboxed usability discovery; grouping/filtering and engine-enforced selection are conditional on its recorded decision. Preserve existing CLI/JSON, dry-run default, fresh revalidation and separate branch deletion. Target >=30% median review-time reduction and 4/5 correct unaided completions; safety failures block release. TUI work is a non-blocking decision spike only; no production TUI. No due date until discovery and capacity are known.

| Plan ID | Delivery issue | Depends on |
|---|---|---|
| R12-1 | [#105: Verify and publish the exact 1.2.0 release through existing PR #104](https://github.com/ben-ranford/wtgc/issues/105) | None |
| R12-2 | [#106: Make first-run documentation match shipped behavior](https://github.com/ben-ranford/wtgc/issues/106) | None |
| R12-3 | [#107: Restore Homebrew action dependency monitoring](https://github.com/ben-ranford/wtgc/issues/107) | None |
| R13-1 | [#108: Validate large-inventory review tasks and lock the command contract](https://github.com/ben-ranford/wtgc/issues/108) | None |
| R13-2 | [#109: Add grouped, filtered CLI inventory review](https://github.com/ben-ranford/wtgc/issues/109) | [R13-1](https://github.com/ben-ranford/wtgc/issues/108) |
| R13-3 | [#110: Clean only explicitly selected live worktrees](https://github.com/ben-ranford/wtgc/issues/110) | [R13-1](https://github.com/ben-ranford/wtgc/issues/108), [R13-2](https://github.com/ben-ranford/wtgc/issues/109) |
| R13-4 | [#111: Prove review compatibility, safety and user benefit](https://github.com/ben-ranford/wtgc/issues/111) | [R13-1](https://github.com/ben-ranford/wtgc/issues/108), [R13-2](https://github.com/ben-ranford/wtgc/issues/109), [R13-3](https://github.com/ben-ranford/wtgc/issues/110) |
| R13-5 | [#112: Reassess the TUI dependency and product case](https://github.com/ben-ranford/wtgc/issues/112) | [R13-1](https://github.com/ben-ranford/wtgc/issues/108) |

Existing [release PR #104](https://github.com/ben-ranford/wtgc/pull/104) is assigned to 1.2.0. Completed coverage #72/#103 remains linked as baseline scope; its historical milestone is unchanged. All eight newly created delivery issues are open.
