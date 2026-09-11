package explain

import (
	"testing"

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
