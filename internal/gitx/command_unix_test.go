//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package gitx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

const processLifecycleTimeout = 10 * time.Second

func TestRunCancellationKillsGitProcessGroup(t *testing.T) {
	t.Parallel()
	lockGitScriptTest(t)
	dir := t.TempDir()
	parentFile := filepath.Join(dir, "parent.pid")
	childFile := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "git")
	content := "#!/bin/sh\n" +
		"printf '%s' \"$$\" > " + quoteShell(parentFile) + "\n" +
		"sleep 60 &\n" +
		"child=$!\n" +
		"printf '%s' \"$child\" > " + quoteShell(childFile) + "\n" +
		"wait \"$child\"\n"
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	client := NewWithTimeout(script, 0)
	result := make(chan error, 1)
	go func() {
		_, err := client.List(ctx, model.Repository{PrimaryPath: dir, CommonDir: filepath.Join(dir, ".git")})
		result <- err
	}()
	waitForFile(t, parentFile)
	waitForFile(t, childFile)
	cancel()
	err := <-result
	if err == nil || !strings.Contains(err.Error(), "canceled") || !errors.Is(err, context.Canceled) {
		t.Fatalf("List error = %v, want context cancellation", err)
	}

	for _, pidFile := range []string{parentFile, childFile} {
		pid := readPID(t, pidFile)
		deadline := time.Now().Add(processLifecycleTimeout)
		for processExists(pid) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if processExists(pid) {
			t.Fatalf("process %d from %s survived command cancellation", pid, pidFile)
		}
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(processLifecycleTimeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stat %q: %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q", path)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pid file %q: %v", path, err)
	}
	pid, err := strconv.Atoi(string(data))
	if err != nil {
		t.Fatalf("parse pid %q: %v", data, err)
	}
	return pid
}

func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
