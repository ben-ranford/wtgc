// Package cli parses command-line options for wtgc.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
)

const (
	CommandClean = "clean"
)

// Options is the stable command contract consumed by the application layer.
type Options struct {
	Command      string
	Roots        []string
	DryRun       bool
	Yes          bool
	Interactive  bool
	DeleteBranch bool
	JSON         bool
	Version      bool
	Help         bool
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

	if len(args) > 0 && args[0] == CommandClean {
		args = args[1:]
	} else if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return Options{}, &UsageError{Message: fmt.Sprintf("unknown command %q", args[0])}
	}

	var roots stringList
	dryRun := boolOption{
		value: true,
		set:   false,
	}

	fs := flag.NewFlagSet("wtgc", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Var(&roots, "scan-root", "root directory to scan; repeatable")
	fs.BoolVar(&opts.Yes, "yes", false, "execute cleanup without prompting")
	fs.BoolVar(&opts.Yes, "y", false, "execute cleanup without prompting")
	fs.BoolVar(&opts.Interactive, "interactive", false, "prompt before destructive cleanup actions")
	fs.BoolVar(&opts.DeleteBranch, "delete-branch", false, "delete branches for removed worktrees when safe")
	fs.BoolVar(&opts.JSON, "json", false, "write machine-readable JSON")
	fs.BoolVar(&opts.Version, "version", false, "print version and exit")
	fs.BoolVar(&opts.Help, "help", false, "show help")
	fs.BoolVar(&opts.Help, "h", false, "show help")
	fs.Var(&dryRun, "dry-run", "preview cleanup actions without removing anything")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			opts.Help = true
			return opts, nil
		}
		return Options{}, &UsageError{Message: err.Error()}
	}

	if opts.Help || opts.Version {
		return opts, nil
	}

	for _, root := range fs.Args() {
		if strings.HasPrefix(root, "-") {
			return Options{}, &UsageError{Message: fmt.Sprintf("unknown argument %q", root)}
		}
		if root == "" {
			return Options{}, &UsageError{Message: "root cannot be empty"}
		}
		roots = append(roots, root)
	}

	if err := validateExecutionMode(&opts, dryRun); err != nil {
		return Options{}, err
	}

	if len(roots) == 0 {
		roots = append(roots, ".")
	}
	opts.Roots = append([]string(nil), roots...)

	return opts, nil
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
  %[1]s [flags] [roots...]      scan when a flag is supplied

Commands:
  clean              scan registered git worktrees and clean safe candidates

Flags:
  --scan-root DIR    root directory to scan; repeatable
  --dry-run          preview cleanup actions without removing anything (default)
  --yes, -y          execute cleanup without prompting
  --interactive      prompt before destructive cleanup actions
  --delete-branch    delete branches for removed worktrees when safe
  --json             write machine-readable JSON
  --version          print version and exit
  -h, --help         show help
`, name)
}
