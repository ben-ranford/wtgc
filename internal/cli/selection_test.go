package cli

import (
	"reflect"
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
