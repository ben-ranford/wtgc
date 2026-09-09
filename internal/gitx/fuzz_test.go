package gitx

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func FuzzParseWorktreeListPorcelainZ(f *testing.F) {
	for _, seed := range readFuzzCorpus(f, "FuzzParseWorktreeListPorcelainZ") {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) { _, _ = ParseWorktreeListPorcelainZ(input) })
}

func FuzzDiscoverRootInput(f *testing.F) {
	for _, seed := range readFuzzCorpus(f, "FuzzDiscoverRootInput") {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		root := t.TempDir()
		name := filepath.Base(string(input))
		if name == "." || name == string(filepath.Separator) {
			name = "candidate"
		}
		_, _ = New("git").Discover(context.Background(), []string{filepath.Join(root, name)})
	})
}

func TestFuzzCorpusContract(t *testing.T) {
	for _, target := range []string{"FuzzParseWorktreeListPorcelainZ", "FuzzDiscoverRootInput"} {
		seeds := readFuzzCorpus(t, target)
		if len(seeds) == 0 {
			t.Fatalf("%s has no committed seeds", target)
		}
		for _, seed := range seeds {
			if target == "FuzzParseWorktreeListPorcelainZ" {
				_, _ = ParseWorktreeListPorcelainZ(seed)
			} else {
				_, _ = New("git").Discover(context.Background(), []string{filepath.Join(t.TempDir(), filepath.Base(string(seed)))})
			}
		}
	}
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "fuzz.yml"))
	if err != nil {
		t.Fatalf("read fuzz workflow: %v", err)
	}
	for _, target := range []string{"FuzzParseWorktreeListPorcelainZ", "FuzzDiscoverRootInput"} {
		if !strings.Contains(string(workflow), target) {
			t.Fatalf("fuzz workflow does not configure %s", target)
		}
	}
}

func readFuzzCorpus(t testing.TB, target string) [][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "fuzz", target, "*"))
	if err != nil {
		t.Fatal(err)
	}
	seeds := make([][]byte, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		value, err := parseFuzzCorpusSeed(data)
		if err != nil {
			t.Fatalf("invalid corpus seed %s", path)
		}
		seeds = append(seeds, []byte(value))
	}
	return seeds
}

func parseFuzzCorpusSeed(data []byte) (string, error) {
	normalized := strings.ReplaceAll(string(data), "\r\n", "\n")
	if strings.Contains(normalized, "\r") {
		return "", strconv.ErrSyntax
	}
	lines := strings.Split(strings.TrimSpace(normalized), "\n")
	if len(lines) != 2 || lines[0] != "go test fuzz v1" || !strings.HasPrefix(lines[1], "[]byte(") {
		return "", strconv.ErrSyntax
	}
	return strconv.Unquote(strings.TrimSuffix(strings.TrimPrefix(lines[1], "[]byte("), ")"))
}

func TestParseFuzzCorpusSeedAcceptsCRLFOnlyAsLineEnding(t *testing.T) {
	value, err := parseFuzzCorpusSeed([]byte("go test fuzz v1\r\n[]byte(\"fixture\")\r\n"))
	if err != nil || value != "fixture" {
		t.Fatalf("parse CRLF corpus seed = %q, %v", value, err)
	}
	if _, err := parseFuzzCorpusSeed([]byte("go test fuzz v1\r[]byte(\"fixture\")\n")); err == nil {
		t.Fatal("accepted malformed lone-CR corpus seed")
	}
}
