package cli

import (
	"reflect"
	"strings"
	"testing"
)

func TestCleanRepeatableSelection(t *testing.T) {
	for _, prefix := range [][]string{{"clean"}, {}} {
		args := append(prefix, "--select", "one path", "--select=two", "--scan-root", "root")
		opts, err := Parse(args)
		if err != nil || !opts.DryRun || !reflect.DeepEqual(opts.SelectedPaths, []string{"one path", "two"}) || !reflect.DeepEqual(opts.Roots, []string{"root"}) {
			t.Fatalf("opts=%+v err=%v", opts, err)
		}
	}
	for _, args := range [][]string{{"clean", "--select"}, {"clean", "--select="}} {
		if _, err := Parse(args); !IsUsageError(err) {
			t.Fatalf("args=%v err=%v", args, err)
		}
	}
}

func TestPickCommandContract(t *testing.T) {
	for _, test := range []struct {
		args       []string
		wantDryRun bool
		wantPick   bool
		wantErr    string
	}{
		{args: []string{"clean", "--pick"}, wantDryRun: true, wantPick: true},
		{args: []string{"clean", "--pick", "--interactive"}, wantPick: true},
		{args: []string{"clean", "--pick", "--yes"}, wantErr: "--pick rejects --yes"},
		{args: []string{"clean", "--pick", "--yes=false"}, wantErr: "--pick rejects --yes"},
		{args: []string{"clean", "--pick", "--select", "one"}, wantErr: "--pick rejects --select"},
		{args: []string{"clean", "--pick", "--json=false"}, wantErr: "--pick rejects --json"},
		{args: []string{"review", "--pick"}, wantErr: "--pick is available only with clean"},
	} {
		t.Run(strings.Join(test.args, " "), func(t *testing.T) {
			opts, err := Parse(test.args)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Parse(%v) err=%v", test.args, err)
				}
				return
			}
			if err != nil || opts.Pick != test.wantPick || opts.DryRun != test.wantDryRun {
				t.Fatalf("Parse(%v) = %+v, %v", test.args, opts, err)
			}
		})
	}
}
