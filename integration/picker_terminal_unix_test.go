//go:build darwin || linux

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestPickBinaryRequiresPTYAndRejectsPipesAndDevices(t *testing.T) {
	repo := newRepository(t)
	worktree := repo.CreateMergedWorktree(t, "selected")
	binary := wtgcBinary(t)
	for _, test := range []struct {
		name  string
		stdin io.Reader
	}{
		{name: "pipe", stdin: strings.NewReader("\n")},
		{name: "device", stdin: openNullInput(t)},
	} {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command(binary, "clean", "--pick", repo.Root)
			cmd.Dir, cmd.Stdin = repo.Path, test.stdin
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			var exitErr *exec.ExitError
			if !errors.As(err, &exitErr) || exitErr.ExitCode() != 2 || !strings.Contains(stderr.String(), "requires terminal") {
				t.Fatalf("err=%v stderr=%q", err, stderr.String())
			}
		})
	}
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is unavailable; no native PTY launcher")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := pickPTYCommand(ctx, binary, repo.Root)
	cmd.Dir, cmd.Stdin = repo.Path, strings.NewReader("\n")
	output, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "Numbered cleanup selection") {
		t.Fatalf("PTY picker err=%v output=%q", err, output)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("empty PTY selection mutated worktree: %v", err)
	}
}

func TestPickBinaryPTYWaitsForIdleInputAndInterrupt(t *testing.T) {
	if _, err := exec.LookPath("script"); err != nil {
		t.Skip("script is unavailable; no native PTY launcher")
	}
	binary := wtgcBinary(t)
	for _, test := range []struct {
		name     string
		input    []byte
		wantExit int
	}{
		{name: "empty_selection", input: []byte("\n"), wantExit: 0},
		{name: "interrupt", input: []byte{3}, wantExit: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			repo := newRepository(t)
			selected := repo.CreateMergedWorktree(t, "selected")
			input, writer := io.Pipe()
			defer input.Close()
			defer writer.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := pickPTYCommand(ctx, binary, repo.Root)
			cmd.Dir, cmd.Stdin = repo.Path, input
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			output := make(chan []byte, 1)
			go func() { text, _ := io.ReadAll(stdout); output <- text }()
			time.Sleep(time.Second)
			if _, err := writer.Write(test.input); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			err = cmd.Wait()
			text := <-output
			if ctx.Err() != nil {
				t.Fatalf("idle picker exceeded timeout: %v output=%q", ctx.Err(), text)
			}
			var exitErr *exec.ExitError
			gotExit := 0
			if errors.As(err, &exitErr) {
				gotExit = exitErr.ExitCode()
			} else if err != nil {
				t.Fatalf("wait PTY picker: %v", err)
			}
			if gotExit != test.wantExit || !strings.Contains(string(text), "Choose worktrees by number") || strings.Contains(string(text), "resource temporarily unavailable") {
				t.Fatalf("exit=%d output=%q", gotExit, text)
			}
			if _, err := os.Stat(selected); err != nil {
				t.Fatalf("idle picker mutated worktree: %v", err)
			}
		})
	}
}

func openNullInput(t *testing.T) *os.File {
	t.Helper()
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { file.Close() })
	return file
}

func pickPTYCommand(ctx context.Context, binary, root string) *exec.Cmd {
	if runtime.GOOS == "darwin" {
		return exec.CommandContext(ctx, "script", "-q", "/dev/null", binary, "clean", "--pick", root)
	}
	return exec.CommandContext(ctx, "script", "-q", "-e", "-c", shellQuote(binary)+" clean --pick "+shellQuote(root), "/dev/null")
}

func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }
