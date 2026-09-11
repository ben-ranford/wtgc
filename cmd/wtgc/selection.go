package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/report"
)

const maxPickCandidates = 300

func numberedPicker(ctx context.Context, input io.Reader, output io.Writer) func(app.PickPreview) app.PickResult {
	p := picker{input: input, output: output}
	return func(preview app.PickPreview) app.PickResult { return p.pick(ctx, preview) }
}

type picker struct {
	input  io.Reader
	output io.Writer
}

func (p picker) pick(ctx context.Context, preview app.PickPreview) app.PickResult {
	if ctx.Err() != nil {
		return app.PickResult{Err: ctx.Err()}
	}
	if selectablePickRows(preview.Rows) > maxPickCandidates {
		if err := writePicker(p.output, "Picker canceled: more than 300 displayed candidates. Narrow the scan scope.\n"); err != nil {
			return app.PickResult{Err: err}
		}
		return app.PickResult{}
	}
	if err := writePicker(p.output, formatPickRows(preview)); err != nil {
		return app.PickResult{Err: err}
	}
	return p.withInput(ctx, func(reader io.Reader) app.PickResult { return p.readAnswers(ctx, reader, preview) })
}

func selectablePickRows(rows []app.PickRow) int {
	count := 0
	for _, row := range rows {
		if row.Selectable {
			count++
		}
	}
	return count
}

func (p picker) withInput(ctx context.Context, read func(io.Reader) app.PickResult) (result app.PickResult) {
	reader, release, err := prepareSelectionInput(p.input)
	if err != nil {
		return app.PickResult{Err: fmt.Errorf("prepare numbered selection: %w", err)}
	}
	if release != nil {
		defer func() {
			if err := release(); err != nil {
				result = app.PickResult{Err: fmt.Errorf("restore numbered selection input: %w", err)}
			}
		}()
	}
	return p.interruptibleRead(ctx, reader, read)
}

func (p picker) interruptibleRead(ctx context.Context, reader io.Reader, read func(io.Reader) app.PickResult) (result app.PickResult) {
	closer, ok := reader.(io.Closer)
	if !ok {
		return read(reader)
	}
	finished := make(chan error, 1)
	stop := context.AfterFunc(ctx, func() { finished <- closer.Close() })
	defer func() {
		if !stop() {
			if err := <-finished; err != nil {
				result = app.PickResult{Err: fmt.Errorf("interrupt numbered selection: %w", err)}
			} else {
				result = app.PickResult{Err: ctx.Err()}
			}
		}
	}()
	return read(reader)
}

func (p picker) readAnswers(ctx context.Context, reader io.Reader, preview app.PickPreview) app.PickResult {
	answers := bufio.NewReaderSize(reader, 1024)
	for {
		if err := writePicker(p.output, "Choose worktrees by number (for example 1,3-5; empty cancels): "); err != nil {
			return app.PickResult{Err: err}
		}
		answer, err := readPickLine(answers)
		if ctx.Err() != nil {
			if writeErr := writePicker(p.output, "\n"); writeErr != nil {
				return app.PickResult{Err: errors.Join(ctx.Err(), writeErr)}
			}
			return app.PickResult{Err: ctx.Err()}
		}
		if err != nil {
			if errors.Is(err, errPickInputTooLong) {
				if writeErr := writePicker(p.output, "Invalid selection: "+err.Error()+". Try again, or press Enter to cancel.\n"); writeErr != nil {
					return app.PickResult{Err: writeErr}
				}
				continue
			}
			if errors.Is(err, io.EOF) {
				if writeErr := writePicker(p.output, "\n"); writeErr != nil {
					return app.PickResult{Err: writeErr}
				}
				return app.PickResult{}
			}
			return app.PickResult{Err: fmt.Errorf("read numbered selection: %w", err)}
		}
		selected, err := parsePickSelection(strings.TrimSpace(answer), preview)
		if err == nil {
			return app.PickResult{Selected: selected}
		}
		if err := writePicker(p.output, "Invalid selection: "+err.Error()+". Try again, or press Enter to cancel.\n"); err != nil {
			return app.PickResult{Err: err}
		}
	}
}

var errPickInputTooLong = errors.New("input is too long")

func readPickLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		if err := discardPickLine(reader); err != nil {
			return "", err
		}
		return "", errPickInputTooLong
	}
	if err != nil {
		return "", err
	}
	return string(line), nil
}

func discardPickLine(reader *bufio.Reader) error {
	for {
		_, err := reader.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return err
	}
}

func formatPickRows(preview app.PickPreview) string {
	var text strings.Builder
	text.WriteString("Numbered cleanup selection (safe live rows have a number):\n")
	for _, row := range preview.Rows {
		worktree := row.Worktree
		prefix := "-"
		if row.Selectable {
			prefix = strconv.Itoa(row.Number)
		}
		writePickField(&text, "  "+prefix+"  ", "repository=", pickerFieldText(worktree.Repository))
		writePickField(&text, "     ", "branch=", pickerFieldText(worktree.Branch))
		writePickField(&text, "     ", "bytes=", pickBytes(worktree))
		writePickField(&text, "     ", "path=", pickerFieldText(worktree.Path))
		if !row.Selectable {
			reason := row.Unavailable
			if reason == "" {
				reason = worktree.Error
			}
			writePickField(&text, "     ", "unavailable: ", pickerFieldText(reason))
		}
	}
	return text.String()
}

// pickerFieldText makes every displayed identity ASCII so byte width equals
// terminal-cell width. It also keeps controls inert and every original rune
// inspectable without splitting combining sequences or wide characters.
func pickerFieldText(value string) string {
	quoted := strconv.QuoteToASCII(value)
	return quoted[1 : len(quoted)-1]
}

func pickBytes(worktree model.Worktree) string {
	if worktree.WorktreeDetails != nil && worktree.DiskBytesMeasured != nil && !*worktree.DiskBytesMeasured {
		return "unmeasured"
	}
	return strconv.FormatInt(worktree.DiskBytes, 10)
}

func writePickField(text *strings.Builder, indent, label, value string) {
	linePrefix := indent + label
	width := 80 - len(linePrefix)
	if width < 1 {
		width = 1
	}
	runes := []rune(value)
	for len(runes) > width {
		fmt.Fprintf(text, "%s%s\n", linePrefix, string(runes[:width]))
		runes = runes[width:]
		linePrefix = indent
		width = 80 - len(linePrefix)
		if width < 1 {
			width = 1
		}
	}
	fmt.Fprintf(text, "%s%s\n", linePrefix, string(runes))
}

func parsePickSelection(answer string, preview app.PickPreview) ([]int, error) {
	if answer == "" {
		return nil, nil
	}
	selectable := make(map[int]bool)
	for _, row := range preview.Rows {
		if row.Selectable {
			selectable[row.Number] = true
		}
	}
	selected := make(map[int]bool)
	for _, part := range strings.Split(answer, ",") {
		if err := selectPickRange(part, selectable, selected); err != nil {
			return nil, err
		}
	}
	result := make([]int, 0, len(selected))
	for number := 1; number <= maxPickCandidates; number++ {
		if selected[number] {
			result = append(result, number)
		}
	}
	return result, nil
}

func selectPickRange(part string, selectable, selected map[int]bool) error {
	first, last, err := parsePickRange(strings.TrimSpace(part))
	if err != nil {
		return err
	}
	for number := first; number <= last; number++ {
		if !selectable[number] {
			return fmt.Errorf("%d is not selectable", number)
		}
		selected[number] = true
	}
	return nil
}

func parsePickRange(part string) (int, int, error) {
	if part == "" {
		return 0, 0, fmt.Errorf("empty item")
	}
	bounds := strings.Split(part, "-")
	if len(bounds) > 2 {
		return 0, 0, fmt.Errorf("malformed range %q", part)
	}
	first, err := strconv.Atoi(strings.TrimSpace(bounds[0]))
	if err != nil || first < 1 {
		return 0, 0, fmt.Errorf("invalid number %q", part)
	}
	last := first
	if len(bounds) == 2 {
		last, err = strconv.Atoi(strings.TrimSpace(bounds[1]))
		if err != nil || last < first {
			return 0, 0, fmt.Errorf("invalid range %q", part)
		}
	}
	if last-first >= maxPickCandidates {
		return 0, 0, fmt.Errorf("range %q is too large", part)
	}
	return first, last, nil
}

func writePicker(output io.Writer, text string) error {
	n, err := io.WriteString(output, text)
	if err != nil {
		return fmt.Errorf("write numbered selection: %w", err)
	}
	if n != len(text) {
		return io.ErrShortWrite
	}
	return nil
}

func selectionConfirmer(ctx context.Context, input io.Reader, output io.Writer, deleteBranch bool) func(app.SelectionPreview) bool {
	return func(preview app.SelectionPreview) (accepted bool) {
		if ctx.Err() != nil {
			return false
		}
		input, release, err := prepareSelectionInput(input)
		if err != nil {
			fmt.Fprintf(output, "prepare selected confirmation: %v\n", err)
			return false
		}
		if release != nil {
			defer func() {
				if err := release(); err != nil {
					fmt.Fprintf(output, "restore selected confirmation input: %v\n", err)
					accepted = false
				}
			}()
		}
		// Close the command-owned stream on cancellation instead of abandoning a
		// blocked reader goroutine. In-memory readers finish synchronously.
		if closer, ok := input.(io.Closer); ok {
			finished := make(chan error, 1)
			stop := context.AfterFunc(ctx, func() { finished <- closer.Close() })
			defer func() {
				if !stop() {
					if err := <-finished; err != nil {
						fmt.Fprintf(output, "interrupt selected confirmation: %v\n", err)
						accepted = false
					}
				}
			}()
		}
		var text strings.Builder
		fmt.Fprintf(&text, "Selected cleanup: %d worktrees, %d reclaimable bytes\n", preview.Count, preview.ReclaimableBytes)
		for _, path := range preview.Paths {
			fmt.Fprintf(&text, "  %s\n", report.SafeHumanText(path))
		}
		if deleteBranch {
			fmt.Fprintln(&text, "Local branches will be deleted only when separately proven safe; provider squash branches remain retained.")
		}
		fmt.Fprint(&text, "Remove this entire selected set? [y/N] ")
		if n, err := io.WriteString(output, text.String()); err != nil || n != text.Len() {
			return false
		}
		answer, err := bufio.NewReader(input).ReadString('\n')
		if err != nil || ctx.Err() != nil {
			fmt.Fprintln(output)
			return false
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		return answer == "y" || answer == "yes"
	}
}
