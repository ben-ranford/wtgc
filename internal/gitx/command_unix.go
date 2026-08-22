//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package gitx

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

const commandWaitDelay = 2 * time.Second

// configureCommand gives every Git invocation its own process group so context
// cancellation stops helpers and hooks spawned beneath Git, not only Git itself.
func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = commandWaitDelay
}
