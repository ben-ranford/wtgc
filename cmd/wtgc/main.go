package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/ben-ranford/wtgc/internal/app"
	"github.com/ben-ranford/wtgc/internal/cli"
	"github.com/ben-ranford/wtgc/internal/explain"
	"github.com/ben-ranford/wtgc/internal/gitx"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/provider"
	"github.com/ben-ranford/wtgc/internal/report"
	"github.com/ben-ranford/wtgc/internal/review"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	os.Exit(runMain(os.Args[1:], processIO{stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr}, startupDependencies{
		getenv: os.Getenv, getwd: os.Getwd, newGit: newGitBackend, newGitWithTimeout: newTimedGitBackend,
	}))
}

func newGitBackend(binary string) app.Git { return gitx.New(binary) }

func newTimedGitBackend(binary string, timeout time.Duration) app.Git {
	return gitx.NewWithTimeout(binary, timeout)
}

type processIO struct {
	stdin          io.Reader // Command-owned; blocking readers must support Close to interrupt confirmation.
	stdout, stderr io.Writer
}

type startupDependencies struct {
	getenv            func(string) string
	getwd             func() (string, error)
	newGit            func(string) app.Git
	newGitWithTimeout func(string, time.Duration) app.Git
}

type commandDependencies struct {
	backend     app.Git
	getwd       func() (string, error)
	newProvider func() provider.MergeFinder
}

// runMain wires process dependencies into the command. Keeping this boundary
// explicit lets tests exercise timeout and static-information startup paths
// without mutating process-wide arguments or environment.
func runMain(args []string, streams processIO, deps startupDependencies) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	backend := deps.newGit("git")
	if !staticInfoRequest(args) {
		timeout, err := gitCommandTimeoutFromEnv(deps.getenv("WTGC_GIT_TIMEOUT"))
		if err != nil {
			fmt.Fprintf(streams.stderr, "WTGC_GIT_TIMEOUT: %v\n", err)
			return 2
		}
		backend = deps.newGitWithTimeout("git", timeout)
	}
	return run(ctx, args, streams, commandDependencies{backend: backend, getwd: deps.getwd, newProvider: func() provider.MergeFinder { return provider.NewGitHub(nil) }})
}

func run(ctx context.Context, args []string, streams processIO, deps commandDependencies) int {
	opts, err := cli.Parse(args)
	if err != nil {
		fmt.Fprintln(streams.stderr, err)
		// cli.Parse exposes malformed input as UsageError, so every parse
		// failure receives the same conventional usage response.
		cli.WriteUsage(streams.stderr, "wtgc")
		return 2
	}
	if opts.Help {
		cli.WriteUsage(streams.stdout, "wtgc")
		return 0
	}
	if opts.Version {
		fmt.Fprintf(streams.stdout, "wtgc %s (commit %s, built %s)\n", version, commit, date)
		return 0
	}

	workingDirectory, err := deps.getwd()
	if err != nil {
		fmt.Fprintf(streams.stderr, "resolve current directory: %v\n", err)
		return 1
	}
	if opts.Command == cli.CommandReview || opts.Command == cli.CommandExplain {
		opts.Repositories, err = normalizeReviewPaths(opts.Repositories, workingDirectory)
		if err == nil && opts.Command == cli.CommandReview {
			opts.SelectedPaths, err = normalizeReviewPaths(opts.SelectedPaths, workingDirectory)
		}
		if err == nil && opts.Command == cli.CommandExplain {
			opts.ExplainPath, err = canonicalReviewPath(opts.ExplainPath, workingDirectory)
		}
		if err != nil {
			fmt.Fprintf(streams.stderr, "review: %v\n", err)
			return 2
		}
	}
	appOptions := app.Options{
		Roots:           opts.Roots,
		Execute:         opts.Command == cli.CommandClean && !opts.DryRun,
		Interactive:     opts.Interactive,
		DeleteBranch:    opts.DeleteBranch,
		ProtectedPath:   workingDirectory,
		Retention:       opts.Retention,
		CacheThreshold:  opts.CacheThreshold,
		ProviderRemote:  opts.ProviderRemote,
		ExplainEvidence: opts.Command == cli.CommandExplain,
	}
	if opts.Command == cli.CommandClean {
		appOptions.SelectedPaths = opts.SelectedPaths
	}
	if opts.Provider == "github" {
		appOptions.Provider = deps.newProvider()
	}
	if opts.Interactive {
		appOptions.Confirm = confirmer(streams.stdin, streams.stderr, opts.DeleteBranch)
		appOptions.ConfirmSelection = selectionConfirmer(ctx, streams.stdin, streams.stderr, opts.DeleteBranch)
	}

	inventory, runErr := app.New(deps.backend).Run(ctx, appOptions)
	format := report.FormatHuman
	if opts.JSON {
		format = report.FormatJSON
	}
	if opts.Command == cli.CommandReview {
		reviewOptions, err := reviewOptionsForInventory(opts, inventory)
		if err != nil {
			fmt.Fprintf(streams.stderr, "review: %v\n", err)
			return 2
		}
		document, err := review.Build(inventory, reviewOptions)
		if err != nil {
			fmt.Fprintf(streams.stderr, "review: %v\n", err)
			if runErr == nil {
				return 2
			}
		}
		if err := report.WriteReview(streams.stdout, document, format); err != nil {
			fmt.Fprintf(streams.stderr, "write report: %v\n", err)
			return 1
		}
	} else if opts.Command == cli.CommandExplain {
		if code := writeExplain(streams, inventory, opts, format); code != 0 {
			return code
		}
	} else if err := report.Write(streams.stdout, inventory, format); err != nil {
		fmt.Fprintf(streams.stderr, "write report: %v\n", err)
		return 1
	}
	if runErr != nil {
		fmt.Fprintf(streams.stderr, "wtgc: %v\n", runErr)
		return 1
	}
	return 0
}

func writeExplain(streams processIO, inventory model.Inventory, opts cli.Options, format report.Format) int {
	matches, err := matchReviewPaths([]string{opts.ExplainPath}, inventory.Worktrees, func(worktree model.Worktree) string { return worktree.Path })
	if err != nil {
		fmt.Fprintf(streams.stderr, "explain: %v\n", err)
		return 2
	}
	found, err := findExplainedWorktree(inventory.Worktrees, matches[0], opts.ExplainPath)
	if err != nil {
		fmt.Fprintf(streams.stderr, "explain: %v\n", err)
		return 2
	}
	if err := report.WriteExplain(streams.stdout, explain.Build(found, opts.Provider == "github", opts.Retention), format); err != nil {
		fmt.Fprintf(streams.stderr, "write report: %v\n", err)
		return 1
	}
	return 0
}

func findExplainedWorktree(worktrees []model.Worktree, matched, requested string) (model.Worktree, error) {
	var found *model.Worktree
	for i := range worktrees {
		if sameReviewPath(worktrees[i].Path, matched) {
			if found != nil {
				return model.Worktree{}, fmt.Errorf("ambiguous worktree path %q", requested)
			}
			found = &worktrees[i]
		}
	}
	if found == nil {
		return model.Worktree{}, fmt.Errorf("unknown registered worktree %q", requested)
	}
	return *found, nil
}

func normalizeReviewPaths(paths []string, base string) ([]string, error) {
	normalized := make([]string, len(paths))
	for i, path := range paths {
		resolved, err := canonicalReviewPath(path, base)
		if err != nil {
			return nil, err
		}
		normalized[i] = resolved
	}
	return normalized, nil
}

func canonicalReviewPath(path, base string) (string, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path = filepath.Clean(path)
	missing := []string(nil)
	for {
		if _, err := os.Lstat(path); err == nil {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				return "", fmt.Errorf("resolve review path %q: %w", path, err)
			}
			return filepath.Join(append([]string{resolved}, missing...)...), nil
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect review path %q: %w", path, err)
		}
		parent, err := reviewPathParent(path)
		if err != nil {
			return "", err
		}
		missing = append([]string{filepath.Base(path)}, missing...)
		path = parent
	}
}

func reviewPathParent(path string) (string, error) {
	parent := filepath.Dir(path)
	if parent == path {
		return "", fmt.Errorf("resolve review path %q: no existing ancestor", path)
	}
	return parent, nil
}

func reviewOptionsForInventory(opts cli.Options, inventory model.Inventory) (review.Options, error) {
	repositories, err := matchReviewPaths(opts.Repositories, inventory.Worktrees, func(worktree model.Worktree) string { return worktree.Repository })
	if err != nil {
		return review.Options{}, err
	}
	selected, err := matchReviewPaths(opts.SelectedPaths, inventory.Worktrees, func(worktree model.Worktree) string { return worktree.Path })
	if err != nil {
		return review.Options{}, err
	}
	return review.Options{Repositories: repositories, Classifications: opts.Classifications, GroupBy: opts.GroupBy, SortBy: opts.SortBy, SelectedPaths: selected}, nil
}

func matchReviewPaths(paths []string, worktrees []model.Worktree, value func(model.Worktree) string) ([]string, error) {
	matched := make([]string, len(paths))
	for index, path := range paths {
		values := make(map[string]struct{})
		for _, worktree := range worktrees {
			candidate := value(worktree)
			if sameReviewPath(candidate, path) {
				values[candidate] = struct{}{}
				continue
			}
			canonical, err := canonicalReviewPath(candidate, "")
			if err != nil {
				continue
			}
			if sameReviewPath(canonical, path) {
				values[candidate] = struct{}{}
			}
		}
		switch len(values) {
		case 0:
			matched[index] = path
		case 1:
			for value := range values {
				matched[index] = value
			}
		default:
			return nil, fmt.Errorf("ambiguous review path %q", path)
		}
	}
	return matched, nil
}

func sameReviewPath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	// Missing stale suffixes cannot be resolved through the filesystem. Windows
	// Git paths use the default case-insensitive identity in that situation;
	// collisions still flow to the caller's ambiguity check.
	return left == right || (runtime.GOOS == "windows" && strings.EqualFold(left, right))
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
