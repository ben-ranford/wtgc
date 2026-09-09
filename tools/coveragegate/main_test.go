package main

import (
	"bytes"
	"os"
	"path/filepath"
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
