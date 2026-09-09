package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

func TestV11MetadataIsSchemaCompatibleAndNilDetailsStayAbsent(t *testing.T) {
	now := time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC)
	eligible := now.Add(time.Hour)
	clean := false
	inv := model.Inventory{
		SchemaVersion: "1.1.0",
		GeneratedAt:   now,
		DryRun:        true,
		Roots:         []string{"/tmp/root"},
		Worktrees: []model.Worktree{
			{
				Path:           "/tmp/feature",
				Repository:     "/tmp/repo",
				DiskBytes:      99,
				Classification: model.Kept,
				Dirty:          &clean,
				Reason:         "retained",
				Action:         model.ActionKept,
				WorktreeDetails: &model.WorktreeDetails{
					RetentionBasis: "provider_merged_at",
					ObservedAt:     &now,
					EligibleAt:     &eligible,
					Remaining:      time.Hour,
					Provider:       "github",
					ProviderPR:     42,
					ProviderURL:    "https://github.example/pr/42",
					MergedAt:       &now,
					CacheWarnings: []model.CacheWarning{{
						Path:   "/tmp/cache",
						Bytes:  3072,
						Kind:   "build output",
						Reason: "large cache",
					}},
				},
			},
			{
				Path:           "/tmp/no-details",
				Repository:     "/tmp/repo",
				Classification: model.Kept,
				Reason:         "ordinary row",
				Action:         model.ActionKept,
			},
		},
		Summary: model.Summary{Repositories: 1, Scanned: 2, Skipped: 2, PotentialBytes: 99, ReclaimedBytes: 7, CacheWarningCount: 1, CacheWarningBytes: 3072},
	}

	var output bytes.Buffer
	if err := Write(&output, inv, FormatJSON); err != nil {
		t.Fatalf("Write(JSON) error = %v", err)
	}

	var document map[string]any
	if err := json.Unmarshal(output.Bytes(), &document); err != nil {
		t.Fatalf("decode inventory JSON: %v", err)
	}
	assertDocumentMatchesPublishedSchemaProperties(t, document)

	worktrees := document["worktrees"].([]any)
	details := worktrees[0].(map[string]any)
	if details["provider"] != "github" || details["provider_pr"] != float64(42) || details["retention_basis"] != "provider_merged_at" {
		t.Fatalf("provider/retention metadata = %#v", details)
	}
	if warnings, ok := details["cache_warnings"].([]any); !ok || len(warnings) != 1 || warnings[0].(map[string]any)["bytes"] != float64(3072) {
		t.Fatalf("cache warnings = %#v", details["cache_warnings"])
	}
	nilDetails := worktrees[1].(map[string]any)
	for _, field := range []string{"provider", "provider_pr", "provider_url", "merged_at", "retention_basis", "cache_warnings"} {
		if _, ok := nilDetails[field]; ok {
			t.Fatalf("nil WorktreeDetails emitted %q: %#v", field, nilDetails)
		}
	}
	summary := document["summary"].(map[string]any)
	if summary["cache_warning_count"] != float64(1) || summary["cache_warning_bytes"] != float64(3072) || summary["reclaimed_bytes"] != float64(7) {
		t.Fatalf("advisory and reclaim summary values mixed: %#v", summary)
	}
}

func TestV11HumanMetadataEscapesAttackerControlledText(t *testing.T) {
	now := time.Date(2026, 9, 9, 1, 2, 3, 0, time.UTC)
	inv := model.Inventory{
		SchemaVersion: "1.1.0",
		GeneratedAt:   now,
		DryRun:        true,
		Worktrees: []model.Worktree{{
			Path:           "/tmp/feature",
			Repository:     "/tmp/repo",
			Classification: model.Kept,
			Reason:         "kept",
			Action:         model.ActionKept,
			WorktreeDetails: &model.WorktreeDetails{
				Provider:       "github",
				ProviderPR:     7,
				ProviderURL:    "https://example.invalid/\x1b[2J\nFORGED-PR",
				RetentionBasis: "worktree_mtime",
				ObservedAt:     &now,
				EligibleAt:     &now,
				CacheWarnings: []model.CacheWarning{{
					Path:   "/tmp/cache\x1b[2J\nFORGED-CACHE",
					Bytes:  1024,
					Kind:   "build\routput",
					Reason: "skip verification\nFORGED-REASON",
					Error:  "error\aFORGED-ERROR",
				}},
			},
		}},
		Summary: model.Summary{Repositories: 1, Scanned: 1, Skipped: 1, CacheWarningCount: 1, CacheWarningBytes: 1024},
	}

	var output bytes.Buffer
	if err := Write(&output, inv, FormatHuman); err != nil {
		t.Fatalf("Write(Human) error = %v", err)
	}
	got := output.String()
	for _, raw := range []string{
		"\x1b",
		"\r",
		"\a",
		"/tmp/cache\x1b[2J\nFORGED-CACHE",
		"https://example.invalid/\x1b[2J\nFORGED-PR",
		"skip verification\nFORGED-REASON",
	} {
		if strings.Contains(got, raw) {
			t.Fatalf("human output contains raw attacker text %q:\n%s", raw, got)
		}
	}
	for _, escaped := range []string{`\x1b`, `\nFORGED-CACHE`, `\aFORGED-ERROR`, `\nFORGED-PR`, `cache warnings: 1 (1.0 KiB)`} {
		if !strings.Contains(got, escaped) {
			t.Fatalf("human output missing escaped metadata %q:\n%s", escaped, got)
		}
	}
}

type inventorySchemaWorktreeDefinition struct {
	Properties map[string]json.RawMessage `json:"properties"`
}

type inventorySchemaDefinitions struct {
	Worktree inventorySchemaWorktreeDefinition `json:"worktree"`
}

type inventorySchemaDocument struct {
	Properties  map[string]json.RawMessage `json:"properties"`
	Definitions inventorySchemaDefinitions `json:"$defs"`
}

func assertDocumentMatchesPublishedSchemaProperties(t *testing.T, document map[string]any) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve report test source path")
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "docs", "inventory.schema.json"))
	if err != nil {
		t.Fatalf("read inventory schema: %v", err)
	}
	var schema inventorySchemaDocument
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse inventory schema: %v", err)
	}
	for field := range document {
		if _, ok := schema.Properties[field]; !ok {
			t.Fatalf("JSON emitted top-level field %q absent from schema", field)
		}
	}
	for _, raw := range document["worktrees"].([]any) {
		for field := range raw.(map[string]any) {
			if _, ok := schema.Definitions.Worktree.Properties[field]; !ok {
				t.Fatalf("JSON emitted worktree field %q absent from schema", field)
			}
		}
	}
}
