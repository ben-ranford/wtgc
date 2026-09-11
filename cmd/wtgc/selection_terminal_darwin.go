//go:build darwin

package main

import "syscall"

const terminalGetAttr = uintptr(syscall.TIOCGETA)
