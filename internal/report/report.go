// Package report renders worktree cleanup inventories.
package report

import (
	"encoding/json"
	"fmt"
	"io"
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
		reason := wt.Reason
		if wt.Error != "" {
			reason = strings.TrimSpace(reason + "; " + wt.Error)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			wt.Path,
			emptyDash(wt.Branch),
			wt.Classification,
			dirtyString(wt.Dirty),
			action(wt),
			ByteString(wt.DiskBytes),
			ByteString(wt.ReclaimedBytes),
			reason,
		)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	s := inv.Summary
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Summary:")
	fmt.Fprintf(w, "  roots: %s\n", strings.Join(inv.Roots, ", "))
	fmt.Fprintf(w, "  dry run: %t\n", inv.DryRun)
	fmt.Fprintf(w, "  repositories: %d\n", s.Repositories)
	fmt.Fprintf(w, "  scanned: %d\n", s.Scanned)
	fmt.Fprintf(w, "  safe: %d\n", s.Safe)
	fmt.Fprintf(w, "  removed: %d\n", s.Removed)
	fmt.Fprintf(w, "  skipped: %d\n", s.Skipped)
	fmt.Fprintf(w, "  pruned: %d\n", s.Pruned)
	fmt.Fprintf(w, "  potential reclaimable: %s\n", ByteString(s.PotentialBytes))
	fmt.Fprintf(w, "  reclaimed: %s\n", ByteString(s.ReclaimedBytes))
	fmt.Fprintf(w, "  duration: %s\n", s.Duration)

	if len(inv.Errors) > 0 {
		fmt.Fprintln(w, "  errors:")
		for _, errText := range inv.Errors {
			fmt.Fprintf(w, "    - %s\n", errText)
		}
	}

	return nil
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
