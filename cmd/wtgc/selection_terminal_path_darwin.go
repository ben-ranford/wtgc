//go:build darwin

package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"reflect"
	"syscall"
)

const terminalGetPath = 50 // F_GETPATH
const terminalPathMax = 1024

// terminalPath resolves the descriptor while RawConn.Control prevents its
// number from being reused by another goroutine.
func terminalPath(file *os.File) (string, error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return "", err
	}
	var value [terminalPathMax]byte
	var pathErr error
	controlErr := raw.Control(func(fd uintptr) {
		_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, terminalGetPath, reflect.ValueOf(&value[0]).Pointer())
		if errno != 0 {
			pathErr = errno
		}
	})
	if err := errors.Join(controlErr, pathErr); err != nil {
		return "", err
	}
	end := bytes.IndexByte(value[:], 0)
	if end <= 0 {
		return "", fmt.Errorf("could not resolve supplied selection terminal")
	}
	return string(value[:end]), nil
}
