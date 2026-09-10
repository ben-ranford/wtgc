package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

func TestSelectedConfirmationSignalsExitWithOpenStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGINT/SIGTERM delivery via os.Process.Signal is not supported on Windows; not signal coverage")
	}
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			repo := newRepository(t)
			selected := repo.CreateMergedWorktree(t, "selected")
			other := repo.CreateMergedWorktree(t, "unselected")
			stale := repo.CreateMergedWorktree(t, "stale")
			if err := os.RemoveAll(stale); err != nil {
				t.Fatal(err)
			}
			before := repo.RegisteredWorktrees(t)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, wtgcBinary(t), "clean", "--select", selected, "--interactive", "--delete-branch", "--json", repo.Root)
			cmd.Dir = repo.Path
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close() // Remains open through the signal and bounded exit assertion.
			var stdout bytes.Buffer
			stderr := &selectionPromptWriter{ready: make(chan struct{})}
			cmd.Stdout, cmd.Stderr = &stdout, stderr
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			done, exited := make(chan error, 1), make(chan struct{})
			go func() { done <- cmd.Wait(); close(exited) }()
			t.Cleanup(func() { cancel(); <-exited })
			select {
			case <-stderr.ready:
			case err := <-done:
				t.Fatalf("exited before confirmation: %v\n%s", err, stderr.String())
			case <-ctx.Done():
				t.Fatal("confirmation prompt deadline exceeded")
			}
			if err := cmd.Process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
					t.Fatalf("exit=%v, want cancellation exit 1", err)
				}
			case <-time.After(5 * time.Second):
				cancel()
				<-exited
				t.Fatal("signal did not interrupt selected confirmation while stdin stayed open")
			}
			var inv model.Inventory
			if err := json.Unmarshal(stdout.Bytes(), &inv); err != nil {
				t.Fatalf("decode: %v\nstdout=%s\nstderr=%s", err, &stdout, stderr.String())
			}
			if inv.Summary.Removed != 0 || inv.Summary.Pruned != 0 || !strings.Contains(strings.Join(inv.Errors, " "), "canceled") {
				t.Fatalf("inv=%+v", inv)
			}
			for _, item := range inv.Worktrees {
				requireKept(t, item)
			}
			for _, path := range []string{selected, other} {
				if _, err := os.Stat(path); err != nil {
					t.Fatal(err)
				}
			}
			for _, branch := range []string{"selected", "unselected", "stale"} {
				if !repo.BranchExists(t, branch) {
					t.Fatalf("deleted branch %s", branch)
				}
			}
			if got := repo.RegisteredWorktrees(t); got != before {
				t.Fatalf("registrations changed:\nbefore=%s\nafter=%s", before, got)
			}
		})
	}
}

type selectionPromptWriter struct {
	buffer bytes.Buffer
	ready  chan struct{}
	once   sync.Once
}

func (w *selectionPromptWriter) Write(p []byte) (int, error) {
	n, err := w.buffer.Write(p)
	if strings.Contains(w.buffer.String(), "[y/N]") {
		w.once.Do(func() { close(w.ready) })
	}
	return n, err
}

func (w *selectionPromptWriter) String() string { return w.buffer.String() }
