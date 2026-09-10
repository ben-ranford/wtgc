package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunRejectsMissingInputsAndReportsRegression(t *testing.T) {
	var stderr bytes.Buffer
	if code := run(nil, &stderr); code != 2 {
		t.Fatalf("missing flags exit = %d", code)
	}
	profile := file(t, "profile", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 0\n")
	config := file(t, "config", `{"total_min":100,"package_min":100}`)
	if code := run([]string{"--coverprofile", profile, "--config", config, "--total-out", filepath.Join(t.TempDir(), "t"), "--packages-out", filepath.Join(t.TempDir(), "p"), "--package-failures-out", filepath.Join(t.TempDir(), "f")}, &stderr); code != 1 {
		t.Fatalf("regression exit = %d", code)
	}
}

func file(t *testing.T, n, s string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), n)
	if e := os.WriteFile(p, []byte(s), 0600); e != nil {
		t.Fatal(e)
	}
	return p
}
func TestCheckWritesMachineReadableResults(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 1\n")
	cfg := file(t, "ratchet.json", `{"total_min":100,"package_min":100,"packages":{}}`)
	if e := check(profile, cfg, filepath.Join(root, "total.json"), filepath.Join(root, "packages.json"), filepath.Join(root, "failures.json")); e != nil {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(filepath.Join(root, "total.json")); e != nil || !strings.Contains(string(b), "100") {
		t.Fatalf("total=%q err=%v", b, e)
	}
}

func TestGateBoundaryAndRegressionProfiles(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 98 1\ngithub.com/acme/demo/b.go:1.1,1.2 2 0\n")
	paths := []string{filepath.Join(root, "total.json"), filepath.Join(root, "packages.json"), filepath.Join(root, "failures.json")}
	if err := check(profile, file(t, "pass.json", `{"total_min":98,"package_min":98}`), paths[0], paths[1], paths[2]); err != nil {
		t.Fatalf("exact 98 boundary rejected: %v", err)
	}
	if err := check(profile, file(t, "package-fail.json", `{"total_min":98,"package_min":99}`), paths[0], paths[1], paths[2]); err == nil || !strings.Contains(err.Error(), "demo") {
		t.Fatalf("package regression not reported: %v", err)
	}
	if err := check(profile, file(t, "total-fail.json", `{"total_min":100,"package_min":0}`), paths[0], paths[1], paths[2]); err == nil || !strings.Contains(err.Error(), "total") {
		t.Fatalf("isolated total regression not reported: %v", err)
	}
}

func TestGateRejectsRoundedUpMaxIntCoverage(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 9038904596117680290 1\ngithub.com/acme/demo/b.go:1.1,1.2 184467440737095517 0\n")
	cfg := file(t, "ratchet.json", `{"total_min":98,"package_min":98}`)
	err := check(profile, cfg, filepath.Join(root, "total.json"), filepath.Join(root, "packages.json"), filepath.Join(root, "failures.json"))
	if err == nil || !strings.Contains(err.Error(), "total") || !strings.Contains(err.Error(), "github.com/acme/demo") {
		t.Fatalf("rounded MaxInt coverage passed: %v", err)
	}
}

func TestGateComparesFractionalFloorsExactly(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, "total.json"), filepath.Join(root, "packages.json"), filepath.Join(root, "failures.json")}
	cfg := file(t, "ratchet.json", `{"total_min":98.1,"package_min":98.1}`)
	if err := check(file(t, "exact.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 981 1\ngithub.com/acme/demo/b.go:1.1,1.2 19 0\n"), cfg, paths[0], paths[1], paths[2]); err != nil {
		t.Fatalf("exact fractional floor rejected: %v", err)
	}
	err := check(file(t, "below.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 980999 1\ngithub.com/acme/demo/b.go:1.1,1.2 19001 0\n"), cfg, paths[0], paths[1], paths[2])
	if err == nil || !strings.Contains(err.Error(), "total") || !strings.Contains(err.Error(), "github.com/acme/demo") {
		t.Fatalf("just-below fractional floor passed: %v", err)
	}
}

func TestReadConfigRejectsUnrepresentableExactFloor(t *testing.T) {
	for _, body := range []string{
		`{"total_min":100.00000000000000001,"package_min":0}`,
		`{"total_min":0,"package_min":0,"packages":{"github.com/acme/demo":100.00000000000000001}}`,
		`{"total_min":0,"package_min":0,"packages":{"github.com/acme/demo":"98"}}`,
	} {
		if _, err := readConfig(file(t, "invalid-exact-floor.json", body)); err == nil {
			t.Fatalf("accepted invalid exact floor %s", body)
		}
	}
}

func TestReadConfigDefaultsMissingAndNullFloors(t *testing.T) {
	for _, body := range []string{
		`{}`,
		`{"total_min":null,"package_min":null,"packages":null}`,
	} {
		c, err := readConfig(file(t, "default-floor.json", body))
		if err != nil || c.TotalMin != 0 || c.PackageMin != 0 || len(c.Packages) != 0 || c.totalExact.Sign() != 0 || c.packageExact.Sign() != 0 {
			t.Fatalf("default config = %+v, %v", c, err)
		}
	}
}

func TestGateFailsClosedForMissingAndUnseenPackages(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/known/a.go:1.1,1.2 1 1\ngithub.com/acme/unseen/a.go:1.1,1.2 1 1\ngithub.com/acme/unseen/b.go:1.1,1.2 1 0\n")
	paths := []string{filepath.Join(root, "total.json"), filepath.Join(root, "packages.json"), filepath.Join(root, "failures.json")}
	err := check(profile, file(t, "ratchet.json", `{"total_min":0,"package_min":98,"packages":{"github.com/acme/known":1,"github.com/acme/missing":98}}`), paths[0], paths[1], paths[2])
	if err == nil || !strings.Contains(err.Error(), "unseen") || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("missing or unseen package accepted: %v", err)
	}
}

func TestConfiguredFloorCannotWeakenDefaultAndArtifactsAreStable(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/z/a.go:1.1,1.2 1 1\ngithub.com/acme/a/a.go:1.1,1.2 1 1\n")
	cfg := file(t, "ratchet.json", `{"total_min":100,"package_min":100,"packages":{"github.com/acme/z":1}}`)
	r, err := evaluate(profile, cfg)
	if err != nil || r.Packages["github.com/acme/z"].Minimum != 100 {
		t.Fatalf("weakened explicit floor: result=%+v err=%v", r, err)
	}
	var stderr bytes.Buffer
	if code := run([]string{"--coverprofile", profile, "--config", cfg, "--total-out", filepath.Join(root, "total.json"), "--packages-out", filepath.Join(root, "packages.json"), "--package-failures-out", filepath.Join(root, "failures.json")}, &stderr); code != 0 {
		t.Fatalf("run code=%d: %s", code, stderr.String())
	}
	if got := stderr.String(); strings.Index(got, "github.com/acme/a") > strings.Index(got, "github.com/acme/z") || !strings.Contains(got, "total:") {
		t.Fatalf("unstable diagnostics: %q", got)
	}
	b, err := os.ReadFile(filepath.Join(root, "packages.json"))
	if err != nil {
		t.Fatal(err)
	}
	var packages map[string]float64
	if err := json.Unmarshal(b, &packages); err != nil || packages["github.com/acme/z"] != 100 {
		t.Fatalf("invalid package artifact %s: %v", b, err)
	}
}

func TestRunFailsAfterWritingArtifactsForCoverageRegression(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 0\n")
	cfg := file(t, "ratchet.json", `{"total_min":100,"package_min":100}`)
	var stderr bytes.Buffer
	args := []string{"--coverprofile", profile, "--config", cfg, "--total-out", filepath.Join(root, "total.json"), "--packages-out", filepath.Join(root, "packages.json"), "--package-failures-out", filepath.Join(root, "failures.json")}
	if code := run(args, &stderr); code != 1 || !strings.Contains(stderr.String(), "below") {
		t.Fatalf("regression run = (%d, %q)", code, stderr.String())
	}
	if b, err := os.ReadFile(filepath.Join(root, "failures.json")); err != nil || !strings.Contains(string(b), "below") {
		t.Fatalf("failure artifact = %q, %v", b, err)
	}
}

func TestGateCoversFailClosedIOAndValidationPaths(t *testing.T) {
	root := t.TempDir()
	validProfile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 1\n")
	validConfig := file(t, "ratchet.json", `{"total_min":100,"package_min":90,"packages":{"github.com/acme/demo":100}}`)
	r, err := evaluate(validProfile, validConfig)
	if err != nil || r.Packages["github.com/acme/demo"].Minimum != 100 {
		t.Fatalf("explicit higher floor not applied: %+v, %v", r, err)
	}
	for _, args := range [][2]string{{validProfile, filepath.Join(root, "missing.json")}, {filepath.Join(root, "missing.out"), validConfig}} {
		if _, err := evaluate(args[0], args[1]); err == nil {
			t.Fatal("accepted missing input")
		}
	}
	if err := check(validProfile, filepath.Join(root, "missing.json"), filepath.Join(root, "total"), filepath.Join(root, "packages"), filepath.Join(root, "failures")); err == nil {
		t.Fatal("check accepted a missing config")
	}
	for _, body := range []string{
		`{"total_min":-1,"package_min":0}`,
		`{"total_min":0,"package_min":101}`,
	} {
		if _, err := readConfig(file(t, "bad-floor.json", body)); err == nil {
			t.Fatalf("accepted floor config %s", body)
		}
	}
	for _, body := range []string{
		"mode: atomic\n",
		"mode: atomic\nmissing-colon 1 1\n",
	} {
		if _, err := readProfile(file(t, "bad-profile", body)); err == nil {
			t.Fatalf("accepted profile %q", body)
		}
	}
	if err := writeJSON(filepath.Join(root, "unsupported.json"), make(chan int)); err == nil {
		t.Fatal("accepted unsupported JSON")
	}
	if _, err := readFile(filepath.Join(root, "missing", "config")); err == nil {
		t.Fatal("readFile accepted missing root")
	}
	if _, err := readProfile("\x00"); err == nil {
		t.Fatal("readProfile accepted invalid root path")
	}
	if _, err := readProfile("/dev/null/cover.out"); err == nil {
		t.Fatal("readProfile accepted a non-directory root")
	}
	blocker := file(t, "blocker", "not a directory")
	if err := writeArtifacts(filepath.Join(root, "total.json"), filepath.Join(blocker, "packages.json"), filepath.Join(root, "failures.json"), r); err == nil {
		t.Fatal("writeArtifacts accepted unwritable package output")
	}
}

func TestProfileAndArtifactSafetyRegressions(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 1\n")
	config := file(t, "config.json", `{"total_min":100,"package_min":100}`)
	outputs := []string{filepath.Join(root, "total.json"), filepath.Join(root, "packages.json"), filepath.Join(root, "failures.json")}
	for _, body := range []string{
		"mode: banana\n",
		"mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 0 0\n",
		"mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 9223372036854775807 1\ngithub.com/acme/demo/b.go:1.1,1.2 1 1\n",
	} {
		if _, err := readProfile(file(t, "invalid.out", body)); err == nil {
			t.Fatalf("accepted invalid profile %q", body)
		}
	}
	if _, err := evaluate(file(t, "overflow.out", "mode: atomic\ngithub.com/acme/a/a.go:1.1,1.2 9223372036854775807 1\ngithub.com/acme/b/b.go:1.1,1.2 1 1\n"), config); err == nil {
		t.Fatal("accepted cross-package overflow")
	}
	if _, err := evaluate(file(t, "total-overflow.out", "mode: atomic\ngithub.com/acme/a/a.go:1.1,1.2 9223372036854775807 0\ngithub.com/acme/b/b.go:1.1,1.2 1 0\n"), config); err == nil {
		t.Fatal("accepted cross-package total overflow")
	}
	if _, err := readProfile(file(t, "package-total-overflow.out", "mode: atomic\ngithub.com/acme/a/a.go:1.1,1.2 9223372036854775807 0\ngithub.com/acme/a/b.go:1.1,1.2 1 0\n")); err == nil {
		t.Fatal("accepted package total overflow")
	}
	if _, err := readProfile(file(t, "oversized.out", "mode: atomic\n"+strings.Repeat("x", 70<<10)+"\n")); err == nil {
		t.Fatal("accepted oversized profile row")
	}
	if _, err := readProfile(file(t, "count.out", "mode: count\ngithub.com/acme/demo/a.go:1.1,1.2 1 1\n")); err != nil {
		t.Fatalf("count profile rejected: %v", err)
	}
	if err := validateArtifactPaths(profile, config, outputs[0], outputs[1], outputs[2]); err != nil {
		t.Fatalf("valid artifacts rejected: %v", err)
	}
	for _, bad := range [][]string{{profile, outputs[1], outputs[2]}, {outputs[0], outputs[0], outputs[2]}} {
		if err := validateArtifactPaths(profile, config, bad[0], bad[1], bad[2]); err == nil {
			t.Fatal("artifact alias accepted")
		}
	}
}

func TestRunReplacesStaleArtifactsOnEvaluationFailureAndRecovery(t *testing.T) {
	root := t.TempDir()
	profile := filepath.Join(root, "cover.out")
	config := file(t, "config.json", `{"total_min":100,"package_min":100}`)
	total, packages, failures := filepath.Join(root, "total.json"), filepath.Join(root, "packages.json"), filepath.Join(root, "failures.json")
	writeProfile := func(body string) {
		t.Helper()
		if err := os.WriteFile(profile, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGate := func(want int) {
		t.Helper()
		var stderr bytes.Buffer
		if got := run([]string{"--coverprofile", profile, "--config", config, "--total-out", total, "--packages-out", packages, "--package-failures-out", failures}, &stderr); got != want {
			t.Fatalf("run=%d want=%d: %s", got, want, stderr.String())
		}
	}
	writeProfile("mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 1\n")
	runGate(0)
	writeProfile("mode: banana\n")
	runGate(1)
	var totalResult map[string]float64
	totalBody, err := os.ReadFile(total)
	if err != nil || json.Unmarshal(totalBody, &totalResult) != nil || totalResult["total"] != 0 {
		t.Fatalf("stale total artifact: %q, %v", totalBody, err)
	}
	packagesBody, err := os.ReadFile(packages)
	if err != nil || string(packagesBody) != "{}\n" {
		t.Fatalf("stale packages artifact: %q, %v", packagesBody, err)
	}
	body, err := os.ReadFile(failures)
	if err != nil || string(body) == "null\n" || !strings.Contains(string(body), "evaluation failed") {
		t.Fatalf("failure artifact=%q err=%v", body, err)
	}
	writeProfile("mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 1\n")
	runGate(0)
	body, err = os.ReadFile(failures)
	if err != nil || string(body) != "[]\n" {
		t.Fatalf("success failures=%q err=%v", body, err)
	}
}

func TestRunRejectsAliasesAndArtifactWriteFailure(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 1\n")
	config := file(t, "config.json", `{"total_min":100,"package_min":100}`)
	var stderr bytes.Buffer
	args := []string{"--coverprofile", profile, "--config", config, "--total-out", profile, "--packages-out", filepath.Join(root, "packages.json"), "--package-failures-out", filepath.Join(root, "failures.json")}
	if code := run(args, &stderr); code != 1 || !strings.Contains(stderr.String(), "aliases input") {
		t.Fatalf("alias run = (%d, %q)", code, stderr.String())
	}
	stderr.Reset()
	args = []string{"--coverprofile", profile, "--config", config, "--total-out", filepath.Join(root, "total.json"), "--packages-out", root, "--package-failures-out", filepath.Join(root, "failures.json")}
	if code := run(args, &stderr); code != 1 || !strings.Contains(stderr.String(), "is a directory") {
		t.Fatalf("write failure run = (%d, %q)", code, stderr.String())
	}
}

func TestRunRejectsAbsentCaseFoldedArtifactAliasesBeforeWrites(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 1\n")
	config := file(t, "config.json", `{"total_min":100,"package_min":100}`)
	total := filepath.Join(root, "total.json")
	packages := filepath.Join(root, "Total.json")
	failures := filepath.Join(root, "failures.json")
	var stderr bytes.Buffer
	code := run([]string{"--coverprofile", profile, "--config", config, "--total-out", total, "--packages-out", packages, "--package-failures-out", failures}, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "outputs alias each other") {
		t.Fatalf("case-folded alias run = (%d, %q)", code, stderr.String())
	}
	for _, path := range []string{total, packages, failures} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("artifact was written before alias rejection: %q, %v", path, err)
		}
	}
}

func TestProfilePathsAndPhysicalArtifactAliases(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/sub/file.go:1.1,1.2 1 1\n")
	config := file(t, "config.json", `{"total_min":100,"package_min":100}`)
	counts, err := readProfile(profile)
	if err != nil || counts["github.com/acme/demo/sub"].total != 1 {
		t.Fatalf("slash import path parsed incorrectly: %+v, %v", counts, err)
	}
	t.Run("symlink target", func(t *testing.T) {
		output := filepath.Join(root, "output.json")
		requireSymlink(t, os.Symlink(profile, output))
		if err := validateArtifactPaths(profile, config, output, filepath.Join(root, "packages.json"), filepath.Join(root, "failures.json")); err == nil {
			t.Fatal("accepted symlink alias to profile")
		}
	})
	hardlink := filepath.Join(root, "hardlink.json")
	if err := os.Link(config, hardlink); err != nil {
		t.Fatal(err)
	}
	if err := validateArtifactPaths(profile, config, filepath.Join(root, "total.json"), hardlink, filepath.Join(root, "failures.json")); err == nil {
		t.Fatal("accepted hardlink alias to config")
	}
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	parentTarget := filepath.Join(realDir, "same.json")
	if err := os.WriteFile(parentTarget, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("symlinked parent", func(t *testing.T) {
		linkDir := filepath.Join(root, "linked")
		requireSymlink(t, os.Symlink(realDir, linkDir))
		if err := validateArtifactPaths(profile, config, filepath.Join(linkDir, "same.json"), parentTarget, filepath.Join(root, "failures.json")); err == nil {
			t.Fatal("accepted symlinked parent alias")
		}
	})
	t.Run("broken symlink", func(t *testing.T) {
		broken := filepath.Join(root, "broken")
		requireSymlink(t, os.Symlink(filepath.Join(root, "missing"), broken))
		if _, err := physicalPath(broken); err == nil {
			t.Fatal("physical path accepted a broken symlink")
		}
	})
}

func TestPhysicalPathRejectsChildOfRegularFile(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "regular-file")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := physicalPath(filepath.Join(file, "child")); err == nil {
		t.Fatal("physical path accepted a regular-file parent")
	}
	if _, err := appendResolvedPath(file, file, []string{"child"}); err == nil {
		t.Fatal("resolved regular-file ancestor accepted a child suffix")
	}
	resolved, err := appendResolvedPath(root, root, []string{"child", "missing"})
	want := filepath.Join(root, "missing", "child")
	if err != nil || resolved != want {
		t.Fatalf("resolved directory ancestor = %q, %v; want %q", resolved, err, want)
	}
}

func requireSymlink(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	if runtime.GOOS == "windows" && (errors.Is(err, fs.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "privilege")) {
		t.Skipf("Windows symlink capability is unavailable: %v", err)
	}
	t.Fatal(err)
}

func TestRunRejectsFlagParsingFailure(t *testing.T) {
	var stderr bytes.Buffer
	if code := run([]string{"--not-a-flag"}, &stderr); code != 2 {
		t.Fatalf("flag parse exit = %d", code)
	}
}
func TestCheckRejectsRegressionAndMalformedProfile(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 0\n")
	cfg := file(t, "ratchet.json", `{"total_min":90,"package_min":90}`)
	if e := check(profile, cfg, filepath.Join(root, "t"), filepath.Join(root, "p"), filepath.Join(root, "f")); e == nil || !strings.Contains(e.Error(), "below") {
		t.Fatalf("err=%v", e)
	}
	if _, e := readProfile(file(t, "bad", "invalid\n")); e == nil {
		t.Fatal("accepted malformed profile")
	}
}

func TestReadConfigRejectsInvalidPackageFloor(t *testing.T) {
	for _, body := range []string{
		`{"total_min":80,"package_min":70,"packages":{"github.com/acme/demo":101}}`,
		`{"total_min":80,"package_min":70,"packages":{"github.com/acme/demo":-1}}`,
		`{`,
	} {
		if _, err := readConfig(file(t, "invalid.json", body)); err == nil {
			t.Fatalf("accepted invalid config %s", body)
		}
	}
}

func TestCheckRejectsMissingConfiguredPackageAndMalformedRows(t *testing.T) {
	root := t.TempDir()
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 1\n")
	cfg := file(t, "ratchet.json", `{"total_min":100,"package_min":100,"packages":{"github.com/acme/missing":90}}`)
	err := check(profile, cfg, filepath.Join(root, "t"), filepath.Join(root, "p"), filepath.Join(root, "f"))
	if err == nil || !strings.Contains(err.Error(), "absent") {
		t.Fatalf("missing configured package err = %v", err)
	}
	for _, body := range []string{
		"mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 nope 1\n",
		"mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 -1\n",
		"mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 1 extra\n",
	} {
		if _, err := readProfile(file(t, "bad-row", body)); err == nil {
			t.Fatalf("accepted malformed coverage row %q", body)
		}
	}
}

func TestCheckRejectsUnwritableMachineArtifacts(t *testing.T) {
	profile := file(t, "cover.out", "mode: atomic\ngithub.com/acme/demo/a.go:1.1,1.2 1 1\n")
	cfg := file(t, "ratchet.json", `{"total_min":100,"package_min":100}`)
	blocker := file(t, "blocker", "not a directory")
	err := check(profile, cfg, filepath.Join(blocker, "total.json"), filepath.Join(t.TempDir(), "packages.json"), filepath.Join(t.TempDir(), "failures.json"))
	if err == nil {
		t.Fatal("accepted unwritable total artifact")
	}
}
