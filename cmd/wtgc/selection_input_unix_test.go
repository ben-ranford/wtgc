//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/app"
	"go.uber.org/goleak"
)

func TestSelectionInputRestoresOriginalMode(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	for _, nonblocking := range []bool{false, true} {
		for _, canceled := range []bool{false, true} {
			reader, writer := pipeWithMode(t, nonblocking)
			before := selectionInputFlags(t, reader)
			ctx, cancel := context.WithCancel(context.Background())
			var output bytes.Buffer
			sink := io.Writer(&output)
			if canceled {
				sink = confirmationWriteFunc(func(p []byte) (int, error) {
					if bytes.Contains(p, []byte("[y/N]")) {
						cancel()
					}
					return output.Write(p)
				})
			} else if _, err := writer.Write([]byte("yes\n")); err != nil {
				t.Fatal(err)
			}
			accepted := selectionConfirmer(ctx, reader, sink, false)(app.SelectionPreview{Count: 1})
			cancel()
			if accepted == canceled {
				t.Fatalf("nonblocking=%t canceled=%t accepted=%t output=%s", nonblocking, canceled, accepted, &output)
			}
			if got := selectionInputFlags(t, reader); got != before {
				t.Fatalf("input flags changed: %d -> %d", before, got)
			}
			if _, err := writer.Write([]byte("still open\n")); err != nil {
				t.Fatalf("closed original input: %v", err)
			}
			reader.Close()
			writer.Close()
		}
	}
}

func TestSelectionInputRejectsUnavailableDescriptor(t *testing.T) {
	closed, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	defer writer.Close()
	for _, input := range []*os.File{nil, closed} {
		var output bytes.Buffer
		if selectionConfirmer(context.Background(), input, &output, false)(app.SelectionPreview{Count: 1}) {
			t.Fatal("invalid descriptor authorized cleanup")
		}
		if !strings.Contains(output.String(), "prepare selected confirmation") {
			t.Fatalf("lost preparation error: %s", &output)
		}
	}
}

func TestSelectionInputRestoreFailureRefusesConfirmation(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	defer writer.Close()
	if _, err := writer.Write([]byte("yes\n")); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	closed := false
	sink := confirmationWriteFunc(func(p []byte) (int, error) {
		if !closed {
			closed = true
			reader.Close()
		}
		return output.Write(p)
	})
	if selectionConfirmer(context.Background(), reader, sink, false)(app.SelectionPreview{Count: 1}) {
		t.Fatal("input restoration failure authorized cleanup")
	}
	if !strings.Contains(output.String(), "restore selected confirmation input") {
		t.Fatalf("lost restoration error: %s", &output)
	}
}

func TestPreparedSelectionInputIsPollable(t *testing.T) {
	reader, writer := pipeWithMode(t, false)
	defer reader.Close()
	defer writer.Close()
	input, release, err := prepareSelectionInput(reader)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	pollable := input.(*os.File)
	if err := pollable.SetReadDeadline(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := pollable.Read(make([]byte, 1)); !os.IsTimeout(err) {
		t.Fatalf("read did not honor deadline: %v", err)
	}
}

func pipeWithMode(t *testing.T, nonblocking bool) (*os.File, *os.File) {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close(); writer.Close() })
	raw, err := reader.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var modeErr error
	if err := raw.Control(func(fd uintptr) { modeErr = syscall.SetNonblock(int(fd), nonblocking) }); err != nil {
		t.Fatal(err)
	}
	if modeErr != nil {
		t.Fatal(modeErr)
	}
	return reader, writer
}

func selectionInputFlags(t *testing.T, file *os.File) uintptr {
	t.Helper()
	raw, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var flags uintptr
	var errno syscall.Errno
	if err := raw.Control(func(fd uintptr) { flags, _, errno = syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFL, 0) }); err != nil {
		t.Fatal(err)
	}
	if errno != 0 {
		t.Fatal(errno)
	}
	return flags
}

type confirmationWriteFunc func([]byte) (int, error)

func (f confirmationWriteFunc) Write(p []byte) (int, error) { return f(p) }

func TestSelectionInputDetectsExternallyClosedDescriptor(t *testing.T) {
	for _, duringRestore := range []bool{false, true} {
		file, err := os.CreateTemp(t.TempDir(), "input")
		if err != nil {
			t.Fatal(err)
		}
		raw, err := file.SyscallConn()
		if err != nil {
			t.Fatal(err)
		}
		var release func() error
		if duringRestore {
			_, release, err = prepareSelectionInput(file)
			if err != nil {
				t.Fatal(err)
			}
		}
		// Simulate an inherited/native descriptor being invalidated outside os.File.
		// Do not allocate another descriptor until the stale File has been closed.
		var closeErr error
		if err := raw.Control(func(fd uintptr) { closeErr = syscall.Close(int(fd)) }); err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if duringRestore {
			err = release()
		} else {
			_, _, err = prepareSelectionInput(file)
		}
		file.Close()
		if err == nil {
			t.Fatalf("externally closed descriptor accepted (restore=%t)", duringRestore)
		}
	}
}

func TestSelectionInputCapabilityRegularAndClosed(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "answers")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := requireInterruptibleSelectionInput(file); err != nil {
		t.Fatalf("finite regular input rejected: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := requireInterruptibleSelectionInput(file); err == nil {
		t.Fatal("closed input accepted")
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	before := selectionInputFlags(t, null)
	if _, _, err := prepareSelectionInput(null); err == nil {
		t.Fatal("nonpollable device accepted")
	}
	if got := selectionInputFlags(t, null); got != before {
		t.Fatalf("rejected input flags changed: %d -> %d", before, got)
	}
}
