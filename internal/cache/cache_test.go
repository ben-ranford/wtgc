package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestScanFindsThresholdedCacheWithoutFollowingSymlink(t *testing.T) {
	root := t.TempDir()
	cacheRoot := filepath.Join(root, "node_modules")
	if err := os.MkdirAll(cacheRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "data"), make([]byte, 32), 0644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(outside, "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	warnings := Scan(context.Background(), root, 32)
	canonicalCacheRoot, err := filepath.EvalSymlinks(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || warnings[0].Path != canonicalCacheRoot || warnings[0].Bytes != 32 {
		t.Fatalf("warnings=%+v", warnings)
	}
}

func TestScanCancellationIsAdvisory(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := Scan(ctx, t.TempDir(), 0)
	if len(got) == 0 || got[0].Reason != "cache scan cancelled" {
		t.Fatalf("warnings=%+v", got)
	}
}

func TestScanSkipsGitAdministration(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "node_modules"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "node_modules", "data"), make([]byte, 64), 0644); err != nil {
		t.Fatal(err)
	}
	if got := Scan(context.Background(), root, 1); len(got) != 0 {
		t.Fatalf("administration cache leaked into findings: %+v", got)
	}
}

func TestScanContinuesPastLinkedWorktreeGitFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: elsewhere\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cacheRoot := filepath.Join(root, "node_modules")
	if err := os.MkdirAll(cacheRoot, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheRoot, "data"), make([]byte, 4), 0644); err != nil {
		t.Fatal(err)
	}
	if got := Scan(context.Background(), root, 4); len(got) != 1 || got[0].Kind != "node dependencies" {
		t.Fatalf("warnings=%+v", got)
	}
}
