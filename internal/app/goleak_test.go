package app

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	if os.Getenv("WTGC_GOLEAK") == "1" {
		goleak.VerifyTestMain(m)
		return
	}
	os.Exit(m.Run())
}

func TestGoleakDetectsLeakedGoroutine(t *testing.T) {
	if os.Getenv("WTGC_GOLEAK_HELPER") == "1" {
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestGoleakLeakHelper$")
	cmd.Env = append(os.Environ(), "WTGC_GOLEAK=1", "WTGC_GOLEAK_HELPER=1")
	output, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "goleak") {
		t.Fatalf("leak detector result = %v, output = %s", err, output)
	}
}

func TestGoleakLeakHelper(t *testing.T) {
	if os.Getenv("WTGC_GOLEAK_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	go func() { select {} }()
}
