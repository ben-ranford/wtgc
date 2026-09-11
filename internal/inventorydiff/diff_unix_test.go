//go:build !windows

package inventorydiff

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLoadRejectsFIFOWithoutOpeningIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); !errors.Is(err, ErrFileRead) {
		t.Fatalf("FIFO error=%v", err)
	}
}

func TestOpenInputUsesNonBlockingDescriptorForFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replacement.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	file, err := openInput(root, filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().IsRegular() {
		t.Fatal("FIFO descriptor reported regular")
	}
}
