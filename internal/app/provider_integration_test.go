package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ben-ranford/wtgc/internal/gitx"
	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/provider"
	"github.com/ben-ranford/wtgc/internal/testgit"
)

func TestGitHubProviderSquashProofWithRealGitWorktree(t *testing.T) {
	repo, worktree, head, merge := squashFixture(t, "squash")
	client := githubProofClient(head, merge, head)
	application := New(gitx.New("git"))
	base := Options{Roots: []string{repo.Root}, Provider: client}

	off, err := application.Run(context.Background(), Options{Roots: []string{repo.Root}})
	if err != nil {
		t.Fatal(err)
	}
	if itemByPath(t, off, worktree).Classification == model.SafeToRemove {
		t.Fatal("provider-off squash worktree became eligible")
	}

	on, err := application.Run(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if got := itemByPath(t, on, worktree); got.Classification != model.SafeToRemove || got.ProviderPR != 42 {
		t.Fatalf("provider proof=%+v", got)
	}
	// A second default-tracking remote makes inference fail closed. Selecting the
	// base remote restores only base proof; the head upstream remains independent.
	testgit.Run(t, repo.Path, "remote", "add", "backup", repo.Origin)
	testgit.Run(t, repo.Path, "fetch", "backup", "main")
	testgit.Run(t, repo.Path, "remote", "set-head", "backup", "main")
	ambiguous, err := application.Run(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if itemByPath(t, ambiguous, worktree).Classification == model.SafeToRemove {
		t.Fatal("ambiguous base remote accepted")
	}
	selected, err := application.Run(context.Background(), Options{Roots: []string{repo.Root}, Provider: client, ProviderRemote: "origin"})
	if err != nil {
		t.Fatal(err)
	}
	if itemByPath(t, selected, worktree).Classification != model.SafeToRemove {
		t.Fatalf("selected base remote did not restore proof: %+v", itemByPath(t, selected, worktree))
	}

	executed, err := application.Run(context.Background(), Options{Roots: []string{repo.Root}, Provider: client, ProviderRemote: "origin", Execute: true})
	if err != nil {
		t.Fatal(err)
	}
	if !itemByPath(t, executed, worktree).Removed {
		t.Fatalf("execution=%+v", itemByPath(t, executed, worktree))
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists: %v", err)
	}
}

func TestGitHubProviderRealGitRejectsDirtyAndChangedProof(t *testing.T) {
	t.Run("dirty", func(t *testing.T) {
		repo, worktree, head, merge := squashFixture(t, "dirty")
		testgit.WriteFile(t, filepath.Join(worktree, "dirty.txt"), "dirty")
		inv, err := New(gitx.New("git")).Run(context.Background(), Options{Roots: []string{repo.Root}, Provider: githubProofClient(head, merge, head)})
		if err != nil {
			t.Fatal(err)
		}
		if got := itemByPath(t, inv, worktree); got.Classification == model.SafeToRemove {
			t.Fatalf("dirty item=%+v", got)
		}
	})
	t.Run("requery changed proof", func(t *testing.T) {
		repo, worktree, head, merge := squashFixture(t, "stale")
		inv, err := New(gitx.New("git")).Run(context.Background(), Options{Roots: []string{repo.Root}, Provider: githubProofClient(head, merge, "different"), Execute: true})
		if err != nil {
			t.Fatal(err)
		}
		if got := itemByPath(t, inv, worktree); got.Removed || got.Classification == model.SafeToRemove {
			t.Fatalf("stale proof item=%+v", got)
		}
		if _, err := os.Stat(worktree); err != nil {
			t.Fatalf("stale proof removed worktree: %v", err)
		}
	})
}

func TestGitHubProviderRealForkHeadAndSeparateBaseRemote(t *testing.T) {
	repo, worktree, head, merge := squashFixture(t, "fork-proof")
	testgit.Run(t, repo.Path, "remote", "add", "fork", repo.Origin)
	testgit.Run(t, repo.Path, "fetch", "fork", "fork-proof")
	testgit.Run(t, repo.Path, "fetch", "fork", "main")
	testgit.Run(t, repo.Path, "remote", "set-head", "fork", "main")
	testgit.Run(t, repo.Path, "remote", "set-url", "fork", "https://github.com/fork/source.git")
	testgit.Run(t, repo.Path, "branch", "--set-upstream-to=fork/fork-proof", "fork-proof")
	client := githubProofClientRepos(head, merge, "fork", "source", "owner", "repo", head)
	inv, err := New(gitx.New("git")).Run(context.Background(), Options{Roots: []string{repo.Root}, Provider: client, ProviderRemote: "origin"})
	if err != nil {
		t.Fatal(err)
	}
	if got := itemByPath(t, inv, worktree); got.Classification != model.SafeToRemove || got.ProviderPR != 42 {
		t.Fatalf("fork proof=%+v", got)
	}
}

func squashFixture(t *testing.T, branch string) (*testgit.Repository, string, string, string) {
	t.Helper()
	repo := testgit.NewRepository(t)
	worktree := repo.CreateUnmergedWorktree(t, branch)
	head := strings.TrimSpace(testgit.Run(t, worktree, "rev-parse", "HEAD"))
	testgit.Run(t, repo.Path, "checkout", "main")
	testgit.Run(t, repo.Path, "cherry-pick", head)
	testgit.Run(t, repo.Path, "commit", "--amend", "-m", "squash "+branch)
	merge := strings.TrimSpace(testgit.Run(t, repo.Path, "rev-parse", "HEAD"))
	testgit.Run(t, repo.Path, "push", "origin", "main")
	testgit.Run(t, repo.Path, "remote", "set-url", "origin", "https://github.com/owner/repo.git")
	return repo, worktree, head, merge
}
func itemByPath(t *testing.T, inv model.Inventory, path string) model.Worktree {
	t.Helper()
	wanted, _ := filepath.EvalSymlinks(path)
	for _, item := range inv.Worktrees {
		got, _ := filepath.EvalSymlinks(item.Path)
		if got == wanted {
			return item
		}
	}
	t.Fatalf("missing %s in %+v", path, inv.Worktrees)
	return model.Worktree{}
}

type providerRoundTrip func(*http.Request) (*http.Response, error)

func (f providerRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func githubProofClient(head, merge string, heads ...string) provider.Client {
	return githubProofClientRepos(head, merge, "owner", "repo", "owner", "repo", heads...)
}
func githubProofClientRepos(head, merge, headOwner, headRepo, baseOwner, baseRepo string, heads ...string) provider.Client {
	calls := 0
	return provider.NewGitHub(&http.Client{Transport: providerRoundTrip(func(r *http.Request) (*http.Response, error) {
		current := head
		if calls < len(heads) {
			current = heads[calls]
		}
		calls++
		body := fmt.Sprintf(`[{"state":"closed","number":42,"html_url":"https://github.com/%s/%s/pull/42","merged_at":"2026-01-02T03:04:05Z","merge_commit_sha":%q,"head":{"sha":%q,"ref":%q,"repo":{"name":%q,"owner":{"login":%q}}},"base":{"ref":"main","repo":{"name":%q,"owner":{"login":%q}}}}]`, baseOwner, baseRepo, merge, current, strings.TrimPrefix(r.URL.Query().Get("head"), headOwner+":"), headRepo, headOwner, baseRepo, baseOwner)
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})})
}
