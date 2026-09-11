//go:build linux

package main

import "syscall"

const terminalGetAttr = uintptr(syscall.TCGETS)
