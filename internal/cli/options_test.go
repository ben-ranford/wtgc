package cli

import (
	"strings"
	"testing"
)

func TestParseDefaultsToDryRunCurrentDirectory(t *testing.T) {
	opts, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if opts.Command != CommandClean {
		t.Fatalf("Command = %q, want %q", opts.Command, CommandClean)
	}
	if !opts.DryRun {
		t.Fatal("DryRun = false, want true")
	}
	if got, want := strings.Join(opts.Roots, ","), "."; got != want {
		t.Fatalf("Roots = %q, want %q", got, want)
	}
}

func TestParseCleanWithPositionalAndScanRoots(t *testing.T) {
	opts, err := Parse([]string{"clean", "--scan-root", "../one", "--scan-root=three", "two"})
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if got, want := strings.Join(opts.Roots, ","), "../one,three,two"; got != want {
		t.Fatalf("Roots = %q, want %q", got, want)
	}
}

func TestParseExecuteModes(t *testing.T) {
	opts, err := Parse([]string{"--yes"})
	if err != nil {
		t.Fatalf("Parse(--yes) error = %v", err)
	}
	if opts.DryRun || !opts.Yes {
		t.Fatalf("Parse(--yes) = DryRun %v Yes %v, want execute yes", opts.DryRun, opts.Yes)
	}

	opts, err = Parse([]string{"--interactive"})
	if err != nil {
		t.Fatalf("Parse(--interactive) error = %v", err)
	}
	if opts.DryRun || !opts.Interactive {
		t.Fatalf("Parse(--interactive) = DryRun %v Interactive %v, want interactive execute", opts.DryRun, opts.Interactive)
	}
}

func TestParseRejectsConflictingExecuteModes(t *testing.T) {
	for _, args := range [][]string{
		{"--yes", "--interactive"},
		{"--yes", "--dry-run"},
		{"--interactive", "--dry-run"},
		{"--dry-run=false"},
	} {
		_, err := Parse(args)
		if err == nil {
			t.Fatalf("Parse(%v) error = nil, want usage error", args)
		}
		if !IsUsageError(err) {
			t.Fatalf("Parse(%v) usage = false for %T %v", args, err, err)
		}
	}
}

func TestParseHelpAndVersion(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}} {
		opts, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v) error = %v", args, err)
		}
		if !opts.Help {
			t.Fatalf("Parse(%v).Help = false, want true", args)
		}
	}

	opts, err := Parse([]string{"--version"})
	if err != nil {
		t.Fatalf("Parse(--version) error = %v", err)
	}
	if !opts.Version {
		t.Fatal("Version = false, want true")
	}
}

func TestParseRejectsUnknownCommandAndFlag(t *testing.T) {
	for _, args := range [][]string{
		{"status"},
		{"--bogus"},
	} {
		_, err := Parse(args)
		if err == nil {
			t.Fatalf("Parse(%v) error = nil, want usage error", args)
		}
		if !IsUsageError(err) {
			t.Fatalf("Parse(%v) usage = false for %T %v", args, err, err)
		}
	}
}

func TestUsageMentionsCoreFlags(t *testing.T) {
	var b strings.Builder
	WriteUsage(&b, "wtgc")
	out := b.String()

	for _, want := range []string{"Usage:", "clean", "--scan-root", "--dry-run", "--yes", "--interactive", "--delete-branch", "--json", "--version"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage missing %q:\n%s", want, out)
		}
	}
}
