//go:build darwin || linux

package main

import (
	"errors"
	"io"
	"os"
	"syscall"
)

// Inherited stdin is not registered with Go's poller. A nonblocking duplicate
// lets Close interrupt Read without abandoning a goroutine. File status flags
// are shared with the parent process, so restore them before returning.
func prepareSelectionInput(input io.Reader) (io.Reader, func() error, error) {
	file, ok := input.(*os.File)
	if !ok {
		return input, nil, nil
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
	return pollable, func() error {
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
	}, nil
}
