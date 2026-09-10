// Package review builds read-only inventory views for the review command.
package review

import (
	"fmt"
	"sort"

	"github.com/ben-ranford/wtgc/internal/model"
)

const SchemaVersion = "1.0.0"

type Options struct {
	Repositories    []string
	Classifications []string
	GroupBy         string
	SortBy          string
	SelectedPaths   []string
}

// Document keeps the complete scan inventory separate from the projected view.
type Document struct {
	ReviewSchemaVersion string          `json:"review_schema_version"`
	Inventory           model.Inventory `json:"inventory"`
	View                View            `json:"view"`
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
	selected, err := resolveSelections(inv.Worktrees, opts.SelectedPaths)
	if err != nil {
		return Document{}, err
	}
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
		if matches(worktree, opts) || hasError(worktree) {
			visible = append(visible, Row{Worktree: worktree, Selected: selected[worktree.Path]})
		}
	}
	sortRows(visible, doc.View.SortBy)
	doc.View.Totals.Visible = totalRows(visible)
	doc.View.Groups = groupRows(visible, doc.View.GroupBy)
	return doc, nil
}

func resolveSelections(worktrees []model.Worktree, paths []string) (map[string]bool, error) {
	selected := make(map[string]bool, len(paths))
	counts := make(map[string]int, len(worktrees))
	for _, worktree := range worktrees {
		counts[worktree.Path]++
	}
	for _, path := range paths {
		if selected[path] {
			return nil, fmt.Errorf("duplicate advisory selection %q", path)
		}
		switch counts[path] {
		case 0:
			return nil, fmt.Errorf("unknown advisory selection %q", path)
		case 1:
			selected[path] = true
		default:
			return nil, fmt.Errorf("ambiguous advisory selection %q", path)
		}
	}
	return selected, nil
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
