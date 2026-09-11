// Package cli parses command-line options for wtgc.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

const (
	CommandClean   = "clean"
	CommandReview  = "review"
	CommandExplain = "explain"
)

// Options is the stable command contract consumed by the application layer.
type Options struct {
	Command          string
	Roots            []string
	DryRun           bool
	Yes              bool
	Interactive      bool
	Pick             bool
	DeleteBranch     bool
	JSON             bool
	Version          bool
	Help             bool
	Provider         string
	ProviderRemote   string
	Retention        time.Duration
	CacheThreshold   int64
	Repositories     []string
	Classifications  []string
	GroupBy          string
	SortBy           string
	SelectedPaths    []string
	ExplainPath      string
	ExcludePaths     []string
	ReclaimTarget    int64
	HasReclaimTarget bool
}

// UsageError reports input that should be shown with command usage and a
// conventional command-line failure exit code.
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string {
	return e.Message
}

func (e *UsageError) ExitCode() int {
	return 2
}

func (e *UsageError) Usage() bool {
	return true
}

// IsUsageError reports whether err should be handled as command usage.
func IsUsageError(err error) bool {
	var usage interface{ Usage() bool }
	return errors.As(err, &usage) && usage.Usage()
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	if value == "" {
		return errors.New("scan root cannot be empty")
	}
	*s = append(*s, value)
	return nil
}

// Parse parses wtgc arguments. It does not write to stdout or stderr.
func Parse(args []string) (Options, error) {
	opts := Options{
		Command: CommandClean,
		DryRun:  true,
	}
	if len(args) == 0 {
		// An implicit scan of the current directory can recurse through an
		// unexpectedly large tree. Require an explicit command before doing
		// filesystem work so `wtgc` is always a fast, informational invocation.
		opts.Help = true
		return opts, nil
	}

	if len(args) > 0 && (args[0] == CommandClean || args[0] == CommandReview || args[0] == CommandExplain) {
		opts.Command = args[0]
		args = args[1:]
	} else if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return Options{}, &UsageError{Message: fmt.Sprintf("unknown command %q", args[0])}
	}

	var roots, repositories, classifications, selectedPaths, excludePaths stringList
	var reclaimTarget reclaimTargetOption
	dryRun := boolOption{
		value: true,
		set:   false,
	}

	fs := flag.NewFlagSet("wtgc", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Var(&roots, "scan-root", "root directory to scan; repeatable")
	fs.Var(&selectedPaths, "select", "worktree path to select for review or clean; repeatable")
	fs.Var(&excludePaths, "exclude", "worktree or directory subtree to exclude for this invocation; repeatable")
	fs.BoolVar(&opts.Yes, "yes", false, "execute cleanup without prompting")
	fs.BoolVar(&opts.Yes, "y", false, "execute cleanup without prompting")
	fs.BoolVar(&opts.Interactive, "interactive", false, "prompt before destructive cleanup actions")
	fs.BoolVar(&opts.Pick, "pick", false, "choose numbered safe worktrees in a terminal")
	fs.BoolVar(&opts.DeleteBranch, "delete-branch", false, "delete branches for removed worktrees when safe")
	fs.BoolVar(&opts.JSON, "json", false, "write machine-readable JSON")
	fs.BoolVar(&opts.Version, "version", false, "print version and exit")
	fs.BoolVar(&opts.Help, "help", false, "show help")
	fs.BoolVar(&opts.Help, "h", false, "show help")
	fs.StringVar(&opts.Provider, "provider", "", "optional merge-proof provider (github)")
	fs.StringVar(&opts.ProviderRemote, "provider-remote", "", "remote used for provider proof when mapping is ambiguous")
	fs.DurationVar(&opts.Retention, "retention", 0, "minimum age before cleanup")
	fs.Int64Var(&opts.CacheThreshold, "cache-threshold", 100*1024*1024, "report cache directories at or above this byte threshold")
	fs.Var(&dryRun, "dry-run", "preview cleanup actions without removing anything")
	if opts.Command == CommandReview {
		fs.Var(&repositories, "repository", "repository path to include; repeatable")
		fs.Var(&classifications, "classification", "classification to include; repeatable")
		fs.StringVar(&opts.GroupBy, "group-by", "none", "review grouping: none, repository, or classification")
		fs.StringVar(&opts.SortBy, "sort-by", "path", "review sorting: path or size")
		fs.Var(&reclaimTarget, "reclaim-target", "advisory bytes to reclaim using eligible review rows")
	}

	if err := fs.Parse(args); err != nil {
		return Options{}, &UsageError{Message: err.Error()}
	}
	if opts.Pick {
		if err := validatePickFlags(&opts, fs, selectedPaths); err != nil {
			return Options{}, err
		}
	}
	if opts.Command == CommandReview || opts.Command == CommandExplain {
		if err := validateReadOnlyFlags(opts.Command, fs, dryRun); err != nil {
			return Options{}, err
		}
		opts.DryRun = true
	}

	if opts.Help || opts.Version {
		return opts, nil
	}

	positionals := fs.Args()
	if opts.Command == CommandExplain {
		if len(positionals) != 1 || positionals[0] == "" || strings.HasPrefix(positionals[0], "-") {
			return Options{}, &UsageError{Message: "explain requires exactly one worktree path"}
		}
		opts.ExplainPath = positionals[0]
		positionals = nil
	}
	for _, root := range positionals {
		if strings.HasPrefix(root, "-") {
			return Options{}, &UsageError{Message: fmt.Sprintf("unknown argument %q", root)}
		}
		if root == "" {
			return Options{}, &UsageError{Message: "root cannot be empty"}
		}
		roots = append(roots, root)
	}

	if opts.Command != CommandReview && opts.Command != CommandExplain {
		if err := validateExecutionMode(&opts, dryRun); err != nil {
			return Options{}, err
		}
	}
	if opts.Provider != "" && opts.Provider != "github" {
		return Options{}, &UsageError{Message: "--provider must be github"}
	}
	if opts.ProviderRemote != "" && opts.Provider == "" {
		return Options{}, &UsageError{Message: "--provider-remote requires --provider github"}
	}
	if opts.Retention < 0 {
		return Options{}, &UsageError{Message: "--retention must not be negative"}
	}
	if opts.CacheThreshold < 0 {
		return Options{}, &UsageError{Message: "--cache-threshold must not be negative"}
	}
	if opts.Command == CommandReview {
		if opts.GroupBy != "none" && opts.GroupBy != "repository" && opts.GroupBy != "classification" {
			return Options{}, &UsageError{Message: "--group-by must be none, repository, or classification"}
		}
		if opts.SortBy != "path" && opts.SortBy != "size" {
			return Options{}, &UsageError{Message: "--sort-by must be path or size"}
		}
		for _, classification := range classifications {
			if !isReviewClassification(classification) {
				return Options{}, &UsageError{Message: fmt.Sprintf("unknown review classification %q", classification)}
			}
		}
		if reclaimTarget.set && len(selectedPaths) > 0 {
			return Options{}, &UsageError{Message: "--reclaim-target cannot be combined with --select"}
		}
	}
	if opts.Command == CommandExplain && len(selectedPaths) > 0 {
		return Options{}, &UsageError{Message: "explain does not accept --select"}
	}

	if len(roots) == 0 {
		roots = append(roots, ".")
	}
	opts.Roots = append([]string(nil), roots...)
	opts.Repositories = append([]string(nil), repositories...)
	opts.Classifications = append([]string(nil), classifications...)
	opts.SelectedPaths = append([]string(nil), selectedPaths...)
	opts.ExcludePaths = append([]string(nil), excludePaths...)
	opts.ReclaimTarget = reclaimTarget.value
	opts.HasReclaimTarget = reclaimTarget.set

	return opts, nil
}

type reclaimTargetOption struct {
	value int64
	set   bool
}

func (r *reclaimTargetOption) String() string {
	if !r.set {
		return ""
	}
	return fmt.Sprintf("%d", r.value)
}

func (r *reclaimTargetOption) Set(value string) error {
	parsed, err := parseReclaimTarget(value)
	if err != nil {
		return err
	}
	r.value, r.set = parsed, true
	return nil
}

func parseReclaimTarget(value string) (int64, error) {
	multiplier := int64(1)
	for suffix, scale := range map[string]int64{"KiB": 1 << 10, "MiB": 1 << 20, "GiB": 1 << 30, "TiB": 1 << 40} {
		if strings.HasSuffix(value, suffix) {
			value, multiplier = strings.TrimSuffix(value, suffix), scale
			break
		}
	}
	if value == "" {
		return 0, errors.New("--reclaim-target must be a positive integer bytes value or integer KiB, MiB, GiB, or TiB value")
	}
	result := int64(0)
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("--reclaim-target must be a positive integer bytes value or integer KiB, MiB, GiB, or TiB value")
		}
		digit := int64(character - '0')
		if result > (math.MaxInt64-digit)/10 {
			return 0, errors.New("--reclaim-target overflows bytes")
		}
		result = result*10 + digit
	}
	if result == 0 {
		return 0, errors.New("--reclaim-target must be greater than zero")
	}
	if result > math.MaxInt64/multiplier {
		return 0, errors.New("--reclaim-target overflows bytes")
	}
	return result * multiplier, nil
}

func isReviewClassification(value string) bool {
	switch model.Classification(value) {
	case model.SafeToRemove, model.MergedButDirty, model.Unmerged, model.Prunable, model.Kept, model.Error:
		return true
	default:
		return false
	}
}

func validateReadOnlyFlags(command string, fs *flag.FlagSet, dryRun boolOption) error {
	var destructive string
	fs.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "yes", "y", "interactive", "delete-branch":
			destructive = value.Name
		}
	})
	if destructive != "" || (dryRun.set && !dryRun.value) {
		return &UsageError{Message: command + " is read-only and rejects --yes, --interactive, --delete-branch, and --dry-run=false"}
	}
	return nil
}

func validateExecutionMode(opts *Options, dryRun boolOption) error {
	modes := 0
	opts.DryRun = dryRun.value
	if dryRun.set && !opts.DryRun {
		return &UsageError{Message: "use --yes or --interactive to execute cleanup"}
	}
	if dryRun.set && opts.DryRun {
		modes++
	}
	if opts.Yes {
		modes++
	}
	if opts.Interactive {
		modes++
	}
	if modes > 1 {
		return &UsageError{Message: "--dry-run, --yes, and --interactive are mutually exclusive"}
	}
	if opts.Yes || opts.Interactive {
		opts.DryRun = false
	}
	return nil
}

func validatePickFlags(opts *Options, fs *flag.FlagSet, selectedPaths stringList) error {
	if opts.Command != CommandClean {
		return &UsageError{Message: "--pick is available only with clean"}
	}
	var forbidden string
	fs.Visit(func(value *flag.Flag) {
		switch value.Name {
		case "yes", "y":
			forbidden = "--yes"
		case "json":
			forbidden = "--json"
		}
	})
	if forbidden != "" {
		return &UsageError{Message: "--pick rejects " + forbidden}
	}
	if len(selectedPaths) != 0 {
		return &UsageError{Message: "--pick rejects --select"}
	}
	return nil
}

type boolOption struct {
	value bool
	set   bool
}

func (b *boolOption) String() string {
	if b.value {
		return "true"
	}
	return "false"
}

func (b *boolOption) Set(value string) error {
	parsed, err := parseBoolFlag(value)
	if err != nil {
		return err
	}
	b.value = parsed
	b.set = true
	return nil
}

func (b *boolOption) IsBoolFlag() bool {
	return true
}

func parseBoolFlag(value string) (bool, error) {
	switch strings.ToLower(value) {
	case "1", "t", "true", "y", "yes":
		return true, nil
	case "0", "f", "false", "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q", value)
	}
}

// WriteUsage writes stable command usage text.
func WriteUsage(w io.Writer, name string) {
	if name == "" {
		name = "wtgc"
	}
	fmt.Fprintf(w, `Usage:
  %[1]s                         show help
  %[1]s clean [flags] [roots...]
  %[1]s review [flags] [roots...]
  %[1]s explain [flags] PATH
  %[1]s [flags] [roots...]      scan when a flag is supplied

Commands:
  clean              scan registered git worktrees and clean safe candidates
  review             scan and display a read-only inventory view
  explain            explain one worktree with read-only evidence

Flags:
  --scan-root DIR    root directory to scan; repeatable
  --exclude PATH     exclude a worktree or directory subtree for this invocation; repeatable
  --select PATH      select live clean targets or advisory review rows; repeatable
  --dry-run          preview cleanup actions without removing anything (default)
  --yes, -y          execute cleanup without prompting
  --interactive      prompt before destructive cleanup actions
  --pick             choose numbered safe worktrees in a terminal (optional --interactive executes)
  --delete-branch    delete branches for removed worktrees when safe
  --json             write machine-readable JSON
  --provider github   use explicit GitHub merge proof for squash merges
  --provider-remote NAME select the mapped GitHub remote when ambiguous
  --retention DURATION keep proven worktrees until this age has elapsed
  --cache-threshold BYTES report advisory caches at or above this size
  --repository PATH   include an exact repository path; repeatable (review)
  --classification VALUE include a classification; repeatable (review)
  --group-by VALUE    group review rows by none, repository, or classification
  --sort-by VALUE     sort review rows by path or size
  --reclaim-target BYTES propose an advisory safe subset for this target (review)
  --version          print version and exit
  -h, --help         show help
`, name)
}
