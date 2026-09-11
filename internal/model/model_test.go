package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type publishedSchemaClassification struct {
	Enum []Classification `json:"enum"`
}

type publishedSchemaVersion struct {
	Const string `json:"const"`
}

type publishedSchemaProperty struct {
	Type  string                   `json:"type"`
	Items *publishedSchemaProperty `json:"items"`
}

type publishedSchemaWorktree struct {
	AdditionalProperties bool                               `json:"additionalProperties"`
	Properties           map[string]publishedSchemaProperty `json:"properties"`
}

func TestPublishedSchemaMatchesModelContract(t *testing.T) {
	t.Parallel()
	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	schemaPath := filepath.Join(filepath.Dir(sourceFile), "..", "..", "docs", "inventory.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read inventory schema: %v", err)
	}
	var schema struct {
		AdditionalProperties bool `json:"additionalProperties"`
		Properties           struct {
			SchemaVersion publishedSchemaVersion  `json:"schema_version"`
			ExcludedPaths publishedSchemaProperty `json:"excluded_paths"`
		} `json:"properties"`
		Definitions struct {
			Classification publishedSchemaClassification `json:"classification"`
			Worktree       publishedSchemaWorktree       `json:"worktree"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse inventory schema: %v", err)
	}
	if schema.Properties.SchemaVersion.Const != "1.1.0" {
		t.Fatalf("schema version = %q, want 1.1.0", schema.Properties.SchemaVersion.Const)
	}
	if schema.Properties.ExcludedPaths.Type != "array" || schema.Properties.ExcludedPaths.Items == nil || schema.Properties.ExcludedPaths.Items.Type != "string" {
		t.Fatalf("excluded_paths schema = %+v, want string array", schema.Properties.ExcludedPaths)
	}
	if schema.AdditionalProperties || schema.Definitions.Worktree.AdditionalProperties || schema.Definitions.Worktree.Properties["excluded"].Type != "boolean" || schema.Definitions.Worktree.Properties["disk_bytes_measured"].Type != "boolean" {
		t.Fatalf("worktree exclusion schema = %+v", schema.Definitions.Worktree)
	}
	want := map[Classification]bool{
		SafeToRemove: true, MergedButDirty: true, Unmerged: true,
		Prunable: true, Kept: true, Error: true,
	}
	for _, value := range schema.Definitions.Classification.Enum {
		delete(want, value)
	}
	if len(want) != 0 {
		t.Fatalf("schema is missing classifications: %v", want)
	}
}

func TestInventoryJSONSerializesEmptyWorktreesAsArray(t *testing.T) {
	t.Parallel()
	data, err := json.Marshal(Inventory{})
	if err != nil {
		t.Fatalf("json.Marshal error = %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("json.Unmarshal error = %v", err)
	}
	if string(got["worktrees"]) != "[]" {
		t.Fatalf("worktrees JSON = %s, want [] in %s", got["worktrees"], data)
	}
	if _, ok := got["excluded_paths"]; ok {
		t.Fatalf("default inventory unexpectedly emits excluded_paths: %s", data)
	}
}

func TestOptionalWorktreeDetailsRemainFlatAndAbsentWhenUnused(t *testing.T) {
	plain, err := json.Marshal(Worktree{Path: "p", Repository: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "WorktreeDetails") || strings.Contains(string(plain), "cache_warnings") || strings.Contains(string(plain), "excluded") || strings.Contains(string(plain), "disk_bytes_measured") {
		t.Fatalf("plain=%s", plain)
	}
	item := Worktree{Path: "p", Repository: "r"}
	item.Details().Provider = "github"
	item.ProviderPR = 1
	data, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"provider":"github"`) || strings.Contains(string(data), "worktree_details") {
		t.Fatalf("details JSON=%s", data)
	}
}

func TestExcludedInventoryJSONMatchesPublishedSchemaTypes(t *testing.T) {
	measured := false
	data, err := json.Marshal(Inventory{
		SchemaVersion: "1.1.0",
		GeneratedAt:   time.Unix(0, 0).UTC(),
		DryRun:        true,
		Roots:         []string{"/repo"},
		ExcludedPaths: []string{"/repo/protected"},
		Worktrees: []Worktree{{
			Path: "/repo/protected", Repository: "/repo", Classification: Kept, Reason: "excluded", Action: ActionKept,
			WorktreeDetails: &WorktreeDetails{Excluded: true, DiskBytesMeasured: &measured},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		ExcludedPaths []string `json:"excluded_paths"`
		Worktrees     []struct {
			Excluded          bool `json:"excluded"`
			DiskBytesMeasured bool `json:"disk_bytes_measured"`
		} `json:"worktrees"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.ExcludedPaths) != 1 || document.ExcludedPaths[0] != "/repo/protected" || len(document.Worktrees) != 1 || !document.Worktrees[0].Excluded || document.Worktrees[0].DiskBytesMeasured {
		t.Fatalf("exclusion JSON=%s", data)
	}
}
