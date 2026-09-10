package main

import (
	"bytes"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsMissingAndInvalidInputs(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(nil, &stderr); code != 2 {
		t.Fatalf("missing flags exit = %d", code)
	}
	stderr.Reset()
	if code := run([]string{"--base", "x", "--head", "y", "--summary-out", "z", "--max-bytes-pct=-1"}, &stderr); code != 2 {
		t.Fatalf("negative threshold exit = %d", code)
	}
	for _, threshold := range []string{"NaN", "+Inf"} {
		stderr.Reset()
		if code := run([]string{"--base", "x", "--head", "y", "--summary-out", "z", "--max-bytes-pct=" + threshold}, &stderr); code != 2 {
			t.Fatalf("non-finite threshold %q exit = %d", threshold, code)
		}
	}
	if validThreshold(math.NaN()) || validThreshold(math.Inf(1)) || !validThreshold(0) {
		t.Fatal("threshold validity accepted a non-finite value")
	}
}

func TestCompareRequiresCompleteRepeatedSamples(t *testing.T) {
	b := fixture(t, "base", "BenchmarkRun-8 1 100 ns/op 100 B/op 10 allocs/op\n")
	h := fixture(t, "head", "BenchmarkRun-8 1 100 ns/op 100 B/op 10 allocs/op\n")
	err := compareSamples(b, h, 10, 10, 2, filepath.Join(t.TempDir(), "out.md"))
	if err == nil || !strings.Contains(err.Error(), "samples") {
		t.Fatalf("incomplete benchmark evidence err = %v", err)
	}
}

func TestParseUsesMedianForDuplicateSamples(t *testing.T) {
	p := fixture(t, "samples", "BenchmarkRun-8 1 100 ns/op 300 B/op 30 allocs/op\nBenchmarkRun-8 1 100 ns/op 100 B/op 10 allocs/op\nBenchmarkRun-8 1 200 ns/op 200 B/op 20 allocs/op\n")
	values, err := parse(p, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got := values["BenchmarkRun-8"]; got.bytes != 200 || got.allocs != 20 {
		t.Fatalf("median metric = %#v", got)
	}
}

func TestCompareRejectsUnwritableArtifact(t *testing.T) {
	b := fixture(t, "base", "BenchmarkRun-8 1 100 ns/op 100 B/op 10 allocs/op\n")
	h := fixture(t, "head", "BenchmarkRun-8 1 100 ns/op 100 B/op 10 allocs/op\n")
	blocker := fixture(t, "blocker", "not a directory")
	if err := compare(b, h, 1, 1, filepath.Join(blocker, "summary.md")); err == nil {
		t.Fatal("accepted unwritable summary path")
	}
}

func fixture(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if e := os.WriteFile(p, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestCompareAcceptsAllocationImprovement(t *testing.T) {
	b := fixture(t, "base", "BenchmarkRun-8 1 100 ns/op 100 B/op 10 allocs/op\n")
	h := fixture(t, "head", "BenchmarkRun-8 1 100 ns/op 90 B/op 9 allocs/op\n")
	if e := compare(b, h, 1, 1, filepath.Join(t.TempDir(), "out.md")); e != nil {
		t.Fatal(e)
	}
}
func TestCompareRejectsMissingAndRegressedMetrics(t *testing.T) {
	b := fixture(t, "base", "BenchmarkRun-8 1 100 ns/op 100 B/op 10 allocs/op\n")
	h := fixture(t, "head", "BenchmarkRun-8 1 100 ns/op 120 B/op 12 allocs/op\n")
	if e := compare(b, h, 10, 10, filepath.Join(t.TempDir(), "out.md")); e == nil || !strings.Contains(e.Error(), "bytes") {
		t.Fatalf("err=%v", e)
	}
	if compare(b, fixture(t, "none", "ok\n"), 10, 10, filepath.Join(t.TempDir(), "out.md")) == nil {
		t.Fatal("accepted malformed benchmark")
	}
}

func TestCompareWritesArtifactOnFailureAndHandlesZeroBaseline(t *testing.T) {
	b := fixture(t, "base", "BenchmarkRun-8 1 100 ns/op 0 B/op 0 allocs/op\n")
	h := fixture(t, "head", "BenchmarkRun-8 1 100 ns/op 1 B/op 1 allocs/op\n")
	summary := filepath.Join(t.TempDir(), "nested", "result.md")
	if err := compare(b, h, 10, 10, summary); err == nil {
		t.Fatal("accepted zero-baseline regression")
	}
	if data, err := os.ReadFile(summary); err != nil || !strings.Contains(string(data), "BenchmarkRun") {
		t.Fatalf("summary = %q, err = %v", data, err)
	}
}

func TestRunAndParseCoverErrorAndBoundaryPaths(t *testing.T) {
	root := t.TempDir()
	base := fixture(t, "base", "BenchmarkB-8 1 10 ns/op 10 B/op 1 allocs/op\n")
	head := fixture(t, "head", "BenchmarkB-8 1 10 ns/op 11 B/op 1 allocs/op\n")
	var stderr bytes.Buffer
	if code := run([]string{"--base", base, "--head", head, "--summary-out", filepath.Join(root, "summary.md"), "--max-bytes-pct", "10", "--max-allocs-pct", "0", "--min-samples", "1"}, &stderr); code != 0 {
		t.Fatalf("valid run=%d: %s", code, stderr.String())
	}
	if code := run([]string{"--base", base, "--head", head, "--summary-out", filepath.Join(root, "summary.md"), "--min-samples", "0"}, &stderr); code != 2 {
		t.Fatalf("invalid sample count=%d", code)
	}
	if code := run([]string{"--base", base, "--head", fixture(t, "missing", "BenchmarkOther-8 1 10 ns/op 1 B/op 1 allocs/op\n"), "--summary-out", filepath.Join(root, "summary.md")}, &stderr); code != 1 {
		t.Fatalf("missing candidate=%d", code)
	}
	for _, body := range []string{
		"BenchmarkB-8 1 10 ns/op nope B/op 1 allocs/op\n",
		"BenchmarkB-8 1 10 ns/op 1 B/op nope allocs/op\n",
		"not a benchmark\n",
	} {
		if _, err := parse(fixture(t, "bad", body), 1); err == nil {
			t.Fatalf("accepted malformed input %q", body)
		}
	}
	if _, err := parse(filepath.Join(root, "missing"), 1); err == nil {
		t.Fatal("accepted missing benchmark file")
	}
	if _, err := parse("\x00", 1); err == nil {
		t.Fatal("accepted invalid benchmark root")
	}
	if _, err := parse("/dev/null/benchmark.out", 1); err == nil {
		t.Fatal("accepted a non-directory benchmark root")
	}
	if err := compareSamples(base, head, 10, 0, 1, filepath.Join(root, "nested", "summary.md")); err != nil {
		t.Fatalf("compareSamples success: %v", err)
	}
	if err := compareSamples(fixture(t, "base-two", "BenchmarkA-8 1 10 ns/op 1 B/op 1 allocs/op\nBenchmarkB-8 1 10 ns/op 1 B/op 1 allocs/op\n"), fixture(t, "head-one", "BenchmarkA-8 1 10 ns/op 1 B/op 1 allocs/op\n"), 0, 0, 1, filepath.Join(root, "missing.md")); err == nil || !strings.Contains(err.Error(), "missing candidate") {
		t.Fatalf("missing candidate failure=%v", err)
	}
	if err := compare(base, head, 10, 0, root); err == nil {
		t.Fatal("compare accepted directory summary target")
	}
	if _, err := parse(fixture(t, "oversized", strings.Repeat("x", 70<<10)+"\n"), 1); err == nil {
		t.Fatal("parse accepted an oversized scanner token")
	}
	if got := percent(0, 0); got != 0 || percent(0, 1) != 100 || percent(10, 9) != -10 {
		t.Fatalf("unexpected percentages: %v", got)
	}
}

func TestRunRejectsFlagParsingFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"--not-a-flag"}, &stderr); code != 2 {
		t.Fatalf("flag parse exit = %d", code)
	}
}
