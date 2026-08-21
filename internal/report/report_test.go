package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

func testInventory() model.Inventory {
	return model.Inventory{
		SchemaVersion: "1.0.0",
		GeneratedAt:   time.Date(2026, 8, 21, 10, 30, 0, 0, time.UTC),
		DryRun:        true,
		Roots:         []string{"."},
		Worktrees: []model.Worktree{
			{
				Path:           "/tmp/repo-wt/feature",
				Branch:         "feature/demo",
				Repository:     "/tmp/repo",
				DiskBytes:      1536,
				Classification: model.SafeToRemove,
				Reason:         "merged and clean",
			},
			{
				Path:           "/tmp/repo-wt/dirty",
				Branch:         "bug/dirty",
				Repository:     "/tmp/repo",
				DiskBytes:      5 * 1024 * 1024,
				Classification: model.MergedButDirty,
				Reason:         "dirty worktree",
				Error:          "has local changes",
			},
		},
		Summary: model.Summary{
			Repositories:   1,
			Scanned:        2,
			Safe:           1,
			Skipped:        1,
			PotentialBytes: 1536,
			Duration:       2 * time.Second,
		},
	}
}

func TestWriteJSONEmitsInventoryOnly(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, testInventory(), FormatJSON); err != nil {
		t.Fatalf("Write(JSON) error = %v", err)
	}

	out := b.String()
	if strings.Contains(out, "Summary") || strings.Contains(out, "PATH") {
		t.Fatalf("JSON output contains human text:\n%s", out)
	}

	var got model.Inventory
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("json.Unmarshal error = %v\n%s", err, out)
	}
	if got.SchemaVersion != "1.0.0" || len(got.Worktrees) != 2 {
		t.Fatalf("decoded inventory = %#v", got)
	}
}

func TestWriteHumanEmitsReadableTableAndSummary(t *testing.T) {
	var b bytes.Buffer
	if err := Write(&b, testInventory(), FormatHuman); err != nil {
		t.Fatalf("Write(Human) error = %v", err)
	}

	out := b.String()
	for _, want := range []string{
		"PATH",
		"BRANCH",
		"CLASSIFICATION",
		"ACTION",
		"SIZE",
		"/tmp/repo-wt/feature",
		"1.5 KiB",
		"5.0 MiB",
		"Summary:",
		"dry run: true",
		"potential reclaimable: 1.5 KiB",
		"duration: 2s",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output missing %q:\n%s", want, out)
		}
	}
}

func TestWriteRejectsUnknownFormat(t *testing.T) {
	if err := Write(&bytes.Buffer{}, testInventory(), Format("yaml")); err == nil {
		t.Fatal("Write(unknown) error = nil, want error")
	}
}

func TestByteString(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1024, "1.0 KiB"},
		{1536, "1.5 KiB"},
		{1024 * 1024, "1.0 MiB"},
	} {
		if got := ByteString(tc.n); got != tc.want {
			t.Fatalf("ByteString(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestAction(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		worktree model.Worktree
		want     string
	}{
		{name: "kept", worktree: model.Worktree{Classification: model.Kept}, want: "kept"},
		{name: "dry run remove", worktree: model.Worktree{Classification: model.SafeToRemove}, want: "would-remove"},
		{name: "dry run prune", worktree: model.Worktree{Classification: model.Prunable}, want: "would-prune"},
		{name: "removed", worktree: model.Worktree{Removed: true}, want: "removed"},
		{name: "pruned", worktree: model.Worktree{Removed: true, Prunable: true}, want: "pruned"},
		{name: "branch deleted", worktree: model.Worktree{Removed: true, BranchDeleted: true}, want: "removed+branch-deleted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := action(test.worktree); got != test.want {
				t.Fatalf("action() = %q, want %q", got, test.want)
			}
		})
	}
}
