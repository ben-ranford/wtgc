//go:build darwin || linux

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Inherited stdin is not registered with Go's poller. A nonblocking duplicate
// lets Close interrupt Read without abandoning a goroutine. File status flags
// are shared with the parent process, so restore them before returning.
func prepareSelectionInput(input io.Reader) (io.Reader, func() error, error) {
	file, ok := input.(*os.File)
	if !ok {
		return input, nil, nil
	}
	if isTerminal(file) {
		return openTerminalSelectionInput(file)
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	fd, flags := -1, uintptr(0)
	var setupErr error
	controlErr := raw.Control(func(original uintptr) {
		var errno syscall.Errno
		flags, _, errno = syscall.Syscall(syscall.SYS_FCNTL, original, syscall.F_GETFL, 0)
		if errno != 0 {
			setupErr = errno
			return
		}
		fd, setupErr = syscall.Dup(int(original))
		if setupErr == nil {
			setupErr = syscall.SetNonblock(fd, true)
		}
	})
	if err := errors.Join(controlErr, setupErr); err != nil {
		if fd >= 0 {
			err = errors.Join(err, syscall.Close(fd))
		}
		return nil, nil, err
	}
	pollable := os.NewFile(uintptr(fd), file.Name())
	release := func() error {
		closeErr := pollable.Close()
		if errors.Is(closeErr, os.ErrClosed) {
			closeErr = nil
		} // Already interrupted by cancellation.
		var restoreErr error
		controlErr := raw.Control(func(original uintptr) {
			_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, original, syscall.F_SETFL, flags)
			if errno != 0 {
				restoreErr = errno
			}
		})
		return errors.Join(closeErr, controlErr, restoreErr)
	}
	if err := requireInterruptibleSelectionInput(pollable); err != nil {
		return nil, nil, errors.Join(err, release())
	}
	return pollable, release, nil
}

// A terminal often shares one open file description between stdin, stdout and
// stderr. Reopening the supplied terminal gives the interruptible reader its
// own nonblocking state, so picker and confirmation output cannot inherit
// O_NONBLOCK and fail under a slow PTY consumer. It deliberately does not use
// /dev/tty: supplied terminal input can be valid without a controlling terminal
// and can differ from the controlling terminal.
func openTerminalSelectionInput(supplied *os.File) (io.Reader, func() error, error) {
	path, err := terminalPath(supplied)
	if err != nil {
		return nil, nil, err
	}
	return openResolvedTerminalSelectionInput(supplied, path)
}

func openResolvedTerminalSelectionInput(supplied *os.File, path string) (io.Reader, func() error, error) {
	terminal, err := openOwnedSelectionTerminal(path)
	if err != nil {
		return nil, nil, err
	}
	if err := sameTerminal(supplied, terminal); err != nil {
		return nil, nil, errors.Join(err, terminal.Close())
	}
	return prepareTerminalSelectionInput(terminal)
}

var openOwnedSelectionTerminal = func(path string) (*os.File, error) {
	name, err := selectionTerminalName(path)
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot("/dev")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NOCTTY|syscall.O_NONBLOCK, 0)
}

func selectionTerminalName(path string) (string, error) {
	name, err := filepath.Rel("/dev", path)
	if err != nil || !filepath.IsAbs(path) || name == "." || !filepath.IsLocal(name) {
		return "", fmt.Errorf("resolved selection terminal is outside /dev")
	}
	return name, nil
}

func sameTerminal(supplied, reopened *os.File) error {
	suppliedInfo, err := supplied.Stat()
	if err != nil {
		return err
	}
	reopenedInfo, err := reopened.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(suppliedInfo, reopenedInfo) {
		return fmt.Errorf("reopened selection terminal does not match supplied input")
	}
	return nil
}

func prepareTerminalSelectionInput(terminal *os.File) (io.Reader, func() error, error) {
	raw, err := terminal.SyscallConn()
	if err != nil {
		return nil, nil, errors.Join(err, terminal.Close())
	}
	var setupErr error
	if err := raw.Control(func(fd uintptr) { setupErr = syscall.SetNonblock(int(fd), true) }); err != nil || setupErr != nil {
		return nil, nil, errors.Join(err, setupErr, terminal.Close())
	}
	reader := &terminalSelectionInput{file: terminal, raw: raw, done: make(chan struct{})}
	return reader, reader.Close, nil
}

const terminalSelectionPollInterval = 25 * time.Millisecond

// terminalSelectionInput uses an owned nonblocking descriptor and raw.Control
// so it can wait for input without changing inherited terminal flags.
type terminalSelectionInput struct {
	file *os.File
	raw  syscall.RawConn
	done chan struct{}
	mu   sync.Mutex
	once sync.Once
}

func (input *terminalSelectionInput) Read(buffer []byte) (int, error) {
	for {
		input.mu.Lock()
		select {
		case <-input.done:
			input.mu.Unlock()
			return 0, os.ErrClosed
		default:
		}
		count := 0
		var readErr error
		controlErr := input.raw.Control(func(fd uintptr) { count, readErr = syscall.Read(int(fd), buffer) })
		input.mu.Unlock()
		if controlErr != nil {
			return 0, controlErr
		}
		if count == 0 && readErr == nil {
			return 0, io.EOF
		}
		if readErr != syscall.EAGAIN && readErr != syscall.EWOULDBLOCK && readErr != syscall.EINTR {
			return count, readErr
		}
		select {
		case <-input.done:
			return 0, os.ErrClosed
		case <-time.After(terminalSelectionPollInterval):
		}
	}
}

func (input *terminalSelectionInput) Close() (err error) {
	input.once.Do(func() {
		close(input.done)
		input.mu.Lock()
		err = closeSelectionInput(input.file)
		input.mu.Unlock()
	})
	return err
}

func closeSelectionInput(file *os.File) error {
	err := file.Close()
	if errors.Is(err, os.ErrClosed) {
		return nil
	}
	return err
}

func requireInterruptibleSelectionInput(file *os.File) error {
	deadline := time.Now()
	if err := file.SetReadDeadline(deadline); err == nil {
		if clearErr := file.SetReadDeadline(time.Time{}); clearErr == nil {
			return nil
		} else {
			return clearErr
		}
	}
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("selected confirmation requires interruptible input on this platform")
	}
	return nil
}
