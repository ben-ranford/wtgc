package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/gitx"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/report"
	"github.com/ben-ranford/wtgc/internal/testgit"
)

func TestSelectionConfirmerRequiresCompleteAnswerAndEscapesPaths(t *testing.T) {
	for _, answer := range []string{"y\n", "YES\n", "n\n", "\n", "", "yes"} {
		var output bytes.Buffer
		preview := app.SelectionPreview{Count: 2, Paths: []string{"/one\nspoof", "/two\x1b[31m"}, ReclaimableBytes: 1234}
		got := selectionConfirmer(strings.NewReader(answer), &output, true)(preview)
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
				var selectedBytes int64
				for _, item := range inv.Worktrees {
					canonicalA, errA := filepath.EvalSymlinks(repo.Worktrees)
					if errA != nil {
						t.Fatal(errA)
					}
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
