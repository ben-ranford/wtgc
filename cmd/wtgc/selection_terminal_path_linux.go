//go:build linux

package main

import (
	"errors"
	"fmt"
	"os"
)

// terminalPath resolves the descriptor while RawConn.Control prevents its
// number from being reused by another goroutine.
func terminalPath(file *os.File) (string, error) {
	raw, err := file.SyscallConn()
	if err != nil {
		return "", err
	}
	var path string
	var pathErr error
	controlErr := raw.Control(func(fd uintptr) {
		path, pathErr = os.Readlink(fmt.Sprintf("/proc/self/fd/%d", fd))
	})
	if err := errors.Join(controlErr, pathErr); err != nil {
		return "", err
	}
	if path == "" {
		return "", fmt.Errorf("could not resolve supplied selection terminal")
	}
	return path, nil
}
