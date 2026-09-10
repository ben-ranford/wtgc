package main

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/ben-ranford/wtgc/internal/app"
	"go.uber.org/goleak"
)

func TestSelectionWindowsBlockingPipeCancellation(t *testing.T) {
	if !selectionWindowsSubprocess(t) {
		return
	}
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	var handles [2]syscall.Handle
	if err := syscall.Pipe(handles[:]); err != nil {
		t.Fatal(err)
	}
	reader, writer := os.NewFile(uintptr(handles[0]), "blocking reader"), os.NewFile(uintptr(handles[1]), "blocking writer")
	defer reader.Close()
	defer writer.Close()
	// Unlike os.Pipe's overlapped handles, these native synchronous handles
	// have no deadlines. Close must still interrupt their ReadFile operation.
	if err := reader.SetReadDeadline(time.Now().Add(time.Second)); err == nil {
		t.Fatal("fixture unexpectedly supports deadlines")
	}
	assertSelectionInputCancellation(t, reader)
}

func assertSelectionInputCancellation(t *testing.T, input io.Reader) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := &selectionReadStarted{Reader: input, started: make(chan struct{})}
	// Prepare first so the wrapper does not hide the native *os.File type.
	prepared, release, err := prepareSelectionInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if release != nil {
		t.Fatal("unexpected Windows release")
	}
	started.Reader = prepared
	done := make(chan bool, 1)
	go func() { done <- selectionConfirmer(ctx, started, io.Discard, false)(app.SelectionPreview{Count: 1}) }()
	<-started.started
	time.Sleep(20 * time.Millisecond) // Allow the native blocking read to enter.
	cancel()
	select {
	case accepted := <-done:
		if accepted {
			t.Fatal("cancellation authorized cleanup")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native input failed to interrupt; subprocess/test timeout will contain the blocked reader")
	}
}

type selectionReadStarted struct {
	io.Reader
	started chan struct{}
	once    sync.Once
}

func (r *selectionReadStarted) Read(p []byte) (int, error) {
	r.once.Do(func() { close(r.started) })
	return r.Reader.Read(p)
}
func (r *selectionReadStarted) Close() error { return r.Reader.(io.Closer).Close() }

// Allocate a private console in a subprocess: never change the test runner's
// console attachment or inject input into a developer's existing console.
func TestSelectionWindowsConsole(t *testing.T) {
	if selectionWindowsSubprocess(t) {
		testSelectionWindowsConsole(t)
	}
}

func selectionWindowsSubprocess(t *testing.T) bool {
	t.Helper()
	if os.Getenv("WTGC_SELECTION_NATIVE_TEST") == t.Name() {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+t.Name()+"$", "-test.v")
	cmd.Env = append(os.Environ(), "WTGC_SELECTION_NATIVE_TEST="+t.Name())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native input subprocess: %v\n%s", err, output)
	}
	t.Logf("native input subprocess:\n%s", output)
	return false
}

func testSelectionWindowsConsole(t *testing.T) {
	kernel := syscall.NewLazyDLL("kernel32.dll")
	// A subprocess can inherit a console; detach before allocating its own.
	kernel.NewProc("FreeConsole").Call()
	if ok, _, err := kernel.NewProc("AllocConsole").Call(); ok == 0 {
		t.Fatalf("private native console unavailable (not console coverage): %v", err)
	}
	defer kernel.NewProc("FreeConsole").Call()
	file, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	for _, answer := range []string{"yes\r", "no\r", "\x1a\r"} {
		// KEY_EVENT_RECORD uses a 16-byte union following a DWORD-aligned type.
		type inputRecord struct {
			Kind                    uint16
			Padding                 uint16
			Down                    int32
			Repeat, Key, Scan, Char uint16
			State                   uint32
		}
		records := make([]inputRecord, 0, len(answer))
		for _, char := range answer {
			record := inputRecord{Kind: 1, Down: 1, Repeat: 1, Char: uint16(char)}
			if char == '\r' {
				record.Key = 0x0d
			}
			records = append(records, record)
		}
		var written uint32
		ok, _, err := kernel.NewProc("WriteConsoleInputW").Call(file.Fd(), uintptr(unsafe.Pointer(&records[0])), uintptr(len(records)), uintptr(unsafe.Pointer(&written)))
		if ok == 0 || int(written) != len(records) {
			t.Fatalf("console input: written=%d err=%v", written, err)
		}
		if got := selectionConfirmer(context.Background(), file, io.Discard, false)(app.SelectionPreview{Count: 1}); got != (answer == "yes\r") {
			t.Fatalf("answer=%q accepted=%t", answer, got)
		}
	}
	assertSelectionInputCancellation(t, file)
	prepared, _, err := prepareSelectionInput(file)
	if err != nil {
		t.Fatal(err)
	}
	console := prepared.(*selectionConsole)
	if n, err := console.Read(nil); n != 0 || err != nil {
		t.Fatalf("empty Read=%d,%v", n, err)
	}
	if err := console.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := console.Read(make([]byte, 1)); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed Read=%v", err)
	}
}
