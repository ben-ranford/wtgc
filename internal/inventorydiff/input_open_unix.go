//go:build !windows

package inventorydiff

import (
	"os"
	"syscall"
)

// openInput cannot block if a checked regular file is replaced with a FIFO
// between Lstat and open. The descriptor remains subject to post-open Stat.
func openInput(root *os.Root, name string) (*os.File, error) {
	return root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
