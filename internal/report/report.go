// Package report renders worktree cleanup inventories.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/ben-ranford/wtgc/internal/model"
)

type Format string

const (
	FormatHuman Format = "human"
	FormatJSON  Format = "json"
)

// Write renders inv in the requested format.
func Write(w io.Writer, inv model.Inventory, format Format) error {
	switch format {
	case "", FormatHuman:
		return writeHuman(w, inv)
	case FormatJSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(inv)
	default:
		return fmt.Errorf("unknown report format %q", format)
	}
}

func writeHuman(w io.Writer, inv model.Inventory) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "PATH\tBRANCH\tCLASSIFICATION\tDIRTY\tACTION\tSIZE\tRECLAIMED\tREASON")
	for _, wt := range inv.Worktrees {
		writeWorktree(tw, wt)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	writeSummary(w, inv)
	return nil
}

func writeWorktree(w io.Writer, wt model.Worktree) {
	reason := withError(wt.Reason, wt.Error)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", SafeHumanText(wt.Path), SafeHumanText(emptyDash(wt.Branch)), wt.Classification, dirtyString(wt.Dirty), action(wt), ByteString(wt.DiskBytes), ByteString(wt.ReclaimedBytes), SafeHumanText(reason))
	if wt.WorktreeDetails != nil {
		writeDetails(w, wt)
	}
}

func writeDetails(w io.Writer, wt model.Worktree) {
	for _, warning := range wt.CacheWarnings {
		fmt.Fprintf(w, "  cache warning\t-\t-\t-\t-\t%s\t-\t%s: %s\n", ByteString(warning.Bytes), SafeHumanText(warning.Path), SafeHumanText(withSuffix(warning.Reason, warning.Error, ": ")))
	}
	if wt.Provider != "" {
		fmt.Fprintf(w, "  provider\t-\t-\t-\t-\t-\t-\t%s PR #%d %s\n", SafeHumanText(wt.Provider), wt.ProviderPR, SafeHumanText(wt.ProviderURL))
	}
	if wt.RetentionBasis != "" {
		fmt.Fprintf(w, "  retention\t-\t-\t-\t-\t-\t-\t%s observed=%s eligible=%s remaining=%s\n", SafeHumanText(wt.RetentionBasis), wt.ObservedAt.UTC(), wt.EligibleAt.UTC(), wt.Remaining)
	}
}

func writeSummary(w io.Writer, inv model.Inventory) {
	s := inv.Summary
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Summary:")
	fmt.Fprintf(w, "  roots: %s\n", strings.Join(safeRoots(inv.Roots), ", "))
	fmt.Fprintf(w, "  dry run: %t\n  repositories: %d\n  scanned: %d\n  safe: %d\n  removed: %d\n  skipped: %d\n  pruned: %d\n", inv.DryRun, s.Repositories, s.Scanned, s.Safe, s.Removed, s.Skipped, s.Pruned)
	fmt.Fprintf(w, "  potential reclaimable: %s\n  reclaimed: %s\n  cache warnings: %d (%s)\n  duration: %s\n", ByteString(s.PotentialBytes), ByteString(s.ReclaimedBytes), s.CacheWarningCount, ByteString(s.CacheWarningBytes), s.Duration)
	if len(inv.Errors) > 0 {
		fmt.Fprintln(w, "  errors:")
		for _, errText := range inv.Errors {
			fmt.Fprintf(w, "    - %s\n", SafeHumanText(errText))
		}
	}
}

func safeRoots(roots []string) []string {
	values := make([]string, len(roots))
	for i, root := range roots {
		values[i] = SafeHumanText(root)
	}
	return values
}

func withError(value, err string) string { return withSuffix(value, err, "; ") }
func withSuffix(value, suffix, separator string) string {
	if suffix == "" {
		return value
	}
	return strings.TrimSpace(value + separator + suffix)
}

// SafeHumanText escapes control characters before untrusted text is displayed
// in a terminal or human-readable log.
func SafeHumanText(value string) string {
	quoted := strconv.QuoteToGraphic(value)
	return quoted[1 : len(quoted)-1]
}

func action(worktree model.Worktree) string {
	if worktree.Action != "" {
		return string(worktree.Action)
	}
	if worktree.Removed {
		if worktree.Prunable {
			return string(model.ActionPruned)
		}
		if worktree.BranchDeleted {
			return string(model.ActionRemovedBranchDeleted)
		}
		return string(model.ActionRemoved)
	}
	if worktree.Classification == model.SafeToRemove {
		return string(model.ActionWouldRemove)
	}
	if worktree.Classification == model.Prunable {
		return string(model.ActionWouldPrune)
	}
	return string(model.ActionKept)
}

func dirtyString(value *bool) string {
	if value == nil {
		return "unknown"
	}
	if *value {
		return "dirty"
	}
	return "clean"
}

func emptyDash(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// ByteString formats bytes with binary units for readable CLI output.
func ByteString(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}

	value := float64(n)
	for _, unit := range []string{"KiB", "MiB", "GiB", "TiB", "PiB"} {
		value /= 1024
		if value < 1024 {
			return fmt.Sprintf("%.1f %s", value, unit)
		}
	}
	return fmt.Sprintf("%.1f EiB", value/1024)
}
