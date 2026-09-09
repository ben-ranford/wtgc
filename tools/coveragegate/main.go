// Command coveragegate enforces checked-in total and package coverage floors.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
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

func main() { os.Exit(run(os.Args[1:], os.Stderr)) }
func run(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("coveragegate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	profile := fs.String("coverprofile", "", "coverage profile")
	configPath := fs.String("config", ".ci/coverage-ratchet.json", "ratchet config")
	totalOut := fs.String("total-out", "", "total result JSON")
	packagesOut := fs.String("packages-out", "", "package result JSON")
	failuresOut := fs.String("package-failures-out", "", "failure result JSON")
	if e := fs.Parse(args); e != nil {
		return 2
	}
	if *profile == "" || *totalOut == "" || *packagesOut == "" || *failuresOut == "" {
		fmt.Fprintln(stderr, "--coverprofile, --total-out, --packages-out, and --package-failures-out are required")
		return 2
	}
	if e := check(*profile, *configPath, *totalOut, *packagesOut, *failuresOut); e != nil {
		fmt.Fprintf(stderr, "coverage ratchet failed: %v\n", e)
		return 1
	}
	fmt.Fprintln(stderr, "Coverage ratchet passed.")
	return 0
}
func check(profile, configPath, totalOut, packagesOut, failuresOut string) error {
	c, err := readConfig(configPath)
	if err != nil {
		return err
	}
	counts, err := readProfile(profile)
	if err != nil {
		return err
	}
	packages := map[string]float64{}
	var covered, total int64
	for p, v := range counts {
		pct := coverage(v)
		packages[p] = pct
		covered += v.covered
		total += v.total
	}
	totalPct := coverage(counter{covered, total})
	failures := []string{}
	if totalPct < c.TotalMin {
		failures = append(failures, fmt.Sprintf("total %.2f%% is below %.2f%%", totalPct, c.TotalMin))
	}
	names := make([]string, 0, len(packages))
	for p := range packages {
		names = append(names, p)
	}
	sort.Strings(names)
	for _, p := range names {
		floor := c.PackageMin
		if f, ok := c.Packages[p]; ok {
			floor = f
		}
		if packages[p] < floor {
			failures = append(failures, fmt.Sprintf("%s %.2f%% is below %.2f%%", p, packages[p], floor))
		}
	}
	for p := range c.Packages {
		if _, ok := packages[p]; !ok {
			failures = append(failures, fmt.Sprintf("configured package %s is absent from coverage profile", p))
		}
	}
	if err := writeJSON(totalOut, map[string]float64{"total": totalPct, "minimum": c.TotalMin}); err != nil {
		return err
	}
	if err := writeJSON(packagesOut, packages); err != nil {
		return err
	}
	if err := writeJSON(failuresOut, failures); err != nil {
		return err
	}
	if len(failures) > 0 {
		return fmt.Errorf("%s", strings.Join(failures, "; "))
	}
	return nil
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
	if !s.Scan() || !strings.HasPrefix(s.Text(), "mode:") {
		return nil, fmt.Errorf("invalid coverage profile header")
	}
	for s.Scan() {
		parts := strings.Fields(s.Text())
		if len(parts) != 3 {
			return nil, fmt.Errorf("invalid coverage line %q", s.Text())
		}
		colon := strings.Index(parts[0], ":")
		if colon < 1 {
			return nil, fmt.Errorf("invalid package path %q", parts[0])
		}
		pkg := filepath.Dir(parts[0][:colon])
		statements, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil || statements < 0 {
			return nil, fmt.Errorf("invalid statement count %q", parts[1])
		}
		hits, e := strconv.ParseInt(parts[2], 10, 64)
		if e != nil || hits < 0 {
			return nil, fmt.Errorf("invalid hit count %q", parts[2])
		}
		v := out[pkg]
		v.total += statements
		if hits > 0 {
			v.covered += statements
		}
		out[pkg] = v
	}
	if e := s.Err(); e != nil {
		return nil, e
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("coverage profile has no statements")
	}
	return out, nil
}
func coverage(v counter) float64 {
	if v.total == 0 {
		return 100
	}
	return float64(v.covered) * 100 / float64(v.total)
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
