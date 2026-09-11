// Package inventorydiff compares saved WTGC inventory reports without reading
// the filesystem or consulting Git.
package inventorydiff

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

const (
	SchemaVersion = "1.0.0"
	maxInputBytes = 64 << 20
)

var ErrFileRead = errors.New("inventory file read")

// Snapshot is a validated, self-contained inventory report.
type Snapshot struct {
	Inventory  model.Inventory
	Warnings   []string
	SizeKnown  map[string]bool
	Exclusions []string
}

// Document is the versioned, advisory-only result of comparing two snapshots.
type Document struct {
	SchemaVersion string     `json:"schema_version"`
	Before        Provenance `json:"before"`
	After         Provenance `json:"after"`
	Warnings      []string   `json:"warnings,omitempty"`
	Rows          []Row      `json:"rows"`
	Summary       Summary    `json:"summary"`
}

type Provenance struct {
	SchemaVersion string    `json:"schema_version"`
	GeneratedAt   time.Time `json:"generated_at"`
	DryRun        bool      `json:"dry_run"`
	Roots         []string  `json:"roots"`
	Errors        []string  `json:"errors,omitempty"`
}

type Row struct {
	Repository string          `json:"repository"`
	Path       string          `json:"path"`
	Status     string          `json:"status"`
	Before     *model.Worktree `json:"before,omitempty"`
	After      *model.Worktree `json:"after,omitempty"`
	ByteDelta  *int64          `json:"byte_delta,omitempty"`
	Changes    []string        `json:"changes,omitempty"`
}

type Summary struct {
	Added       int `json:"added"`
	NotObserved int `json:"not_observed"`
	Changed     int `json:"changed"`
	Unchanged   int `json:"unchanged"`
}

// Load reads exactly one JSON document from path. It deliberately does not
// canonicalize the requested path or inspect anything else on disk.
func Load(path string) (Snapshot, error) {
	dir, name := filepath.Split(path)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return Snapshot{}, fmt.Errorf("%w: %q must name a file", ErrFileRead, path)
	}
	if dir == "" {
		dir = "."
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrFileRead, err)
	}
	defer root.Close()
	info, err := root.Lstat(name)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrFileRead, err)
	}
	if !info.Mode().IsRegular() {
		return Snapshot{}, fmt.Errorf("%w: %q is not a regular file", ErrFileRead, path)
	}
	f, err := openInput(root, name)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrFileRead, err)
	}
	defer f.Close()
	info, err = f.Stat()
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: %w", ErrFileRead, err)
	}
	if !info.Mode().IsRegular() {
		return Snapshot{}, fmt.Errorf("%w: %q is not a regular file", ErrFileRead, path)
	}
	return decode(io.LimitReader(f, maxInputBytes+1), path)
}

func decode(r io.Reader, source string) (Snapshot, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return Snapshot{}, fmt.Errorf("%w: read %q: %v", ErrFileRead, source, err)
	}
	if len(data) > maxInputBytes {
		return Snapshot{}, fmt.Errorf("parse %q: input exceeds 64 MiB limit", source)
	}
	raw, err := decodeDocument(data, source)
	if err != nil {
		return Snapshot{}, err
	}
	invRaw, err := inventoryRaw(raw, source)
	if err != nil {
		return Snapshot{}, err
	}
	return snapshotFromRaw(invRaw, source)
}

func decodeDocument(data []byte, source string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw map[string]json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse %q: %v", source, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("parse %q: trailing JSON document", source)
		}
		return nil, fmt.Errorf("parse %q: %v", source, err)
	}
	return raw, nil
}

func inventoryRaw(raw map[string]json.RawMessage, source string) (map[string]json.RawMessage, error) {
	invRaw := raw
	if reviewRaw, ok := raw["review_schema_version"]; ok {
		var version string
		if err := json.Unmarshal(reviewRaw, &version); err != nil || version != "1.0.0" {
			return nil, fmt.Errorf("parse %q: unsupported review schema", source)
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw["inventory"], &nested); err != nil {
			return nil, fmt.Errorf("parse %q: review inventory is required", source)
		}
		invRaw = nested
	}
	for _, key := range []string{"schema_version", "generated_at", "dry_run", "roots", "worktrees", "summary"} {
		if value, ok := invRaw[key]; !ok || isNull(value) {
			return nil, fmt.Errorf("parse %q: inventory %q is required", source, key)
		}
	}
	return invRaw, nil
}

func snapshotFromRaw(invRaw map[string]json.RawMessage, source string) (Snapshot, error) {
	encodedInventory, err := json.Marshal(invRaw)
	if err != nil {
		return Snapshot{}, fmt.Errorf("parse %q: encode inventory: %w", source, err)
	}
	var inv model.Inventory
	if err := json.Unmarshal(encodedInventory, &inv); err != nil {
		return Snapshot{}, fmt.Errorf("parse %q: inventory: %v", source, err)
	}
	if inv.SchemaVersion != "1.1.0" {
		return Snapshot{}, fmt.Errorf("parse %q: unsupported inventory schema %q", source, inv.SchemaVersion)
	}
	if inv.GeneratedAt.IsZero() {
		return Snapshot{}, fmt.Errorf("parse %q: generated_at is required", source)
	}
	if value, ok := invRaw["errors"]; ok && isNull(value) {
		return Snapshot{}, fmt.Errorf("parse %q: errors must not be null", source)
	}
	if value, ok := invRaw["errors"]; ok {
		var errors []json.RawMessage
		if err := json.Unmarshal(value, &errors); err != nil {
			return Snapshot{}, fmt.Errorf("parse %q: errors: %v", source, err)
		}
		for _, item := range errors {
			if isNull(item) {
				return Snapshot{}, fmt.Errorf("parse %q: errors must not contain null", source)
			}
		}
	}
	if _, err := stringArray(invRaw["roots"], "roots", source); err != nil {
		return Snapshot{}, err
	}
	var rawRows []map[string]json.RawMessage
	if err := json.Unmarshal(invRaw["worktrees"], &rawRows); err != nil {
		return Snapshot{}, fmt.Errorf("parse %q: worktrees: %v", source, err)
	}
	sizeKnown, err := validateRows(inv.Worktrees, rawRows, source)
	if err != nil {
		return Snapshot{}, err
	}
	if err := validateSummary(invRaw["summary"], inv.Summary, source); err != nil {
		return Snapshot{}, err
	}
	exclusions, err := stringArray(invRaw["excluded_paths"], "excluded_paths", source)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Inventory: inv, Warnings: metadataWarnings(invRaw, inv.Roots), SizeKnown: sizeKnown, Exclusions: exclusions}, nil
}

func validateRows(rows []model.Worktree, rawRows []map[string]json.RawMessage, source string) (map[string]bool, error) {
	seen := make(map[string]struct{}, len(rows))
	sizeKnown := make(map[string]bool, len(rows))
	for i, item := range rows {
		if err := validateRow(item, rawRows[i], source, i); err != nil {
			return nil, err
		}
		key := item.Repository + "\x00" + item.Path
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("parse %q: duplicate repository/path identity", source)
		}
		seen[key] = struct{}{}
		measured := !item.Prunable && (item.Error == "" || item.DiskBytes > 0)
		if item.WorktreeDetails != nil && item.DiskBytesMeasured != nil {
			measured = *item.DiskBytesMeasured
		}
		sizeKnown[key] = measured
	}
	return sizeKnown, nil
}

func validateRow(item model.Worktree, raw map[string]json.RawMessage, source string, index int) error {
	for _, field := range []string{"repository", "path", "disk_bytes", "classification", "reason", "action", "reclaimed_bytes"} {
		if value, ok := raw[field]; !ok || isNull(value) {
			return fmt.Errorf("parse %q: worktree %d %q is required", source, index, field)
		}
	}
	if item.Repository == "" || item.Path == "" || strings.ContainsRune(item.Repository, 0) || strings.ContainsRune(item.Path, 0) || item.Classification == "" || item.Reason == "" {
		return fmt.Errorf("parse %q: worktree %d has a missing identity or decision", source, index)
	}
	if item.DiskBytes < 0 || item.ReclaimedBytes < 0 {
		return fmt.Errorf("parse %q: worktree %d has negative bytes", source, index)
	}
	if !knownClassification(item.Classification) {
		return fmt.Errorf("parse %q: worktree %d has unsupported classification %q", source, index, item.Classification)
	}
	if !knownAction(item.Action) {
		return fmt.Errorf("parse %q: worktree %d has unsupported action %q", source, index, item.Action)
	}
	return validateOptionalRowFields(raw, source, index)
}

func validateOptionalRowFields(raw map[string]json.RawMessage, source string, index int) error {
	for _, field := range []string{"branch", "head", "default_branch", "primary", "detached", "locked", "prunable", "dirty", "removed", "branch_deleted", "error", "retention_basis", "observed_at", "eligible_at", "retention_remaining_ns", "disk_bytes_measured", "excluded", "cache_warnings", "provider", "provider_pr", "provider_url", "merged_at"} {
		if value, ok := raw[field]; ok && isNull(value) {
			return fmt.Errorf("parse %q: worktree %d %q must not be null", source, index, field)
		}
	}
	if value, ok := raw["provider_pr"]; ok {
		var providerPR int
		if err := json.Unmarshal(value, &providerPR); err == nil && providerPR < 1 {
			return fmt.Errorf("parse %q: worktree %d provider_pr must be at least 1", source, index)
		}
	}
	return validateCacheWarnings(raw, source, index)
}

func validateCacheWarnings(raw map[string]json.RawMessage, source string, index int) error {
	value, ok := raw["cache_warnings"]
	if !ok {
		return nil
	}
	var warnings []map[string]json.RawMessage
	if err := json.Unmarshal(value, &warnings); err != nil {
		return fmt.Errorf("parse %q: worktree %d cache_warnings: %v", source, index, err)
	}
	for warningIndex, warning := range warnings {
		for _, field := range []string{"path", "bytes", "kind", "reason"} {
			if fieldValue, ok := warning[field]; !ok || isNull(fieldValue) {
				return fmt.Errorf("parse %q: worktree %d cache warning %d %q is required", source, index, warningIndex, field)
			}
		}
		if fieldValue, ok := warning["error"]; ok && isNull(fieldValue) {
			return fmt.Errorf("parse %q: worktree %d cache warning %d error must not be null", source, index, warningIndex)
		}
		var bytes int64
		if err := json.Unmarshal(warning["bytes"], &bytes); err == nil && bytes < 0 {
			return fmt.Errorf("parse %q: worktree %d cache warning %d bytes must not be negative", source, index, warningIndex)
		}
	}
	return nil
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func stringArray(raw json.RawMessage, field, source string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if isNull(raw) {
		return nil, fmt.Errorf("parse %q: %s must not be null", source, field)
	}
	var values []string
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, fmt.Errorf("parse %q: %s: %v", source, field, err)
	}
	for _, value := range values {
		if value == "" || strings.ContainsRune(value, 0) {
			return nil, fmt.Errorf("parse %q: %s has invalid path", source, field)
		}
	}
	return values, nil
}

func validateSummary(raw json.RawMessage, summary model.Summary, source string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("parse %q: summary: %v", source, err)
	}
	for _, name := range []string{"repositories", "scanned", "safe", "removed", "skipped", "pruned", "potential_bytes", "reclaimed_bytes", "duration_ns"} {
		if value, ok := fields[name]; !ok || isNull(value) {
			return fmt.Errorf("parse %q: summary %q is required", source, name)
		}
	}
	for _, name := range []string{"cache_warning_count", "cache_warning_bytes"} {
		if value, ok := fields[name]; ok && isNull(value) {
			return fmt.Errorf("parse %q: summary %q must not be null", source, name)
		}
	}
	if summary.Repositories < 0 || summary.Scanned < 0 || summary.Safe < 0 || summary.Removed < 0 || summary.Skipped < 0 || summary.Pruned < 0 || summary.PotentialBytes < 0 || summary.ReclaimedBytes < 0 || summary.Duration < 0 || summary.CacheWarningCount < 0 || summary.CacheWarningBytes < 0 {
		return fmt.Errorf("parse %q: summary has negative numeric field", source)
	}
	return nil
}

func knownClassification(value model.Classification) bool {
	switch value {
	case model.SafeToRemove, model.MergedButDirty, model.Unmerged, model.Prunable, model.Kept, model.Error:
		return true
	default:
		return false
	}
}

func knownAction(value model.Action) bool {
	switch value {
	case model.ActionKept, model.ActionWouldRemove, model.ActionWouldPrune, model.ActionRemoved, model.ActionPruned, model.ActionRemovedBranchDeleted:
		return true
	default:
		return false
	}
}

func metadataWarnings(raw map[string]json.RawMessage, roots []string) []string {
	var warnings []string
	for _, key := range []string{"host", "retention_policy", "provider_policy"} {
		warnings = append(warnings, "snapshot schema does not record "+strings.ReplaceAll(key, "_", " "))
	}
	if len(roots) == 0 {
		warnings = append(warnings, "snapshot records an empty scan scope")
	}
	if value, ok := raw["excluded_paths"]; !ok || isNull(value) {
		warnings = append(warnings, "snapshot does not record exclusion settings")
	}
	return warnings
}

// Compare matches literal repository/path identities. It never treats an
// absent row as deletion or reclaimed disk space.
func Compare(before, after Snapshot) Document {
	doc := Document{SchemaVersion: SchemaVersion, Before: provenance(before.Inventory), After: provenance(after.Inventory), Rows: make([]Row, 0)}
	doc.Warnings = comparisonWarnings(before, after)
	oldRows := index(before.Inventory.Worktrees)
	newRows := index(after.Inventory.Worktrees)
	for _, key := range comparisonKeys(oldRows, newRows) {
		row := compareRow(key, oldRows, newRows, before.SizeKnown, after.SizeKnown)
		increment(&doc.Summary, row.Status)
		doc.Rows = append(doc.Rows, row)
	}
	return doc
}

func comparisonWarnings(before, after Snapshot) []string {
	warnings := append(append([]string(nil), before.Warnings...), after.Warnings...)
	if !sameStrings(before.Inventory.Roots, after.Inventory.Roots) {
		warnings = append(warnings, "snapshot roots differ; scopes may not be comparable")
	}
	if !sameStrings(before.Exclusions, after.Exclusions) {
		warnings = append(warnings, "snapshot exclusion settings differ; scopes may not be comparable")
	}
	if len(before.Inventory.Errors) > 0 || len(after.Inventory.Errors) > 0 {
		warnings = append(warnings, "one or both snapshots report partial-scan errors")
	}
	if after.Inventory.GeneratedAt.Before(before.Inventory.GeneratedAt) {
		warnings = append(warnings, "after timestamp precedes before timestamp")
	}
	if before.Inventory.DryRun != after.Inventory.DryRun {
		warnings = append(warnings, "snapshot dry-run modes differ; recorded actions may not be comparable")
	}
	return warnings
}

func comparisonKeys(oldRows, newRows map[string]model.Worktree) []string {
	keys := make([]string, 0, len(oldRows)+len(newRows))
	for key := range oldRows {
		keys = append(keys, key)
	}
	for key := range newRows {
		if _, ok := oldRows[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func compareRow(key string, oldRows, newRows map[string]model.Worktree, oldSizes, newSizes map[string]bool) Row {
	old, oldOK := oldRows[key]
	next, nextOK := newRows[key]
	row := Row{Repository: value(old, next, oldOK, func(w model.Worktree) string { return w.Repository }), Path: value(old, next, oldOK, func(w model.Worktree) string { return w.Path })}
	if !oldOK {
		row.Status, row.After = "added", &next
		return row
	}
	if !nextOK {
		row.Status, row.Before = "not_observed_in_after", &old
		return row
	}
	row.Before, row.After = &old, &next
	row.Changes, row.ByteDelta = changes(old, next, oldSizes[key], newSizes[key])
	if len(row.Changes) == 0 {
		row.Status = "unchanged"
	} else {
		row.Status = "changed"
	}
	return row
}

func increment(summary *Summary, status string) {
	switch status {
	case "added":
		summary.Added++
	case "not_observed_in_after":
		summary.NotObserved++
	case "changed":
		summary.Changed++
	case "unchanged":
		summary.Unchanged++
	}
}

func provenance(inv model.Inventory) Provenance {
	return Provenance{SchemaVersion: inv.SchemaVersion, GeneratedAt: inv.GeneratedAt, DryRun: inv.DryRun, Roots: append([]string{}, inv.Roots...), Errors: append([]string(nil), inv.Errors...)}
}
func index(rows []model.Worktree) map[string]model.Worktree {
	result := make(map[string]model.Worktree, len(rows))
	for _, row := range rows {
		result[row.Repository+"\x00"+row.Path] = row
	}
	return result
}
func value(a, b model.Worktree, aOK bool, get func(model.Worktree) string) string {
	if aOK {
		return get(a)
	}
	return get(b)
}
func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func changes(before, after model.Worktree, beforeKnown, afterKnown bool) ([]string, *int64) {
	var result []string
	if before.Head != after.Head {
		result = append(result, "head")
	}
	if before.Branch != after.Branch {
		result = append(result, "branch")
	}
	if before.Classification != after.Classification {
		result = append(result, "classification")
	}
	if before.Reason != after.Reason {
		result = append(result, "reason")
	}
	if before.Error != after.Error {
		result = append(result, "error")
	}
	if before.Action != after.Action {
		result = append(result, "action")
	}
	if before.Removed != after.Removed {
		result = append(result, "removed")
	}
	if before.BranchDeleted != after.BranchDeleted {
		result = append(result, "branch_deleted")
	}
	if before.ReclaimedBytes != after.ReclaimedBytes {
		result = append(result, "reclaimed_bytes")
	}
	if beforeKnown && afterKnown {
		delta := after.DiskBytes - before.DiskBytes
		if delta != 0 {
			result = append(result, "disk_bytes")
		}
		return result, &delta
	}
	if beforeKnown != afterKnown || before.DiskBytes != after.DiskBytes {
		result = append(result, "disk_bytes_unknown")
	}
	return result, nil
}

// Write renders a diff report without allowing terminal control bytes through
// its human-readable output.
func Write(w io.Writer, doc Document, jsonOutput bool) error {
	if jsonOutput {
		var encoded bytes.Buffer
		enc := json.NewEncoder(&encoded)
		enc.SetIndent("", "  ")
		if err := enc.Encode(doc); err != nil {
			return err
		}
		return writeComplete(w, encoded.String())
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Inventory diff: %s -> %s\n", doc.Before.GeneratedAt.UTC().Format(time.RFC3339), doc.After.GeneratedAt.UTC().Format(time.RFC3339))
	fmt.Fprintf(&b, "Before dry-run: %t\nAfter dry-run: %t\n", doc.Before.DryRun, doc.After.DryRun)
	fmt.Fprintf(&b, "Before roots: %s\nAfter roots: %s\n", safe(strings.Join(doc.Before.Roots, ", ")), safe(strings.Join(doc.After.Roots, ", ")))
	for _, warning := range doc.Warnings {
		fmt.Fprintf(&b, "WARNING: %s\n", safe(warning))
	}
	writeErrors(&b, "Before errors", doc.Before.Errors)
	writeErrors(&b, "After errors", doc.After.Errors)
	for _, row := range doc.Rows {
		fmt.Fprintf(&b, "%s\t%s\t%s", row.Status, safe(row.Repository), safe(row.Path))
		if row.ByteDelta != nil {
			fmt.Fprintf(&b, "\t%s bytes", strconv.FormatInt(*row.ByteDelta, 10))
		}
		if len(row.Changes) > 0 {
			fmt.Fprintf(&b, "\t%s", strings.Join(row.Changes, ","))
		}
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "Summary: added=%d not_observed=%d changed=%d unchanged=%d\n", doc.Summary.Added, doc.Summary.NotObserved, doc.Summary.Changed, doc.Summary.Unchanged)
	return writeComplete(w, b.String())
}

func writeErrors(w *strings.Builder, label string, errors []string) {
	if len(errors) == 0 {
		return
	}
	fmt.Fprintf(w, "%s:\n", label)
	for _, value := range errors {
		fmt.Fprintf(w, "  - %s\n", safe(value))
	}
}

func writeComplete(w io.Writer, value string) error {
	written, err := io.WriteString(w, value)
	if err != nil {
		return err
	}
	if written != len(value) {
		return io.ErrShortWrite
	}
	return nil
}
func safe(s string) string { q := strconv.QuoteToGraphic(s); return q[1 : len(q)-1] }
