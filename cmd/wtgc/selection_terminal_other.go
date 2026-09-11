//go:build !linux && !darwin && !windows

package main

func isTerminal(any) bool { return false }
