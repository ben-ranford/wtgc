// Package review builds read-only inventory views for the review command.
package review

import (
	"fmt"
	"math/big"
	"sort"

	"github.com/ben-ranford/wtgc/internal/model"
)

const SchemaVersion = "1.0.0"

type Options struct {
	Repositories     []string
	Classifications  []string
	GroupBy          string
	SortBy           string
	SelectedPaths    []string
	ReclaimTarget    int64
	HasReclaimTarget bool
}

// Document keeps the complete scan inventory separate from the projected view.
type Document struct {
	ReviewSchemaVersion string          `json:"review_schema_version"`
	Inventory           model.Inventory `json:"inventory"`
	View                View            `json:"view"`
	Proposal            *Proposal       `json:"proposal,omitempty"`
}

// Proposal is a versioned, advisory-only reclaim estimate. It does not
// authorize cleanup; each candidate must pass clean's fresh validation.
type Proposal struct {
	SchemaVersion  string      `json:"schema_version"`
	Advisory       bool        `json:"advisory"`
	TargetBytes    int64       `json:"target_bytes"`
	TargetMet      bool        `json:"target_met"`
	EstimatedBytes *big.Int    `json:"estimated_bytes"`
	ExcessBytes    *big.Int    `json:"excess_bytes"`
	ShortfallBytes *big.Int    `json:"shortfall_bytes"`
	Candidates     []Candidate `json:"candidates"`
	SelectionRule  string      `json:"selection_rule"`
	Authorization  string      `json:"authorization"`
	Uncertainties  []string    `json:"uncertainties,omitempty"`
}

type Candidate struct {
	Repository string `json:"repository"`
	Path       string `json:"path"`
	Bytes      int64  `json:"bytes"`
}

type View struct {
	GroupBy string  `json:"group_by"`
	SortBy  string  `json:"sort_by"`
	Groups  []Group `json:"groups"`
	Totals  Totals  `json:"totals"`
}

type Group struct {
	Key       string `json:"key"`
	Worktrees []Row  `json:"worktrees"`
}

type Row struct {
	Worktree model.Worktree `json:"worktree"`
	Selected bool           `json:"selected"`
}

type Totals struct {
	Full     Total `json:"full"`
	Visible  Total `json:"visible"`
	Selected Total `json:"selected"`
}

type Total struct {
	Count            int   `json:"count"`
	ReclaimableBytes int64 `json:"reclaimable_bytes"`
	CacheBytes       int64 `json:"cache_bytes"`
}

// Build validates advisory selections and deterministically projects inv.
func Build(inv model.Inventory, opts Options) (Document, error) {
	if opts.HasReclaimTarget && opts.ReclaimTarget <= 0 {
		return Document{}, fmt.Errorf("--reclaim-target must be greater than zero")
	}
	if opts.HasReclaimTarget && len(opts.SelectedPaths) > 0 {
		return Document{}, fmt.Errorf("--reclaim-target cannot be combined with --select")
	}
	selected, selectionErr := resolveSelections(inv.Worktrees, opts.SelectedPaths)
	doc := Document{ReviewSchemaVersion: SchemaVersion, Inventory: inv}
	doc.View.GroupBy, doc.View.SortBy = opts.GroupBy, opts.SortBy
	if doc.View.GroupBy == "" {
		doc.View.GroupBy = "none"
	}
	if doc.View.SortBy == "" {
		doc.View.SortBy = "path"
	}
	doc.View.Totals.Full = total(inv.Worktrees, nil)
	doc.View.Totals.Selected = total(inv.Worktrees, selected)

	visible := make([]Row, 0, len(inv.Worktrees))
	for _, worktree := range inv.Worktrees {
		if matches(worktree, opts) || hasError(worktree) || selected[worktree.Path] {
			visible = append(visible, Row{Worktree: worktree, Selected: selected[worktree.Path]})
		}
	}
	sortRows(visible, doc.View.SortBy)
	doc.View.Totals.Visible = totalRows(visible)
	doc.View.Groups = groupRows(visible, doc.View.GroupBy)
	if opts.HasReclaimTarget {
		doc.Proposal = buildProposal(inv, opts)
	}
	return doc, selectionErr
}

func buildProposal(inv model.Inventory, opts Options) *Proposal {
	proposal := &Proposal{
		SchemaVersion: "1.0.0", Advisory: true, TargetBytes: opts.ReclaimTarget,
		Candidates:     []Candidate{},
		SelectionRule:  "shortest descending-size prefix; minimizes candidate count under estimates, not excess bytes or user disruption",
		Authorization:  "advisory only; cleanup requires explicit paths and fresh validation",
		EstimatedBytes: big.NewInt(0), ExcessBytes: big.NewInt(0), ShortfallBytes: big.NewInt(0),
	}
	eligible, uncertain := proposalScope(inv.Worktrees, opts)
	addProposalCandidates(proposal, eligible)
	setProposalResult(proposal)
	addProposalUncertainties(proposal, inv, uncertain)
	return proposal
}

func proposalScope(worktrees []model.Worktree, opts Options) ([]model.Worktree, int) {
	eligible := make([]model.Worktree, 0, len(worktrees))
	uncertain := 0
	for _, worktree := range worktrees {
		if !matches(worktree, opts) || worktree.Classification != model.SafeToRemove {
			continue
		}
		if eligibleForProposal(worktree) {
			eligible = append(eligible, worktree)
		} else if uncertainSafeWorktree(worktree) {
			uncertain++
		}
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].DiskBytes != eligible[j].DiskBytes {
			return eligible[i].DiskBytes > eligible[j].DiskBytes
		}
		return proposalIdentity(eligible[i]) < proposalIdentity(eligible[j])
	})
	return eligible, uncertain
}

func addProposalCandidates(proposal *Proposal, eligible []model.Worktree) {
	target := big.NewInt(proposal.TargetBytes)
	for _, worktree := range eligible {
		if proposal.EstimatedBytes.Cmp(target) >= 0 {
			return
		}
		proposal.Candidates = append(proposal.Candidates, Candidate{Repository: worktree.Repository, Path: worktree.Path, Bytes: worktree.DiskBytes})
		proposal.EstimatedBytes.Add(proposal.EstimatedBytes, big.NewInt(worktree.DiskBytes))
	}
}

func setProposalResult(proposal *Proposal) {
	target := big.NewInt(proposal.TargetBytes)
	proposal.TargetMet = proposal.EstimatedBytes.Cmp(target) >= 0
	if proposal.TargetMet {
		proposal.ExcessBytes.Sub(proposal.EstimatedBytes, target)
		return
	}
	proposal.ShortfallBytes.Sub(target, proposal.EstimatedBytes)
}

func addProposalUncertainties(proposal *Proposal, inv model.Inventory, uncertain int) {
	if len(inv.Errors) > 0 {
		proposal.Uncertainties = append(proposal.Uncertainties, fmt.Sprintf("inventory has %d scan error(s); unobserved worktrees are not proposed", len(inv.Errors)))
	}
	if uncertain > 0 {
		proposal.Uncertainties = append(proposal.Uncertainties, fmt.Sprintf("%d scoped safe worktree(s) have an unknown, non-positive, excluded, stale, or failed measurement and were not proposed", uncertain))
	}
	if len(inv.ExcludedPaths) > 0 {
		proposal.Uncertainties = append(proposal.Uncertainties, "excluded invocation paths were not inspected or proposed")
	}
	proposal.Uncertainties = append(proposal.Uncertainties, "estimated bytes do not promise actual free disk space")
}

func eligibleForProposal(worktree model.Worktree) bool {
	return worktree.Path != "" && worktree.Repository != "" && worktree.DiskBytes > 0 &&
		!unmeasured(worktree) && !excluded(worktree) && !worktree.Prunable && !worktree.Removed && worktree.Error == ""
}

func uncertainSafeWorktree(worktree model.Worktree) bool {
	return excluded(worktree) || worktree.Prunable || worktree.Error != "" || worktree.DiskBytes <= 0 || unmeasured(worktree)
}

func unmeasured(worktree model.Worktree) bool {
	return worktree.WorktreeDetails != nil && worktree.DiskBytesMeasured != nil && !*worktree.DiskBytesMeasured
}

func excluded(worktree model.Worktree) bool {
	return worktree.WorktreeDetails != nil && worktree.Excluded
}

func proposalIdentity(worktree model.Worktree) string {
	return worktree.Repository + "\x00" + worktree.Path
}

func resolveSelections(worktrees []model.Worktree, paths []string) (map[string]bool, error) {
	selected := make(map[string]bool, len(paths))
	counts := make(map[string]int, len(worktrees))
	for _, worktree := range worktrees {
		counts[worktree.Path]++
	}
	var selectionErr error
	for _, path := range paths {
		if selected[path] {
			if selectionErr == nil {
				selectionErr = fmt.Errorf("duplicate advisory selection %q", path)
			}
			continue
		}
		switch counts[path] {
		case 0:
			if selectionErr == nil {
				selectionErr = fmt.Errorf("unknown advisory selection %q", path)
			}
		case 1:
			selected[path] = true
		default:
			if selectionErr == nil {
				selectionErr = fmt.Errorf("ambiguous advisory selection %q", path)
			}
		}
	}
	return selected, selectionErr
}

func matches(worktree model.Worktree, opts Options) bool {
	return matchesString(worktree.Repository, opts.Repositories) && matchesClassification(worktree.Classification, opts.Classifications)
}

func matchesString(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func matchesClassification(value model.Classification, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if string(value) == candidate {
			return true
		}
	}
	return false
}

func hasError(worktree model.Worktree) bool {
	if worktree.Error != "" || worktree.Classification == model.Error {
		return true
	}
	for _, warning := range cacheWarnings(worktree) {
		if warning.Error != "" {
			return true
		}
	}
	return false
}

func sortRows(rows []Row, sortBy string) {
	sort.SliceStable(rows, func(i, j int) bool {
		if sortBy == "size" && rows[i].Worktree.DiskBytes != rows[j].Worktree.DiskBytes {
			return rows[i].Worktree.DiskBytes > rows[j].Worktree.DiskBytes
		}
		return rows[i].Worktree.Path < rows[j].Worktree.Path
	})
}

func groupRows(rows []Row, groupBy string) []Group {
	groups := make(map[string][]Row)
	for _, row := range rows {
		key := "all"
		switch groupBy {
		case "repository":
			key = row.Worktree.Repository
		case "classification":
			key = string(row.Worktree.Classification)
		}
		groups[key] = append(groups[key], row)
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]Group, 0, len(keys))
	for _, key := range keys {
		result = append(result, Group{Key: key, Worktrees: groups[key]})
	}
	return result
}

func total(worktrees []model.Worktree, selected map[string]bool) Total {
	rows := make([]Row, 0, len(worktrees))
	for _, worktree := range worktrees {
		if selected == nil || selected[worktree.Path] {
			rows = append(rows, Row{Worktree: worktree})
		}
	}
	return totalRows(rows)
}

func totalRows(rows []Row) (result Total) {
	for _, row := range rows {
		result.Count++
		if row.Worktree.Classification == model.SafeToRemove {
			result.ReclaimableBytes += row.Worktree.DiskBytes
		}
		for _, warning := range cacheWarnings(row.Worktree) {
			result.CacheBytes += warning.Bytes
		}
	}
	return result
}

func cacheWarnings(worktree model.Worktree) []model.CacheWarning {
	if worktree.WorktreeDetails == nil {
		return nil
	}
	return worktree.CacheWarnings
}
