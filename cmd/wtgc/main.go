package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/cli"
	"github.com/ben-ranford/wtgc/internal/gitx"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/report"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	timeout, err := gitCommandTimeoutFromEnv(os.Getenv("WTGC_GIT_TIMEOUT"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "WTGC_GIT_TIMEOUT: %v\n", err)
		os.Exit(2)
	}
	os.Exit(run(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, gitx.NewWithTimeout("git", timeout), os.Getwd))
}

func run(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	backend app.Git,
	getwd func() (string, error),
) int {
	opts, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		if cli.IsUsageError(err) {
			cli.WriteUsage(stderr, "wtgc")
			return 2
		}
		return 1
	}
	if opts.Help {
		cli.WriteUsage(stdout, "wtgc")
		return 0
	}
	if opts.Version {
		fmt.Fprintf(stdout, "wtgc %s (commit %s, built %s)\n", version, commit, date)
		return 0
	}

	workingDirectory, err := getwd()
	if err != nil {
		fmt.Fprintf(stderr, "resolve current directory: %v\n", err)
		return 1
	}
	appOptions := app.Options{
		Roots:         opts.Roots,
		Execute:       !opts.DryRun,
		Interactive:   opts.Interactive,
		DeleteBranch:  opts.DeleteBranch,
		ProtectedPath: workingDirectory,
	}
	if opts.Interactive {
		appOptions.Confirm = confirmer(stdin, stderr, opts.DeleteBranch)
	}

	inventory, runErr := app.New(backend).Run(ctx, appOptions)
	format := report.FormatHuman
	if opts.JSON {
		format = report.FormatJSON
	}
	if err := report.Write(stdout, inventory, format); err != nil {
		fmt.Fprintf(stderr, "write report: %v\n", err)
		return 1
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "wtgc: %v\n", runErr)
		return 1
	}
	return 0
}

func confirmer(input io.Reader, output io.Writer, deleteBranch bool) func(model.Worktree) bool {
	reader := bufio.NewReader(input)
	return func(worktree model.Worktree) bool {
		action := "remove"
		if worktree.Prunable {
			action = "prune stale metadata for"
		}
		suffix := ""
		if deleteBranch && worktree.Branch != "" && !worktree.Prunable {
			suffix = " and delete its local branch"
		}
		fmt.Fprintf(output, "%s %s%s? [y/N] ", action, worktree.Path, suffix)
		answer, err := reader.ReadString('\n')
		if err != nil && len(answer) == 0 {
			fmt.Fprintln(output)
			return false
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			return true
		default:
			return false
		}
	}
}

func gitCommandTimeoutFromEnv(value string) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 30 * time.Second, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, fmt.Errorf("must be greater than zero")
	}
	return duration, nil
}
