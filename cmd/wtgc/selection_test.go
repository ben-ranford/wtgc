package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/gitx"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/report"
	"github.com/ben-ranford/wtgc/internal/review"
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

func TestNumberedPickerParsingAndDisplay(t *testing.T) {
	preview := app.PickPreview{Rows: []app.PickRow{
		{Number: 1, Selectable: true, Worktree: model.Worktree{Repository: "/repo", Branch: "one", Path: "/worktrees/one", DiskBytes: 123}},
		{Worktree: model.Worktree{Repository: "/repo", Path: "/worktrees/unsafe\nspoof"}, Unavailable: "dirty"},
		{Number: 2, Selectable: true, Worktree: model.Worktree{Repository: "/repo", Branch: "two", Path: "/worktrees/two", DiskBytes: 456}},
	}}
	for _, test := range []struct {
		input string
		want  []int
		err   string
	}{
		{"1,2,1", []int{1, 2}, ""},
		{"1-2", []int{1, 2}, ""},
		{"", nil, ""},
		{"3", nil, "not selectable"},
		{"1,,2", nil, "empty item"},
		{"2-1", nil, "invalid range"},
	} {
		got, err := parsePickSelection(test.input, preview)
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("parsePickSelection(%q) err=%v", test.input, err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, test.want) {
			t.Fatalf("parsePickSelection(%q) = %v, %v", test.input, got, err)
		}
	}
	text := formatPickRows(preview)
	for _, want := range []string{"repository=/repo", "branch=one", "bytes=123", "path=/worktrees/one", "unavailable: dirty", `/worktrees/unsafe\nspoof`} {
		if !strings.Contains(text, want) {
			t.Fatalf("display missing %q: %q", want, text)
		}
	}
}

func TestNumberedPickerRetriesAndCancelsOnEOF(t *testing.T) {
	preview := app.PickPreview{Rows: []app.PickRow{{Number: 1, Selectable: true, Worktree: model.Worktree{Path: "/one"}}}}
	for _, test := range []struct {
		input string
		want  []int
	}{
		{"x\n1\n", []int{1}},
		{"", nil},
		{"\n", nil},
	} {
		var output bytes.Buffer
		result := numberedPicker(context.Background(), strings.NewReader(test.input), &output)(preview)
		if result.Err != nil || !reflect.DeepEqual(result.Selected, test.want) {
			t.Fatalf("input=%q result=%+v want=%v", test.input, result, test.want)
		}
		if test.input == "x\n1\n" && !strings.Contains(output.String(), "Invalid selection") {
			t.Fatalf("retry was not shown: %q", output.String())
		}
	}
}

func TestNumberedPickerAcceptsThreeHundredCandidatesWithContextAndBoundsInput(t *testing.T) {
	rows := make([]app.PickRow, 0, maxPickCandidates+1)
	for i := 1; i <= maxPickCandidates; i++ {
		rows = append(rows, app.PickRow{Number: i, Selectable: true, Worktree: model.Worktree{Path: fmt.Sprintf("/safe/%d", i)}})
	}
	rows = append(rows, app.PickRow{Worktree: model.Worktree{Path: "/kept"}, Unavailable: "kept"})
	var output bytes.Buffer
	input := strings.Repeat("9", 1025) + "\n300\n"
	result := numberedPicker(context.Background(), strings.NewReader(input), &output)(app.PickPreview{Rows: rows})
	if result.Err != nil || !reflect.DeepEqual(result.Selected, []int{300}) {
		t.Fatalf("result=%+v", result)
	}
	text := output.String()
	if strings.Contains(text, "more than 300") || !strings.Contains(text, "input is too long") || !strings.Contains(text, "  300  repository=") {
		t.Fatalf("picker did not retain 300 candidates safely: %q", text)
	}
}

func TestNumberedPickerReportsUnmeasuredBytesHonestly(t *testing.T) {
	measured := false
	text := formatPickRows(app.PickPreview{Rows: []app.PickRow{{Worktree: model.Worktree{Path: "/excluded", WorktreeDetails: &model.WorktreeDetails{DiskBytesMeasured: &measured}}}}})
	if !strings.Contains(text, "bytes=unmeasured") || strings.Contains(text, "bytes=0") {
		t.Fatalf("picker bytes=%q", text)
	}
}

func TestWritePickFieldHandlesNarrowLabels(t *testing.T) {
	var text strings.Builder
	writePickField(&text, strings.Repeat(" ", 81), "label=", "value")
	if !strings.HasSuffix(text.String(), "e\n") || strings.Count(text.String(), "\n") != len("value") {
		t.Fatalf("narrow field=%q", text.String())
	}
}

func TestAbsoluteExcludePathsRejectsEmptyAndResolvesRelative(t *testing.T) {
	if _, err := absoluteExcludePaths([]string{"  "}, "/base"); err == nil {
		t.Fatal("accepted an empty exclusion path")
	}
	paths, err := absoluteExcludePaths([]string{"relative", "/absolute/../excluded"}, "/base")
	if err != nil || !reflect.DeepEqual(paths, []string{"/base/relative", "/excluded"}) {
		t.Fatalf("paths=%q err=%v", paths, err)
	}
}

func TestNumberedPickerDistinguishesFailuresFromVoluntaryCancellation(t *testing.T) {
	preview := app.PickPreview{Rows: []app.PickRow{{Number: 1, Selectable: true}}}
	readFailure := errors.New("read failed")
	result := numberedPicker(context.Background(), pickerErrorReader{err: readFailure}, io.Discard)(preview)
	if !errors.Is(result.Err, readFailure) || result.Selected != nil {
		t.Fatalf("read result=%+v", result)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result = numberedPicker(ctx, strings.NewReader("\n"), io.Discard)(preview)
	if !errors.Is(result.Err, context.Canceled) || result.Selected != nil {
		t.Fatalf("canceled result=%+v", result)
	}
}

func TestPickerFailsClosedAtDisplayAndInputBoundaries(t *testing.T) {
	preview := app.PickPreview{Rows: []app.PickRow{{Number: 1, Selectable: true}}}
	tooMany := make([]app.PickRow, maxPickCandidates+1)
	for i := range tooMany {
		tooMany[i] = app.PickRow{Number: i + 1, Selectable: true}
	}

	for _, test := range []struct {
		name    string
		preview app.PickPreview
		input   io.Reader
		output  io.Writer
		wantErr string
	}{
		{
			name:    "refuses more than the bounded candidate count",
			preview: app.PickPreview{Rows: tooMany},
			input:   strings.NewReader("1\n"),
			output:  io.Discard,
		},
		{
			name:    "reports cancellation notice failure",
			preview: app.PickPreview{Rows: tooMany},
			input:   strings.NewReader("1\n"),
			output:  pickerErrorWriter{err: errors.New("notice failed")},
			wantErr: "notice failed",
		},
		{
			name:    "reports display failure before reading input",
			preview: preview,
			input:   strings.NewReader("1\n"),
			output:  pickerErrorWriter{err: errors.New("display failed")},
			wantErr: "display failed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := numberedPicker(context.Background(), test.input, test.output)(test.preview)
			if test.wantErr == "" {
				if result.Err != nil || result.Selected != nil {
					t.Fatalf("result=%+v", result)
				}
				return
			}
			if result.Err == nil || !strings.Contains(result.Err.Error(), test.wantErr) {
				t.Fatalf("result=%+v, want error containing %q", result, test.wantErr)
			}
		})
	}
}

func TestPickerReadAnswersPropagatesOutputAndCancellationFailures(t *testing.T) {
	preview := app.PickPreview{Rows: []app.PickRow{{Number: 1, Selectable: true}}}
	for _, test := range []struct {
		name    string
		ctx     context.Context
		input   string
		output  io.Writer
		wantErr string
	}{
		{"prompt write", context.Background(), "1\n", pickerErrorWriter{err: errors.New("prompt failed")}, "prompt failed"},
		{"invalid answer write", context.Background(), "x\n", pickerErrorWriter{err: errors.New("invalid failed")}, "invalid failed"},
		{"eof newline write", context.Background(), "", pickerErrorWriter{err: errors.New("newline failed")}, "newline failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := picker{output: test.output}.readAnswers(test.ctx, strings.NewReader(test.input), preview)
			if result.Err == nil || !strings.Contains(result.Err.Error(), test.wantErr) {
				t.Fatalf("result=%+v, want error containing %q", result, test.wantErr)
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	result := picker{output: pickerWriteFunc(func(value []byte) (int, error) {
		cancel()
		if string(value) == "\n" {
			return 0, errors.New("cancel newline failed")
		}
		return len(value), nil
	})}.readAnswers(ctx, strings.NewReader("1\n"), preview)
	if !errors.Is(result.Err, context.Canceled) || !strings.Contains(result.Err.Error(), "cancel newline failed") {
		t.Fatalf("canceled result=%+v", result)
	}

	ctx, cancel = context.WithCancel(context.Background())
	result = picker{output: pickerWriteFunc(func(value []byte) (int, error) {
		if strings.Contains(string(value), "Choose worktrees") {
			cancel()
		}
		return len(value), nil
	})}.readAnswers(ctx, strings.NewReader("1\n"), preview)
	if !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("canceled result=%+v", result)
	}

	for _, test := range []struct {
		name  string
		input string
	}{
		{"oversized answer", strings.Repeat("x", 1025) + "\n"},
		{"invalid answer", "x\n"},
		{"eof answer", ""},
	} {
		t.Run(test.name+" output failure after prompt", func(t *testing.T) {
			writes := 0
			result := picker{output: pickerWriteFunc(func(value []byte) (int, error) {
				writes++
				if writes == 1 {
					return len(value), nil
				}
				return 0, errors.New("follow-up failed")
			})}.readAnswers(context.Background(), strings.NewReader(test.input), preview)
			if result.Err == nil || !strings.Contains(result.Err.Error(), "follow-up failed") {
				t.Fatalf("result=%+v", result)
			}
		})
	}
}

func TestPickerWithInputAndInterruptibleReadCloseOnCancellation(t *testing.T) {
	defer goleak.VerifyNone(t, goleak.IgnoreCurrent())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer writer.Close()
	done := make(chan app.PickResult, 1)
	go func() {
		done <- picker{}.interruptibleRead(ctx, reader, func(input io.Reader) app.PickResult {
			_, err := input.Read(make([]byte, 1))
			return app.PickResult{Err: err}
		})
	}()
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.Err, context.Canceled) {
			t.Fatalf("result=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt picker read")
	}

	plain := picker{}.interruptibleRead(context.Background(), strings.NewReader("1\n"), func(input io.Reader) app.PickResult {
		line, err := io.ReadAll(input)
		if err != nil {
			return app.PickResult{Err: err}
		}
		return app.PickResult{Selected: []int{len(line)}}
	})
	if plain.Err != nil || !reflect.DeepEqual(plain.Selected, []int{2}) {
		t.Fatalf("plain result=%+v", plain)
	}

	closeFailure := errors.New("close failed")
	closable := newPickerBlockingReader(closeFailure)
	ctx, cancel = context.WithCancel(context.Background())
	done = make(chan app.PickResult, 1)
	go func() {
		done <- picker{}.interruptibleRead(ctx, closable, func(input io.Reader) app.PickResult {
			_, err := input.Read(make([]byte, 1))
			return app.PickResult{Err: err}
		})
	}()
	cancel()
	select {
	case result := <-done:
		if !errors.Is(result.Err, closeFailure) || !strings.Contains(result.Err.Error(), "interrupt numbered selection") {
			t.Fatalf("close failure result=%+v", result)
		}
	case <-time.After(time.Second):
		t.Fatal("close failure did not finish picker read")
	}
}

func TestPickerRejectsPreparationFailures(t *testing.T) {
	closed, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	closed.Close()
	defer writer.Close()
	result := picker{input: closed}.withInput(context.Background(), func(io.Reader) app.PickResult { return app.PickResult{} })
	if result.Err == nil || !strings.Contains(result.Err.Error(), "prepare numbered selection") {
		t.Fatalf("result=%+v", result)
	}
}

func TestOversizedPickerLinePreservesDrainFailure(t *testing.T) {
	boom := errors.New("drain failed")
	reader := bufio.NewReaderSize(io.MultiReader(strings.NewReader(strings.Repeat("x", 1024)), pickerErrorReader{err: boom}), 1024)
	_, err := readPickLine(reader)
	if !errors.Is(err, boom) {
		t.Fatalf("readPickLine error=%v, want %v", err, boom)
	}
}

func TestDiscardPickerLineConsumesBufferedChunks(t *testing.T) {
	reader := bufio.NewReaderSize(strings.NewReader(strings.Repeat("x", 2048)+"\n"), 1024)
	if _, err := reader.ReadSlice('\n'); !errors.Is(err, bufio.ErrBufferFull) {
		t.Fatalf("first chunk error=%v", err)
	}
	if err := discardPickLine(reader); err != nil {
		t.Fatalf("discard error=%v", err)
	}
}

type pickerErrorReader struct{ err error }

func (r pickerErrorReader) Read([]byte) (int, error) { return 0, r.err }

type pickerErrorWriter struct{ err error }

func (w pickerErrorWriter) Write([]byte) (int, error) { return 0, w.err }

type pickerWriteFunc func([]byte) (int, error)

func (f pickerWriteFunc) Write(value []byte) (int, error) { return f(value) }

func TestPickerFormattingAndWriteFailures(t *testing.T) {
	if _, _, err := parsePickRange("1-2-3"); err == nil {
		t.Fatal("malformed range accepted")
	}
	if _, _, err := parsePickRange("1-301"); err == nil {
		t.Fatal("oversized range accepted")
	}
	if err := writePicker(pickerErrorWriter{err: errors.New("write failed")}, "text"); err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("write error=%v", err)
	}
	if err := writePicker(shortPickerWriter{}, "text"); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write error=%v", err)
	}
}

type shortPickerWriter struct{}

func (shortPickerWriter) Write(value []byte) (int, error) { return len(value) - 1, nil }

type pickerBlockingReader struct {
	done     chan struct{}
	closeErr error
	once     sync.Once
}

func newPickerBlockingReader(closeErr error) *pickerBlockingReader {
	return &pickerBlockingReader{done: make(chan struct{}), closeErr: closeErr}
}

func (r *pickerBlockingReader) Read([]byte) (int, error) {
	<-r.done
	return 0, io.EOF
}

func (r *pickerBlockingReader) Close() error {
	r.once.Do(func() { close(r.done) })
	return r.closeErr
}

func TestNumberedPickerFormatsThreeHundredRowsWithinTerminalWidth(t *testing.T) {
	rows := make([]app.PickRow, maxPickCandidates)
	for i := range rows {
		rows[i] = app.PickRow{Number: i + 1, Selectable: true, Worktree: model.Worktree{Repository: "/repo", Branch: "feature", Path: "/worktrees/" + strings.Repeat("x", 120), DiskBytes: int64(i)}}
	}
	text := formatPickRows(app.PickPreview{Rows: rows})
	if !strings.Contains(text, "  300  repository=") {
		t.Fatalf("last candidate missing")
	}
	for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
		if len([]rune(line)) > 80 {
			t.Fatalf("line exceeds terminal width (%d): %q", len([]rune(line)), line)
		}
	}
}

func TestPickRejectsNonTerminalBeforeScanning(t *testing.T) {
	backend := newMainFakeGit(mainRecord("main"))
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"clean", "--pick"}, processIO{stdin: strings.NewReader("1\n"), stdout: &stdout, stderr: &stderr}, mainCommandDependencies(backend, func() (string, error) { return "/repo", nil }))
	if code != 2 || !strings.Contains(stderr.String(), "requires terminal") || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestPickRunsWithTerminalStreams(t *testing.T) {
	repo := testgit.NewRepository(t)
	target := repo.CreateMergedWorktree(t, "picked")
	var stdout, stderr bytes.Buffer
	deps := mainCommandDependencies(gitx.New("git"), func() (string, error) { return repo.Path, nil })
	deps.isTerminal = func(any) bool { return true }
	code := run(context.Background(), []string{"clean", "--pick", repo.Root}, processIO{stdin: strings.NewReader("1\n"), stdout: &stdout, stderr: &stderr}, deps)
	if code != 0 || !strings.Contains(stdout.String(), target) || !strings.Contains(stdout.String(), "would_remove") || !strings.Contains(stderr.String(), "Choose worktrees by number") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("dry-run picker removed %q: %v", target, err)
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
	if selectionConfirmer(context.Background(), file, io.Discard, false)(app.SelectionPreview{Count: 1}) {
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
}

func TestSelectionConfirmerRefusesIncompletePreview(t *testing.T) {
	preview := app.SelectionPreview{Count: 2, Paths: []string{"/first", "/second"}, ReclaimableBytes: 42}
	var complete bytes.Buffer
	if !selectionConfirmer(context.Background(), strings.NewReader("yes\n"), &complete, true)(preview) {
		t.Fatal("complete preview rejected")
	}
	for _, shortOnly := range []bool{false, true} {
		for limit := 0; limit < complete.Len(); limit++ {
			input := strings.NewReader("yes\n")
			output := &limitedSelectionOutput{remaining: limit, shortOnly: shortOnly}
			if selectionConfirmer(context.Background(), input, output, true)(preview) {
				t.Fatalf("authorized with truncated preview: limit=%d shortOnly=%t", limit, shortOnly)
			}
			if input.Len() != len("yes\n") {
				t.Fatalf("read answer without complete preview: limit=%d", limit)
			}
		}
	}
}

type limitedSelectionOutput struct {
	remaining int
	shortOnly bool
}

func (w *limitedSelectionOutput) Write(p []byte) (int, error) {
	n := min(len(p), w.remaining)
	w.remaining -= n
	if n < len(p) && !w.shortOnly {
		return n, errors.New("preview output failed")
	}
	return n, nil
}

func TestReviewAdvisorySelectionAndStrictCleanRemainSeparate(t *testing.T) {
	repo := testgit.NewRepository(t)
	dirty := repo.CreateMergedWorktree(t, "dirty")
	safe := repo.CreateMergedWorktree(t, "safe")
	testgit.WriteFile(t, filepath.Join(dirty, "untracked.txt"), "keep this data\n")
	registrations := repo.RegisteredWorktrees(t)
	deps := mainCommandDependencies(gitx.New("git"), func() (string, error) { return repo.Path, nil })
	var stdout, stderr bytes.Buffer
	streams := processIO{stdin: strings.NewReader(""), stdout: &stdout, stderr: &stderr}
	args := []string{"review", "--json", "--select", dirty, "--classification", "safe_to_remove", "--group-by", "classification", "--sort-by", "size", repo.Root}
	if code := run(context.Background(), args, streams, deps); code != 0 {
		t.Fatalf("unsafe advisory review failed: code=%d stderr=%s", code, &stderr)
	}
	var doc review.Document
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.ReviewSchemaVersion != "1.0.0" || doc.Inventory.SchemaVersion != "1.1.0" || !doc.Inventory.DryRun || len(doc.Inventory.Errors) != 0 || doc.Inventory.Summary.Removed != 0 || doc.View.Totals.Selected.Count != 1 {
		t.Fatalf("review=%+v", doc)
	}
	selectedDirty := false
	for _, group := range doc.View.Groups {
		for _, row := range group.Worktrees {
			if row.Selected && row.Worktree.Classification == model.MergedButDirty {
				selectedDirty = true
			}
		}
	}
	if !selectedDirty || repo.RegisteredWorktrees(t) != registrations {
		t.Fatal("advisory unsafe row lost or review mutated registrations")
	}
	for _, target := range []string{dirty, safe} {
		stdout.Reset()
		stderr.Reset()
		code := run(context.Background(), []string{"clean", "--json", "--yes", "--select", target, repo.Root}, streams, deps)
		var inv model.Inventory
		if err := json.Unmarshal(stdout.Bytes(), &inv); err != nil {
			t.Fatal(err)
		}
		if target == dirty {
			if code != 1 || inv.Summary.Removed != 0 || repo.RegisteredWorktrees(t) != registrations {
				t.Fatalf("unsafe clean did not fail closed: code=%d inv=%+v", code, inv)
			}
		} else if code != 0 || inv.Summary.Removed != 1 {
			t.Fatalf("usual selected clean failed: code=%d inv=%+v", code, inv)
		}
	}
	if _, err := os.Stat(safe); !os.IsNotExist(err) {
		t.Fatalf("safe checkout survived cleanup: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dirty, "untracked.txt"))
	if err != nil || string(data) != "keep this data\n" || !repo.BranchExists(t, "dirty") {
		t.Fatalf("dirty checkout mutated: data=%q err=%v", data, err)
	}
}
