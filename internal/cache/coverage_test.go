package cache

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoverageScanInputFailuresAndThresholdBoundary(t *testing.T) {
	if got := Scan(context.Background(), t.TempDir(), -1); len(got) != 1 || got[0].Reason != "cache scan disabled: invalid threshold" {
		t.Fatalf("negative threshold=%+v", got)
	}
	if got := Scan(context.Background(), filepath.Join(t.TempDir(), "missing"), 0); len(got) != 1 || got[0].Reason != scanErrorReason {
		t.Fatalf("missing root=%+v", got)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Scan(context.Background(), file, 0); len(got) != 1 || got[0].Reason != scanErrorReason {
		t.Fatalf("file root=%+v", got)
	}
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "dist", "asset"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Scan(context.Background(), root, 2); len(got) != 0 {
		t.Fatalf("below threshold=%+v", got)
	}
	if got := Scan(context.Background(), root, 1); len(got) != 1 || got[0].Kind != "build output" {
		t.Fatalf("at threshold=%+v", got)
	}
	if err := os.Mkdir(filepath.Join(root, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "node_modules", "asset"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := Scan(context.Background(), root, 1); len(got) != 2 || got[0].Path >= got[1].Path {
		t.Fatalf("sorted warnings=%+v", got)
	}
}

func TestCoverageScannerEntryAndSizeErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "dist"), 0o755); err != nil {
		t.Fatal(err)
	}
	entry, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	s := cacheScanner{root: root, filesystem: os.DirFS(root), threshold: 0}
	assertScannerEntryBoundaries(t, entry[0], &s)
	assertSizeBoundaries(t, root)
}

func assertScannerEntryBoundaries(t *testing.T, entry fs.DirEntry, s *cacheScanner) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.entry(ctx, "dist", entry, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
	if err := s.entry(context.Background(), "bad", entry, errors.New("walk")); err != nil || len(s.out) != 1 {
		t.Fatalf("walk err=%v out=%+v", err, s.out)
	}
	if err := s.entry(context.Background(), ".git", fakeDirEntry{name: ".git", dir: true}, nil); err != filepath.SkipDir {
		t.Fatalf("git dir=%v", err)
	}
	if err := s.entry(context.Background(), ".git", fakeDirEntry{name: ".git"}, nil); err != nil {
		t.Fatalf("git file=%v", err)
	}
	if err := s.entry(context.Background(), "plain", fakeDirEntry{name: "plain"}, nil); err != nil {
		t.Fatalf("plain=%v", err)
	}
	if err := s.entry(context.Background(), "other", fakeDirEntry{name: "other", dir: true}, nil); err != nil {
		t.Fatalf("unknown=%v", err)
	}
	failed := cacheScanner{root: s.root, filesystem: errorFS{}, threshold: 0}
	if err := failed.entry(context.Background(), "node_modules", fakeDirEntry{name: "node_modules", dir: true}, nil); err != filepath.SkipDir || len(failed.out) != 1 || failed.out[0].Reason != "cache size unavailable" {
		t.Fatalf("size failure err=%v warnings=%+v", err, failed.out)
	}
	if _, err := size(context.Background(), errorFS{}, "."); err == nil {
		t.Fatal("error filesystem accepted")
	}
}

func assertSizeBoundaries(t *testing.T, root string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := size(ctx, os.DirFS(root), "."); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled size=%v", err)
	}
	for _, item := range []fs.DirEntry{
		fakeDirEntry{name: "linked-file", mode: fs.ModeSymlink},
		fakeDirEntry{name: "linked-dir", dir: true, mode: fs.ModeSymlink},
	} {
		bytes, err := size(context.Background(), entriesFS{entries: []fs.DirEntry{item}}, ".")
		if err != nil || bytes != 0 {
			t.Fatalf("symlink bytes=%d err=%v", bytes, err)
		}
	}
	if _, err := size(context.Background(), entriesFS{entries: []fs.DirEntry{fakeDirEntry{name: "broken-info"}}}, "."); err == nil || !strings.Contains(err.Error(), "info") {
		t.Fatalf("broken regular entry error=%v", err)
	}
}

type fakeDirEntry struct {
	name string
	dir  bool
	mode fs.FileMode
}

func (e fakeDirEntry) Name() string { return e.name }
func (e fakeDirEntry) IsDir() bool  { return e.dir }
func (e fakeDirEntry) Type() fs.FileMode {
	if e.mode != 0 {
		return e.mode
	}
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e fakeDirEntry) Info() (fs.FileInfo, error) { return nil, errors.New("info") }

type errorFS struct{}

func (errorFS) Open(string) (fs.File, error) { return nil, errors.New("open") }

type entriesFS struct{ entries []fs.DirEntry }

func (f entriesFS) Open(name string) (fs.File, error) {
	if name != "." {
		return nil, fs.ErrNotExist
	}
	return entriesFile{entries: f.entries}, nil
}

type entriesFile struct{ entries []fs.DirEntry }

func (entriesFile) Stat() (fs.FileInfo, error) { return fakeFileInfo{dir: true}, nil }
func (entriesFile) Read([]byte) (int, error)   { return 0, io.EOF }
func (entriesFile) Close() error               { return nil }
func (f entriesFile) ReadDir(int) ([]fs.DirEntry, error) {
	return f.entries, nil
}

type fakeFileInfo struct{ dir bool }

func (fakeFileInfo) Name() string { return "fake" }
func (fakeFileInfo) Size() int64  { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode {
	if f.dir {
		return fs.ModeDir
	}
	return 0
}
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool      { return f.dir }
func (fakeFileInfo) Sys() any           { return nil }
