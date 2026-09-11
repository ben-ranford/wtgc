//go:build darwin

package main

import (
	"os"
	"reflect"
	"syscall"
)

func isTerminal(value any) bool {
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	raw, err := file.SyscallConn()
	if err != nil {
		return false
	}
	terminal := false
	if err := raw.Control(func(fd uintptr) {
		var state syscall.Termios
		_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, uintptr(syscall.TIOCGETA), reflect.ValueOf(&state).Pointer())
		terminal = errno == 0
	}); err != nil {
		return false
	}
	return terminal
}
