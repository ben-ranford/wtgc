//go:build windows

package gitx

import (
	"os/exec"
	"time"
)

const commandWaitDelay = 2 * time.Second

// Windows falls back to CommandContext's direct-process termination. WaitDelay
// still bounds inherited pipes if a child process outlives Git.
func configureCommand(cmd *exec.Cmd) {
	cmd.WaitDelay = commandWaitDelay
}
