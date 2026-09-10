//go:build !darwin && !linux

package main

import "io"

// The caller verifies that native file input supports interruption or is a
// regular file; unsupported consoles/pipes fail closed before reading.
func prepareSelectionInput(input io.Reader) (io.Reader, func() error, error) {
	return input, nil, nil
}
