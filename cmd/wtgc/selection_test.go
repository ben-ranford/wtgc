package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/gitx"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/report"
	"github.com/ben-ranford/wtgc/internal/testgit"
	"go.uber.org/goleak"
)

func TestSelectionConfirmerRequiresCompleteAnswerAndEscapesPaths(t *testing.T) {
	for _, answer := range []string{"y\n", "YES\n", "n\n", "\n", "", "yes"} {
		var output bytes.Buffer
		preview := app.SelectionPreview{Count: 2, Paths: []string{"/one\nspoof", "/two\x1b[31m"}, ReclaimableBytes: 1234}
		got := selectionConfirmer(context.Background(), strings.NewReader(answer), &output, true)(preview)
		if got != (answer == "y\n" || answer == "YES\n") {
			t.Fatalf("answer=%q got=%t", answer, got)
		}
		text := output.String()
		if !strings.Contains(text, "2 worktrees, 1234 reclaimable bytes") || !strings.Contains(text, "provider squash branches remain retained") || strings.Count(text, "[y/N]") != 1 {
			t.Fatalf("preview=%q", text)
		}
		for _, path := range preview.Paths {
			if !strings.Contains(text, report.SafeHumanText(path)) {
				t.Fatalf("unsafe preview=%q", text)
			}
		}
	}
}

func TestCleanSelectedCLIRealGit(t *testing.T) {
	for _, mode := range []string{"dry", "yes", "interactive", "decline", "EOF", "invalid"} {
		t.Run(mode, func(t *testing.T) {
			repo := testgit.NewRepository(t)
			a, b := repo.CreateMergedWorktree(t, "a"), repo.CreateMergedWorktree(t, "b")
			other := repo.CreateMergedWorktree(t, "unselected")
			args := []string{"clean", "--json", "--select", a, "--select", b}
			answer := "yes\n"
			switch mode {
			case "yes":
				args = append(args, "--yes")
			case "interactive", "decline", "EOF":
				args = append(args, "--interactive")
			case "invalid":
				args = append(args, "--yes", "--select", a)
			}
			if mode == "decline" {
				answer = "no\n"
			}
			if mode == "EOF" {
				answer = "yes"
			}
			args = append(args, repo.Root)
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), args, processIO{stdin: strings.NewReader(answer), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(gitx.New("git"), func() (string, error) { return repo.Path, nil }))
			wantCode := 0
			if mode == "invalid" {
				wantCode = 1
			}
			if code != wantCode {
				t.Fatalf("code=%d stderr=%s", code, &stderr)
			}
			var inv model.Inventory
			if err := json.Unmarshal(stdout.Bytes(), &inv); err != nil {
				t.Fatal(err)
			}
			wantRemoved := 0
			if mode == "yes" || mode == "interactive" {
				wantRemoved = 2
			}
			if inv.SchemaVersion != "1.1.0" || inv.Summary.Removed != wantRemoved {
				t.Fatalf("inventory=%+v", inv)
			}
			for _, path := range []string{a, b} {
				_, err := os.Stat(path)
				if wantRemoved > 0 && !os.IsNotExist(err) || wantRemoved == 0 && err != nil {
					t.Fatalf("path %s: %v", path, err)
				}
			}
			if _, err := os.Stat(other); err != nil {
				t.Fatal(err)
			}
			if mode == "interactive" || mode == "decline" || mode == "EOF" {
				if strings.Count(stderr.String(), "[y/N]") != 1 || !strings.Contains(stderr.String(), "2 worktrees") {
					t.Fatalf("prompt=%q", stderr.String())
				}
				canonicalA, err := filepath.EvalSymlinks(repo.Worktrees)
				if err != nil {
					t.Fatal(err)
				}
				var selectedBytes int64
				for _, item := range inv.Worktrees {
					if filepath.Clean(item.Path) == filepath.Join(canonicalA, "a") || filepath.Clean(item.Path) == filepath.Join(canonicalA, "b") {
						selectedBytes += item.DiskBytes
					}
				}
				if !strings.Contains(stderr.String(), fmt.Sprintf("%d reclaimable bytes", selectedBytes)) {
					t.Fatalf("wrong bytes preview=%s", &stderr)
				}
			} else if strings.Contains(stderr.String(), "[y/N]") {
				t.Fatal("unexpected confirmation")
			}
		})
	}
}

func TestSelectionConfirmerInterruptsReadWithoutLeaking(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	for _, mode := range []string{"before read", "during read", "close failure"} {
		canceledBeforeRead := mode == "before read"
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			reader, writer := io.Pipe()
			defer writer.Close()
			input := &confirmationPipe{PipeReader: reader, started: make(chan struct{})}
			if mode == "close failure" {
				input.closeErr = errors.New("input close failed")
			}
			var output bytes.Buffer
			done := make(chan bool, 1)
			t.Cleanup(func() { reader.Close() })
			if canceledBeforeRead {
				cancel()
			}
			go func() { done <- selectionConfirmer(ctx, input, &output, false)(app.SelectionPreview{Count: 1}) }()
			if !canceledBeforeRead {
				select {
				case <-input.started:
				case <-time.After(time.Second):
					t.Fatal("read did not start")
				}
				cancel()
			}
			select {
			case accepted := <-done:
				if accepted {
					t.Fatal("canceled prompt authorized removal")
				}
			case <-time.After(time.Second):
				reader.Close()
				<-done
				t.Fatal("cancellation left confirmation blocked")
			}
			if mode == "close failure" && !strings.Contains(output.String(), "input close failed") {
				t.Fatalf("lost close failure: %s", &output)
			}
			wantCloses := int32(1)
			if canceledBeforeRead {
				wantCloses = 0
			}
			if got := input.closes.Load(); got != wantCloses {
				t.Fatalf("Close calls=%d, want %d", got, wantCloses)
			}
		})
	}
}

func TestSelectionConfirmerDetachesCancellationAfterAnswer(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input := &confirmationPipe{PipeReader: nil, started: make(chan struct{}), answer: strings.NewReader("yes\n")}
	if !selectionConfirmer(ctx, input, io.Discard, false)(app.SelectionPreview{Count: 1}) {
		t.Fatal("complete answer rejected")
	}
	cancel()
	// AfterFunc registration must be stopped before the confirmer returns.
	if input.closes.Load() != 0 {
		t.Fatal("closed input after normal confirmation")
	}
}

type confirmationPipe struct {
	*io.PipeReader
	started  chan struct{}
	once     sync.Once
	closes   atomic.Int32
	answer   *strings.Reader
	closeErr error
}

func (p *confirmationPipe) Read(data []byte) (int, error) {
	p.once.Do(func() { close(p.started) })
	if p.answer != nil {
		return p.answer.Read(data)
	}
	return p.PipeReader.Read(data)
}
func (p *confirmationPipe) Close() error {
	p.closes.Add(1)
	if p.PipeReader != nil {
		return errors.Join(p.PipeReader.Close(), p.closeErr)
	}
	return p.closeErr
}

func TestSelectionConfirmationInputCapabilities(t *testing.T) {
	file, err := os.CreateTemp(t.TempDir(), "answers")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := requireInterruptibleSelectionInput(file); err != nil {
		t.Fatalf("regular file capability rejected: %v", err)
	}
	if _, err := file.WriteString("yes\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if !selectionConfirmer(context.Background(), file, io.Discard, false)(app.SelectionPreview{Count: 1}) {
		t.Fatal("regular file answer rejected")
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := requireInterruptibleSelectionInput(file); err == nil {
		t.Fatal("closed file accepted")
	}
	null, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer null.Close()
	var output bytes.Buffer
	if selectionConfirmer(context.Background(), null, &output, false)(app.SelectionPreview{Count: 1}) {
		t.Fatal("null device accepted confirmation")
	}
	if !strings.Contains(output.String(), "requires interruptible input") {
		t.Fatalf("missing capability failure: %s", &output)
	}
}
