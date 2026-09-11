// Package explain builds the versioned, single-worktree diagnostic view.
package explain

import (
	"slices"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

const SchemaVersion = "1.0.0"

type Document struct {
	ExplainSchemaVersion string               `json:"explain_schema_version"`
	Worktree             model.Worktree       `json:"worktree"`
	Checks               []model.ExplainCheck `json:"checks"`
	NextChecks           []string             `json:"next_checks"`
}

// Build derives a diagnostic from classifier fields. It never parses a human
// reason string, and it makes checks skipped by a short circuit explicit.
func Build(worktree model.Worktree, providerRequested bool, retention time.Duration) Document {
	if worktree.WorktreeDetails != nil && worktree.ExplainChecks != nil {
		return Document{ExplainSchemaVersion: SchemaVersion, Worktree: worktree, Checks: appendMissingChecks(worktree, slices.Clone(worktree.ExplainChecks), providerRequested, retention), NextChecks: nextChecks(worktree, providerRequested, retention)}
	}
	return Document{ExplainSchemaVersion: SchemaVersion, Worktree: worktree, Checks: unavailableChecks(), NextChecks: nextChecks(worktree, providerRequested, retention)}
}

func unavailableChecks() []model.ExplainCheck {
	checks := make([]model.ExplainCheck, 0, 8)
	for _, id := range []string{"registration", "disk_usage", "working_tree", "protection", "local_default_reachability", "remote_tracking_reachability", "provider_proof", "retention"} {
		checks = append(checks, statusCheck(id, model.ExplainUnavailable, "classifier evidence was unavailable"))
	}
	return checks
}

func appendMissingChecks(worktree model.Worktree, checks []model.ExplainCheck, providerRequested bool, retention time.Duration) []model.ExplainCheck {
	seen := make(map[string]bool, len(checks))
	for _, check := range checks {
		seen[check.ID] = true
	}
	for _, id := range []string{"registration", "disk_usage", "working_tree", "protection", "local_default_reachability", "remote_tracking_reachability", "provider_proof", "retention"} {
		if seen[id] {
			continue
		}
		checks = append(checks, missingCheck(worktree, id, providerRequested, retention))
	}
	return checks
}

func missingCheck(worktree model.Worktree, id string, providerRequested bool, retention time.Duration) model.ExplainCheck {
	if id == "provider_proof" && !providerRequested {
		return statusCheck(id, model.ExplainNotEvaluated, "not evaluated; provider proof requires --provider github")
	}
	if id == "retention" && retention == 0 {
		return statusCheck(id, model.ExplainNotEvaluated, "not evaluated; no retention window was requested")
	}
	if id == "retention" && worktree.RetentionBasis != "" && worktree.EligibleAt != nil {
		status := model.ExplainPassed
		if worktree.Remaining > 0 {
			status = model.ExplainBlocked
		}
		return statusCheck(id, status, "basis="+worktree.RetentionBasis+" eligible_at="+worktree.EligibleAt.UTC().Format(time.RFC3339))
	}
	return statusCheck(id, model.ExplainNotEvaluated, "not evaluated because an earlier safety decision short-circuited classification")
}

func statusCheck(id string, status model.ExplainCheckStatus, detail string) model.ExplainCheck {
	return model.ExplainCheck{ID: id, Status: status, Detail: detail}
}
func nextChecks(w model.Worktree, providerRequested bool, retention time.Duration) []string {
	result := []string{"Inspect this worktree's status with your local Git tooling."}
	if w.Locked {
		result = append(result, "Inspect the repository worktree registrations and lock state.")
	}
	if w.Classification == model.Unmerged && !providerRequested {
		result = append(result, "Confirm local remote-tracking reachability.", "Rerun with --provider github only if explicit provider merge proof is required.")
	}
	if retention > 0 && w.EligibleAt != nil && w.Remaining > 0 {
		result = append(result, "wait until "+w.EligibleAt.UTC().Format(time.RFC3339))
	}
	if w.Error != "" {
		result = append(result, "rerun wtgc explain after resolving the reported local inspection error")
	}
	return slices.Compact(result)
}
