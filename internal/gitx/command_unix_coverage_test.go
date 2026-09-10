//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package gitx

import (
	"context"
	"os/exec"
	"testing"
)

func TestCoverageConfigureCommandRejectsUnstartedCancellation(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "true")
	configureCommand(cmd)
	if err := cmd.Cancel(); err == nil {
		t.Fatal("unstarted command cancellation succeeded")
	}
}
