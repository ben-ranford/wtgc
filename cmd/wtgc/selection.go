package main

import (
	"bufio"
	"fmt"
	"io"
	"strings"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/report"
)

func selectionConfirmer(input io.Reader, output io.Writer, deleteBranch bool) func(app.SelectionPreview) bool {
	return func(preview app.SelectionPreview) bool {
		fmt.Fprintf(output, "Selected cleanup: %d worktrees, %d reclaimable bytes\n", preview.Count, preview.ReclaimableBytes)
		for _, path := range preview.Paths {
			fmt.Fprintf(output, "  %s\n", report.SafeHumanText(path))
		}
		if deleteBranch {
			fmt.Fprintln(output, "Local branches will be deleted only when separately proven safe; provider squash branches remain retained.")
		}
		fmt.Fprint(output, "Remove this entire selected set? [y/N] ")
		answer, err := bufio.NewReader(input).ReadString('\n')
		if err != nil {
			fmt.Fprintln(output)
			return false
		}
		answer = strings.ToLower(strings.TrimSpace(answer))
		return answer == "y" || answer == "yes"
	}
}
