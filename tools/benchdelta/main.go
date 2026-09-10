// Command benchdelta compares stable Go benchmark allocation metrics.
package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

var benchLine = regexp.MustCompile(`^(Benchmark\S+)\s+\d+\s+\S+\s+ns/op\s+(\S+)\s+B/op\s+(\S+)\s+allocs/op`)

type metric struct{ bytes, allocs float64 }

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("benchdelta", flag.ContinueOnError)
	fs.SetOutput(stderr)
	base := fs.String("base", "", "baseline benchmark output")
	head := fs.String("head", "", "candidate benchmark output")
	maxBytes := fs.Float64("max-bytes-pct", 15, "maximum bytes/op regression percentage")
	maxAllocs := fs.Float64("max-allocs-pct", 10, "maximum allocs/op regression percentage")
	minSamples := fs.Int("min-samples", 1, "minimum benchmark samples per name")
	summary := fs.String("summary-out", "", "comparison markdown output")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *base == "" || *head == "" || *summary == "" || !validThreshold(*maxBytes) || !validThreshold(*maxAllocs) || *minSamples < 1 {
		fmt.Fprintln(stderr, "--base, --head, --summary-out, finite non-negative thresholds, and --min-samples >= 1 are required")
		return 2
	}
	err := compareSamples(*base, *head, *maxBytes, *maxAllocs, *minSamples, *summary)
	if err != nil {
		fmt.Fprintf(stderr, "memory benchmark gate failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(stderr, "Memory benchmark allocation comparison passed.")
	return 0
}
func validThreshold(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func parse(path string, minSamples int) (map[string]metric, error) {
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return nil, e
	}
	defer root.Close()
	f, e := root.Open(filepath.Base(path))
	if e != nil {
		return nil, e
	}
	defer f.Close()
	values := map[string][]metric{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		m := benchLine.FindStringSubmatch(s.Text())
		if len(m) == 0 {
			continue
		}
		b, err := strconv.ParseFloat(m[2], 64)
		if err != nil {
			return nil, fmt.Errorf("parse bytes/op for %s: %w", m[1], err)
		}
		a, err := strconv.ParseFloat(m[3], 64)
		if err != nil {
			return nil, fmt.Errorf("parse allocs/op for %s: %w", m[1], err)
		}
		values[m[1]] = append(values[m[1]], metric{b, a})
	}
	if e := s.Err(); e != nil {
		return nil, e
	}
	out := map[string]metric{}
	for n, v := range values {
		if len(v) < minSamples {
			return nil, fmt.Errorf("%s has %d benchmark samples, need at least %d", n, len(v), minSamples)
		}
		sort.Slice(v, func(i, j int) bool { return v[i].bytes < v[j].bytes })
		b := v[len(v)/2].bytes
		sort.Slice(v, func(i, j int) bool { return v[i].allocs < v[j].allocs })
		out[n] = metric{b, v[len(v)/2].allocs}
	}
	if len(out) == 0 {
		return nil, errors.New("no benchmark allocation lines found")
	}
	return out, nil
}
func compare(basePath, headPath string, maxBytes, maxAllocs float64, summary string) error {
	return compareSamples(basePath, headPath, maxBytes, maxAllocs, 1, summary)
}

func compareSamples(basePath, headPath string, maxBytes, maxAllocs float64, minSamples int, summary string) error {
	base, e := parse(basePath, minSamples)
	if e != nil {
		return fmt.Errorf("parse baseline: %w", e)
	}
	head, e := parse(headPath, minSamples)
	if e != nil {
		return fmt.Errorf("parse candidate: %w", e)
	}
	names := make([]string, 0, len(base))
	for n := range base {
		names = append(names, n)
	}
	sort.Strings(names)
	var lines []string
	lines = append(lines, "# Memory benchmark comparison", "", "| Benchmark | bytes/op change | allocs/op change |", "|---|---:|---:|")
	var failures []string
	for _, n := range names {
		h, ok := head[n]
		if !ok {
			failures = append(failures, n+": missing candidate benchmark")
			continue
		}
		b := base[n]
		bp := percent(b.bytes, h.bytes)
		ap := percent(b.allocs, h.allocs)
		lines = append(lines, fmt.Sprintf("| %s | %.2f%% | %.2f%% |", n, bp, ap))
		if bp > maxBytes || ap > maxAllocs {
			failures = append(failures, fmt.Sprintf("%s: bytes %.2f%% (max %.2f%%), allocs %.2f%% (max %.2f%%)", n, bp, maxBytes, ap, maxAllocs))
		}
	}
	if e := os.MkdirAll(filepath.Dir(summary), 0o750); e != nil {
		return fmt.Errorf("create summary directory: %w", e)
	}
	if e := os.WriteFile(summary, []byte(strings.Join(lines, "\n")+"\n"), 0o600); e != nil {
		return fmt.Errorf("write summary: %w", e)
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}
func percent(base, head float64) float64 {
	if base == 0 {
		if head == 0 {
			return 0
		}
		return 100
	}
	return (head - base) * 100 / base
}
