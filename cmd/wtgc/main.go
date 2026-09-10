package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/cli"
	"github.com/ben-ranford/wtgc/internal/gitx"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/provider"
	"github.com/ben-ranford/wtgc/internal/report"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	os.Exit(runMain(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv, os.Getwd, newGitBackend, newTimedGitBackend))
}

func newGitBackend(binary string) app.Git { return gitx.New(binary) }

func newTimedGitBackend(binary string, timeout time.Duration) app.Git {
	return gitx.NewWithTimeout(binary, timeout)
}

// runMain wires process dependencies into the command. Keeping this boundary
// explicit lets tests exercise timeout and static-information startup paths
// without mutating process-wide arguments or environment.
func runMain(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	getenv func(string) string,
	getwd func() (string, error),
	newGit func(string) app.Git,
	newGitWithTimeout func(string, time.Duration) app.Git,
) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	backend := newGit("git")
	if !staticInfoRequest(args) {
		timeout, err := gitCommandTimeoutFromEnv(getenv("WTGC_GIT_TIMEOUT"))
		if err != nil {
			fmt.Fprintf(stderr, "WTGC_GIT_TIMEOUT: %v\n", err)
			return 2
		}
		backend = newGitWithTimeout("git", timeout)
	}
	return run(ctx, args, stdin, stdout, stderr, backend, getwd)
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
	return runWithProvider(ctx, args, stdin, stdout, stderr, backend, getwd, func() provider.MergeFinder {
		return provider.NewGitHub(nil)
	})
}

func runWithProvider(
	ctx context.Context,
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	backend app.Git,
	getwd func() (string, error),
	newProvider func() provider.MergeFinder,
) int {
	opts, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintln(stderr, err)
		// cli.Parse exposes malformed input as UsageError, so every parse
		// failure receives the same conventional usage response.
		cli.WriteUsage(stderr, "wtgc")
		return 2
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
		Roots:          opts.Roots,
		Execute:        !opts.DryRun,
		Interactive:    opts.Interactive,
		DeleteBranch:   opts.DeleteBranch,
		ProtectedPath:  workingDirectory,
		Retention:      opts.Retention,
		CacheThreshold: opts.CacheThreshold,
		ProviderRemote: opts.ProviderRemote,
	}
	if opts.Provider == "github" {
		appOptions.Provider = newProvider()
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
			if worktree.WorktreeDetails != nil && worktree.ProviderProof.HeadSHA != "" {
				suffix = " and retain its local branch (provider squash proof)"
			} else {
				suffix = " and delete its local branch"
			}
		}
		fmt.Fprintf(output, "%s %s%s? [y/N] ", action, report.SafeHumanText(worktree.Path), suffix)
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
		return 0, nil
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

func staticInfoRequest(args []string) bool {
	opts, err := cli.Parse(args)
	return err == nil && (opts.Help || opts.Version)
}
