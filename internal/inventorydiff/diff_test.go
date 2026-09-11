package inventorydiff

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

func fixture(t *testing.T, generated time.Time, rows []model.Worktree, errors ...string) string {
	t.Helper()
	data, err := json.Marshal(model.Inventory{SchemaVersion: "1.1.0", GeneratedAt: generated, Roots: []string{"/scan"}, Worktrees: rows, Summary: model.Summary{Scanned: len(rows)}, Errors: errors})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "inventory.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func row(path string, bytes int64) model.Worktree {
	return model.Worktree{Repository: "/repo", Path: path, Head: "a", Branch: "feature", DiskBytes: bytes, Classification: model.Kept, Reason: "retained", Action: model.ActionKept}
}

func TestCompareReportsGrowthChangesAndNotObservedWithoutDeletionClaim(t *testing.T) {
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	before, err := Load(fixture(t, then, []model.Worktree{row("/old", 4), row("/changed", 10), row("/same", 5)}))
	if err != nil {
		t.Fatal(err)
	}
	changed := row("/changed", 17)
	changed.Head = "b"
	changed.Classification = model.SafeToRemove
	changed.Reason = "merged"
	after, err := Load(fixture(t, then.Add(time.Hour), []model.Worktree{changed, row("/same", 5), row("/added", 9)}))
	if err != nil {
		t.Fatal(err)
	}
	doc := Compare(before, after)
	if got, want := doc.Summary, (Summary{Added: 1, NotObserved: 1, Changed: 1, Unchanged: 1}); got != want {
		t.Fatalf("summary=%+v want=%+v", got, want)
	}
	for _, item := range doc.Rows {
		if item.Path == "/old" && (item.Status != "not_observed_in_after" || item.ByteDelta != nil) {
			t.Fatalf("old row claims deletion/reclamation: %+v", item)
		}
	}
	var out bytes.Buffer
	if err := Write(&out, doc, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "not_observed_in_after") || !strings.Contains(out.String(), "7 bytes") {
		t.Fatalf("human output=%s", out.String())
	}
}

func TestCompareUnknownSizeDoesNotFabricateDeltaAndEscapesOutput(t *testing.T) {
	then := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	before, err := Load(fixture(t, then, []model.Worktree{row("/bad\x1b[2J", 10)}))
	if err != nil {
		t.Fatal(err)
	}
	afterRow := row("/bad\x1b[2J", 0)
	afterRow.Error = "measurement failed"
	after, err := Load(fixture(t, then.Add(time.Hour), []model.Worktree{afterRow}))
	if err != nil {
		t.Fatal(err)
	}
	doc := Compare(before, after)
	if doc.Rows[0].ByteDelta != nil || !strings.Contains(strings.Join(doc.Rows[0].Changes, ","), "disk_bytes_unknown") {
		t.Fatalf("unknown size=%+v", doc.Rows[0])
	}
	var out bytes.Buffer
	if err := Write(&out, doc, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "\x1b") || !strings.Contains(out.String(), `\x1b`) {
		t.Fatalf("unsafe output=%q", out.String())
	}
}

func TestCompareIdenticalUnknownSizeIsUnchanged(t *testing.T) {
	when := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	unknown := row("/unknown", 0)
	unknown.Error = "measurement failed"
	before, err := Load(fixture(t, when, []model.Worktree{unknown}))
	if err != nil {
		t.Fatal(err)
	}
	after, err := Load(fixture(t, when, []model.Worktree{unknown}))
	if err != nil {
		t.Fatal(err)
	}
	doc := Compare(before, after)
	if doc.Summary != (Summary{Unchanged: 1}) || doc.Rows[0].ByteDelta != nil || len(doc.Rows[0].Changes) != 0 {
		t.Fatalf("doc=%+v", doc)
	}
}

func TestLoadRejectsMalformedTrailingDuplicateAndWrongSchema(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"bad": "{", "trailing": `{"schema_version":"1.1.0"}{}`, "wrong": `{"schema_version":"1.0.0"}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil || strings.Contains(err.Error(), content) {
			t.Fatalf("Load(%s) error=%v", name, err)
		}
	}
	when := time.Now().UTC()
	duplicate := fixture(t, when, []model.Worktree{row("/same", 1), row("/same", 2)})
	if _, err := Load(duplicate); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error=%v", err)
	}
}

func TestLoadRejectsNullRequiredNumbersAndIdentities(t *testing.T) {
	when := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	path := fixture(t, when, []model.Worktree{row("/ok", 1)})
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, replacement := range []struct{ from, to string }{
		{`"disk_bytes":1`, `"disk_bytes":null`},
		{`"reclaimed_bytes":0`, `"reclaimed_bytes":null`},
		{`"dry_run":false`, `"dry_run":null`},
		{`"path":"/ok"`, `"path":"/bad\u0000key"`},
	} {
		bad := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(bad, []byte(strings.Replace(string(data), replacement.from, replacement.to, 1)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(bad); err == nil {
			t.Fatalf("Load accepted %s", replacement.to)
		}
	}
}

func TestCompareUsesExplicitExclusionAndMeasurementMetadata(t *testing.T) {
	when := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	first := fixture(t, when, []model.Worktree{row("/same", 100)})
	data, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	doc["excluded_paths"] = []string{"/archive"}
	rows := doc["worktrees"].([]any)
	rows[0].(map[string]any)["disk_bytes_measured"] = false
	encoded, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(t.TempDir(), "second.json")
	if err := os.WriteFile(second, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := Load(first)
	if err != nil {
		t.Fatal(err)
	}
	after, err := Load(second)
	if err != nil {
		t.Fatal(err)
	}
	compared := Compare(before, after)
	if compared.Rows[0].ByteDelta != nil || !strings.Contains(strings.Join(compared.Rows[0].Changes, ","), "disk_bytes_unknown") {
		t.Fatalf("measurement=%+v", compared.Rows[0])
	}
	if !strings.Contains(strings.Join(compared.Warnings, "\n"), "exclusion settings differ") {
		t.Fatalf("warnings=%v", compared.Warnings)
	}
}

func TestLoadAcceptsReviewWrapperUsingFullInventory(t *testing.T) {
	when := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	rawPath := fixture(t, when, []model.Worktree{row("/full", 3)})
	raw, err := Load(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := filepath.Join(t.TempDir(), "review.json")
	if err := os.WriteFile(wrapped, []byte(`{"review_schema_version":"1.0.0","inventory":`+string(data)+`,"view":{"groups":[]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Inventory.Worktrees) != 1 || got.Inventory.Worktrees[0].Path != raw.Inventory.Worktrees[0].Path {
		t.Fatalf("wrapped=%+v raw=%+v", got, raw)
	}
}

func TestLoadRejectsOversizedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte(" "), maxInputBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("oversize error=%v", err)
	}
}

func TestLoadRejectsNonRegularInputBeforeReading(t *testing.T) {
	if _, err := Load(t.TempDir()); !errors.Is(err, ErrFileRead) {
		t.Fatalf("directory error=%v", err)
	}
}

func TestWriteRejectsShortHumanOutput(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, Before: Provenance{GeneratedAt: time.Now()}, After: Provenance{GeneratedAt: time.Now()}}
	if err := Write(shortWriter{}, document, false); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Write error=%v", err)
	}
}

func TestWriteRejectsShortJSONOutputAndRendersScanErrors(t *testing.T) {
	document := Document{SchemaVersion: SchemaVersion, Before: Provenance{GeneratedAt: time.Now(), Errors: []string{"before\x1b[2J"}}, After: Provenance{GeneratedAt: time.Now(), Errors: []string{"after\nerror"}}}
	if err := Write(shortWriter{}, document, true); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("JSON error=%v", err)
	}
	var human bytes.Buffer
	if err := Write(&human, document, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(human.String(), "\x1b") || !strings.Contains(human.String(), "Before errors:") || !strings.Contains(human.String(), `before\x1b`) || !strings.Contains(human.String(), "After errors:") || !strings.Contains(human.String(), `after\nerror`) {
		t.Fatalf("human=%q", human.String())
	}
	var jsonOut bytes.Buffer
	if err := Write(&jsonOut, document, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(jsonOut.String(), `"errors"`) {
		t.Fatalf("json=%s", jsonOut.String())
	}
}

func TestValidationHelpersCoverRejectedContracts(t *testing.T) {
	source := "fixture"
	if _, err := decodeDocument([]byte("{"), source); err == nil {
		t.Fatal("malformed document accepted")
	}
	if _, err := decodeDocument([]byte("{} garbage"), source); err == nil {
		t.Fatal("invalid trailing document accepted")
	}
	valid := map[string]json.RawMessage{"schema_version": json.RawMessage(`"1.1.0"`), "generated_at": json.RawMessage(`"2026-09-01T00:00:00Z"`), "dry_run": json.RawMessage(`true`), "roots": json.RawMessage(`["/root"]`), "worktrees": json.RawMessage(`[]`), "summary": json.RawMessage(`{"repositories":0,"scanned":0,"safe":0,"removed":0,"skipped":0,"pruned":0,"potential_bytes":0,"reclaimed_bytes":0,"duration_ns":0}`)}
	if _, err := inventoryRaw(map[string]json.RawMessage{"review_schema_version": json.RawMessage(`"2.0.0"`)}, source); err == nil {
		t.Fatal("unsupported wrapper accepted")
	}
	if _, err := inventoryRaw(map[string]json.RawMessage{"review_schema_version": json.RawMessage(`"1.0.0"`)}, source); err == nil {
		t.Fatal("missing wrapper inventory accepted")
	}
	missing := mapsClone(valid)
	delete(missing, "roots")
	if _, err := inventoryRaw(missing, source); err == nil {
		t.Fatal("missing inventory field accepted")
	}
	wrongVersion := mapsClone(valid)
	wrongVersion["schema_version"] = json.RawMessage(`"1.0.0"`)
	if _, err := snapshotFromRaw(wrongVersion, source); err == nil {
		t.Fatal("wrong schema accepted")
	}
	zeroTime := mapsClone(valid)
	zeroTime["generated_at"] = json.RawMessage(`"0001-01-01T00:00:00Z"`)
	if _, err := snapshotFromRaw(zeroTime, source); err == nil {
		t.Fatal("zero timestamp accepted")
	}
	for _, raw := range []json.RawMessage{nil, json.RawMessage(`null`), json.RawMessage(`1`), json.RawMessage(`[""]`), json.RawMessage(`["bad\u0000path"]`)} {
		if _, err := stringArray(raw, "paths", source); raw != nil && err == nil {
			t.Fatalf("array %s accepted", raw)
		}
	}
	if err := validateSummary(json.RawMessage(`[]`), model.Summary{}, source); err == nil {
		t.Fatal("invalid summary accepted")
	}
	if err := validateSummary(json.RawMessage(`{"repositories":0}`), model.Summary{}, source); err == nil {
		t.Fatal("incomplete summary accepted")
	}
	if err := validateSummary(valid["summary"], model.Summary{Repositories: -1}, source); err == nil {
		t.Fatal("negative summary accepted")
	}
	if knownClassification("unknown") || knownAction("unknown") {
		t.Fatal("unknown enum accepted")
	}
}

func TestComparisonHelpersCoverWarningsAndRows(t *testing.T) {
	before := Snapshot{Inventory: model.Inventory{Roots: []string{"/before"}, GeneratedAt: time.Now(), Errors: []string{"partial"}}, Warnings: []string{"before"}, Exclusions: []string{"/one"}, SizeKnown: map[string]bool{}}
	after := Snapshot{Inventory: model.Inventory{Roots: []string{"/after"}, GeneratedAt: before.Inventory.GeneratedAt.Add(-time.Second), Errors: []string{"partial"}}, Warnings: []string{"after"}, Exclusions: []string{"/two"}, SizeKnown: map[string]bool{}}
	if got := comparisonWarnings(before, after); len(got) < 6 {
		t.Fatalf("warnings=%v", got)
	}
	old := row("/old", 1)
	next := row("/next", 2)
	keyOld, keyNext := old.Repository+"\x00"+old.Path, next.Repository+"\x00"+next.Path
	if got := comparisonKeys(map[string]model.Worktree{keyOld: old}, map[string]model.Worktree{keyNext: next}); len(got) != 2 {
		t.Fatalf("keys=%v", got)
	}
	if got := compareRow(keyOld, map[string]model.Worktree{keyOld: old}, nil, nil, nil); got.Status != "not_observed_in_after" {
		t.Fatalf("row=%+v", got)
	}
	if got := compareRow(keyNext, nil, map[string]model.Worktree{keyNext: next}, nil, nil); got.Status != "added" {
		t.Fatalf("row=%+v", got)
	}
	if sameStrings([]string{"a"}, []string{"b"}) || sameStrings([]string{"a"}, []string{"a", "b"}) {
		t.Fatal("sameStrings")
	}
	if changes, delta := changes(old, old, false, true); delta != nil || len(changes) == 0 {
		t.Fatalf("unknown changes=%v delta=%v", changes, delta)
	}
	var summary Summary
	for _, status := range []string{"added", "not_observed_in_after", "changed", "unchanged", "unknown"} {
		increment(&summary, status)
	}
	if summary != (Summary{Added: 1, NotObserved: 1, Changed: 1, Unchanged: 1}) {
		t.Fatalf("summary=%+v", summary)
	}
}

func TestErrorPathsRemainFailClosed(t *testing.T) {
	dir := t.TempDir()
	for _, path := range []string{dir + string(filepath.Separator), filepath.Join(dir, "missing"), filepath.Join(dir, "missing-parent", "input")} {
		if _, err := Load(path); !errors.Is(err, ErrFileRead) {
			t.Fatalf("Load(%q) error=%v", path, err)
		}
	}
	item := row("/item", 1)
	validRow := map[string]json.RawMessage{"repository": json.RawMessage(`"/repo"`), "path": json.RawMessage(`"/item"`), "disk_bytes": json.RawMessage(`1`), "classification": json.RawMessage(`"kept"`), "reason": json.RawMessage(`"retained"`), "action": json.RawMessage(`"kept"`), "reclaimed_bytes": json.RawMessage(`0`)}
	if _, err := validateRows([]model.Worktree{item, item}, []map[string]json.RawMessage{validRow, validRow}, "fixture"); err == nil {
		t.Fatal("duplicate row accepted")
	}
	badMeasured := mapsClone(validRow)
	badMeasured["disk_bytes_measured"] = json.RawMessage(`"yes"`)
	if _, err := validateRows([]model.Worktree{item}, []map[string]json.RawMessage{badMeasured}, "fixture"); err == nil {
		t.Fatal("invalid measurement accepted")
	}
	for _, broken := range []model.Worktree{{Repository: "/repo", Path: "/item", DiskBytes: -1, Classification: model.Kept, Reason: "retained", Action: model.ActionKept}, {Repository: "/repo", Path: "/item", Classification: "unknown", Reason: "retained", Action: model.ActionKept}, {Repository: "/repo", Path: "/item", Classification: model.Kept, Reason: "retained", Action: "unknown"}} {
		if err := validateRow(broken, validRow, "fixture", 0); err == nil {
			t.Fatalf("invalid row=%+v accepted", broken)
		}
	}
	changed := item
	changed.Head, changed.Branch, changed.Classification, changed.Reason, changed.DiskBytes = "next", "next", model.SafeToRemove, "changed", 2
	if changes, delta := changes(item, changed, true, true); delta == nil || *delta != 1 || len(changes) != 5 {
		t.Fatalf("changes=%v delta=%v", changes, delta)
	}
	if err := Write(failingWriter{}, Document{}, true); err == nil {
		t.Fatal("JSON writer failure accepted")
	}
	if err := Write(failingWriter{}, Document{}, false); err == nil {
		t.Fatal("human writer failure accepted")
	}
}

func TestDirectParserBoundaryFailures(t *testing.T) {
	if _, err := decode(failingReader{}, "fixture"); err == nil || !errors.Is(err, ErrFileRead) {
		t.Fatal("reader failure accepted")
	}
	invalidRaw := map[string]json.RawMessage{"schema_version": json.RawMessage{0xff}}
	if _, err := snapshotFromRaw(invalidRaw, "fixture"); err == nil {
		t.Fatal("unmarshallable inventory accepted")
	}
	valid := map[string]json.RawMessage{"schema_version": json.RawMessage(`"1.1.0"`), "generated_at": json.RawMessage(`"2026-09-01T00:00:00Z"`), "dry_run": json.RawMessage(`true`), "roots": json.RawMessage(`["/root"]`), "worktrees": json.RawMessage(`[]`), "summary": json.RawMessage(`{"repositories":0,"scanned":0,"safe":0,"removed":0,"skipped":0,"pruned":0,"potential_bytes":0,"reclaimed_bytes":0,"duration_ns":0}`)}
	invalidType := mapsClone(valid)
	invalidType["dry_run"] = json.RawMessage(`"not-a-bool"`)
	if _, err := snapshotFromRaw(invalidType, "fixture"); err == nil {
		t.Fatal("invalid inventory type accepted")
	}
	badSummary := mapsClone(valid)
	badSummary["summary"] = json.RawMessage(`{"repositories":0}`)
	if _, err := snapshotFromRaw(badSummary, "fixture"); err == nil {
		t.Fatal("incomplete summary accepted")
	}
	badExclusions := mapsClone(valid)
	badExclusions["excluded_paths"] = json.RawMessage(`null`)
	if _, err := snapshotFromRaw(badExclusions, "fixture"); err == nil {
		t.Fatal("null exclusions accepted")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "relative.json")
	data, err := json.Marshal(model.Inventory{SchemaVersion: "1.1.0", GeneratedAt: time.Now(), Roots: []string{"/root"}, Worktrees: []model.Worktree{}, Summary: model.Summary{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(original)
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Load("relative.json"); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitEmptyRootsWarnsButMissingRootsIsInvalid(t *testing.T) {
	when := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	data, err := json.Marshal(model.Inventory{SchemaVersion: "1.1.0", GeneratedAt: when, Roots: []string{}, Worktrees: []model.Worktree{}, Summary: model.Summary{}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "empty-roots.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(snapshot.Warnings, "\n"), "empty scan scope") {
		t.Fatalf("warnings=%v", snapshot.Warnings)
	}
	whitespacePath := filepath.Join(t.TempDir(), "whitespace-empty-roots.json")
	if err := os.WriteFile(whitespacePath, bytes.Replace(data, []byte(`"roots":[]`), []byte(`"roots": [ ]`), 1), 0o600); err != nil {
		t.Fatal(err)
	}
	whitespace, err := Load(whitespacePath)
	if err != nil || !strings.Contains(strings.Join(whitespace.Warnings, "\n"), "empty scan scope") {
		t.Fatalf("whitespace roots snapshot=%+v err=%v", whitespace, err)
	}
	missing := map[string]json.RawMessage{"schema_version": json.RawMessage(`"1.1.0"`)}
	if _, err := inventoryRaw(missing, "fixture"); err == nil {
		t.Fatal("missing roots accepted")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func mapsClone(value map[string]json.RawMessage) map[string]json.RawMessage {
	result := make(map[string]json.RawMessage, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

type shortWriter struct{}

func (shortWriter) Write(data []byte) (int, error) { return len(data) - 1, nil }
