// Package report renders worktree cleanup inventories.
package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/ben-ranford/wtgc/internal/explain"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/review"
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

// WriteExplain renders the versioned single-worktree diagnostic without
// changing inventory or review output contracts.
func WriteExplain(w io.Writer, document explain.Document, format Format) error {
	if format == FormatJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(document)
	}
	if format != "" && format != FormatHuman {
		return fmt.Errorf("unknown report format %q", format)
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "Worktree: %s\nRepository: %s\nHEAD: %s\nClassification: %s\nReason: %s\n", SafeHumanText(document.Worktree.Path), SafeHumanText(document.Worktree.Repository), SafeHumanText(emptyDash(document.Worktree.Head)), document.Worktree.Classification, SafeHumanText(withError(document.Worktree.Reason, document.Worktree.Error)))
	if document.Worktree.RetentionBasis != "" && document.Worktree.ObservedAt != nil && document.Worktree.EligibleAt != nil {
		fmt.Fprintf(&output, "Retention: basis=%s observed=%s eligible=%s remaining=%s\n", SafeHumanText(document.Worktree.RetentionBasis), document.Worktree.ObservedAt.UTC().Format(time.RFC3339), document.Worktree.EligibleAt.UTC().Format(time.RFC3339), document.Worktree.Remaining)
	}
	if document.Worktree.Provider != "" {
		fmt.Fprintf(&output, "Provider proof: %s PR #%d %s\n", SafeHumanText(document.Worktree.Provider), document.Worktree.ProviderPR, SafeHumanText(document.Worktree.ProviderURL))
	}
	fmt.Fprintln(&output, "Checks:")
	for _, check := range document.Checks {
		fmt.Fprintf(&output, "  %s\t%s\t%s\n", check.ID, check.Status, SafeHumanText(check.Detail))
	}
	fmt.Fprintln(&output, "Safe next checks:")
	for _, next := range document.NextChecks {
		fmt.Fprintf(&output, "  - %s\n", SafeHumanText(next))
	}
	if len(document.OperationalErrors) > 0 {
		fmt.Fprintln(&output, "Operational errors:")
		for _, errText := range document.OperationalErrors {
			fmt.Fprintf(&output, "  - %s\n", SafeHumanText(errText))
		}
	}
	if written, err := w.Write(output.Bytes()); err != nil {
		return err
	} else if written != output.Len() {
		return io.ErrShortWrite
	}
	return nil
}

// WriteReview renders the separate review document without changing clean's
// established inventory formats.
func WriteReview(w io.Writer, document review.Document, format Format) error {
	if format == FormatJSON {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(document)
	}
	if format != "" && format != FormatHuman {
		return fmt.Errorf("unknown report format %q", format)
	}
	var output bytes.Buffer
	tw := tabwriter.NewWriter(&output, 0, 0, 2, ' ', 0)
	for _, group := range document.View.Groups {
		fmt.Fprintf(tw, "GROUP\t%s\n", SafeHumanText(group.Key))
		fmt.Fprintln(tw, "SELECTED\tHEAD\tPATH\tCLASSIFICATION\tSIZE\tREASON\tEVIDENCE")
		for _, row := range group.Worktrees {
			writeReviewRow(tw, row)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	writeReviewTotals(&output, document.View.Totals)
	writeExclusionScope(&output, document.Inventory)
	writeReclaimProposal(&output, document.Proposal)
	if len(document.Inventory.Errors) > 0 {
		fmt.Fprintln(&output, "Errors:")
		for _, errText := range document.Inventory.Errors {
			fmt.Fprintf(&output, "  - %s\n", SafeHumanText(errText))
		}
	}
	if written, err := w.Write(output.Bytes()); err != nil {
		return err
	} else if written != output.Len() {
		return io.ErrShortWrite
	}
	return nil
}

func writeReclaimProposal(w io.Writer, proposal *review.Proposal) {
	if proposal == nil {
		return
	}
	fmt.Fprintln(w, "Reclaim proposal (advisory only; not cleanup authorization):")
	fmt.Fprintf(w, "  target: %s\n  target met: %t\n  proposed: %d worktree(s)\n  estimated total: %s\n", ByteString(proposal.TargetBytes), proposal.TargetMet, len(proposal.Candidates), byteStringBig(proposal.EstimatedBytes))
	if proposal.TargetMet {
		fmt.Fprintf(w, "  estimated excess: %s\n", byteStringBig(proposal.ExcessBytes))
	} else {
		fmt.Fprintf(w, "  estimated shortfall: %s\n", byteStringBig(proposal.ShortfallBytes))
	}
	fmt.Fprintln(w, "  candidates:")
	for _, candidate := range proposal.Candidates {
		fmt.Fprintf(w, "    - %s (%s; repository %s)\n", SafeHumanText(candidate.Path), ByteString(candidate.Bytes), SafeHumanText(candidate.Repository))
	}
	fmt.Fprintf(w, "  selection: %s\n  cleanup: %s\n", SafeHumanText(proposal.SelectionRule), SafeHumanText(proposal.Authorization))
	if len(proposal.Uncertainties) > 0 {
		fmt.Fprintln(w, "  uncertainties:")
		for _, uncertainty := range proposal.Uncertainties {
			fmt.Fprintf(w, "    - %s\n", SafeHumanText(uncertainty))
		}
	}
}

func writeReviewRow(w io.Writer, row review.Row) {
	worktree := row.Worktree
	selected := ""
	if row.Selected {
		selected = "selected"
	}
	evidence := reviewEvidence(worktree)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", selected, SafeHumanText(emptyDash(worktree.Head)), SafeHumanText(worktree.Path), worktree.Classification, displayDiskBytes(worktree), SafeHumanText(withError(worktree.Reason, worktree.Error)), SafeHumanText(evidence))
}

func reviewEvidence(worktree model.Worktree) string {
	if worktree.WorktreeDetails == nil {
		return ""
	}
	parts := make([]string, 0, 3+len(worktree.CacheWarnings))
	if worktree.Provider != "" {
		parts = append(parts, fmt.Sprintf("provider=%s PR #%d %s", worktree.Provider, worktree.ProviderPR, worktree.ProviderURL))
	}
	if worktree.RetentionBasis != "" {
		parts = append(parts, fmt.Sprintf("retention=%s observed=%s eligible=%s remaining=%s", worktree.RetentionBasis, worktree.ObservedAt.UTC(), worktree.EligibleAt.UTC(), worktree.Remaining))
	}
	for _, warning := range worktree.CacheWarnings {
		parts = append(parts, fmt.Sprintf("cache=%s %s: %s", ByteString(warning.Bytes), warning.Path, withError(warning.Reason, warning.Error)))
	}
	return strings.Join(parts, "; ")
}

func writeReviewTotals(w io.Writer, totals review.Totals) {
	fmt.Fprintln(w, "Totals:")
	for _, value := range []struct {
		name  string
		total review.Total
	}{{"full", totals.Full}, {"visible", totals.Visible}, {"selected", totals.Selected}} {
		fmt.Fprintf(w, "  %s: count=%d reclaimable=%s cache=%s\n", value.name, value.total.Count, ByteString(value.total.ReclaimableBytes), ByteString(value.total.CacheBytes))
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
	writeExclusionScope(w, inv)
	return nil
}

func writeExclusionScope(w io.Writer, inv model.Inventory) {
	if len(inv.ExcludedPaths) == 0 {
		return
	}
	fmt.Fprintln(w, "Scope limitations:")
	for _, path := range inv.ExcludedPaths {
		fmt.Fprintf(w, "  excluded: %s (working files, size, caches, cleanup, and repository-wide prune skipped)\n", SafeHumanText(path))
	}
}

func writeWorktree(w io.Writer, wt model.Worktree) {
	reason := withError(wt.Reason, wt.Error)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", SafeHumanText(wt.Path), SafeHumanText(emptyDash(wt.Branch)), wt.Classification, dirtyString(wt.Dirty), action(wt), displayDiskBytes(wt), ByteString(wt.ReclaimedBytes), SafeHumanText(reason))
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

func displayDiskBytes(worktree model.Worktree) string {
	if worktree.WorktreeDetails != nil && worktree.Excluded && worktree.DiskBytesMeasured != nil && !*worktree.DiskBytesMeasured {
		return "unmeasured"
	}
	return ByteString(worktree.DiskBytes)
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

func byteStringBig(n *big.Int) string {
	if n == nil {
		return "0 B"
	}
	if n.IsInt64() {
		return ByteString(n.Int64())
	}
	return n.String() + " B"
}
