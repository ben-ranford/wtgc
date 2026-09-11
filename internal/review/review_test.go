package review

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/ben-ranford/wtgc/internal/model"
)

func TestBuildFiltersSortsGroupsAndTotals(t *testing.T) {
	inv := model.Inventory{Worktrees: []model.Worktree{
		worktree("/r1/z", "/r1", model.SafeToRemove, 10),
		worktree("/r1/a", "/r1", model.Kept, 10),
		worktree("/r2/b", "/r2", model.SafeToRemove, 30),
		{Path: "/r3/error", Repository: "/r3", Classification: model.Error, Error: "inspect failed"},
	}}
	inv.Worktrees[0].Details().CacheWarnings = []model.CacheWarning{{Bytes: 5}}
	doc, err := Build(inv, Options{Repositories: []string{"/r1", "/r2"}, Classifications: []string{"safe_to_remove"}, GroupBy: "repository", SortBy: "size", SelectedPaths: []string{"/r1/z"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.ReviewSchemaVersion; got != SchemaVersion {
		t.Fatalf("schema=%q", got)
	}
	if doc.View.Totals.Full != (Total{Count: 4, ReclaimableBytes: 40, CacheBytes: 5}) || doc.View.Totals.Visible != (Total{Count: 3, ReclaimableBytes: 40, CacheBytes: 5}) || doc.View.Totals.Selected != (Total{Count: 1, ReclaimableBytes: 10, CacheBytes: 5}) {
		t.Fatalf("totals=%+v", doc.View.Totals)
	}
	if len(doc.View.Groups) != 3 || doc.View.Groups[0].Key != "/r1" || doc.View.Groups[1].Key != "/r2" || doc.View.Groups[2].Key != "/r3" {
		t.Fatalf("groups=%+v", doc.View.Groups)
	}
	if got := doc.View.Groups[0].Worktrees[0]; got.Worktree.Path != "/r1/z" || !got.Selected {
		t.Fatalf("first row=%+v", got)
	}
	if got := doc.View.Groups[2].Worktrees[0].Worktree.Path; got != "/r3/error" {
		t.Fatalf("filtered error missing: %q", got)
	}
}

func TestBuildPathSortAndLargeInventory(t *testing.T) {
	worktrees := make([]model.Worktree, 0, 300)
	for i := 299; i >= 0; i-- {
		worktrees = append(worktrees, worktree(fmt.Sprintf("/repo/%03d", i), "/repo", model.Kept, int64(i)))
	}
	doc, err := Build(model.Inventory{Worktrees: worktrees}, Options{SortBy: "path"})
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.View.Groups) != 1 || len(doc.View.Groups[0].Worktrees) != 300 {
		t.Fatalf("groups=%d rows=%d", len(doc.View.Groups), len(doc.View.Groups[0].Worktrees))
	}
	if first, last := doc.View.Groups[0].Worktrees[0].Worktree.Path, doc.View.Groups[0].Worktrees[299].Worktree.Path; first != "/repo/000" || last != "/repo/299" {
		t.Fatalf("sorted paths=%q...%q", first, last)
	}
}

func TestBuildRejectsInvalidSelections(t *testing.T) {
	inv := model.Inventory{Worktrees: []model.Worktree{worktree("/one", "/repo", model.Kept, 1), worktree("/dup", "/repo", model.Kept, 1), worktree("/dup", "/other", model.Kept, 1)}}
	for _, test := range []struct {
		paths []string
		want  string
	}{{[]string{"/missing"}, "unknown"}, {[]string{"/one", "/one"}, "duplicate"}, {[]string{"/dup"}, "ambiguous"}} {
		_, err := Build(inv, Options{SelectedPaths: test.paths})
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("Build(%v) error=%v, want %q", test.paths, err, test.want)
		}
	}
}

func TestBuildKeepsCacheWarningErrorsVisible(t *testing.T) {
	matching := worktree("/matching", "/matching", model.Kept, 1)
	hidden := worktree("/warning", "/other", model.Kept, 1)
	hidden.Details().CacheWarnings = []model.CacheWarning{{Error: "cache unreadable"}}
	doc, err := Build(model.Inventory{Worktrees: []model.Worktree{matching, hidden}}, Options{Repositories: []string{"/matching"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := doc.View.Totals.Visible.Count; got != 2 {
		t.Fatalf("visible count=%d, want cache warning row retained", got)
	}
}

func TestBuildKeepsSelectedRowsVisibleThroughFilters(t *testing.T) {
	selected := worktree("/selected", "/selected-repository", model.SafeToRemove, 1)
	matching := worktree("/matching", "/matching-repository", model.Kept, 1)
	doc, err := Build(model.Inventory{Worktrees: []model.Worktree{selected, matching}}, Options{
		Repositories:    []string{"/matching-repository"},
		Classifications: []string{string(model.Kept)},
		SelectedPaths:   []string{"/selected"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if doc.View.Totals.Visible.Count != 2 || doc.View.Totals.Selected.Count != 1 {
		t.Fatalf("totals=%+v", doc.View.Totals)
	}
	for _, group := range doc.View.Groups {
		for _, row := range group.Worktrees {
			if row.Worktree.Path == "/selected" && row.Selected {
				return
			}
		}
	}
	t.Fatal("selected row was filtered from the view")
}

func TestBuildEncodesEmptyGroupsAsArray(t *testing.T) {
	doc, err := Build(model.Inventory{Worktrees: []model.Worktree{worktree("/kept", "/repository", model.Kept, 1)}}, Options{Classifications: []string{string(model.SafeToRemove)}})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"groups":[]`) {
		t.Fatalf("document=%s", encoded)
	}
}

func TestBuildReclaimProposalSelectsDeterministicShortestPrefix(t *testing.T) {
	inv := model.Inventory{SchemaVersion: "1.1.0", Worktrees: []model.Worktree{
		worktree("/z", "/repo-z", model.SafeToRemove, 10),
		worktree("/b", "/repo-a", model.SafeToRemove, 20),
		worktree("/a", "/repo-a", model.SafeToRemove, 20),
		worktree("/kept", "/repo", model.Kept, 100),
	}}
	doc, err := Build(inv, Options{HasReclaimTarget: true, ReclaimTarget: 30})
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Proposal
	if p == nil || !p.TargetMet || p.EstimatedBytes.Cmp(big.NewInt(40)) != 0 || p.ExcessBytes.Cmp(big.NewInt(10)) != 0 || p.ShortfallBytes.Sign() != 0 || len(p.Candidates) != 2 {
		t.Fatalf("proposal=%+v", p)
	}
	if p.Candidates[0].Path != "/a" || p.Candidates[1].Path != "/b" || p.SchemaVersion != "1.0.0" || !strings.Contains(p.SelectionRule, "minimizes candidate count") {
		t.Fatalf("proposal=%+v", p)
	}
}

func TestBuildReclaimProposalUsesDiskBytesForExactTarget(t *testing.T) {
	inv := model.Inventory{Worktrees: []model.Worktree{
		worktree("/large", "/repo", model.SafeToRemove, 8),
		worktree("/small", "/repo", model.SafeToRemove, 2),
	}}
	inv.Worktrees[0].Details().CacheWarnings = []model.CacheWarning{{Bytes: 100}}
	doc, err := Build(inv, Options{HasReclaimTarget: true, ReclaimTarget: 10})
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Proposal
	if !p.TargetMet || p.EstimatedBytes.Cmp(big.NewInt(10)) != 0 || p.ExcessBytes.Sign() != 0 || p.ShortfallBytes.Sign() != 0 || len(p.Candidates) != 2 {
		t.Fatalf("proposal=%+v", p)
	}
	if p.Candidates[0].Path != "/large" || p.Candidates[1].Path != "/small" {
		t.Fatalf("candidates=%+v", p.Candidates)
	}
}

func TestBuildReclaimProposalDoesNotCountCacheWarnings(t *testing.T) {
	item := worktree("/candidate", "/repo", model.SafeToRemove, 8)
	item.Details().CacheWarnings = []model.CacheWarning{{Bytes: 100}}
	doc, err := Build(model.Inventory{Worktrees: []model.Worktree{item}}, Options{HasReclaimTarget: true, ReclaimTarget: 10})
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Proposal
	if p.TargetMet || p.EstimatedBytes.Cmp(big.NewInt(8)) != 0 || p.ShortfallBytes.Cmp(big.NewInt(2)) != 0 || len(p.Candidates) != 1 {
		t.Fatalf("proposal=%+v", p)
	}
}

func TestBuildReclaimProposalQualifiesUnmetAndIneligibleRows(t *testing.T) {
	measured := false
	inv := model.Inventory{Errors: []string{"scan interrupted"}, ExcludedPaths: []string{"/protected"}, Worktrees: []model.Worktree{
		worktree("/eligible", "/repo", model.SafeToRemove, 5),
		worktree("/zero", "/repo", model.SafeToRemove, 0),
		{Path: "/unknown", Repository: "/repo", Classification: model.SafeToRemove, WorktreeDetails: &model.WorktreeDetails{DiskBytesMeasured: &measured}},
		{Path: "/excluded", Repository: "/repo", Classification: model.SafeToRemove, DiskBytes: 100, WorktreeDetails: &model.WorktreeDetails{Excluded: true}},
		{Path: "/stale", Repository: "/repo", Classification: model.SafeToRemove, DiskBytes: 100, Prunable: true},
		{Path: "/failed", Repository: "/repo", Classification: model.SafeToRemove, DiskBytes: 100, Error: "disk read failed"},
	}}
	doc, err := Build(inv, Options{HasReclaimTarget: true, ReclaimTarget: 10})
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Proposal
	if p.TargetMet || p.EstimatedBytes.Cmp(big.NewInt(5)) != 0 || p.ShortfallBytes.Cmp(big.NewInt(5)) != 0 || len(p.Candidates) != 1 || p.Candidates[0].Path != "/eligible" {
		t.Fatalf("proposal=%+v", p)
	}
	joined := strings.Join(p.Uncertainties, " ")
	for _, want := range []string{"scan error", "5 scoped safe", "excluded invocation", "do not promise"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("uncertainties=%q missing %q", joined, want)
		}
	}
}

func TestBuildReclaimProposalFiltersAndAvoidsIntegerOverflow(t *testing.T) {
	inv := model.Inventory{Worktrees: []model.Worktree{
		worktree("/first", "/included", model.SafeToRemove, math.MaxInt64-1),
		worktree("/second", "/included", model.SafeToRemove, math.MaxInt64-1),
		worktree("/other", "/other", model.SafeToRemove, math.MaxInt64),
	}}
	doc, err := Build(inv, Options{Repositories: []string{"/included"}, HasReclaimTarget: true, ReclaimTarget: math.MaxInt64})
	if err != nil {
		t.Fatal(err)
	}
	p := doc.Proposal
	wantTotal := new(big.Int).Sub(new(big.Int).Mul(big.NewInt(math.MaxInt64), big.NewInt(2)), big.NewInt(2))
	wantExcess := new(big.Int).Sub(wantTotal, big.NewInt(math.MaxInt64))
	if !p.TargetMet || len(p.Candidates) != 2 || p.Candidates[0].Path != "/first" || p.Candidates[1].Path != "/second" || p.EstimatedBytes.Cmp(wantTotal) != 0 || p.ExcessBytes.Cmp(wantExcess) != 0 {
		t.Fatalf("proposal=%+v", p)
	}
	encoded, err := json.Marshal(BuildMust(t, inv, Options{}))
	if err != nil || strings.Contains(string(encoded), "proposal") {
		t.Fatalf("unflagged document changed: %s err=%v", encoded, err)
	}
}

func TestBuildReclaimProposalRejectsSelection(t *testing.T) {
	_, err := Build(model.Inventory{}, Options{HasReclaimTarget: true, ReclaimTarget: 1, SelectedPaths: []string{"/one"}})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("Build error=%v", err)
	}
}

func TestBuildReclaimProposalRejectsNonPositiveTarget(t *testing.T) {
	_, err := Build(model.Inventory{}, Options{HasReclaimTarget: true})
	if err == nil || !strings.Contains(err.Error(), "greater than zero") {
		t.Fatalf("Build error=%v", err)
	}
}

func BuildMust(t *testing.T, inv model.Inventory, opts Options) Document {
	t.Helper()
	doc, err := Build(inv, opts)
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func worktree(path, repository string, classification model.Classification, size int64) model.Worktree {
	return model.Worktree{Path: path, Repository: repository, Classification: classification, DiskBytes: size}
}
