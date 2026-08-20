package filescom

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The two workflows that carry the build graph. A rule that held in one and not the other would
// leave the branch a release is cut from unchecked.
func buildWorkflows() []string { return []string{"pull-request.yml", "main.yml"} }

//nolint:gosec // G101: this names the secret a workflow reads, and holds no key.
const credentialSecret = "FILES_API_KEY"

func readWorkflow(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

// exampleTestRuns answers the go test invocations over the examples package in one job body.
func exampleTestRuns(job string) []string {
	var found []string
	for _, line := range strings.Split(job, "\n") {
		if strings.Contains(line, "go test") && strings.Contains(line, "examples") {
			found = append(found, strings.TrimSpace(line))
		}
	}
	return found
}

// The example tests that read no credential caught a red suite once and nothing in CI ran them,
// so a fork pull request never saw them. They need no key, so one job runs them everywhere.
func TestOneKeylessJobRunsTheExampleTestsThatNeedNoCredential(t *testing.T) {
	for _, workflow := range buildWorkflows() {
		t.Run(workflow, func(t *testing.T) {
			jobs := jobBlocks(readWorkflow(t, workflow))

			var runners []string
			for name, block := range jobs {
				for _, run := range exampleTestRuns(block) {
					if !strings.Contains(run, "-tags") {
						runners = append(runners, name)
					}
				}
			}
			if len(runners) != 1 {
				t.Fatalf("one job should run the untagged example tests, and these do: %v", runners)
			}

			block := jobs[runners[0]]
			if strings.Contains(block, credentialSecret) {
				t.Errorf("job %s runs the untagged example tests and reads %s; they need none",
					runners[0], credentialSecret)
			}
			if strings.Contains(block, "pull_request.head.repo") {
				t.Errorf("job %s carries a fork gate, which skips these tests on the pull requests"+
					" that need them most", runners[0])
			}
		})
	}
}
