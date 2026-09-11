package explain

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

func TestBuildPreservesClassifierTraceAndMarksShortCircuits(t *testing.T) {
	document := Build(model.Worktree{Path: "/repo/locked", Classification: model.Kept, Locked: true, WorktreeDetails: &model.WorktreeDetails{ExplainChecks: []model.ExplainCheck{{ID: "registration", Status: model.ExplainPassed, Detail: "registered"}, {ID: "protection", Status: model.ExplainBlocked, Detail: "locked"}}}}, false, 0)
	checks := map[string]model.ExplainCheck{}
	for _, check := range document.Checks {
		checks[check.ID] = check
	}
	if checks["protection"].Status != model.ExplainBlocked || checks["local_default_reachability"].Status != model.ExplainNotEvaluated || checks["provider_proof"].Status != model.ExplainNotEvaluated {
		t.Fatalf("checks=%+v", checks)
	}
}

func TestBuildProjectsOnlyExplainFieldsAndRetentionTrace(t *testing.T) {
	now := time.Now().UTC()
	document := Build(model.Worktree{Path: "/repo/wt", Branch: "feature", Head: "head", Repository: "/repo", Classification: model.SafeToRemove, WorktreeDetails: &model.WorktreeDetails{RetentionBasis: "provider_merged_at", ObservedAt: &now, EligibleAt: &now, ProviderProof: model.ProviderProof{Kind: "github", HeadSHA: "head", HeadOwner: "owner", HeadRepo: "headrepo", HeadRef: "feature", BaseOwner: "owner", BaseRepo: "base", BaseRef: "main", HeadRemote: "fork", BaseRemote: "origin", MergeCommitSHA: "merge", MergedAt: now}, ExplainChecks: []model.ExplainCheck{{ID: "retention", Status: model.ExplainPassed, Detail: "elapsed"}}}}, true, time.Hour)
	if document.Worktree.Proof == nil || document.Worktree.Proof.HeadRepository != "owner/headrepo" || document.Worktree.Proof.BaseRemote != "origin" {
		t.Fatalf("proof=%+v", document.Worktree.Proof)
	}
	for _, check := range document.Checks {
		if check.ID == "retention" && check.Status != model.ExplainPassed {
			t.Fatalf("retention=%+v", check)
		}
	}
}

func TestBuildDoesNotSynthesizeRetentionProofWhenTraceIsAbsent(t *testing.T) {
	now := time.Now().UTC()
	document := Build(model.Worktree{Path: "/repo/wt", Classification: model.Kept, WorktreeDetails: &model.WorktreeDetails{RetentionBasis: "worktree_mtime", EligibleAt: &now, Remaining: time.Hour, ExplainChecks: []model.ExplainCheck{{ID: "registration", Status: model.ExplainPassed}}}}, false, time.Hour)
	for _, check := range document.Checks {
		if check.ID == "retention" && check.Status != model.ExplainNotEvaluated {
			t.Fatalf("retention=%+v, want trace absence to remain not evaluated", check)
		}
	}
}

func TestWithOperationalErrorsCopiesDiagnostics(t *testing.T) {
	document := WithOperationalErrors(Build(model.Worktree{Path: "/repo/wt"}, false, 0), []string{"scan failed"})
	if len(document.OperationalErrors) != 1 || document.OperationalErrors[0] != "scan failed" {
		t.Fatalf("operational errors=%+v", document.OperationalErrors)
	}
}

func TestOperationalBuildsUnavailableDiagnosticForUninspectedTarget(t *testing.T) {
	document := Operational("/repo/missing", []string{"context canceled"})
	if document.Worktree.Path != "/repo/missing" || document.Worktree.Classification != model.Error || document.Worktree.Reason != "requested worktree could not be inspected" || !slices.Equal(document.OperationalErrors, []string{"context canceled"}) {
		t.Fatalf("document=%+v", document)
	}
	for _, check := range document.Checks {
		if check.Status != model.ExplainUnavailable {
			t.Fatalf("check=%+v", check)
		}
	}
}

func TestNextChecksDescribeConcreteBlockedStates(t *testing.T) {
	now := time.Now().UTC()
	document := Build(model.Worktree{Path: "/repo/wt", Classification: model.Unmerged, Locked: true, Error: "status unavailable", WorktreeDetails: &model.WorktreeDetails{EligibleAt: &now, Remaining: time.Hour, ExplainChecks: []model.ExplainCheck{{ID: "registration", Status: model.ExplainPassed}}}}, false, time.Hour)
	joined := strings.Join(document.NextChecks, "\n")
	for _, want := range []string{"lock state", "remote-tracking", "--provider github", "wait until", "after resolving"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("next checks=%q, missing %q", joined, want)
		}
	}
}

func TestBuildWithoutTraceDoesNotInferProofOrEmitExecutablePath(t *testing.T) {
	document := Build(model.Worktree{Path: "/repo/hostile; run-this", Head: "deadbeef", Classification: model.Unmerged}, false, 0)
	for _, check := range document.Checks {
		if check.Status != model.ExplainUnavailable {
			t.Fatalf("check %s status=%s, want unavailable without classifier trace", check.ID, check.Status)
		}
	}
	for _, next := range document.NextChecks {
		if next == "" || next == "/repo/hostile; run-this" {
			t.Fatalf("unsafe next check %q", next)
		}
	}
}
