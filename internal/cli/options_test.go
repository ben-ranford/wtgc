package cli

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestParseNoArgsShowsUsage(t *testing.T) {
	opts, err := Parse(nil)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if opts.Command != CommandClean {
		t.Fatalf("Command = %q, want %q", opts.Command, CommandClean)
	}
	if !opts.Help {
		t.Fatal("Help = false, want true")
	}
	if len(opts.Roots) != 0 {
		t.Fatalf("Roots = %v, want no roots", opts.Roots)
	}
}

func TestParseCleanDefaultsToDryRunCurrentDirectory(t *testing.T) {
	opts, err := Parse([]string{"clean"})
	if err != nil {
		t.Fatalf("Parse(clean) error = %v", err)
	}

	if !opts.DryRun {
		t.Fatal("DryRun = false, want true")
	}
	if got, want := strings.Join(opts.Roots, ","), "."; got != want {
		t.Fatalf("Roots = %q, want %q", got, want)
	}
}

func TestParseRepeatableExclusions(t *testing.T) {
	opts, err := Parse([]string{"review", "--exclude", "client", "--exclude", "archive", "/scan"})
	if err != nil || !reflect.DeepEqual(opts.ExcludePaths, []string{"client", "archive"}) {
		t.Fatalf("options=%+v err=%v", opts, err)
	}
}

func TestParseDiffAcceptsOnlyTwoFilesAndJSON(t *testing.T) {
	for _, args := range [][]string{{"diff", "--json", "before.json", "after.json"}, {"diff", "before.json", "after.json", "--json"}} {
		opts, err := Parse(args)
		if err != nil || opts.Command != CommandDiff || !opts.JSON || opts.BeforePath != "before.json" || opts.AfterPath != "after.json" {
			t.Fatalf("args=%v opts=%+v err=%v", args, opts, err)
		}
	}
	for _, args := range [][]string{
		{"diff"}, {"diff", "one"}, {"diff", "one", "two", "three"},
		{"diff", "--yes", "one", "two"}, {"diff", "--scan-root", "/tmp", "one", "two"},
		{"diff", "--select", "/tmp/wt", "one", "two"}, {"diff", "--provider", "github", "one", "two"},
		{"diff", "--dry-run", "one", "two"}, {"diff", "--exclude", "/tmp/ignored", "one", "two"},
		{"diff", "--retention", "1h", "one", "two"}, {"diff", "--cache-threshold", "1", "one", "two"},
		{"diff", "--pick", "one", "two"},
		{"diff", "before.json", "--yes"},
	} {
		if _, err := Parse(args); err == nil || !IsUsageError(err) {
			t.Fatalf("Parse(%v) error=%v", args, err)
		}
	}
}

func TestParseDiffPreservesDelimiterAndJSONFlagOrder(t *testing.T) {
	options, err := Parse([]string{"diff", "--", "--json", "after.json"})
	if err != nil || options.JSON || options.BeforePath != "--json" || options.AfterPath != "after.json" {
		t.Fatalf("delimiter options=%+v err=%v", options, err)
	}
	options, err = Parse([]string{"diff", "--", "before.json", "--yes"})
	if err != nil || options.BeforePath != "before.json" || options.AfterPath != "--yes" {
		t.Fatalf("literal operand options=%+v err=%v", options, err)
	}
	options, err = Parse([]string{"diff", "before.json", "--", "--yes"})
	if err != nil || options.BeforePath != "before.json" || options.AfterPath != "--yes" {
		t.Fatalf("middle delimiter options=%+v err=%v", options, err)
	}
	for _, args := range [][]string{{"diff", "before.json", "--"}, {"diff", "before.json", "--yes", "--", "after.json"}, {"diff", "", "after.json"}} {
		if _, err := Parse(args); err == nil || !IsUsageError(err) {
			t.Fatalf("args=%v error=%v", args, err)
		}
	}
	for _, tc := range []struct {
		args []string
		json bool
	}{
		{[]string{"diff", "before.json", "after.json", "--json=false", "--json"}, true},
		{[]string{"diff", "before.json", "after.json", "--json", "--json=false"}, false},
	} {
		options, err := Parse(tc.args)
		if err != nil || options.JSON != tc.json || options.BeforePath != "before.json" || options.AfterPath != "after.json" {
			t.Fatalf("args=%v options=%+v err=%v", tc.args, options, err)
		}
	}
}

func TestParseReviewFlagsAndReadOnlyContract(t *testing.T) {
	opts, err := Parse([]string{"review", "--repository", "/repo", "--repository", "/other", "--classification", "kept", "--classification", "error", "--group-by", "repository", "--sort-by", "size", "--select", "/repo/wt", "/scan"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Command != CommandReview || !opts.DryRun || strings.Join(opts.Repositories, ",") != "/repo,/other" || strings.Join(opts.Classifications, ",") != "kept,error" || opts.GroupBy != "repository" || opts.SortBy != "size" || strings.Join(opts.SelectedPaths, ",") != "/repo/wt" || strings.Join(opts.Roots, ",") != "/scan" {
		t.Fatalf("opts=%+v", opts)
	}
	for _, args := range [][]string{{"review", "--yes"}, {"review", "--yes=false"}, {"review", "-y=false"}, {"review", "--interactive"}, {"review", "--interactive=false"}, {"review", "--delete-branch"}, {"review", "--delete-branch=false"}, {"review", "--dry-run=false"}, {"review", "--group-by", "branch"}, {"review", "--sort-by", "branch"}} {
		if _, err := Parse(args); err == nil || !IsUsageError(err) {
			t.Fatalf("Parse(%v) err=%v, want usage", args, err)
		}
	}
	for _, static := range []string{"--help", "--version"} {
		if _, err := Parse([]string{"review", "--yes", static}); err == nil || !IsUsageError(err) {
			t.Fatalf("Parse(review --yes %s) err=%v, want usage", static, err)
		}
	}
}

func TestParseExplainRequiresOnePathAndRejectsDestructiveFlags(t *testing.T) {
	opts, err := Parse([]string{"explain", "--retention", "24h", "/repo/worktree"})
	if err != nil || opts.Command != CommandExplain || opts.ExplainPath != "/repo/worktree" || !opts.DryRun {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	for _, args := range [][]string{{"explain"}, {"explain", "/one", "/two"}, {"explain", "--yes=false", "/one"}, {"explain", "--interactive=false", "/one"}, {"explain", "--delete-branch=false", "/one"}, {"explain", "--dry-run=false", "/one"}} {
		if _, err := Parse(args); err == nil || !IsUsageError(err) {
			t.Fatalf("Parse(%v) err=%v, want usage", args, err)
		}
	}
}

func TestParseReclaimTarget(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want int64
	}{
		{[]string{"review", "--reclaim-target", "10"}, 10},
		{[]string{"review", "--reclaim-target", "10KiB"}, 10 << 10},
		{[]string{"review", "--reclaim-target", "10MiB"}, 10 << 20},
		{[]string{"review", "--reclaim-target", "10GiB"}, 10 << 30},
		{[]string{"review", "--reclaim-target", "1TiB"}, 1 << 40},
	} {
		opts, err := Parse(tc.args)
		if err != nil || !opts.HasReclaimTarget || opts.ReclaimTarget != tc.want {
			t.Fatalf("Parse(%v) options=%+v err=%v", tc.args, opts, err)
		}
	}
	for _, args := range [][]string{
		{"review", "--reclaim-target", "0"}, {"review", "--reclaim-target", "+1"},
		{"review", "--reclaim-target", "-1"}, {"review", "--reclaim-target", "1.5GiB"},
		{"review", "--reclaim-target", "1GB"}, {"review", "--reclaim-target", "999999999999999999999TiB"},
		{"review", "--reclaim-target", "1", "--select", "/worktree"},
	} {
		if _, err := Parse(args); err == nil || !IsUsageError(err) {
			t.Fatalf("Parse(%v) err=%v, want usage error", args, err)
		}
	}
}

func TestReclaimTargetOptionHelpersAndParseEdges(t *testing.T) {
	option := reclaimTargetOption{}
	if option.String() != "" {
		t.Fatalf("unset String=%q", option.String())
	}
	if err := option.Set("2KiB"); err != nil || option.String() != "2048" {
		t.Fatalf("option=%+v err=%v", option, err)
	}
	for _, input := range []string{"", "KiB", "9000000TiB"} {
		if _, err := parseReclaimTarget(input); err == nil {
			t.Fatalf("parseReclaimTarget(%q) accepted invalid value", input)
		}
	}
	if _, err := Parse([]string{"review", ""}); err == nil || !IsUsageError(err) {
		t.Fatalf("empty root error=%v", err)
	}
}

func TestParseReviewClassifications(t *testing.T) {
	valid := []string{"safe_to_remove", "merged_but_dirty", "unmerged", "stale_orphaned", "kept", "error"}
	args := []string{"review"}
	for _, classification := range valid {
		args = append(args, "--classification", classification)
	}
	opts, err := Parse(args)
	if err != nil || strings.Join(opts.Classifications, ",") != strings.Join(valid, ",") {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	if _, err := Parse([]string{"review", "--classification", "safe-to-remove"}); err == nil || !IsUsageError(err) {
		t.Fatalf("invalid classification error=%v", err)
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

func TestParseFlagContracts(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		wantRoots        string
		wantDryRun       bool
		wantYes          bool
		wantInteractive  bool
		wantDeleteBranch bool
		wantJSON         bool
	}{
		{name: "scan root", args: []string{"--scan-root", "/tmp/scan"}, wantRoots: "/tmp/scan", wantDryRun: true},
		{name: "flag-only positional root", args: []string{"--json", "/tmp/scan"}, wantRoots: "/tmp/scan", wantDryRun: true, wantJSON: true},
		{name: "dry run", args: []string{"--dry-run"}, wantRoots: ".", wantDryRun: true},
		{name: "yes", args: []string{"--yes"}, wantRoots: ".", wantYes: true},
		{name: "short yes", args: []string{"-y"}, wantRoots: ".", wantYes: true},
		{name: "interactive", args: []string{"--interactive"}, wantRoots: ".", wantInteractive: true},
		{name: "delete branch", args: []string{"--delete-branch"}, wantRoots: ".", wantDryRun: true, wantDeleteBranch: true},
		{name: "json", args: []string{"--json"}, wantRoots: ".", wantDryRun: true, wantJSON: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			opts, err := Parse(test.args)
			if err != nil {
				t.Fatalf("Parse(%v) error = %v", test.args, err)
			}
			if opts.Command != CommandClean {
				t.Fatalf("Command = %q, want %q", opts.Command, CommandClean)
			}
			if got := strings.Join(opts.Roots, ","); got != test.wantRoots {
				t.Fatalf("Roots = %q, want %q", got, test.wantRoots)
			}
			if opts.DryRun != test.wantDryRun || opts.Yes != test.wantYes || opts.Interactive != test.wantInteractive || opts.DeleteBranch != test.wantDeleteBranch || opts.JSON != test.wantJSON {
				t.Fatalf("options = %+v, want dry-run=%t yes=%t interactive=%t delete-branch=%t json=%t", opts, test.wantDryRun, test.wantYes, test.wantInteractive, test.wantDeleteBranch, test.wantJSON)
			}
		})
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
	for _, args := range [][]string{{"--help"}, {"-h"}, {"clean", "--help"}, {"clean", "-h"}} {
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
		{"/tmp/scan"},
		{"--bogus"},
		{"clean", "--group-by", "repository"},
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

	for _, want := range []string{"Usage:", "show help", "clean [flags] [roots...]", "review [flags] [roots...]", "explain [flags] PATH", "diff [--json] BEFORE AFTER", "scan when a flag is supplied", "--scan-root", "--exclude PATH", "for this invocation; repeatable", "--dry-run", "--yes", "--interactive", "--delete-branch", "--json", "--repository", "--classification", "--group-by", "--sort-by", "--select", "--version"} {
		if !strings.Contains(out, want) {
			t.Fatalf("usage missing %q:\n%s", want, out)
		}
	}
}

func TestProviderRetentionAndCacheFlags(t *testing.T) {
	opts, err := Parse([]string{"clean", "--provider", "github", "--provider-remote", "origin", "--retention", "72h", "--cache-threshold", "42"})
	if err != nil {
		t.Fatal(err)
	}
	if opts.Provider != "github" || opts.ProviderRemote != "origin" || opts.Retention.Hours() != 72 || opts.CacheThreshold != 42 {
		t.Fatalf("opts=%+v", opts)
	}
	for _, args := range [][]string{{"clean", "--provider", "gitlab"}, {"clean", "--provider-remote", "origin"}, {"clean", "--retention", "-1h"}, {"clean", "--cache-threshold", "-1"}} {
		if _, err := Parse(args); err == nil || !IsUsageError(err) {
			t.Fatalf("Parse(%v) err=%v, want usage", args, err)
		}
	}
}

func TestUsageAndFlagHelperContracts(t *testing.T) {
	t.Parallel()
	usage := &UsageError{Message: "bad invocation"}
	if usage.Error() != "bad invocation" || usage.ExitCode() != 2 || !usage.Usage() || !IsUsageError(usage) {
		t.Fatalf("usage error contract = %#v", usage)
	}
	if IsUsageError(errors.New("ordinary")) {
		t.Fatal("ordinary error reported as usage error")
	}

	var roots stringList
	if err := roots.Set(""); err == nil {
		t.Fatal("empty scan root accepted")
	}
	if err := roots.Set("/first"); err != nil {
		t.Fatal(err)
	}
	if got := roots.String(); got != "/first" {
		t.Fatalf("roots=%q", got)
	}

	for _, tc := range []struct {
		input string
		want  bool
	}{
		{"true", true}, {"FALSE", false}, {"yes", true}, {"n", false},
	} {
		got, err := parseBoolFlag(tc.input)
		if err != nil || got != tc.want {
			t.Fatalf("parseBoolFlag(%q)=(%t,%v), want %t", tc.input, got, err, tc.want)
		}
	}
	if _, err := parseBoolFlag("perhaps"); err == nil {
		t.Fatal("invalid boolean accepted")
	}

	value := boolOption{value: true}
	if err := value.Set("no"); err != nil || value.value || !value.set || value.String() != "false" || !value.IsBoolFlag() {
		t.Fatalf("bool option=%+v err=%v", value, err)
	}
	if err := value.Set("wat"); err == nil {
		t.Fatal("invalid bool option accepted")
	}

	var usageOutput strings.Builder
	WriteUsage(&usageOutput, "")
	if !strings.Contains(usageOutput.String(), "wtgc clean") {
		t.Fatalf("default usage=%q", usageOutput.String())
	}
}

func TestParseRejectsDashPrefixedPositionalArgument(t *testing.T) {
	_, err := Parse([]string{"clean", "--", "-looks-like-flag"})
	if err == nil || !IsUsageError(err) || !strings.Contains(err.Error(), "unknown argument") {
		t.Fatalf("post -- value err=%v", err)
	}
}
