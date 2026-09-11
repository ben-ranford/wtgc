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
	Worktree             Worktree             `json:"worktree"`
	Checks               []model.ExplainCheck `json:"checks"`
	NextChecks           []string             `json:"next_checks"`
}

// Worktree is the explain schema projection. It deliberately omits cleanup
// actions and inventory-only mutation fields.
type Worktree struct {
	Path           string               `json:"path"`
	Branch         string               `json:"branch,omitempty"`
	Head           string               `json:"head,omitempty"`
	Repository     string               `json:"repository"`
	DefaultBranch  string               `json:"default_branch,omitempty"`
	Classification model.Classification `json:"classification"`
	Reason         string               `json:"reason"`
	Error          string               `json:"error,omitempty"`
	Dirty          *bool                `json:"dirty,omitempty"`
	DiskBytes      int64                `json:"disk_bytes"`
	RetentionBasis string               `json:"retention_basis,omitempty"`
	ObservedAt     *time.Time           `json:"observed_at,omitempty"`
	EligibleAt     *time.Time           `json:"eligible_at,omitempty"`
	Remaining      time.Duration        `json:"retention_remaining_ns,omitempty"`
	Provider       string               `json:"provider,omitempty"`
	ProviderPR     int                  `json:"provider_pr,omitempty"`
	ProviderURL    string               `json:"provider_url,omitempty"`
	MergedAt       *time.Time           `json:"merged_at,omitempty"`
	Proof          *ProviderProof       `json:"provider_proof,omitempty"`
}
type ProviderProof struct {
	HeadSHA        string    `json:"head_sha"`
	HeadRef        string    `json:"head_ref"`
	HeadRepository string    `json:"head_repository"`
	BaseRef        string    `json:"base_ref"`
	BaseRepository string    `json:"base_repository"`
	HeadRemote     string    `json:"head_remote"`
	BaseRemote     string    `json:"base_remote"`
	MergeCommitSHA string    `json:"merge_commit_sha"`
	MergedAt       time.Time `json:"merged_at"`
}

// Build derives a diagnostic from classifier fields. It never parses a human
// reason string, and it makes checks skipped by a short circuit explicit.
func Build(worktree model.Worktree, providerRequested bool, retention time.Duration) Document {
	if worktree.WorktreeDetails != nil && worktree.ExplainChecks != nil {
		return Document{ExplainSchemaVersion: SchemaVersion, Worktree: project(worktree), Checks: appendMissingChecks(worktree, slices.Clone(worktree.ExplainChecks), providerRequested, retention), NextChecks: nextChecks(worktree, providerRequested, retention)}
	}
	return Document{ExplainSchemaVersion: SchemaVersion, Worktree: project(worktree), Checks: unavailableChecks(), NextChecks: nextChecks(worktree, providerRequested, retention)}
}

func project(w model.Worktree) Worktree {
	v := Worktree{Path: w.Path, Branch: w.Branch, Head: w.Head, Repository: w.Repository, DefaultBranch: w.DefaultBranch, Classification: w.Classification, Reason: w.Reason, Error: w.Error, Dirty: w.Dirty, DiskBytes: w.DiskBytes}
	if w.WorktreeDetails == nil {
		return v
	}
	v.RetentionBasis, v.ObservedAt, v.EligibleAt, v.Remaining = w.RetentionBasis, w.ObservedAt, w.EligibleAt, w.Remaining
	v.Provider, v.ProviderPR, v.ProviderURL, v.MergedAt = w.Provider, w.ProviderPR, w.ProviderURL, w.MergedAt
	p := w.ProviderProof
	if p.Kind != "" {
		v.Proof = &ProviderProof{HeadSHA: p.HeadSHA, HeadRef: p.HeadRef, HeadRepository: p.HeadOwner + "/" + p.HeadRepo, BaseRef: p.BaseRef, BaseRepository: p.BaseOwner + "/" + p.BaseRepo, HeadRemote: p.HeadRemote, BaseRemote: p.BaseRemote, MergeCommitSHA: p.MergeCommitSHA, MergedAt: p.MergedAt}
	}
	return v
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
