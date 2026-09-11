package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/explain"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/review"
)

func testInventory() model.Inventory {
	clean := false
	dirty := true
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
				Dirty:          &clean,
				Reason:         "merged and clean",
			},
			{
				Path:           "/tmp/repo-wt/dirty",
				Branch:         "bug/dirty",
				Repository:     "/tmp/repo",
				DiskBytes:      5 * 1024 * 1024,
				Classification: model.MergedButDirty,
				Dirty:          &dirty,
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

func TestWriteExplainFormatsProjectionAndErrors(t *testing.T) {
	now := time.Now().UTC()
	doc := explain.Document{ExplainSchemaVersion: "1.0.0", Worktree: explain.Worktree{Path: "/repo/hostile\n", Repository: "/repo", Head: "abc", Classification: model.Kept, Reason: "locked", Error: "inspect failed", RetentionBasis: "worktree_mtime", ObservedAt: &now, EligibleAt: &now, Provider: "github", ProviderPR: 4, ProviderURL: "https://github.com/example/pr/4"}, Checks: []model.ExplainCheck{{ID: "protection", Status: model.ExplainBlocked, Detail: "locked"}}, NextChecks: []string{"Inspect status."}, OperationalErrors: []string{"default branch unavailable"}}
	var human bytes.Buffer
	if err := WriteExplain(&human, doc, FormatHuman); err != nil || !strings.Contains(human.String(), `\n`) || !strings.Contains(human.String(), "Provider proof") || !strings.Contains(human.String(), "Operational errors:") {
		t.Fatalf("err=%v output=%q", err, human.String())
	}
	var jsonOutput bytes.Buffer
	if err := WriteExplain(&jsonOutput, doc, FormatJSON); err != nil || !strings.Contains(jsonOutput.String(), `"explain_schema_version"`) {
		t.Fatalf("err=%v output=%q", err, jsonOutput.String())
	}
	if err := WriteExplain(io.Discard, doc, Format("bad")); err == nil {
		t.Fatal("bad format accepted")
	}
	if err := WriteExplain(failingReportWriter{}, doc, FormatHuman); err == nil {
		t.Fatal("WriteExplain accepted writer failure")
	}
	if err := WriteExplain(shortReportWriter{}, doc, FormatHuman); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WriteExplain short write error=%v", err)
	}
}

func TestWriteReviewShowsEvidenceTotalsAndEscapesText(t *testing.T) {
	inv := testInventory()
	inv.Worktrees[0].Head = "abc123"
	inv.Worktrees[0].Reason = "merged\nforged"
	inv.Worktrees[0].Details().Provider = "github"
	now := time.Now().UTC()
	inv.Worktrees[0].RetentionBasis = "worktree_mtime"
	inv.Worktrees[0].ObservedAt = &now
	inv.Worktrees[0].EligibleAt = &now
	inv.Worktrees[0].CacheWarnings = []model.CacheWarning{{Path: "/cache\x1b[2J", Bytes: 1024, Reason: "large"}}
	inv.Errors = []string{"scan warning\nkept visible"}
	doc, err := review.Build(inv, review.Options{SelectedPaths: []string{"/tmp/repo-wt/feature"}})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteReview(&output, doc, FormatHuman); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{"HEAD", "abc123", "provider=github", "retention=worktree_mtime", "cache=1.0 KiB", "Totals:", "full: count=2", "visible: count=2", "selected: count=1", "Errors:", `merged\nforged`, `\x1b`} {
		if !strings.Contains(got, want) {
			t.Fatalf("review output missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "merged\nforged") || strings.Contains(got, "\x1b") {
		t.Fatalf("review output contains unescaped controls:\n%s", got)
	}
	output.Reset()
	if err := WriteReview(&output, doc, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var decoded review.Document
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded.ReviewSchemaVersion != review.SchemaVersion || decoded.Inventory.SchemaVersion != "1.0.0" {
		t.Fatalf("review JSON=%s err=%v decoded=%+v", output.String(), err, decoded)
	}
	if err := WriteReview(&output, doc, Format("csv")); err == nil {
		t.Fatal("WriteReview accepted unknown format")
	}
}

func TestWriteReviewReportsCompleteDocumentWriteFailures(t *testing.T) {
	empty, err := review.Build(model.Inventory{}, review.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteReview(failingReportWriter{}, empty, FormatHuman); err == nil {
		t.Fatal("WriteReview accepted empty-view writer failure")
	}
	doc, err := review.Build(testInventory(), review.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteReview(shortReportWriter{}, doc, FormatHuman); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("WriteReview partial document error=%v, want short write", err)
	}
}

func TestWriteReviewRendersAdvisoryReclaimProposal(t *testing.T) {
	inv := testInventory()
	doc, err := review.Build(inv, review.Options{HasReclaimTarget: true, ReclaimTarget: 2048})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := WriteReview(&output, doc, FormatHuman); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Reclaim proposal", "advisory only", "target: 2.0 KiB", "target met: false", "estimated shortfall: 512 B", "estimated bytes do not promise"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, output.String())
		}
	}
	output.Reset()
	if err := WriteReview(&output, doc, FormatJSON); err != nil {
		t.Fatal(err)
	}
	var decoded review.Document
	if err := json.Unmarshal(output.Bytes(), &decoded); err != nil || decoded.Proposal == nil || decoded.Proposal.SchemaVersion != "1.0.0" || decoded.Inventory.SchemaVersion != "1.0.0" {
		t.Fatalf("JSON=%s err=%v decoded=%+v", output.String(), err, decoded)
	}
}

func TestWriteReviewRendersMetProposalAndLargeTotals(t *testing.T) {
	proposal := &review.Proposal{SchemaVersion: "1.0.0", Advisory: true, TargetBytes: 1, TargetMet: true, EstimatedBytes: uint64(math.MaxInt64) + 1, ExcessBytes: uint64(math.MaxInt64), Candidates: []review.Candidate{}, SelectionRule: "count", Authorization: "fresh validation"}
	var output bytes.Buffer
	if err := WriteReview(&output, review.Document{View: review.View{Groups: []review.Group{}}, Proposal: proposal}, FormatHuman); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"target met: true", "estimated excess", "9223372036854775808 B"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("output missing %q: %s", want, output.String())
		}
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
		"DIRTY",
		"ACTION",
		"SIZE",
		"RECLAIMED",
		"/tmp/repo-wt/feature",
		"clean",
		"dirty",
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

func TestReportsDescribeExclusionScope(t *testing.T) {
	inv := testInventory()
	inv.ExcludedPaths = []string{"/tmp/protected\npath"}
	measured := false
	inv.Worktrees = append(inv.Worktrees, model.Worktree{
		Path: "/tmp/repo-wt/excluded", Classification: model.Kept, Reason: "excluded by invocation scope",
		WorktreeDetails: &model.WorktreeDetails{Excluded: true, DiskBytesMeasured: &measured},
	})
	var human bytes.Buffer
	if err := Write(&human, inv, FormatHuman); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(human.String(), "Scope limitations:") || !strings.Contains(human.String(), `excluded: /tmp/protected\npath`) || !strings.Contains(human.String(), "/tmp/repo-wt/excluded") || !strings.Contains(human.String(), "unmeasured") {
		t.Fatalf("human output=%q", human.String())
	}
	doc, err := review.Build(inv, review.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var reviewOutput bytes.Buffer
	if err := WriteReview(&reviewOutput, doc, FormatHuman); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reviewOutput.String(), "Scope limitations:") || !strings.Contains(reviewOutput.String(), `excluded: /tmp/protected\npath`) || !strings.Contains(reviewOutput.String(), "/tmp/repo-wt/excluded") || !strings.Contains(reviewOutput.String(), "unmeasured") {
		t.Fatalf("review output=%q", reviewOutput.String())
	}
}

func TestWriteHumanEscapesTerminalControlCharacters(t *testing.T) {
	t.Parallel()
	inv := testInventory()
	inv.Roots = []string{"/tmp/root\nspoof"}
	inv.Worktrees[0].Path = "/tmp/wt\tcolumn\x1b[2J"
	inv.Worktrees[0].Branch = "feature\rspoof"
	inv.Worktrees[0].Reason = "safe\nFAKE ROW"
	inv.Errors = []string{"git error\x1b]0;spoof\a"}

	var b bytes.Buffer
	if err := Write(&b, inv, FormatHuman); err != nil {
		t.Fatalf("Write(Human) error = %v", err)
	}
	out := b.String()
	for _, forbidden := range []string{"\x1b", "\r", "\a", "safe\nFAKE ROW", "root\nspoof"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("human output contains raw control sequence %q:\n%s", forbidden, out)
		}
	}
	for _, want := range []string{`\t`, `\x1b`, `\r`, `safe\nFAKE ROW`, `root\nspoof`, `\a`} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output missing escaped text %q:\n%s", want, out)
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
		{1024 * 1024 * 1024, "1.0 GiB"},
		{1024 * 1024 * 1024 * 1024 * 1024 * 1024, "1.0 EiB"},
	} {
		if got := ByteString(tc.n); got != tc.want {
			t.Fatalf("ByteString(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestWriteHumanPropagatesTableWriterFailure(t *testing.T) {
	err := Write(failingReportWriter{}, testInventory(), FormatHuman)
	if err == nil || !strings.Contains(err.Error(), "report write failed") {
		t.Fatalf("Write error=%v", err)
	}
}

type failingReportWriter struct{}

func (failingReportWriter) Write([]byte) (int, error) {
	return 0, errors.New("report write failed")
}

type shortReportWriter struct{}

func (shortReportWriter) Write(value []byte) (int, error) {
	return len(value) - 1, nil
}

func TestAction(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		worktree model.Worktree
		want     string
	}{
		{name: "kept", worktree: model.Worktree{Classification: model.Kept}, want: "kept"},
		{name: "dry run remove", worktree: model.Worktree{Classification: model.SafeToRemove}, want: "would_remove"},
		{name: "dry run prune", worktree: model.Worktree{Classification: model.Prunable}, want: "would_prune"},
		{name: "removed", worktree: model.Worktree{Removed: true}, want: "removed"},
		{name: "pruned", worktree: model.Worktree{Removed: true, Prunable: true}, want: "pruned"},
		{name: "branch deleted", worktree: model.Worktree{Removed: true, BranchDeleted: true}, want: "removed_branch_deleted"},
		{name: "explicit", worktree: model.Worktree{Action: model.ActionKept, Classification: model.SafeToRemove}, want: "kept"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := action(test.worktree); got != test.want {
				t.Fatalf("action() = %q, want %q", got, test.want)
			}
		})
	}
}
