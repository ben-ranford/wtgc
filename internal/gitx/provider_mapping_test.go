package gitx

import (
	"context"
	"testing"

	"github.com/ben-ranford/wtgc/internal/model"
	"github.com/ben-ranford/wtgc/internal/testgit"
)

func TestProviderMappingUsesExactGitUpstreamAtoms(t *testing.T) {
	repo := testgit.NewRepository(t)
	testgit.Run(t, repo.Path, "remote", "rename", "origin", "fork/team")
	testgit.Run(t, repo.Path, "checkout", "-b", "topic/one")
	testgit.Run(t, repo.Path, "push", "-u", "fork/team", "topic/one")
	client := New("git")
	identity := model.Repository{PrimaryPath: repo.Path, CommonDir: repo.Path + "/.git"}
	mapping, err := client.BranchUpstream(context.Background(), identity, "topic/one")
	if err != nil {
		t.Fatal(err)
	}
	if mapping.Remote != "fork/team" || mapping.Branch != "topic/one" {
		t.Fatalf("mapping=%+v", mapping)
	}
}

func TestProviderMappingFailsClosedForMissingUpstreamAndAmbiguousDefault(t *testing.T) {
	repo := testgit.NewRepository(t)
	client := New("git")
	identity := model.Repository{PrimaryPath: repo.Path, CommonDir: repo.Path + "/.git"}
	testgit.Run(t, repo.Path, "checkout", "-b", "local-only")
	if _, err := client.BranchUpstream(context.Background(), identity, "local-only"); err == nil {
		t.Fatal("missing upstream accepted")
	}
	testgit.Run(t, repo.Path, "checkout", "main")
	testgit.Run(t, repo.Path, "remote", "add", "second", repo.Origin)
	testgit.Run(t, repo.Path, "fetch", "second", "main")
	if _, err := client.DefaultTrackingRef(context.Background(), identity, "main"); err == nil {
		t.Fatal("multiple default tracking refs accepted")
	}
}

func TestProviderMappingRejectsMalformedBranchInput(t *testing.T) {
	repo := testgit.NewRepository(t)
	client := New("git")
	identity := model.Repository{PrimaryPath: repo.Path, CommonDir: repo.Path + "/.git"}
	for _, branch := range []string{"", "-bad", "bad..name"} {
		if _, err := client.BranchUpstream(context.Background(), identity, branch); err == nil {
			t.Fatalf("branch %q accepted", branch)
		}
	}
}

func TestProviderDefaultTrackingExplicitRemoteRequiresExactRef(t *testing.T) {
	repo := testgit.NewRepository(t)
	client := New("git")
	identity := model.Repository{PrimaryPath: repo.Path, CommonDir: repo.Path + "/.git"}
	got, branch, url, err := client.ProviderDefaultTracking(context.Background(), identity, "main", "origin")
	if err != nil || got != "origin" || branch != "main" || url == "" {
		t.Fatalf("mapping=%q/%q/%q err=%v", got, branch, url, err)
	}
	if _, _, _, err := client.ProviderDefaultTracking(context.Background(), identity, "main", "missing"); err == nil {
		t.Fatal("nonexistent selected remote accepted")
	}
}

func TestDefaultTrackingRefSupportsSlashRemoteAndDefaultBranchNames(t *testing.T) {
	repo := testgit.NewRepository(t)
	testgit.Run(t, repo.Path, "checkout", "-b", "release/main")
	testgit.Run(t, repo.Path, "push", "origin", "release/main")
	testgit.Run(t, repo.Path, "checkout", "main")
	testgit.Run(t, repo.Path, "remote", "rename", "origin", "fork/team")
	testgit.Run(t, repo.Path, "fetch", "fork/team", "release/main")

	client := New("git")
	identity := model.Repository{PrimaryPath: repo.Path, CommonDir: repo.Path + "/.git"}
	mapping, err := client.DefaultTrackingRef(context.Background(), identity, "release/main")
	if err != nil {
		t.Fatal(err)
	}
	if mapping.Remote != "fork/team" || mapping.Branch != "release/main" || mapping.URL == "" {
		t.Fatalf("mapping=%+v", mapping)
	}
	testgit.Run(t, repo.Path, "remote", "add", "unrelated/team", repo.Origin)
	mapping, err = client.DefaultTrackingRef(context.Background(), identity, "release/main")
	if err != nil || mapping.Remote != "fork/team" {
		t.Fatalf("mapping with unrelated remote=%+v err=%v", mapping, err)
	}

	remote, branch, url, err := client.ProviderDefaultTracking(context.Background(), identity, "release/main", "fork/team")
	if err != nil || remote != "fork/team" || branch != "release/main" || url == "" {
		t.Fatalf("explicit mapping=%q/%q/%q err=%v", remote, branch, url, err)
	}

	testgit.Run(t, repo.Path, "remote", "add", "backup/team", repo.Origin)
	testgit.Run(t, repo.Path, "fetch", "backup/team", "release/main")
	if _, err := client.DefaultTrackingRef(context.Background(), identity, "release/main"); err == nil {
		t.Fatal("ambiguous slash-named default tracking refs accepted")
	}
	remote, branch, url, err = client.ProviderDefaultTracking(context.Background(), identity, "release/main", "fork/team")
	if err != nil || remote != "fork/team" || branch != "release/main" || url == "" {
		t.Fatalf("explicit mapping after ambiguity=%q/%q/%q err=%v", remote, branch, url, err)
	}
}
