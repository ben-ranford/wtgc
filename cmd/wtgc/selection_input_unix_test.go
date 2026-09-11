//go:build darwin || linux

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/app"
	"go.uber.org/goleak"
)

const terminalFlagsProbe = "WTGC_SELECTION_TERMINAL_FLAGS_PROBE"

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

func TestTerminalSelectionInputPreservesInheritedFlags(t *testing.T) {
	if os.Getenv(terminalFlagsProbe) == "1" {
		before := []uintptr{selectionInputFlags(t, os.Stdin), selectionInputFlags(t, os.Stdout), selectionInputFlags(t, os.Stderr)}
		reader, release, err := prepareSelectionInput(os.Stdin)
		if err != nil {
			t.Fatal(err)
		}
		owned, ok := reader.(*terminalSelectionInput)
		if !ok {
			t.Fatalf("prepared terminal input type=%T", reader)
		}
		if err := sameTerminal(os.Stdin, owned.file); err != nil {
			t.Fatalf("prepared input did not reopen supplied terminal: %v", err)
		}
		if err := release(); err != nil {
			t.Fatal(err)
		}
		after := []uintptr{selectionInputFlags(t, os.Stdin), selectionInputFlags(t, os.Stdout), selectionInputFlags(t, os.Stderr)}
		if !slices.Equal(before, after) {
			t.Fatalf("terminal flags changed: %v -> %v", before, after)
		}
		return
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is unavailable; no native PTY launcher")
	}
	args := []string{"-test.run=^TestTerminalSelectionInputPreservesInheritedFlags$"}
	if runtime.GOOS == "darwin" {
		args = append([]string{"-q", "/dev/null", "env", terminalFlagsProbe + "=1", os.Args[0]}, args...)
	} else {
		command := "env " + terminalFlagsProbe + "=1 " + quoteSelectionProbe(os.Args[0])
		for _, arg := range args {
			command += " " + quoteSelectionProbe(arg)
		}
		args = []string{"-q", "-c", command, "/dev/null"}
	}
	output, err := exec.Command("script", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("PTY flags probe: %v output=%q", err, output)
	}
}

func quoteSelectionProbe(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
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

func TestPickerRejectsRestorationFailure(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "answers")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("1\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	result := picker{input: file}.withInput(context.Background(), func(io.Reader) app.PickResult {
		if err := file.Close(); err != nil {
			return app.PickResult{Err: err}
		}
		return app.PickResult{Selected: []int{1}}
	})
	if result.Err == nil || !strings.Contains(result.Err.Error(), "restore numbered selection input") {
		t.Fatalf("result=%+v", result)
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

func TestTerminalSelectionInputCloseInterruptsNonblockingRead(t *testing.T) {
	reader, writer := pipeWithMode(t, true)
	defer writer.Close()
	raw, err := reader.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	input := &terminalSelectionInput{file: reader, raw: raw, done: make(chan struct{})}
	done := make(chan error, 1)
	go func() {
		_, err := input.Read(make([]byte, 1))
		done <- err
	}()
	time.Sleep(2 * terminalSelectionPollInterval)
	if err := input.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrClosed) {
			t.Fatalf("read error = %v", err)
		}
	case <-time.After(terminalSelectionPollInterval):
		t.Fatal("close did not interrupt terminal input read")
	}
}

func TestPrepareTerminalSelectionInputOwnsAndClosesReader(t *testing.T) {
	reader, writer := pipeWithMode(t, false)
	defer writer.Close()
	input, release, err := prepareTerminalSelectionInput(reader)
	if err != nil {
		t.Fatal(err)
	}
	terminal, ok := input.(*terminalSelectionInput)
	if !ok {
		t.Fatalf("input type=%T", input)
	}
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, 1)
	if count, err := terminal.Read(buffer); err != nil || count != 1 || string(buffer) != "x" {
		t.Fatalf("read count=%d buffer=%q err=%v", count, buffer, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Stat(); err == nil {
		t.Fatal("terminal input remained open after release")
	}
}

func TestPrepareTerminalSelectionInputRejectsClosedFile(t *testing.T) {
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := prepareTerminalSelectionInput(reader); err == nil {
		t.Fatal("closed terminal input accepted")
	}
}

func TestOpenTerminalSelectionInputReturnsUsableInputOrTTYError(t *testing.T) {
	input, release, err := openTerminalSelectionInput(os.Stdin)
	if err != nil {
		if input != nil || release != nil {
			t.Fatalf("failed opener returned input=%T release present=%t", input, release != nil)
		}
		return
	}
	if input == nil || release == nil {
		t.Fatalf("opened terminal input=%T release present=%t", input, release != nil)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenResolvedTerminalSelectionInputUsesOwnedFile(t *testing.T) {
	supplied, err := os.CreateTemp(t.TempDir(), "supplied")
	if err != nil {
		t.Fatal(err)
	}
	defer supplied.Close()
	path := supplied.Name()
	originalOpen := openOwnedSelectionTerminal
	t.Cleanup(func() { openOwnedSelectionTerminal = originalOpen })
	called := false
	openOwnedSelectionTerminal = func(gotPath string) (*os.File, error) {
		called = true
		if gotPath != path {
			t.Fatalf("path=%q want %q", gotPath, path)
		}
		return os.OpenFile(gotPath, os.O_RDONLY|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
	}
	input, release, err := openResolvedTerminalSelectionInput(supplied, path)
	if err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("owned terminal opener was not called")
	}
	if input == supplied {
		t.Fatal("selection input reused supplied descriptor")
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	if _, err := supplied.Stat(); err != nil {
		t.Fatalf("release closed supplied input: %v", err)
	}
}

func TestOpenResolvedTerminalSelectionInputRejectsIdentityMismatch(t *testing.T) {
	supplied, err := os.CreateTemp(t.TempDir(), "supplied")
	if err != nil {
		t.Fatal(err)
	}
	defer supplied.Close()
	different, err := os.CreateTemp(t.TempDir(), "different")
	if err != nil {
		t.Fatal(err)
	}
	different.Close()
	originalOpen := openOwnedSelectionTerminal
	t.Cleanup(func() { openOwnedSelectionTerminal = originalOpen })
	var reopened *os.File
	openOwnedSelectionTerminal = func(string) (*os.File, error) {
		reopened, err = os.Open(different.Name())
		return reopened, err
	}
	if _, _, err := openResolvedTerminalSelectionInput(supplied, supplied.Name()); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("identity mismatch err=%v", err)
	}
	if _, err := reopened.Stat(); err == nil {
		t.Fatal("identity mismatch left reopened terminal open")
	}
}

func TestTerminalPathResolvesSuppliedDescriptor(t *testing.T) {
	supplied, err := os.CreateTemp(t.TempDir(), "supplied")
	if err != nil {
		t.Fatal(err)
	}
	defer supplied.Close()
	path, err := terminalPath(supplied)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := sameTerminal(supplied, reopened); err != nil {
		t.Fatalf("resolved path did not reopen supplied descriptor: %v", err)
	}
}

func TestTerminalSelectionInputReportsEOFAndClosedControl(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "empty")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := file.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	input := &terminalSelectionInput{file: file, raw: raw, done: make(chan struct{})}
	if count, err := input.Read(make([]byte, 1)); count != 0 || !errors.Is(err, io.EOF) {
		t.Fatalf("empty read count=%d err=%v", count, err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := input.Read(make([]byte, 1)); err == nil {
		t.Fatal("closed descriptor read succeeded")
	}
	if err := closeSelectionInput(file); err != nil {
		t.Fatalf("closed input cleanup failed: %v", err)
	}

	closed := &terminalSelectionInput{done: make(chan struct{})}
	close(closed.done)
	if _, err := closed.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed reader error=%v", err)
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
