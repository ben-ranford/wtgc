package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/cache"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/provider"
)

func TestClassificationsFailClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		record     model.RegisteredWorktree
		clean      bool
		ancestor   bool
		remote     bool
		want       model.Classification
		wantReason string
	}{
		{name: "primary", record: record("main"), clean: true, ancestor: true, remote: true, want: model.Kept, wantReason: "primary"},
		{name: "bare", record: bareRecord(), clean: true, ancestor: true, remote: true, want: model.Kept, wantReason: "bare"},
		{name: "prunable", record: model.RegisteredWorktree{Path: "/missing", Prunable: true}, want: model.Prunable, wantReason: "prunable"},
		{name: "detached", record: detachedRecord(), clean: true, ancestor: true, remote: true, want: model.Kept, wantReason: "detached"},
		{name: "locked", record: lockedRecord(), clean: true, ancestor: true, remote: true, want: model.Kept, wantReason: "locked"},
		{name: "default branch", record: branchRecord("main"), clean: true, ancestor: true, remote: true, want: model.Kept, wantReason: "default-branch"},
		{name: "unmerged", record: branchRecord("feature"), clean: true, ancestor: false, remote: true, want: model.Unmerged, wantReason: "not reachable"},
		{name: "merged dirty", record: branchRecord("feature"), clean: false, ancestor: true, remote: true, want: model.MergedButDirty, wantReason: "changes exist"},
		{name: "not pushed", record: branchRecord("feature"), clean: true, ancestor: true, remote: false, want: model.Unmerged, wantReason: "remote-tracking"},
		{name: "safe", record: branchRecord("feature"), clean: true, ancestor: true, remote: true, want: model.SafeToRemove, wantReason: "reachable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := newFakeGit(tt.record)
			backend.clean = []bool{tt.clean}
			backend.ancestor = tt.ancestor
			backend.remote = tt.remote
			inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if got := inventory.Worktrees[0].Classification; got != tt.want {
				t.Fatalf("classification = %q, want %q", got, tt.want)
			}
			if got := inventory.Worktrees[0].Reason; !contains(got, tt.wantReason) {
				t.Fatalf("reason = %q, want substring %q", got, tt.wantReason)
			}
			if backend.removeCalls != 0 || backend.pruneCalls != 0 {
				t.Fatal("dry-run mutated repository")
			}
		})
	}
}

func TestExecuteRevalidatesBeforeRemoval(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, false}
	backend.ancestor = true
	backend.remote = true

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.removeCalls != 0 {
		t.Fatalf("Remove() calls = %d, want 0", backend.removeCalls)
	}
	if got := inventory.Worktrees[0].Classification; got != model.MergedButDirty {
		t.Fatalf("classification = %q, want %q", got, model.MergedButDirty)
	}
	if !contains(inventory.Worktrees[0].Reason, "revalidation blocked") {
		t.Fatalf("reason = %q, want revalidation block", inventory.Worktrees[0].Reason)
	}
}

func TestDirtyUnmergedWorktreeIsKept(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{false}
	backend.ancestor = false
	backend.remote = true

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	item := inventory.Worktrees[0]
	if item.Classification != model.Kept {
		t.Fatalf("classification = %q, want %q", item.Classification, model.Kept)
	}
	if item.Dirty == nil || !*item.Dirty {
		t.Fatalf("dirty = %v, want true", item.Dirty)
	}
	if item.Action != model.ActionKept {
		t.Fatalf("action = %q, want %q", item.Action, model.ActionKept)
	}
	if backend.removeCalls != 0 {
		t.Fatal("dirty unmerged worktree was removed")
	}
}

func TestRetentionKeepsUntilExactBoundary(t *testing.T) {
	root := t.TempDir()
	backend := newFakeGit(model.RegisteredWorktree{Path: root, Head: "abc123", Branch: "feature"})
	backend.clean = []bool{true}
	backend.ancestor = true
	backend.remote = true
	observed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(root, observed, observed); err != nil {
		t.Fatal(err)
	}
	before, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Retention: time.Hour, Now: func() time.Time { return observed.Add(time.Hour - time.Nanosecond) }})
	if err != nil {
		t.Fatal(err)
	}
	if before.Worktrees[0].Classification != model.Kept {
		t.Fatalf("before boundary=%+v", before.Worktrees[0])
	}
	backend.resetCounters()
	backend.clean = []bool{true}
	at, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Retention: time.Hour, Now: func() time.Time { return observed.Add(time.Hour) }})
	if err != nil {
		t.Fatal(err)
	}
	if at.Worktrees[0].Classification != model.SafeToRemove {
		t.Fatalf("at boundary=%+v", at.Worktrees[0])
	}
}

func TestRetentionKeepsFutureLocalMtime(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(root, now.Add(time.Second), now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	backend := newFakeGit(model.RegisteredWorktree{Path: root, Head: "abc123", Branch: "feature"})
	backend.clean = []bool{true}
	backend.ancestor, backend.remote = true, true
	inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Retention: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	item := inv.Worktrees[0]
	if item.Classification != model.Kept || !contains(item.Reason, "retention timestamp could not be proven") {
		t.Fatalf("item=%+v", item)
	}
}

func TestExplicitZeroCacheThresholdReachesScanner(t *testing.T) {
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true}
	backend.ancestor, backend.remote = true, true
	var got int64 = -1
	_, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, CacheThreshold: 0, CacheScanner: func(_ context.Context, _ string, threshold int64) []cache.Warning {
		got = threshold
		return nil
	}})
	if err != nil || got != 0 {
		t.Fatalf("err=%v threshold=%d", err, got)
	}
}

func TestProviderProofRequiresExactIdentityAndSelectedDefaultReachability(t *testing.T) {
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true}
	backend.ancestorResults = []bool{false, true, true}
	p := proofProvider{proof: provider.PullRequest{Number: 7, URL: "https://github.com/owner/repo/pull/7", MergedAt: time.Now().UTC(), HeadSHA: "abc123", HeadOwner: "owner", HeadRepo: "repo", HeadRef: "feature", BaseOwner: "owner", BaseRepo: "repo", BaseRef: "main", MergeCommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Provider: p})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Worktrees[0].Classification != model.SafeToRemove || inv.Worktrees[0].ProviderPR != 7 {
		t.Fatalf("item=%+v", inv.Worktrees[0])
	}
	p.proof.HeadSHA = "reused"
	backend.ancestorResults = []bool{false}
	inv, err = New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Provider: p})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Worktrees[0].Classification == model.SafeToRemove {
		t.Fatal("wrong provider OID made candidate removable")
	}
}

func TestProviderProofRejectsLocalAndSelectedDefaultReachabilityFailures(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, test := range []struct {
		name     string
		ancestor []bool
		want     string
	}{
		{name: "merge SHA absent from local default", ancestor: []bool{false, false}, want: "local default reachability failed"},
		{name: "merge SHA absent from selected default", ancestor: []bool{false, true, false}, want: "selected default reachability failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeGit(branchRecord("feature"))
			backend.clean = []bool{true}
			backend.ancestorResults = test.ancestor
			// An unrelated remote containing the branch must not substitute for
			// the selected default remote in provider proof.
			backend.remote = true
			inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true, Provider: proofProvider{proof: validProviderProof(now)}, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			item := inv.Worktrees[0]
			if backend.removeCalls != 0 || item.Removed || item.Classification == model.SafeToRemove || !contains(item.Reason, test.want) {
				t.Fatalf("item=%+v removes=%d", item, backend.removeCalls)
			}
		})
	}
}

func TestProviderFutureMergeTimeIsRejectedWithoutRetention(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true}
	backend.ancestorResults = []bool{false}
	p := proofProvider{proof: provider.PullRequest{MergedAt: now.Add(time.Nanosecond), HeadSHA: "abc123", HeadOwner: "owner", HeadRepo: "repo", HeadRef: "feature", BaseOwner: "owner", BaseRepo: "repo", BaseRef: "main", MergeCommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}
	inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Provider: p, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if inv.Worktrees[0].Classification == model.SafeToRemove {
		t.Fatal("future provider proof made worktree eligible")
	}
}

func TestProviderFailureIsCategorizedWithoutLeakingError(t *testing.T) {
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true}
	backend.ancestor = false
	inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Provider: proofProvider{err: errors.New("HTTP 401 bearer secret-token")}})
	if err != nil {
		t.Fatalf("provider nonproof made run fail: %v", err)
	}
	item := inv.Worktrees[0]
	if !contains(item.Reason, "authentication failed") || contains(item.Reason, "secret-token") {
		t.Fatalf("reason=%q", item.Reason)
	}
}

func TestProviderFailureCategoriesAreSanitized(t *testing.T) {
	for _, test := range []struct {
		err  string
		want string
	}{{"HTTP 429 token=x", "rate limit failed"}, {"decode response secret", "malformed or oversized response"}, {"GitHub pull request response could not be decoded", "malformed or oversized response"}, {"no exact merged pull request proof found", "no exact merged pull request"}, {"network down secret", "provider response rejected"}} {
		if got := providerFailureCategory(errors.New(test.err)); got != test.want || contains(got, "secret") {
			t.Fatalf("%q => %q", test.err, got)
		}
	}
}

func TestActualProviderFailuresAreCategorizedWithoutLeakingTransportDetails(t *testing.T) {
	for _, test := range []struct {
		name      string
		transport http.RoundTripper
		want      string
	}{
		{name: "rate limit", transport: appRoundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("secret-token"))}, nil
		}), want: "rate limit failed"},
		{name: "offline", transport: appRoundTrip(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("offline secret-token")
		}), want: "timeout, cancellation, or offline failure"},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeGit(branchRecord("feature"))
			backend.clean = []bool{true}
			backend.ancestor = false
			inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Provider: provider.NewGitHub(&http.Client{Transport: test.transport})})
			if err != nil {
				t.Fatalf("provider nonproof made run fail: %v", err)
			}
			item := inv.Worktrees[0]
			if item.Classification == model.SafeToRemove || !contains(item.Reason, test.want) || contains(item.Reason, "secret-token") {
				t.Fatalf("item=%+v", item)
			}
		})
	}
}

func TestExecuteRejectsChangedProviderProof(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, true}
	backend.ancestorResults = []bool{false, true, true, false, true, true}
	proof := provider.PullRequest{Number: 7, MergedAt: now, HeadSHA: "abc123", HeadOwner: "owner", HeadRepo: "repo", HeadRef: "feature", BaseOwner: "owner", BaseRepo: "repo", BaseRef: "main", MergeCommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	changed := proof
	changed.MergeCommitSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Provider: &proofSequence{proofs: []provider.PullRequest{proof, changed}}, Execute: true, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if backend.removeCalls != 0 || inv.Worktrees[0].Classification != model.Kept || !contains(inv.Worktrees[0].Reason, "provider proof changed") {
		t.Fatalf("item=%+v removes=%d", inv.Worktrees[0], backend.removeCalls)
	}
}

func TestExecuteRejectsChangedDefaultBranch(t *testing.T) {
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, true}
	backend.ancestor, backend.remote = true, true
	backend.defaultBranches = []string{"main", "trunk"}
	inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if backend.removeCalls != 0 || inv.Worktrees[0].Classification != model.Kept || !contains(inv.Worktrees[0].Reason, "default branch changed") {
		t.Fatalf("item=%+v removes=%d", inv.Worktrees[0], backend.removeCalls)
	}
}

func TestExecuteRejectsProviderProofBasisSubstitution(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	proof := validProviderProof(now)
	for _, test := range []struct {
		name     string
		ancestor []bool
		provider provider.MergeFinder
	}{
		{name: "local to provider", ancestor: []bool{true, false, true, true}, provider: proofProvider{proof: proof}},
		{name: "provider to local", ancestor: []bool{false, true, true, true}, provider: proofProvider{proof: proof}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeGit(branchRecord("feature"))
			backend.clean = []bool{true, true}
			backend.ancestorResults = test.ancestor
			backend.remote = true
			inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true, Provider: test.provider, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if backend.removeCalls != 0 || inv.Worktrees[0].Classification != model.Kept || !contains(inv.Worktrees[0].Reason, "provider proof changed") {
				t.Fatalf("item=%+v removes=%d", inv.Worktrees[0], backend.removeCalls)
			}
		})
	}
}

func TestExecuteRejectsChangedProviderProofTuple(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	base := validProviderProof(now)
	for _, test := range []struct {
		name    string
		change  func(*provider.PullRequest)
		prepare func(*fakeGit)
	}{
		{name: "pull request number", change: func(p *provider.PullRequest) { p.Number++ }},
		{name: "merged timestamp", change: func(p *provider.PullRequest) { p.MergedAt = p.MergedAt.Add(-time.Second) }},
		{name: "merge commit", change: func(p *provider.PullRequest) { p.MergeCommitSHA = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" }},
		{name: "head remote", prepare: func(f *fakeGit) {
			f.providerUpstreams = []providerMapping{{remote: "origin", branch: "feature", url: "https://github.com/owner/repo.git"}, {remote: "fork", branch: "feature", url: "https://github.com/owner/repo.git"}}
		}},
		{name: "base remote", prepare: func(f *fakeGit) {
			f.providerDefaults = []providerMapping{{remote: "origin", branch: "main", url: "https://github.com/owner/repo.git"}, {remote: "upstream", branch: "main", url: "https://github.com/owner/repo.git"}}
		}},
		{name: "head owner", change: func(p *provider.PullRequest) { p.HeadOwner = "fork" }, prepare: func(f *fakeGit) {
			f.providerUpstreams = []providerMapping{{remote: "origin", branch: "feature", url: "https://github.com/owner/repo.git"}, {remote: "origin", branch: "feature", url: "https://github.com/fork/repo.git"}}
		}},
		{name: "head repository", change: func(p *provider.PullRequest) { p.HeadRepo = "other" }, prepare: func(f *fakeGit) {
			f.providerUpstreams = []providerMapping{{remote: "origin", branch: "feature", url: "https://github.com/owner/repo.git"}, {remote: "origin", branch: "feature", url: "https://github.com/owner/other.git"}}
		}},
		{name: "head ref", change: func(p *provider.PullRequest) { p.HeadRef = "feature/two" }, prepare: func(f *fakeGit) {
			f.providerUpstreams = []providerMapping{{remote: "origin", branch: "feature", url: "https://github.com/owner/repo.git"}, {remote: "origin", branch: "feature/two", url: "https://github.com/owner/repo.git"}}
		}},
		{name: "base owner", change: func(p *provider.PullRequest) { p.BaseOwner = "upstream" }, prepare: func(f *fakeGit) {
			f.providerDefaults = []providerMapping{{remote: "origin", branch: "main", url: "https://github.com/owner/repo.git"}, {remote: "origin", branch: "main", url: "https://github.com/upstream/repo.git"}}
		}},
		{name: "base repository", change: func(p *provider.PullRequest) { p.BaseRepo = "other" }, prepare: func(f *fakeGit) {
			f.providerDefaults = []providerMapping{{remote: "origin", branch: "main", url: "https://github.com/owner/repo.git"}, {remote: "origin", branch: "main", url: "https://github.com/owner/other.git"}}
		}},
		{name: "base ref", change: func(p *provider.PullRequest) { p.BaseRef = "trunk" }, prepare: func(f *fakeGit) {
			f.providerDefaults = []providerMapping{{remote: "origin", branch: "main", url: "https://github.com/owner/repo.git"}, {remote: "origin", branch: "trunk", url: "https://github.com/owner/repo.git"}}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := base
			if test.change != nil {
				test.change(&changed)
			}
			backend := newFakeGit(branchRecord("feature"))
			backend.clean = []bool{true, true}
			backend.ancestorResults = []bool{false, true, true, false, true, true}
			if test.prepare != nil {
				test.prepare(backend)
			}
			inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true, Provider: &proofSequence{proofs: []provider.PullRequest{base, changed}}, Now: func() time.Time { return now }})
			if err != nil {
				t.Fatal(err)
			}
			if backend.removeCalls != 0 || inv.Worktrees[0].Classification != model.Kept {
				t.Fatalf("item=%+v removes=%d", inv.Worktrees[0], backend.removeCalls)
			}
		})
	}
}

func TestExecuteRejectsChangedWorktreeHeadBeforeProviderReplay(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	backend := newFakeGit()
	backend.records = nil
	backend.listResults = [][]model.RegisteredWorktree{{branchRecord("feature")}, {{Path: "/repo-worktrees/feature", Head: "def456", Branch: "feature"}}}
	backend.clean = []bool{true}
	backend.ancestorResults = []bool{false, true, true}
	inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true, Provider: proofProvider{proof: validProviderProof(now)}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if backend.removeCalls != 0 || inv.Worktrees[0].Classification != model.Kept || !contains(inv.Worktrees[0].Reason, "HEAD or branch changed") {
		t.Fatalf("item=%+v removes=%d", inv.Worktrees[0], backend.removeCalls)
	}
}

func TestExecuteRejectsChangedOldRetentionTimestamp(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	first, second := now.Add(-2*time.Hour), now.Add(-3*time.Hour)
	if err := os.Chtimes(root, first, first); err != nil {
		t.Fatal(err)
	}
	backend := newFakeGit(model.RegisteredWorktree{Path: root, Head: "abc123", Branch: "feature"})
	backend.clean = []bool{true, true}
	backend.ancestor, backend.remote = true, true
	backend.cleanHook = func(call int) {
		if call == 2 {
			if err := os.Chtimes(root, second, second); err != nil {
				t.Errorf("change mtime: %v", err)
			}
		}
	}
	inv, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true, Retention: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if backend.removeCalls != 0 || inv.Worktrees[0].Classification != model.Kept || !contains(inv.Worktrees[0].Reason, "timestamp changed") {
		t.Fatalf("item=%+v removes=%d", inv.Worktrees[0], backend.removeCalls)
	}
}

func TestRevalidateRejectsProviderToMtimeRetentionBasisFlip(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	observed := now.Add(-2 * time.Hour)
	if err := os.Chtimes(root, observed, observed); err != nil {
		t.Fatal(err)
	}
	backend := newFakeGit(model.RegisteredWorktree{Path: root, Head: "abc123", Branch: "feature"})
	backend.clean = []bool{true}
	backend.ancestorResults = []bool{false, true, true}
	proof := validProviderProof(now)
	proof.MergedAt = observed
	application := New(backend)
	inv, err := application.Run(context.Background(), Options{Roots: []string{"/scan"}, Provider: proofProvider{proof: proof}, Retention: time.Hour, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	previous := inv.Worktrees[0]
	if previous.RetentionBasis != "provider_merged_at" {
		t.Fatalf("scan retention basis=%q", previous.RetentionBasis)
	}
	backend.clean = []bool{true}
	backend.ancestorResults = []bool{true}
	backend.remote = true
	fresh, ok := application.revalidate(context.Background(), backend.repository, Options{Provider: proofProvider{proof: proof}, Now: func() time.Time { return now }}, previous)
	if ok || fresh.Classification != model.Kept || !contains(fresh.Reason, "provider proof changed") {
		t.Fatalf("fresh=%+v ok=%v", fresh, ok)
	}
}

func TestRevalidateRejectsIncompleteRetentionEvidence(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for _, test := range []struct {
		name    string
		details *model.WorktreeDetails
	}{
		{name: "missing observed", details: &model.WorktreeDetails{RetentionBasis: "worktree_mtime", EligibleAt: timePtr(now)}},
		{name: "missing eligible", details: &model.WorktreeDetails{RetentionBasis: "worktree_mtime", ObservedAt: timePtr(now.Add(-time.Hour))}},
	} {
		t.Run(test.name, func(t *testing.T) {
			backend := newFakeGit(branchRecord("feature"))
			backend.clean = []bool{true}
			backend.ancestor, backend.remote = true, true
			previous := model.Worktree{Path: "/repo-worktrees/feature", Branch: "feature", Head: "abc123", DefaultBranch: "main", Classification: model.SafeToRemove, WorktreeDetails: test.details}
			fresh, ok := New(backend).revalidate(context.Background(), backend.repository, Options{Now: func() time.Time { return now }}, previous)
			if ok || fresh.Classification != model.Kept || !contains(fresh.Reason, "retention evidence is incomplete") {
				t.Fatalf("fresh=%+v ok=%v", fresh, ok)
			}
		})
	}
}

func validProviderProof(now time.Time) provider.PullRequest {
	return provider.PullRequest{Number: 7, MergedAt: now.Add(-time.Second), HeadSHA: "abc123", HeadOwner: "owner", HeadRepo: "repo", HeadRef: "feature", BaseOwner: "owner", BaseRepo: "repo", BaseRef: "main", MergeCommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
}

func timePtr(value time.Time) *time.Time { return &value }

type proofProvider struct {
	proof provider.PullRequest
	err   error
}
type proofSequence struct {
	proofs []provider.PullRequest
	calls  int
}

func (p *proofSequence) FindMerged(context.Context, provider.Query) (provider.PullRequest, error) {
	proof := p.proofs[p.calls]
	p.calls++
	return proof, nil
}

func (p proofProvider) FindMerged(context.Context, provider.Query) (provider.PullRequest, error) {
	return p.proof, p.err
}

func TestExecuteRemovesThenOptionallyDeletesBranch(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, true}
	backend.ancestor = true
	backend.remote = true

	inventory, err := New(backend).Run(context.Background(), Options{
		Roots:        []string{"/scan"},
		Execute:      true,
		DeleteBranch: true,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.removeCalls != 1 || backend.deleteCalls != 1 {
		t.Fatalf("remove/delete calls = %d/%d, want 1/1", backend.removeCalls, backend.deleteCalls)
	}
	if !inventory.Worktrees[0].Removed || !inventory.Worktrees[0].BranchDeleted {
		t.Fatalf("result = %+v, want removed branch", inventory.Worktrees[0])
	}
	if inventory.Worktrees[0].Action != model.ActionRemovedBranchDeleted {
		t.Fatalf("action = %q, want %q", inventory.Worktrees[0].Action, model.ActionRemovedBranchDeleted)
	}
	if inventory.Worktrees[0].ReclaimedBytes != 2048 || inventory.Summary.ReclaimedBytes != 2048 {
		t.Fatalf("reclaimed item/summary = %d/%d, want 2048/2048", inventory.Worktrees[0].ReclaimedBytes, inventory.Summary.ReclaimedBytes)
	}
	if backend.pruneCalls != 0 {
		t.Fatalf("Prune() calls = %d, want 0 without an accepted stale record", backend.pruneCalls)
	}
}

func TestProviderSquashRemovalRetainsBranchWithoutDeleteError(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, true}
	backend.ancestorResults = []bool{false, true, true, false, true, true}
	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true, DeleteBranch: true, Provider: proofProvider{proof: validProviderProof(now)}, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	item := inventory.Worktrees[0]
	if !item.Removed || item.BranchDeleted || backend.removeCalls != 1 || backend.deleteCalls != 0 || !contains(item.Error, "provider squash proof") {
		t.Fatalf("item=%+v remove/delete=%d/%d", item, backend.removeCalls, backend.deleteCalls)
	}
}

func TestDryRunPopulatesActionAndReclaimedFields(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true}
	backend.ancestor = true
	backend.remote = true

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	item := inventory.Worktrees[0]
	if item.Action != model.ActionWouldRemove {
		t.Fatalf("action = %q, want %q", item.Action, model.ActionWouldRemove)
	}
	if item.ReclaimedBytes != 0 || inventory.Summary.ReclaimedBytes != 0 {
		t.Fatalf("reclaimed item/summary = %d/%d, want 0/0", item.ReclaimedBytes, inventory.Summary.ReclaimedBytes)
	}
}

func TestErrorsKeepWorktree(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.cleanErr = errors.New("status failed")
	backend.ancestor = true
	backend.remote = true

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}})
	if err == nil {
		t.Fatal("classification command failure should produce a non-zero run result")
	}
	if got := inventory.Worktrees[0].Classification; got != model.Error {
		t.Fatalf("classification = %q, want %q", got, model.Error)
	}
	if backend.removeCalls != 0 {
		t.Fatal("ambiguous worktree was removed")
	}
}

func TestNoRepositoriesReturnsDiscoveryError(t *testing.T) {
	t.Parallel()
	backend := newFakeGit()
	backend.repositories = nil

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}})
	if err == nil {
		t.Fatal("Run() error = nil, want no repositories error")
	}
	if inventory.Summary.Repositories != 0 || len(inventory.Worktrees) != 0 {
		t.Fatalf("inventory = %+v, want no repositories or worktrees", inventory)
	}
}

func TestDefaultBranchErrorPreventsUnsafeClassification(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"), model.RegisteredWorktree{Path: "/repo-worktrees/missing", Prunable: true})
	backend.defaultErr = errors.New("remote head missing")

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want default branch error")
	}
	if len(inventory.Worktrees) != 2 {
		t.Fatalf("worktrees = %#v, want records emitted after list succeeds", inventory.Worktrees)
	}
	foundPrunable := false
	for _, item := range inventory.Worktrees {
		if item.Prunable {
			foundPrunable = true
			if item.Classification != model.Prunable {
				t.Fatalf("prunable classification = %q, want %q", item.Classification, model.Prunable)
			}
		} else if item.Classification != model.Kept {
			t.Fatalf("live classification = %q, want %q", item.Classification, model.Kept)
		}
		if item.Error == "" || !contains(item.Error, "default branch") {
			t.Fatalf("worktree error = %q, want default branch error", item.Error)
		}
	}
	if !foundPrunable {
		t.Fatal("default-branch error inventory did not retain prunable record")
	}
	if backend.removeCalls != 0 || backend.pruneCalls != 0 {
		t.Fatalf("mutating calls remove/prune = %d/%d, want 0/0", backend.removeCalls, backend.pruneCalls)
	}
}

func TestListErrorPreventsUnsafeClassification(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.listErrs = []error{errors.New("worktree list failed")}

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want list error")
	}
	if len(inventory.Worktrees) != 0 {
		t.Fatalf("worktrees = %#v, want no classifications from failed list", inventory.Worktrees)
	}
	if backend.removeCalls != 0 || backend.pruneCalls != 0 {
		t.Fatalf("mutating calls remove/prune = %d/%d, want 0/0", backend.removeCalls, backend.pruneCalls)
	}
}

func TestDiskUsageErrorKeepsWorktree(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.diskErr = errors.New("permission denied")

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want disk usage error")
	}
	if got := inventory.Worktrees[0].Classification; got != model.Error {
		t.Fatalf("classification = %q, want %q", got, model.Error)
	}
	if backend.removeCalls != 0 {
		t.Fatal("worktree with unknown disk state was removed")
	}
}

func TestProtectedPathKeepsContainingWorktree(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))

	inventory, err := New(backend).Run(context.Background(), Options{
		Roots:         []string{"/scan"},
		Execute:       true,
		ProtectedPath: "/repo-worktrees/feature/subdir/process",
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := inventory.Worktrees[0].Classification; got != model.Kept {
		t.Fatalf("classification = %q, want %q", got, model.Kept)
	}
	if backend.removeCalls != 0 {
		t.Fatal("protected worktree was removed")
	}
}

func TestInteractiveRejectionKeepsSafeWorktree(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true}
	backend.ancestor = true
	backend.remote = true

	inventory, err := New(backend).Run(context.Background(), Options{
		Roots:       []string{"/scan"},
		Execute:     true,
		Interactive: true,
		Confirm:     func(model.Worktree) bool { return false },
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := inventory.Worktrees[0].Classification; got != model.Kept {
		t.Fatalf("classification = %q, want %q", got, model.Kept)
	}
	if backend.removeCalls != 0 {
		t.Fatal("interactively rejected worktree was removed")
	}
}

func TestExecutePrunesAcceptedPrunableMetadata(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(model.RegisteredWorktree{Path: "/repo-worktrees/missing", Prunable: true})
	backend.listResults = [][]model.RegisteredWorktree{
		{{Path: "/repo-worktrees/missing", Prunable: true}},
		{{Path: "/repo-worktrees/missing", Prunable: true}},
	}

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if backend.pruneCalls != 1 {
		t.Fatalf("Prune() calls = %d, want 1", backend.pruneCalls)
	}
	if !inventory.Worktrees[0].Removed {
		t.Fatalf("removed = false, want prunable metadata marked removed")
	}
	if inventory.Worktrees[0].Action != model.ActionPruned {
		t.Fatalf("action = %q, want %q", inventory.Worktrees[0].Action, model.ActionPruned)
	}
}

func TestPrunableRevalidationBlocksWhenMetadataChanges(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(model.RegisteredWorktree{Path: "/repo-worktrees/missing", Prunable: true})
	backend.listResults = [][]model.RegisteredWorktree{
		{{Path: "/repo-worktrees/missing", Prunable: true}},
		{{Path: "/repo-worktrees/missing"}},
	}

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want changed prunable set error")
	}
	if backend.pruneCalls != 0 {
		t.Fatal("prune ran after prunable metadata changed")
	}
	if got := inventory.Worktrees[0].Classification; got != model.Error {
		t.Fatalf("classification = %q, want %q", got, model.Error)
	}
	if !contains(inventory.Worktrees[0].Reason, "prunable set changed") {
		t.Fatalf("reason = %q, want revalidation block", inventory.Worktrees[0].Reason)
	}
}

func TestPrunableRevalidationErrorFailsClosed(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(model.RegisteredWorktree{Path: "/repo-worktrees/missing", Prunable: true})
	backend.listErrs = []error{nil, errors.New("list disappeared")}

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want revalidation error")
	}
	if got := inventory.Worktrees[0].Classification; got != model.Error {
		t.Fatalf("classification = %q, want %q", got, model.Error)
	}
	if backend.pruneCalls != 0 {
		t.Fatal("prune ran after prunable revalidation error")
	}
}

func TestPrunableExactSetChangeBlocksPrune(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(model.RegisteredWorktree{Path: "/repo-worktrees/missing-a", Prunable: true})
	backend.listResults = [][]model.RegisteredWorktree{
		{{Path: "/repo-worktrees/missing-a", Prunable: true}},
		{
			{Path: "/repo-worktrees/missing-a", Prunable: true},
			{Path: "/repo-worktrees/missing-b", Prunable: true},
		},
	}

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want changed prunable set error")
	}
	if backend.pruneCalls != 0 {
		t.Fatal("prune ran after prunable set changed")
	}
	if inventory.Worktrees[0].Action != model.ActionKept {
		t.Fatalf("action = %q, want %q", inventory.Worktrees[0].Action, model.ActionKept)
	}
	if !contains(inventory.Worktrees[0].Error, "changed") {
		t.Fatalf("item error = %q, want changed-set evidence", inventory.Worktrees[0].Error)
	}
}

func TestPruneErrorMarksAcceptedPrunable(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(model.RegisteredWorktree{Path: "/repo-worktrees/missing", Prunable: true})
	backend.pruneErr = errors.New("prune failed")

	inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}, Execute: true})
	if err == nil {
		t.Fatal("Run() error = nil, want prune error")
	}
	if inventory.Worktrees[0].Removed {
		t.Fatal("prunable metadata marked removed after prune failed")
	}
	if inventory.Worktrees[0].Error != "prune failed" {
		t.Fatalf("worktree error = %q, want prune failed", inventory.Worktrees[0].Error)
	}
}

func TestRemoveErrorSkipsBranchDeletion(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, true}
	backend.ancestor = true
	backend.remote = true
	backend.removeErr = errors.New("remove failed")

	inventory, err := New(backend).Run(context.Background(), Options{
		Roots:        []string{"/scan"},
		Execute:      true,
		DeleteBranch: true,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want remove error")
	}
	if inventory.Worktrees[0].Removed {
		t.Fatal("worktree marked removed after remove failed")
	}
	if got := inventory.Worktrees[0].Classification; got != model.Error {
		t.Fatalf("classification = %q, want %q", got, model.Error)
	}
	if got := inventory.Worktrees[0].Action; got != model.ActionKept {
		t.Fatalf("action = %q, want %q", got, model.ActionKept)
	}
	if inventory.Summary.Safe != 0 || inventory.Summary.Skipped != 1 || inventory.Summary.Removed != 0 {
		t.Fatalf("summary = %+v, want failed removal counted as one skipped item and no safe/removal total", inventory.Summary)
	}
	if backend.deleteCalls != 0 {
		t.Fatal("branch delete ran after remove failed")
	}
}

func TestDeleteBranchErrorRetainsBranch(t *testing.T) {
	t.Parallel()
	backend := newFakeGit(branchRecord("feature"))
	backend.clean = []bool{true, true}
	backend.ancestor = true
	backend.remote = true
	backend.deleteErr = errors.New("branch not merged")

	inventory, err := New(backend).Run(context.Background(), Options{
		Roots:        []string{"/scan"},
		Execute:      true,
		DeleteBranch: true,
	})
	if err == nil {
		t.Fatal("Run() error = nil, want branch delete error")
	}
	if !inventory.Worktrees[0].Removed {
		t.Fatal("worktree should remain marked removed after branch delete failure")
	}
	if inventory.Worktrees[0].BranchDeleted {
		t.Fatal("branch marked deleted after delete failure")
	}
	if !contains(inventory.Worktrees[0].Error, "branch retained") {
		t.Fatalf("worktree error = %q, want branch retained", inventory.Worktrees[0].Error)
	}
}

func TestGlobalClassificationPoolScansManyRepositories(t *testing.T) {
	t.Parallel()
	const repoCount = 10
	const recordsPerRepo = 35
	const workerCount = 8
	backend := newFakeGit()
	backend.repositories = nil
	backend.recordsByRepo = make(map[string][]model.RegisteredWorktree)
	backend.cleanByPath = make(map[string]bool)
	backend.cleanStarted = make(chan struct{}, repoCount*recordsPerRepo)
	cleanRelease := make(chan struct{})
	backend.cleanRelease = cleanRelease
	backend.ancestor = true
	backend.remote = true
	for repoIndex := 0; repoIndex < repoCount; repoIndex++ {
		repo := model.Repository{
			CommonDir:   fmt.Sprintf("/repo-%02d/.git", repoIndex),
			PrimaryPath: fmt.Sprintf("/repo-%02d", repoIndex),
		}
		backend.repositories = append(backend.repositories, repo)
		for recordIndex := 0; recordIndex < recordsPerRepo; recordIndex++ {
			path := fmt.Sprintf("/repo-%02d-worktrees/feature-%02d", repoIndex, recordIndex)
			backend.recordsByRepo[repo.PrimaryPath] = append(backend.recordsByRepo[repo.PrimaryPath], model.RegisteredWorktree{
				Path:   path,
				Head:   fmt.Sprintf("head-%02d-%02d", repoIndex, recordIndex),
				Branch: fmt.Sprintf("feature-%02d", recordIndex),
			})
			backend.cleanByPath[path] = true
		}
	}

	type runResult struct {
		inventory model.Inventory
		err       error
	}
	resultCh := make(chan runResult, 1)
	go func() {
		inventory, err := New(backend).Run(context.Background(), Options{Roots: []string{"/scan"}})
		resultCh <- runResult{inventory: inventory, err: err}
	}()
	for range workerCount {
		select {
		case <-backend.cleanStarted:
		case <-time.After(time.Second):
			close(cleanRelease)
			t.Fatal("classification workers did not start concurrently")
		}
	}
	close(cleanRelease)
	result := <-resultCh
	inventory, err := result.inventory, result.err
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(inventory.Worktrees) != repoCount*recordsPerRepo {
		t.Fatalf("worktrees = %d, want %d", len(inventory.Worktrees), repoCount*recordsPerRepo)
	}
	if backend.cleanCalls != repoCount*recordsPerRepo {
		t.Fatalf("clean calls = %d, want %d", backend.cleanCalls, repoCount*recordsPerRepo)
	}
	if backend.maxCleanCalls != workerCount {
		t.Fatalf("maximum concurrent clean calls = %d, want %d", backend.maxCleanCalls, workerCount)
	}
	for i := 1; i < len(inventory.Worktrees); i++ {
		prev, cur := inventory.Worktrees[i-1], inventory.Worktrees[i]
		if prev.Repository > cur.Repository || (prev.Repository == cur.Repository && prev.Path > cur.Path) {
			t.Fatalf("worktrees not sorted at %d: %s/%s before %s/%s", i, prev.Repository, prev.Path, cur.Repository, cur.Path)
		}
	}
}

func BenchmarkRunClassifies350Worktrees(b *testing.B) {
	const repoCount = 10
	const recordsPerRepo = 35
	backend := newLargeFakeGit(repoCount, recordsPerRepo)
	application := New(backend)

	b.ReportAllocs()
	for b.Loop() {
		backend.resetCounters()
		// This benchmark measures classifier throughput; cache walking has its own
		// filesystem-backed tests and must not turn fake paths into warning costs.
		inventory, err := application.Run(context.Background(), Options{Roots: []string{"/scan"}, CacheScanner: func(context.Context, string, int64) []cache.Warning { return nil }})
		if err != nil {
			b.Fatalf("Run() error = %v", err)
		}
		if len(inventory.Worktrees) != repoCount*recordsPerRepo {
			b.Fatalf("worktrees = %d, want %d", len(inventory.Worktrees), repoCount*recordsPerRepo)
		}
	}
}

type fakeGit struct {
	mu                sync.Mutex
	repository        model.Repository
	repositories      []model.Repository
	records           []model.RegisteredWorktree
	recordsByRepo     map[string][]model.RegisteredWorktree
	listResults       [][]model.RegisteredWorktree
	listErrs          []error
	clean             []bool
	cleanByPath       map[string]bool
	cleanDelay        time.Duration
	cleanStarted      chan struct{}
	cleanRelease      <-chan struct{}
	cleanHook         func(int)
	cleanCalls        int
	activeCleanCalls  int
	maxCleanCalls     int
	cleanErr          error
	defaultErr        error
	defaultBranches   []string
	ancestor          bool
	ancestorResults   []bool
	remote            bool
	diskErr           error
	removeErr         error
	pruneErr          error
	deleteErr         error
	removeCalls       int
	pruneCalls        int
	deleteCalls       int
	providerUpstreams []providerMapping
	providerDefaults  []providerMapping
}

type providerMapping struct {
	remote string
	branch string
	url    string
	err    error
}

type appRoundTrip func(*http.Request) (*http.Response, error)

func (r appRoundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return r(request) }

func newFakeGit(records ...model.RegisteredWorktree) *fakeGit {
	return &fakeGit{
		repository:   model.Repository{CommonDir: "/repo/.git", PrimaryPath: "/repo"},
		repositories: []model.Repository{{CommonDir: "/repo/.git", PrimaryPath: "/repo"}},
		records:      records,
	}
}

func newLargeFakeGit(repoCount, recordsPerRepo int) *fakeGit {
	backend := newFakeGit()
	backend.repositories = nil
	backend.recordsByRepo = make(map[string][]model.RegisteredWorktree)
	backend.cleanByPath = make(map[string]bool)
	backend.ancestor = true
	backend.remote = true
	for repoIndex := 0; repoIndex < repoCount; repoIndex++ {
		repo := model.Repository{
			CommonDir:   fmt.Sprintf("/repo-%02d/.git", repoIndex),
			PrimaryPath: fmt.Sprintf("/repo-%02d", repoIndex),
		}
		backend.repositories = append(backend.repositories, repo)
		for recordIndex := 0; recordIndex < recordsPerRepo; recordIndex++ {
			path := fmt.Sprintf("/repo-%02d-worktrees/feature-%02d", repoIndex, recordIndex)
			backend.recordsByRepo[repo.PrimaryPath] = append(backend.recordsByRepo[repo.PrimaryPath], model.RegisteredWorktree{
				Path:   path,
				Head:   fmt.Sprintf("head-%02d-%02d", repoIndex, recordIndex),
				Branch: fmt.Sprintf("feature-%02d", recordIndex),
			})
			backend.cleanByPath[path] = true
		}
	}
	return backend
}

func (f *fakeGit) resetCounters() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanCalls = 0
	f.activeCleanCalls = 0
	f.maxCleanCalls = 0
	f.removeCalls = 0
	f.pruneCalls = 0
	f.deleteCalls = 0
}

func (f *fakeGit) Discover(context.Context, []string) ([]model.Repository, []error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.Repository(nil), f.repositories...), nil
}
func (f *fakeGit) List(_ context.Context, repo model.Repository) ([]model.RegisteredWorktree, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var err error
	if len(f.listErrs) > 0 {
		err = f.listErrs[0]
		f.listErrs = f.listErrs[1:]
	}
	if err != nil {
		return nil, err
	}
	if len(f.listResults) > 0 {
		records := append([]model.RegisteredWorktree(nil), f.listResults[0]...)
		f.listResults = f.listResults[1:]
		return records, nil
	}
	if f.recordsByRepo != nil {
		return append([]model.RegisteredWorktree(nil), f.recordsByRepo[repo.PrimaryPath]...), nil
	}
	return append([]model.RegisteredWorktree(nil), f.records...), nil
}
func (f *fakeGit) DefaultBranch(context.Context, model.Repository) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.defaultErr != nil {
		return "", f.defaultErr
	}
	if len(f.defaultBranches) > 0 {
		value := f.defaultBranches[0]
		if len(f.defaultBranches) > 1 {
			f.defaultBranches = f.defaultBranches[1:]
		}
		return value, nil
	}
	return "main", nil
}
func (f *fakeGit) IsClean(_ context.Context, path string) (bool, error) {
	f.mu.Lock()
	f.cleanCalls++
	call := f.cleanCalls
	f.activeCleanCalls++
	if f.activeCleanCalls > f.maxCleanCalls {
		f.maxCleanCalls = f.activeCleanCalls
	}
	delay := f.cleanDelay
	started := f.cleanStarted
	release := f.cleanRelease
	f.mu.Unlock()
	if hook := f.cleanHook; hook != nil {
		hook(call)
	}

	if started != nil {
		started <- struct{}{}
	}
	if release != nil {
		<-release
	} else if delay > 0 {
		time.Sleep(delay)
	}

	f.mu.Lock()
	defer func() {
		f.activeCleanCalls--
		f.mu.Unlock()
	}()
	if f.cleanErr != nil {
		return false, f.cleanErr
	}
	if f.cleanByPath != nil {
		return f.cleanByPath[path], nil
	}
	if len(f.clean) == 0 {
		return false, nil
	}
	value := f.clean[0]
	if len(f.clean) > 1 {
		f.clean = f.clean[1:]
	}
	return value, nil
}
func (f *fakeGit) IsAncestor(context.Context, model.Repository, string, string) (bool, error) {
	if len(f.ancestorResults) > 0 {
		value := f.ancestorResults[0]
		f.ancestorResults = f.ancestorResults[1:]
		return value, nil
	}
	return f.ancestor, nil
}
func (f *fakeGit) RemoteContains(context.Context, model.Repository, string) (bool, error) {
	return f.remote, nil
}
func (f *fakeGit) DiskUsage(string) (int64, error) {
	if f.diskErr != nil {
		return 0, f.diskErr
	}
	return 2048, nil
}
func (f *fakeGit) Remove(context.Context, model.Repository, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeCalls++
	return f.removeErr
}
func (f *fakeGit) Prune(context.Context, model.Repository) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pruneCalls++
	return f.pruneErr
}
func (f *fakeGit) DeleteBranch(context.Context, model.Repository, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteCalls++
	return f.deleteErr
}
func (f *fakeGit) ProviderUpstream(context.Context, model.Repository, string) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.providerUpstreams) > 0 {
		mapping := f.providerUpstreams[0]
		if len(f.providerUpstreams) > 1 {
			f.providerUpstreams = f.providerUpstreams[1:]
		}
		return mapping.remote, mapping.branch, mapping.url, mapping.err
	}
	return "origin", "feature", "https://github.com/owner/repo.git", nil
}
func (f *fakeGit) ProviderDefaultTracking(context.Context, model.Repository, string, string) (string, string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.providerDefaults) > 0 {
		mapping := f.providerDefaults[0]
		if len(f.providerDefaults) > 1 {
			f.providerDefaults = f.providerDefaults[1:]
		}
		return mapping.remote, mapping.branch, mapping.url, mapping.err
	}
	return "origin", "main", "https://github.com/owner/repo.git", nil
}

func record(branch string) model.RegisteredWorktree {
	r := branchRecord(branch)
	r.Primary = true
	return r
}

func bareRecord() model.RegisteredWorktree {
	return model.RegisteredWorktree{Path: "/repo-bare", Head: "abc123", Bare: true}
}

func branchRecord(branch string) model.RegisteredWorktree {
	return model.RegisteredWorktree{Path: "/repo-worktrees/" + branch, Head: "abc123", Branch: branch}
}

func detachedRecord() model.RegisteredWorktree {
	return model.RegisteredWorktree{Path: "/repo-worktrees/detached", Head: "abc123", Detached: true}
}

func lockedRecord() model.RegisteredWorktree {
	r := branchRecord("feature")
	r.Locked = true
	return r
}

func contains(value, substring string) bool {
	return len(substring) == 0 || (len(value) >= len(substring) && find(value, substring))
}

func find(value, substring string) bool {
	for i := 0; i+len(substring) <= len(value); i++ {
		if value[i:i+len(substring)] == substring {
			return true
		}
	}
	return false
}
