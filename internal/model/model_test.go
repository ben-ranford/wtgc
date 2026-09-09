package model

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type publishedSchemaClassification struct {
	Enum []Classification `json:"enum"`
}

type publishedSchemaVersion struct {
	Const string `json:"const"`
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
		Properties struct {
			SchemaVersion publishedSchemaVersion `json:"schema_version"`
		} `json:"properties"`
		Definitions struct {
			Classification publishedSchemaClassification `json:"classification"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatalf("parse inventory schema: %v", err)
	}
	if schema.Properties.SchemaVersion.Const != "1.1.0" {
		t.Fatalf("schema version = %q, want 1.1.0", schema.Properties.SchemaVersion.Const)
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
}

func TestOptionalWorktreeDetailsRemainFlatAndAbsentWhenUnused(t *testing.T) {
	plain, err := json.Marshal(Worktree{Path: "p", Repository: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "WorktreeDetails") || strings.Contains(string(plain), "cache_warnings") {
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
