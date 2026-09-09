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
	if e := compare(b, fixture(t, "none", "ok\n"), 10, 10, filepath.Join(t.TempDir(), "out.md")); e == nil {
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
