//go:build !darwin && !linux && !windows

package main

import "io"

// Other targets retain their native input behavior.
func prepareSelectionInput(input io.Reader) (io.Reader, func() error, error) {
	return input, nil, nil
}
