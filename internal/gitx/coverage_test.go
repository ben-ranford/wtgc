package gitx

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ben-ranford/wtgc/internal/model"
)

func TestCoverageValidationAndPorcelainVariants(t *testing.T) {
	t.Parallel()
	for _, branch := range []string{" ", " feature", "-feature", "refs/heads/main", `a\\b`, "@", "a..b", "a//b", "a@{b", "/a", "a/", "a.", "a.lock", "a b", "a~b", "a\x7fb"} {
		if err := validateShortBranchName("branch", branch); err == nil {
			t.Fatalf("accepted invalid branch %q", branch)
		}
	}
	if err := validateShortBranchName("branch", "feature/x"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"", "abc", strings.Repeat("g", 40)} {
		if isFullObjectID(value) {
			t.Fatalf("accepted object id %q", value)
		}
	}
	if !isFullObjectID(strings.Repeat("a", 40)) || !isFullObjectID(strings.Repeat("B", 64)) {
		t.Fatal("valid object id rejected")
	}
	if shortBranch("refs/heads/main") != "main" || shortBranch("origin/main") != "origin/main" {
		t.Fatal("short branch mismatch")
	}
	if err := validateRepository(model.Repository{}); err == nil {
		t.Fatal("empty repository accepted")
	}

	records, err := ParseWorktreeListPorcelainZ([]byte("worktree /a\x00HEAD h\x00bare\x00worktree /b\x00HEAD i\x00branch refs/heads/feature\x00locked\x00detached\x00prunable\x00"))
	if err != nil || len(records) != 2 || !records[0].Bare || records[1].Branch != "feature" || !records[1].Locked || !records[1].Detached || !records[1].Prunable {
		t.Fatalf("records=%+v err=%v", records, err)
	}
}

func TestCoverageProviderAndTrackingSuccessAndFailures(t *testing.T) {
	repo := model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"}
	validOID := strings.Repeat("a", 40)
	client := New(providerTrackingGit(t, validOID))
	upstream, err := client.BranchUpstream(context.Background(), repo, "feature")
	if err != nil || upstream.Remote != "origin" || upstream.Branch != "main" {
		t.Fatalf("upstream=%+v err=%v", upstream, err)
	}
	if remote, branch, url, err := client.ProviderUpstream(context.Background(), repo, "feature"); err != nil || remote != "origin" || branch != "main" || url == "" {
		t.Fatalf("provider upstream=%q %q %q %v", remote, branch, url, err)
	}
	if remote, branch, _, err := client.ProviderDefaultTracking(context.Background(), repo, "main", "origin"); err != nil || remote != "origin" || branch != "main" {
		t.Fatalf("selected=%q %q %v", remote, branch, err)
	}
	if ref, err := client.DefaultTrackingRef(context.Background(), repo, "main"); err != nil || ref.Remote != "origin" {
		t.Fatalf("default=%+v %v", ref, err)
	}
	if _, _, _, err := client.ProviderDefaultTracking(context.Background(), repo, "main", "-bad"); err == nil {
		t.Fatal("option remote accepted")
	}
	bad := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"for-each-ref", "--format=%(upstream:remotename)%00%(upstream:remoteref)", "refs/heads/feature"}, "origin\x00refs/tags/v1", 0),
	}}))
	if _, err := bad.BranchUpstream(context.Background(), repo, "feature"); err == nil {
		t.Fatal("ambiguous upstream accepted")
	}
	blank := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"remote", "get-url", "origin"}, "", 0),
	}}))
	if _, err := blank.remoteURL(context.Background(), repo, "origin"); err == nil {
		t.Fatal("blank remote URL accepted")
	}
}

func TestCoverageDiscoveryDiskAndCommandBoundaries(t *testing.T) {
	client := New("git")
	if repos, errs := client.Discover(context.Background(), nil); repos != nil || len(errs) != 1 {
		t.Fatalf("empty discover repos=%v errs=%v", repos, errs)
	}
	if _, errs := client.Discover(context.Background(), []string{"", filepath.Join(t.TempDir(), "missing")}); len(errs) != 2 {
		t.Fatalf("discovery errors=%v", errs)
	}
	file := filepath.Join(t.TempDir(), "file")
	writeFile(t, file, "x")
	if _, errs := client.Discover(context.Background(), []string{file}); len(errs) != 1 {
		t.Fatalf("file discover errors=%v", errs)
	}
	if bytes, err := DiskUsage(filepath.Dir(file)); err != nil || bytes < 1 {
		t.Fatalf("usage=%d err=%v", bytes, err)
	}
	if _, err := client.run(context.Background(), "", "status"); err == nil {
		t.Fatal("empty command dir accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.run(ctx, t.TempDir(), "status"); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("cancel=%v", err)
	}
}

func TestCoverageConfigureCommandStopsAChildGroup(t *testing.T) {
	// Fixture creation copies the scripted binary on Windows and can take long
	// enough to consume a one-second cancellation budget. Keep that setup out
	// of the manual-cancellation assertion below.
	binary := scriptedGit(t, fixture{Default: response{Hang: true}})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary)
	configureCommand(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Cancel(); err != nil && !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("cancel=%v", err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("manual cancellation fell back to deadline: %v", err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed process succeeded")
	}
	if err := cmd.Cancel(); err == nil {
		t.Fatal("second cancel succeeded")
	}
	if cmd.WaitDelay != commandWaitDelay {
		t.Fatalf("wait delay=%s", cmd.WaitDelay)
	}
}

func TestCoverageGitCommandSafetyBranches(t *testing.T) {
	repo := model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"}
	oid := strings.Repeat("a", 40)
	client := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"remote"}, "origin\n", 0),
		reply([]string{"show-ref", "--verify", "--quiet", "refs/remotes/origin/main"}, "", 0),
		reply([]string{"rev-parse", "--verify", "refs/remotes/origin/main^{commit}"}, oid, 0),
		reply([]string{"remote", "get-url", "origin"}, "https://example.test/origin", 0),
		reply([]string{"worktree", "list", "--porcelain", "-z"}, "", 0),
		reply([]string{"status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignore-submodules=none"}, "", 0),
		reply([]string{"branch", "-r", "--contains", "commit", "--format=%(refname)"}, "refs/remotes/origin/main\n", 0),
		reply([]string{"worktree", "remove", "--", "/worktree"}, "", 0),
		reply([]string{"worktree", "prune", "--expire=now"}, "", 0),
		reply([]string{"rev-parse", "--git-common-dir"}, ".git", 0),
		reply([]string{"rev-parse", "--verify", "refs/heads/feature^{commit}"}, oid, 0),
		reply([]string{"rev-parse", "--verify", "refs/heads/main^{commit}"}, oid, 0),
		reply([]string{"merge-base", "--is-ancestor", oid, "refs/heads/main"}, "", 0),
		reply([]string{"update-ref", "-d", "refs/heads/feature", oid}, "", 0),
	}, Default: response{Exit: 2}}))
	if _, _, _, err := client.ProviderDefaultTracking(context.Background(), repo, "main", ""); err != nil {
		t.Fatalf("default provider tracking: %v", err)
	}
	if worktrees, err := client.List(context.Background(), repo); err != nil || worktrees != nil {
		t.Fatalf("empty list=%+v err=%v", worktrees, err)
	}
	if clean, err := client.IsClean(context.Background(), repo.PrimaryPath); err != nil || !clean {
		t.Fatalf("clean=%v err=%v", clean, err)
	}
	if contains, err := client.RemoteContains(context.Background(), repo, "commit"); err != nil || !contains {
		t.Fatalf("contains=%v err=%v", contains, err)
	}
	if err := client.Remove(context.Background(), repo, "/worktree"); err != nil {
		t.Fatal(err)
	}
	if err := client.Prune(context.Background(), repo); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteBranch(context.Background(), repo, "feature", "main"); err != nil {
		t.Fatal(err)
	}
	if common, err := client.commonGitDir(context.Background(), repo.PrimaryPath); err != nil || common != filepath.Join(repo.PrimaryPath, ".git") {
		t.Fatalf("common=%q err=%v", common, err)
	}

	notFound := New(scriptedGit(t, all(1)))
	if found, err := notFound.hasTrackingRef(context.Background(), repo, "origin", "main"); err != nil || found {
		t.Fatalf("not found=%v err=%v", found, err)
	}
	if ancestor, err := notFound.IsAncestor(context.Background(), repo, "a", "b"); err != nil || ancestor {
		t.Fatalf("ancestor=%v err=%v", ancestor, err)
	}
	if clean, err := notFound.IsClean(context.Background(), repo.PrimaryPath); err == nil || clean {
		t.Fatalf("clean error=%v clean=%v", err, clean)
	}
	if _, err := notFound.configuredRemotes(context.Background(), repo); err == nil {
		t.Fatal("remote command failure accepted")
	}
	if _, err := notFound.commonGitDir(context.Background(), repo.PrimaryPath); err == nil {
		t.Fatal("common dir failure accepted")
	}
	if _, err := notFound.List(context.Background(), repo); err == nil {
		t.Fatal("list command failure accepted")
	}
	if _, err := notFound.RemoteContains(context.Background(), repo, "commit"); err == nil {
		t.Fatal("remote contains command failure accepted")
	}
	if err := notFound.Remove(context.Background(), repo, "/worktree"); err == nil {
		t.Fatal("remove command failure accepted")
	}
	if err := notFound.Prune(context.Background(), repo); err == nil {
		t.Fatal("prune command failure accepted")
	}
}

func TestCoverageGitParseAndDiscoveryErrors(t *testing.T) {
	if records, err := ParseWorktreeListPorcelainZ(nil); err != nil || records != nil {
		t.Fatalf("empty parse=%+v %v", records, err)
	}
	record := model.RegisteredWorktree{Path: "/repo"}
	for _, field := range []struct {
		key, value string
		hasValue   bool
	}{{"HEAD", "", false}, {"branch", "", false}, {"unknown", "x", true}} {
		if err := applyWorktreeField(&record, field.key, field.value, field.hasValue); err == nil {
			t.Fatalf("accepted field %+v", field)
		}
	}
	root := t.TempDir()
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	seen := map[string]string{}
	var errs []error
	New(scriptedGit(t, all(2))).discoverRoot(context.Background(), root, seen, &errs)
	if len(errs) == 0 {
		t.Fatal("git discovery failure was hidden")
	}
}

func TestDiscoverReportsUnresolvableCurrentDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("removing the active directory has different semantics on Windows")
	}
	original, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(original); err != nil {
			t.Errorf("restore working directory: %v", err)
		}
	})
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	repos, errs := New("git").Discover(context.Background(), []string{"."})
	if len(repos) != 0 || len(errs) != 1 || (!strings.Contains(errs[0].Error(), "resolve root") && !strings.Contains(errs[0].Error(), "stat root")) {
		t.Fatalf("repos=%+v errs=%v", repos, errs)
	}
}

func TestDiscoverReportsUnreadableRootWalkFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX directory permissions are required")
	}
	root := filepath.Join(t.TempDir(), "unreadable")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(root, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(root, 0o700) })
	if _, err := os.ReadDir(root); err == nil {
		t.Skip("filesystem permissions do not enforce an unreadable directory")
	}
	seen := map[string]string{}
	var errs []error
	New("git").discoverRoot(context.Background(), root, seen, &errs)
	if len(seen) != 0 || len(errs) == 0 || !strings.Contains(errs[0].Error(), "walk ") {
		t.Fatalf("seen=%v errs=%v", seen, errs)
	}
}

func TestCoverageGitProofFailurePaths(t *testing.T) {
	repo := model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"}
	for _, branch := range []string{"", " refs"} {
		if _, err := New("git").BranchUpstream(context.Background(), repo, branch); err == nil {
			t.Fatalf("invalid upstream branch %q accepted", branch)
		}
	}
	if _, _, _, err := New("git").ProviderDefaultTracking(context.Background(), repo, " main", "origin"); err == nil {
		t.Fatal("invalid provider branch accepted")
	}

	noTracking := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"remote"}, "origin\n", 0),
	}, Default: response{Exit: 1}}))
	if _, err := noTracking.DefaultTrackingRef(context.Background(), repo, "main"); err == nil || !strings.Contains(err.Error(), "0 candidate") {
		t.Fatalf("no candidate=%v", err)
	}
	if _, _, _, err := noTracking.ProviderDefaultTracking(context.Background(), repo, "main", "origin"); err == nil || !strings.Contains(err.Error(), "no default tracking") {
		t.Fatalf("missing selected tracking=%v", err)
	}

	oid := strings.Repeat("a", 40)
	multiple := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"remote"}, "origin\nupstream\n", 0),
		reply([]string{"show-ref", "--verify", "--quiet", "refs/remotes/origin/main"}, "", 0),
		reply([]string{"show-ref", "--verify", "--quiet", "refs/remotes/upstream/main"}, "", 0),
		reply([]string{"rev-parse", "--verify", "refs/remotes/origin/main^{commit}"}, oid, 0),
		reply([]string{"rev-parse", "--verify", "refs/remotes/upstream/main^{commit}"}, oid, 0),
	}, Default: response{Exit: 2}}))
	if _, err := multiple.DefaultTrackingRef(context.Background(), repo, "main"); err == nil || !strings.Contains(err.Error(), "2 candidate") {
		t.Fatalf("multiple candidates=%v", err)
	}

	badOID := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"show-ref", "--verify", "--quiet", "refs/remotes/origin/main"}, "", 0),
	}, Default: response{Stdout: "short"}}))
	if found, err := badOID.hasTrackingRef(context.Background(), repo, "origin", "main"); err != nil || found {
		t.Fatalf("bad oid found=%v err=%v", found, err)
	}
}

func TestCoverageGitAdapterCommandErrors(t *testing.T) {
	repo := model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"}
	failing := New(scriptedGit(t, all(2)))
	if _, err := failing.BranchUpstream(context.Background(), repo, "feature"); err == nil {
		t.Fatal("upstream command failure accepted")
	}
	if _, err := failing.DefaultTrackingRef(context.Background(), repo, "main"); err == nil {
		t.Fatal("tracking command failure accepted")
	}
	if _, err := failing.hasTrackingRef(context.Background(), repo, "origin", "main"); err == nil {
		t.Fatal("unexpected show-ref failure accepted")
	}
	if _, err := failing.remoteURL(context.Background(), repo, "origin"); err == nil {
		t.Fatal("remote URL command failure accepted")
	}
	if _, err := failing.DefaultBranch(context.Background(), repo); err == nil {
		t.Fatal("default branch command failure accepted")
	}
	if _, err := failing.IsAncestor(context.Background(), repo, "a", "b"); err == nil {
		t.Fatal("ancestry command failure accepted")
	}
	if err := failing.DeleteBranch(context.Background(), repo, "feature", "main"); err == nil {
		t.Fatal("branch delete proof failure accepted")
	}

	emptyCommon := New(scriptedGit(t, all(0)))
	if _, err := emptyCommon.commonGitDir(context.Background(), repo.PrimaryPath); err == nil {
		t.Fatal("empty common git directory accepted")
	}
}

func TestCoverageGitRemainingInputAndProofCases(t *testing.T) {
	repo := model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"}
	if _, err := New("git").BranchUpstream(context.Background(), model.Repository{}, "feature"); err == nil {
		t.Fatal("invalid upstream repo accepted")
	}
	if _, err := New("git").DefaultTrackingRef(context.Background(), model.Repository{}, "main"); err == nil {
		t.Fatal("invalid tracking repo accepted")
	}
	if _, err := New("git").DefaultTrackingRef(context.Background(), repo, " refs/main"); err == nil {
		t.Fatal("invalid default branch accepted")
	}
	if _, err := New("git").IsAncestor(context.Background(), model.Repository{}, "a", "b"); err == nil {
		t.Fatal("invalid ancestry repo accepted")
	}
	if _, err := New("git").RemoteContains(context.Background(), model.Repository{}, "a"); err == nil {
		t.Fatal("invalid remote repo accepted")
	}

	noURL := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"for-each-ref", "--format=%(upstream:remotename)%00%(upstream:remoteref)", "refs/heads/feature"}, "origin\x00refs/heads/main", 0),
	}, Default: response{Exit: 2}}))
	if _, err := noURL.BranchUpstream(context.Background(), repo, "feature"); err == nil {
		t.Fatal("upstream without remote URL accepted")
	}
	badParse := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"worktree", "list", "--porcelain", "-z"}, "worktree", 0),
	}}))
	if _, err := badParse.List(context.Background(), repo); err == nil {
		t.Fatal("malformed worktree list accepted")
	}
	noRemotes := New(scriptedGit(t, all(0)))
	if _, err := noRemotes.DefaultBranch(context.Background(), repo); err == nil || !strings.Contains(err.Error(), "no remotes") {
		t.Fatalf("no remotes=%v", err)
	}

	missingRev := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"show-ref", "--verify", "--quiet", "refs/remotes/origin/main"}, "", 0),
	}, Default: response{Exit: 2}}))
	if _, err := missingRev.hasTrackingRef(context.Background(), repo, "origin", "main"); err == nil {
		t.Fatal("missing tracking oid accepted")
	}
	shortOID := New(scriptedGit(t, all(0)))
	if err := shortOID.DeleteBranch(context.Background(), repo, "feature", "main"); err == nil || !strings.Contains(err.Error(), "unexpected object id") {
		t.Fatalf("short oid=%v", err)
	}
	missingDefault := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"rev-parse", "--verify", "refs/heads/feature^{commit}"}, strings.Repeat("a", 40), 0),
	}, Default: response{Exit: 2}}))
	if err := missingDefault.DeleteBranch(context.Background(), repo, "feature", "main"); err == nil || !strings.Contains(err.Error(), "resolve default") {
		t.Fatalf("missing default=%v", err)
	}
}

func TestCoverageWalkCallbacksWithoutFilesystemRaces(t *testing.T) {
	workRoot := t.TempDir()
	commonDir := filepath.Join(workRoot, "common")
	client := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"rev-parse", "--git-common-dir"}, commonDir, 0),
	}}))
	seen := map[string]string{}
	var errs []error
	if got := client.walkRepositoryEntry(context.Background(), "/root/missing", nil, errors.New("walk"), seen, &errs); got != nil || len(errs) != 1 {
		t.Fatalf("file walk=%v errs=%v", got, errs)
	}
	if got := client.walkRepositoryEntry(context.Background(), "/root/dir", gitxEntry{name: "dir", dir: true}, errors.New("walk"), seen, &errs); got != filepath.SkipDir {
		t.Fatalf("dir walk=%v", got)
	}
	if got := client.walkRepositoryEntry(context.Background(), "/root/plain", gitxEntry{name: "plain"}, nil, seen, &errs); got != nil {
		t.Fatalf("plain=%v", got)
	}
	if got := client.walkRepositoryEntry(context.Background(), filepath.Join(workRoot, ".git"), gitxEntry{name: ".git", dir: true}, nil, seen, &errs); got != filepath.SkipDir || canonicalTestPath(seen[commonDir]) != canonicalTestPath(workRoot) {
		t.Fatalf("git directory=%v seen=%v", got, seen)
	}
	failing := New(scriptedGit(t, all(2)))
	if got := failing.walkRepositoryEntry(context.Background(), filepath.Join(workRoot, ".git"), gitxEntry{name: ".git"}, nil, seen, &errs); got != nil || len(errs) < 2 {
		t.Fatalf("git file=%v errs=%v", got, errs)
	}

	var total int64
	if err := diskUsageEntry("/root", &total, "/root/gone", nil, fs.ErrNotExist); err != nil {
		t.Fatalf("transient walk=%v", err)
	}
	if err := diskUsageEntry("/root", &total, "/root", nil, fs.ErrNotExist); err == nil {
		t.Fatal("missing root accepted")
	}
	if err := diskUsageEntry("/root", &total, "/root/gone", gitxEntry{name: "gone", infoErr: fs.ErrNotExist}, nil); err != nil {
		t.Fatalf("transient stat=%v", err)
	}
	if err := diskUsageEntry("/root", &total, "/root/bad", gitxEntry{name: "bad", infoErr: errors.New("info")}, nil); err == nil {
		t.Fatal("bad stat accepted")
	}
	if err := diskUsageEntry("/root", &total, "/root/data", gitxEntry{name: "data", size: 7}, nil); err != nil || total != 7 {
		t.Fatalf("usage err=%v total=%d", err, total)
	}
}

func TestCoverageGitDiscoveryAndRemainingErrorContracts(t *testing.T) {
	repo := model.Repository{PrimaryPath: t.TempDir(), CommonDir: "/repo/.git"}
	oid := strings.Repeat("a", 40)
	trackingFailure := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"remote"}, "origin\n", 0),
	}, Default: response{Exit: 2}}))
	if _, err := trackingFailure.DefaultTrackingRef(context.Background(), repo, "main"); err == nil {
		t.Fatal("tracking lookup failure accepted")
	}
	remoteFailure := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"remote"}, "origin\n", 0),
		reply([]string{"show-ref", "--verify", "--quiet", "refs/remotes/origin/main"}, "", 0),
		reply([]string{"rev-parse", "--verify", "refs/remotes/origin/main^{commit}"}, oid, 0),
	}, Default: response{Exit: 2}}))
	if _, err := remoteFailure.DefaultTrackingRef(context.Background(), repo, "main"); err == nil {
		t.Fatal("default tracking URL failure accepted")
	}

	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	discoveryFailure := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"rev-parse", "--git-common-dir"}, "/common", 0),
	}, Default: response{Exit: 2}}))
	if repos, errs := discoveryFailure.Discover(context.Background(), []string{root}); len(repos) != 1 || len(errs) != 1 || !strings.Contains(errs[0].Error(), "list worktrees") {
		t.Fatalf("repos=%v errs=%v", repos, errs)
	}

	if err := New("git").Remove(context.Background(), model.Repository{PrimaryPath: repo.PrimaryPath}, "/worktree"); err == nil {
		t.Fatal("remove without common dir accepted")
	}
	if err := New("git").DeleteBranch(context.Background(), model.Repository{PrimaryPath: repo.PrimaryPath}, "feature", "main"); err == nil {
		t.Fatal("delete without common dir accepted")
	}
	deleteProofFailure := New(scriptedGit(t, fixture{Responses: []response{
		reply([]string{"rev-parse", "--verify", "refs/heads/feature^{commit}"}, oid, 0),
		reply([]string{"rev-parse", "--verify", "refs/heads/main^{commit}"}, oid, 0),
	}, Default: response{Exit: 2}}))
	if err := deleteProofFailure.DeleteBranch(context.Background(), repo, "feature", "main"); err == nil || !strings.Contains(err.Error(), "prove") {
		t.Fatalf("delete proof=%v", err)
	}
}

type gitxEntry struct {
	name    string
	dir     bool
	size    int64
	infoErr error
}

func (e gitxEntry) Name() string { return e.name }
func (e gitxEntry) IsDir() bool  { return e.dir }
func (e gitxEntry) Type() fs.FileMode {
	if e.dir {
		return fs.ModeDir
	}
	return 0
}
func (e gitxEntry) Info() (fs.FileInfo, error) {
	if e.infoErr != nil {
		return nil, e.infoErr
	}
	return gitxInfo{dir: e.dir, size: e.size}, nil
}

type gitxInfo struct {
	dir  bool
	size int64
}

func (i gitxInfo) Name() string { return "info" }
func (i gitxInfo) Size() int64  { return i.size }
func (i gitxInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir
	}
	return 0
}
func (gitxInfo) ModTime() time.Time { return time.Time{} }
func (i gitxInfo) IsDir() bool      { return i.dir }
func (gitxInfo) Sys() any           { return nil }

func providerTrackingGit(t *testing.T, oid string) string {
	t.Helper()
	return scriptedGit(t, fixture{Responses: []response{
		reply([]string{"for-each-ref", "--format=%(upstream:remotename)%00%(upstream:remoteref)", "refs/heads/feature"}, "origin\x00refs/heads/main", 0),
		reply([]string{"remote", "get-url", "origin"}, "https://example.test/origin", 0),
		reply([]string{"remote"}, "origin\n", 0),
		reply([]string{"show-ref", "--verify", "--quiet", "refs/remotes/origin/main"}, "", 0),
		reply([]string{"rev-parse", "--verify", "refs/remotes/origin/main^{commit}"}, oid, 0),
	}, Default: response{Exit: 1}})
}

type response struct {
	Args   []string `json:"args"`
	Stdout string   `json:"stdout"`
	Stderr string   `json:"stderr"`
	Exit   int      `json:"exit"`
	Hang   bool     `json:"hang"`
}

type fixture struct {
	Responses []response `json:"responses"`
	Default   response   `json:"default"`
}

func reply(args []string, stdout string, exit int) response {
	return response{Args: args, Stdout: stdout, Exit: exit}
}

func all(exit int) fixture { return fixture{Default: response{Exit: exit}} }

func scriptedGit(t *testing.T, data fixture) string {
	t.Helper()
	t.Setenv("GITX_HELPER", "1")
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ext := filepath.Ext(source)
	path := filepath.Join(t.TempDir(), "git-helper"+ext)
	if err := copyTestBinary(source, path); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".fixture", encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func copyTestBinary(source, destination string) error {
	input, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return os.WriteFile(destination, input, 0o700)
}

func TestMain(m *testing.M) {
	if os.Getenv("GITX_HELPER") == "1" {
		encoded, err := os.ReadFile(os.Args[0] + ".fixture")
		if err != nil {
			os.Exit(2)
		}
		var data fixture
		if json.Unmarshal(encoded, &data) != nil {
			os.Exit(2)
		}
		selected := data.Default
		for _, candidate := range data.Responses {
			if strings.Join(candidate.Args, "\x00") == strings.Join(os.Args[1:], "\x00") {
				selected = candidate
				break
			}
		}
		if selected.Hang {
			<-time.NewTimer(time.Hour).C
		}
		_, _ = os.Stdout.WriteString(selected.Stdout)
		_, _ = os.Stderr.WriteString(selected.Stderr)
		os.Exit(selected.Exit)
	}
	os.Exit(m.Run())
}
