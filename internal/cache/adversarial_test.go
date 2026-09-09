package cache

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestScanAdversarialMissingAndInvalidRootsRemainWarnings(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	for _, test := range []struct {
		name      string
		root      string
		threshold int64
		want      string
	}{
		{name: "missing root", root: missing, threshold: 1, want: "cache scan error"},
		{name: "negative threshold", root: missing, threshold: -1, want: "invalid threshold"},
	} {
		t.Run(test.name, func(t *testing.T) {
			warnings := Scan(context.Background(), test.root, test.threshold)
			if len(warnings) != 1 || !strings.Contains(warnings[0].Reason, test.want) {
				t.Fatalf("Scan(%q, %d) = %#v", test.root, test.threshold, warnings)
			}
		})
	}
}

func TestScanAdversarialThresholdBoundaryAndNestedSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires privileges on some Windows hosts")
	}
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "node_modules")
	if err := os.Mkdir(cacheRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "payload"), make([]byte, 8), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Scan(context.Background(), root, 8); len(got) != 1 || got[0].Bytes != 8 {
		t.Fatalf("exact threshold warnings = %#v, want one 8-byte warning", got)
	}
	if got := Scan(context.Background(), root, 9); len(got) != 0 {
		t.Fatalf("above threshold warnings = %#v, want none", got)
	}

	outside := t.TempDir()
	outsideCache := filepath.Join(outside, "node_modules")
	if err := os.Mkdir(outsideCache, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideCache, "secret"), make([]byte, 4096), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "nested-link")); err != nil {
		t.Fatal(err)
	}
	warnings := Scan(context.Background(), root, 1)
	for _, warning := range warnings {
		if strings.HasPrefix(warning.Path, outside) {
			t.Fatalf("symlink escape warning = %#v", warning)
		}
	}
}

func TestScanAdversarialUnreadableCacheIsAdvisory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("portable permission-denial setup is not available on Windows")
	}
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "node_modules")
	denied := filepath.Join(cacheRoot, "denied")
	if err := os.MkdirAll(denied, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(denied, "payload"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(denied, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(denied, 0o755) })

	for _, warning := range Scan(context.Background(), root, 1) {
		if warning.Reason == "cache size unavailable" {
			if warning.Error == "" {
				t.Fatalf("unreadable cache warning lacks error: %#v", warning)
			}
			return
		}
	}
	t.Skip("host filesystem permits reading chmod-0 test directory")
}
