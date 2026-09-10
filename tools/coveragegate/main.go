// Command coveragegate enforces checked-in total and package coverage floors.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type config struct {
	TotalMin   float64            `json:"total_min"`
	PackageMin float64            `json:"package_min"`
	Packages   map[string]float64 `json:"packages"`
}
type counter struct{ covered, total int64 }
type packageResult struct {
	Coverage float64 `json:"coverage"`
	Minimum  float64 `json:"minimum"`
}
type result struct {
	Total    packageResult            `json:"total"`
	Packages map[string]packageResult `json:"packages"`
	Failures []string                 `json:"failures"`
}

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("coveragegate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("coverprofile", "", "coverage profile")
	configPath := fs.String("config", ".ci/coverage-ratchet.json", "ratchet config")
	totalOut := fs.String("total-out", "", "total result JSON")
	packagesOut := fs.String("packages-out", "", "package result JSON")
	failuresOut := fs.String("package-failures-out", "", "failure result JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *profile == "" || *totalOut == "" || *packagesOut == "" || *failuresOut == "" {
		fmt.Fprintln(stderr, "--coverprofile, --total-out, --packages-out, and --package-failures-out are required")
		return 2
	}
	if e := validateArtifactPaths(*profile, *configPath, *totalOut, *packagesOut, *failuresOut); e != nil {
		fmt.Fprintf(stderr, "coverage ratchet failed: %v\n", e)
		return 1
	}
	r, e := evaluate(*profile, *configPath)
	if e != nil {
		r = failedResult(*configPath, e)
	}
	writeErr := writeArtifacts(*totalOut, *packagesOut, *failuresOut, r)
	if writeErr != nil {
		e = writeErr
	}
	if e == nil && len(r.Failures) > 0 {
		e = fmt.Errorf("%s", strings.Join(r.Failures, "; "))
	}
	if e != nil {
		fmt.Fprintf(stderr, "coverage ratchet failed: %v\n", e)
		return 1
	}
	writeDiagnostics(stderr, r)
	fmt.Fprintln(stderr, "Coverage ratchet passed.")
	return 0
}
func failedResult(configPath string, err error) result {
	r := result{Packages: map[string]packageResult{}, Failures: []string{fmt.Sprintf("evaluation failed: %v", err)}}
	if c, configErr := readConfig(configPath); configErr == nil {
		r.Total.Minimum = c.TotalMin
	}
	return r
}
func check(profile, configPath, totalOut, packagesOut, failuresOut string) error {
	r, err := evaluate(profile, configPath)
	if err != nil {
		return err
	}
	if err := writeArtifacts(totalOut, packagesOut, failuresOut, r); err != nil {
		return err
	}
	if len(r.Failures) > 0 {
		return fmt.Errorf("%s", strings.Join(r.Failures, "; "))
	}
	return nil
}
func evaluate(profile, configPath string) (result, error) {
	c, err := readConfig(configPath)
	if err != nil {
		return result{}, err
	}
	counts, err := readProfile(profile)
	if err != nil {
		return result{}, err
	}
	r := result{Packages: map[string]packageResult{}, Failures: []string{}}
	var covered, total int64
	for p, v := range counts {
		pct := coverage(v)
		floor := c.PackageMin
		if configured, ok := c.Packages[p]; ok && configured > floor {
			floor = configured
		}
		r.Packages[p] = packageResult{Coverage: pct, Minimum: floor}
		if err := addCounter(&covered, v.covered); err != nil {
			return result{}, err
		}
		if err := addCounter(&total, v.total); err != nil {
			return result{}, err
		}
	}
	totalPct := coverage(counter{covered, total})
	r.Total = packageResult{Coverage: totalPct, Minimum: c.TotalMin}
	if totalPct < c.TotalMin {
		r.Failures = append(r.Failures, fmt.Sprintf("total %.2f%% is below %.2f%%", totalPct, c.TotalMin))
	}
	names := make([]string, 0, len(r.Packages))
	for p := range r.Packages {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		item := r.Packages[p]
		if item.Coverage < item.Minimum {
			r.Failures = append(r.Failures, fmt.Sprintf("%s %.2f%% is below %.2f%%", p, item.Coverage, item.Minimum))
		}
	}
	configuredNames := make([]string, 0, len(c.Packages))
	for p := range c.Packages {
		configuredNames = append(configuredNames, p)
	}
	sort.Strings(configuredNames)
	for _, p := range configuredNames {
		if _, ok := r.Packages[p]; !ok {
			r.Failures = append(r.Failures, fmt.Sprintf("configured package %s is absent from coverage profile", p))
		}
	}
	return r, nil
}
func addCounter(current *int64, add int64) error {
	if add > math.MaxInt64-*current {
		return fmt.Errorf("coverage statement count overflows int64")
	}
	*current += add
	return nil
}
func writeArtifacts(totalOut, packagesOut, failuresOut string, r result) error {
	if err := writeJSON(totalOut, map[string]float64{"total": r.Total.Coverage, "minimum": r.Total.Minimum}); err != nil {
		return err
	}
	packages := make(map[string]float64, len(r.Packages))
	for name, item := range r.Packages {
		packages[name] = item.Coverage
	}
	if err := writeJSON(packagesOut, packages); err != nil {
		return err
	}
	return writeJSON(failuresOut, r.Failures)
}
func writeDiagnostics(stderr io.Writer, r result) {
	fmt.Fprintf(stderr, "total: %.2f%% (minimum %.2f%%)\n", r.Total.Coverage, r.Total.Minimum)
	names := make([]string, 0, len(r.Packages))
	for name := range r.Packages {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		item := r.Packages[name]
		fmt.Fprintf(stderr, "%s: %.2f%% (minimum %.2f%%)\n", name, item.Coverage, item.Minimum)
	}
}
func readConfig(path string) (config, error) {
	b, e := readFile(path)
	if e != nil {
		return config{}, fmt.Errorf("read ratchet config: %w", e)
	}
	var c config
	if e = json.Unmarshal(b, &c); e != nil {
		return c, fmt.Errorf("parse ratchet config: %w", e)
	}
	if c.TotalMin < 0 || c.TotalMin > 100 || c.PackageMin < 0 || c.PackageMin > 100 {
		return c, fmt.Errorf("coverage floors must be between 0 and 100")
	}
	if c.Packages == nil {
		c.Packages = map[string]float64{}
	}
	for pkg, floor := range c.Packages {
		if floor < 0 || floor > 100 {
			return c, fmt.Errorf("coverage floor for %q must be between 0 and 100", pkg)
		}
	}
	return c, nil
}
func readProfile(path string) (map[string]counter, error) {
	root, e := os.OpenRoot(filepath.Dir(path))
	if e != nil {
		return nil, fmt.Errorf("open profile root: %w", e)
	}
	defer root.Close()
	f, e := root.Open(filepath.Base(path))
	if e != nil {
		return nil, fmt.Errorf("open profile: %w", e)
	}
	defer f.Close()
	out := map[string]counter{}
	s := bufio.NewScanner(f)
	if !s.Scan() || !validMode(strings.TrimSuffix(s.Text(), "\r")) {
		return nil, fmt.Errorf("invalid coverage profile header")
	}
	for s.Scan() {
		pkg, statements, hits, err := parseProfileRow(s.Text())
		if err != nil {
			return nil, err
		}
		v := out[pkg]
		if hits > 0 {
			if err := addCounter(&v.covered, statements); err != nil {
				return nil, err
			}
		}
		if err := addCounter(&v.total, statements); err != nil {
			return nil, err
		}
		out[pkg] = v
	}
	if e := s.Err(); e != nil {
		return nil, e
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("coverage profile has no statements")
	}
	for pkg, count := range out {
		if count.total == 0 {
			return nil, fmt.Errorf("coverage profile package %s has zero statements", pkg)
		}
	}
	return out, nil
}
func validMode(header string) bool {
	return header == "mode: set" || header == "mode: count" || header == "mode: atomic"
}

func parseProfileRow(row string) (string, int64, int64, error) {
	parts := strings.Fields(row)
	if len(parts) != 3 {
		return "", 0, 0, fmt.Errorf("invalid coverage line %q", row)
	}
	colon := strings.Index(parts[0], ":")
	if colon < 1 {
		return "", 0, 0, fmt.Errorf("invalid package path %q", parts[0])
	}
	statements, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || statements < 0 {
		return "", 0, 0, fmt.Errorf("invalid statement count %q", parts[1])
	}
	hits, err := strconv.ParseInt(parts[2], 10, 64)
	if err != nil || hits < 0 {
		return "", 0, 0, fmt.Errorf("invalid hit count %q", parts[2])
	}
	return pathpkg.Dir(parts[0][:colon]), statements, hits, nil
}
func coverage(v counter) float64 {
	return float64(v.covered) * 100 / float64(v.total)
}
func validateArtifactPaths(profile, configPath, totalOut, packagesOut, failuresOut string) error {
	paths := []string{profile, configPath, totalOut, packagesOut, failuresOut}
	canonical := make([]string, len(paths))
	for index, path := range paths {
		absolute, err := physicalPath(path)
		if err != nil {
			return fmt.Errorf("resolve artifact path: %w", err)
		}
		canonical[index] = absolute
	}
	for output := 2; output < len(canonical); output++ {
		for input := 0; input < 2; input++ {
			if samePhysicalPath(canonical[output], canonical[input]) {
				return fmt.Errorf("artifact output aliases input %q", paths[input])
			}
		}
		for other := output + 1; other < len(canonical); other++ {
			if samePhysicalPath(canonical[output], canonical[other]) {
				return fmt.Errorf("artifact outputs alias each other")
			}
		}
	}
	return nil
}
func physicalPath(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	current := absolute
	var suffix []string
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return resolved, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return absolute, nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}
func samePhysicalPath(first, second string) bool {
	if first == second {
		return true
	}
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}
func writeJSON(path string, v any) error {
	b, e := json.MarshalIndent(v, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0750); e != nil {
		return e
	}
	return os.WriteFile(path, append(b, '\n'), 0600)
}

func readFile(path string) ([]byte, error) {
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.ReadFile(filepath.Base(path))
}
