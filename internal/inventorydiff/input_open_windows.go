//go:build windows

package inventorydiff

import "os"

func openInput(root *os.Root, name string) (*os.File, error) { return root.Open(name) }
