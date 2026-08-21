//go:build plan9 || js || wasip1

package gitx

import (
	"os/exec"
	"time"
)

const commandWaitDelay = 2 * time.Second

func configureCommand(cmd *exec.Cmd) {
	cmd.WaitDelay = commandWaitDelay
}
