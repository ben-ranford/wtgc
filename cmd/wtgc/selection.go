package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/report"
)

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
		fmt.Fprintf(output, "Selected cleanup: %d worktrees, %d reclaimable bytes\n", preview.Count, preview.ReclaimableBytes)
		for _, path := range preview.Paths {
			fmt.Fprintf(output, "  %s\n", report.SafeHumanText(path))
		}
		if deleteBranch {
			fmt.Fprintln(output, "Local branches will be deleted only when separately proven safe; provider squash branches remain retained.")
		}
		fmt.Fprint(output, "Remove this entire selected set? [y/N] ")
		answer, err := bufio.NewReader(input).ReadString('\n')
		if err != nil || ctx.Err() != nil {
			fmt.Fprintln(output)
			return false
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		return answer == "y" || answer == "yes"
	}
}
