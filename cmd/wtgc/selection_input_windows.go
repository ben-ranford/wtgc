package main

import (
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
)

func prepareSelectionInput(input io.Reader) (io.Reader, func() error, error) {
	file, ok := input.(*os.File)
	if !ok {
		return input, nil, nil
	}
	if _, err := file.Stat(); err != nil {
		return nil, nil, err
	}
	var mode uint32
	if syscall.GetConsoleMode(syscall.Handle(file.Fd()), &mode) != nil {
		// Go's Windows pipe Close uses CancelIoEx even without deadline support.
		return input, nil, nil
	}
	return &selectionConsole{file: file}, nil, nil
}

// Console files are always blocking in Go. Unlike pipe Close, console Close
// does not cancel ReadConsole. Read it directly so a zero-character canceled
// read cannot loop inside os.File.Read and start another blocking read.
// The command owns a single reader; only Close runs concurrently with Read.
type selectionConsole struct {
	file    *os.File
	mu      sync.Mutex
	closed  bool
	reading chan struct{}
	pending []byte
}

func (c *selectionConsole) Read(p []byte) (int, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return 0, os.ErrClosed
	}
	if len(p) == 0 {
		c.mu.Unlock()
		return 0, nil
	}
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		c.mu.Unlock()
		return n, nil
	}
	done := make(chan struct{})
	c.reading = done
	c.mu.Unlock()
	var chars [256]uint16
	var count uint32
	err := syscall.ReadConsole(syscall.Handle(c.file.Fd()), &chars[0], uint32(len(chars)), &count, nil)
	c.mu.Lock()
	defer c.mu.Unlock()
	close(done)
	c.reading = nil
	if c.closed {
		return 0, os.ErrClosed
	}
	if err != nil {
		return 0, err
	}
	if count == 0 || chars[0] == 0x1a {
		return 0, io.EOF
	}
	c.pending = []byte(string(utf16.Decode(chars[:count])))
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *selectionConsole) Close() error {
	c.mu.Lock()
	c.closed = true
	done := c.reading
	c.mu.Unlock()
	if done == nil {
		return nil
	}
	// Cancellation can arrive between registering the read and entering the
	// syscall. Repeat until that read joins; ERROR_NOT_FOUND alone is not proof
	// that the reader has finished. No detached read/cancellation goroutine.
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	var cancelErr error
	for {
		err := syscall.CancelIoEx(syscall.Handle(c.file.Fd()), nil)
		if err != nil && !errors.Is(err, syscall.ERROR_NOT_FOUND) && cancelErr == nil {
			cancelErr = err
		}
		select {
		case <-done:
			return cancelErr
		case <-ticker.C:
		}
	}
}
