package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestQueueMeWorkflowContract(t *testing.T) {
	workflowText := readQueueMeFile(t, ".github/workflows/queue-me.yml")

	required := []string{
		"pull_request_target:",
		"workflow_dispatch:",
		"push:",
		"workflow_run:",
		"workflow_run:",
		"- ci",
		"- pr metadata",
		"pull_request_review:",
		"- submitted",
		"- dismissed",
		"- main",
		"- labeled",
		"- unlabeled",
		"- synchronize",
		"- converted_to_draft",
		"- closed",
		"- edited",
		"- auto_merge_enabled",
		"cancel-in-progress: false",
		"runs-on: ubuntu-latest",
		"github.event_name == 'workflow_dispatch' &&",
		"github.ref == 'refs/heads/main'",
		"permissions:\n  contents: read",
		"actions/create-github-app-token@bcd2ba49218906704ab6c1aa796996da409d3eb1 # v3",
		"app-id: ${{ vars.QUEUE_APP_ID }}",
		"private-key: ${{ secrets.QUEUE_APP_PRIVATE_KEY }}",
		"permission-contents: write",
		"permission-issues: write",
		"permission-pull-requests: write",
		"permission-workflows: write",
		"actions/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3 # v7",
		"QUEUE_CONTROLLER_PATH: ${{ runner.temp }}/queue_me_controller.js",
		"TRUSTED_CONTROLLER_REF: ${{ github.workflow_sha }}",
		"github.rest.repos.getContent",
		"path: 'scripts/queue_me_controller.js'",
		"ref: process.env.TRUSTED_CONTROLLER_REF",
		"flag: 'wx'",
		"QUEUE_LABEL: queue-me",
		"QUEUE_APP_SLUG: ${{ steps.queue_token.outputs.app-slug }}",
		"require(process.env.QUEUE_CONTROLLER_PATH)",
	}
	for _, fragment := range required {
		if !strings.Contains(workflowText, fragment) {
			t.Fatalf("queue-me workflow missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"actions/checkout@",
		"actions/github-script@v",
		"github.event.pull_request.head",
		"pull_request:\n",
	} {
		if strings.Contains(workflowText, forbidden) {
			t.Fatalf("queue-me workflow contains unsafe fragment %q", forbidden)
		}
	}
}

func TestQueueMeControllerContract(t *testing.T) {
	controller := readQueueMeFile(t, "scripts/queue_me_controller.js")
	for _, fragment := range []string{
		"compareCommitsWithBasehead",
		"updatePullRequestBranch",
		"expectedHeadOid",
		"updateMethod: REBASE",
		"enablePullRequestAutoMerge",
		"disablePullRequestAutoMerge",
		"mergePullRequest",
		"mergeMethod: SQUASH",
		"left.number - right.number",
		"COMMENT_MARKER",
	} {
		if !strings.Contains(controller, fragment) {
			t.Fatalf("queue-me controller missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"requestReviews",
		"force-push",
		"process.env.QUEUE_APP_PRIVATE_KEY",
	} {
		if strings.Contains(controller, forbidden) {
			t.Fatalf("queue-me controller contains forbidden fragment %q", forbidden)
		}
	}
}

func TestQueueMeControllerNodeSuite(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("node is required to test the queue-me controller")
	}
	command := exec.Command(node, "--test", "queue_me_controller.test.js")
	command.Dir = queueMeRepoPath(t, "scripts")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("queue-me node tests failed: %v\n%s", err, output)
	}
}

func readQueueMeFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(queueMeRepoPath(t, path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return strings.ReplaceAll(string(content), "\r\n", "\n")
}

func queueMeRepoPath(t *testing.T, path string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get current directory: %v", err)
	}
	return filepath.Join(dir, "..", path)
}
