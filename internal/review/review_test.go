package review

import (
	"encoding/json"
	"fmt"
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

func worktree(path, repository string, classification model.Classification, size int64) model.Worktree {
	return model.Worktree{Path: path, Repository: repository, Classification: classification, DiskBytes: size}
}
