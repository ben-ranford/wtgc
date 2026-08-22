// Package model defines the stable inventory and reporting contract.
package model

import (
	"encoding/json"
	"time"
)

// Repository identifies a repository by its shared Git directory and primary
// working tree. CommonDir is the stable deduplication key across linked
// worktrees.
type Repository struct {
	CommonDir   string
	PrimaryPath string
}

// RegisteredWorktree is the state reported by `git worktree list --porcelain`.
// It is intentionally separate from Worktree, which contains WTGC's decision.
type RegisteredWorktree struct {
	Path     string
	Head     string
	Branch   string
	Primary  bool
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
}

// Classification is a conservative cleanup decision for a registered worktree.
type Classification string

const (
	SafeToRemove   Classification = "safe_to_remove"
	MergedButDirty Classification = "merged_but_dirty"
	Unmerged       Classification = "unmerged"
	Prunable       Classification = "stale_orphaned"
	Kept           Classification = "kept"
	Error          Classification = "error"
)

// Action is the explicit cleanup action taken or planned for a worktree.
type Action string

const (
	ActionKept                 Action = "kept"
	ActionWouldRemove          Action = "would_remove"
	ActionWouldPrune           Action = "would_prune"
	ActionRemoved              Action = "removed"
	ActionPruned               Action = "pruned"
	ActionRemovedBranchDeleted Action = "removed_branch_deleted"
)

// Worktree is one registered Git worktree and its cleanup decision.
type Worktree struct {
	Path           string         `json:"path"`
	Branch         string         `json:"branch,omitempty"`
	Head           string         `json:"head,omitempty"`
	Repository     string         `json:"repository"`
	DefaultBranch  string         `json:"default_branch,omitempty"`
	DiskBytes      int64          `json:"disk_bytes"`
	Classification Classification `json:"classification"`
	Reason         string         `json:"reason"`
	Primary        bool           `json:"primary,omitempty"`
	Detached       bool           `json:"detached,omitempty"`
	Locked         bool           `json:"locked,omitempty"`
	Prunable       bool           `json:"prunable,omitempty"`
	Dirty          *bool          `json:"dirty,omitempty"`
	Action         Action         `json:"action"`
	ReclaimedBytes int64          `json:"reclaimed_bytes"`
	Removed        bool           `json:"removed,omitempty"`
	BranchDeleted  bool           `json:"branch_deleted,omitempty"`
	Error          string         `json:"error,omitempty"`
}

// Summary contains aggregate scan and cleanup results.
type Summary struct {
	Repositories   int           `json:"repositories"`
	Scanned        int           `json:"scanned"`
	Safe           int           `json:"safe"`
	Removed        int           `json:"removed"`
	Skipped        int           `json:"skipped"`
	Pruned         int           `json:"pruned"`
	PotentialBytes int64         `json:"potential_bytes"`
	ReclaimedBytes int64         `json:"reclaimed_bytes"`
	Duration       time.Duration `json:"duration_ns"`
}

// Inventory is the machine-readable output document.
type Inventory struct {
	SchemaVersion string     `json:"schema_version"`
	GeneratedAt   time.Time  `json:"generated_at"`
	DryRun        bool       `json:"dry_run"`
	Roots         []string   `json:"roots"`
	Worktrees     []Worktree `json:"worktrees"`
	Summary       Summary    `json:"summary"`
	Errors        []string   `json:"errors,omitempty"`
}

// MarshalJSON keeps the published contract stable: an empty inventory reports
// worktrees as [] rather than null.
func (inv Inventory) MarshalJSON() ([]byte, error) {
	type inventoryAlias Inventory
	if inv.Worktrees == nil {
		inv.Worktrees = []Worktree{}
	}
	return json.Marshal(inventoryAlias(inv))
}
