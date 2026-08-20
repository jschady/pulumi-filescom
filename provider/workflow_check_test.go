package filescom

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The probe a seeded test drops into the workflow directory. No allowlist names it, so a check
// that reads the file refuses it and a check that never reads the file stays green.
const (
	probeWorkflow = "probe.yaml"
	probeAction   = "example/probe@v1.0.0"
	probeBody     = `name: probe
on: workflow_dispatch
jobs:
  probe:
    runs-on: ubuntu-latest
    steps:
      - name: Run the probe action
        uses: ` + probeAction + `
`
)

// workflowCheckSandbox copies what the workflow check reads into a directory a test can doctor,
// so a seeded violation never reaches the working tree.
func workflowCheckSandbox(t *testing.T) string {
	t.Helper()
	sandbox := t.TempDir()
	source := filepath.Join(repoRoot(t), ".github", "workflows")
	target := filepath.Join(sandbox, ".github", "workflows")
	if err := os.MkdirAll(target, 0o750); err != nil {
		t.Fatalf("make the sandbox workflow directory: %v", err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatalf("read %s: %v", source, err)
	}
	copied := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		writeSandboxFile(t, filepath.Join(target, entry.Name()), string(body))
		copied++
	}
	if copied == 0 {
		t.Fatal("the workflow directory holds no file, so the sandbox proves nothing")
	}
	return sandbox
}

// sandboxWorkflow answers the path of one workflow file inside the sandbox.
func sandboxWorkflow(sandbox, name string) string {
	return filepath.Join(sandbox, ".github", "workflows", name)
}

// The two helpers below join a TempDir this test made with a name read from this repository's own
// workflow directory, so the taint the analyzer follows never leaves the test.
func writeSandboxFile(t *testing.T, path, body string) {
	t.Helper()
	//nolint:gosec // G703
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readSandboxFile(t *testing.T, path string) string {
	t.Helper()
	//nolint:gosec // G304
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// runWorkflowCheckIn runs the copied check against the sandbox. bash runs it by path, so the copy
// needs no execute bit and every file the sandbox holds stays at 0600.
func runWorkflowCheckIn(t *testing.T, sandbox string) (string, error) {
	t.Helper()
	//nolint:gosec // G204: the path is this repository's own check script, copied into a
	// directory this test just made under TempDir.
	cmd := exec.Command("bash", sandboxWorkflow(sandbox, "check-workflows.sh"))
	cmd.Dir = sandbox
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// indentOf answers the count of leading spaces, which is what tells a YAML block from what follows.
func indentOf(line string) int {
	return len(line) - len(strings.TrimLeft(line, " "))
}

// commentOutStep rewrites one step of a workflow into YAML comments. The step disappears from the
// run while every literal a text scan looks for stays on the page.
func commentOutStep(t *testing.T, path, step string) {
	t.Helper()
	lines := strings.Split(readSandboxFile(t, path), "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimSpace(line) == "- name: "+step {
			start = index
			break
		}
	}
	if start < 0 {
		t.Fatalf("%s carries no step named %q", filepath.Base(path), step)
	}
	indent := indentOf(lines[start])
	lines[start] = "#" + lines[start]
	for index := start + 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) != "" && indentOf(lines[index]) <= indent {
			break
		}
		lines[index] = "#" + lines[index]
	}
	writeSandboxFile(t, path, strings.Join(lines, "\n"))
}

// commentOutLine rewrites the one line starting with prefix into a YAML comment.
func commentOutLine(t *testing.T, path, prefix string) {
	t.Helper()
	lines := strings.Split(readSandboxFile(t, path), "\n")
	found := false
	for index, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[index] = "#" + line
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("%s carries no line starting with %q", filepath.Base(path), prefix)
	}
	writeSandboxFile(t, path, strings.Join(lines, "\n"))
}

// TestTheWorkflowCheckPassesInACopyOfTheRepository is the control. Without it every seeded test
// below could pass on a check that refuses something the copy itself broke.
func TestTheWorkflowCheckPassesInACopyOfTheRepository(t *testing.T) {
	out, err := runWorkflowCheckIn(t, workflowCheckSandbox(t))
	if err != nil {
		t.Fatalf("the check failed on a copy of the repository: %v\n%s", err, out)
	}
}

// GitHub runs a workflow spelled .yaml as readily as one spelled .yml. A file the check never
// enumerates carries an unreviewed action, secret or publish step into every run.
func TestTheWorkflowCheckReadsAWorkflowSpelledYaml(t *testing.T) {
	sandbox := workflowCheckSandbox(t)
	writeSandboxFile(t, sandboxWorkflow(sandbox, probeWorkflow), probeBody)

	out, err := runWorkflowCheckIn(t, sandbox)
	if err == nil {
		t.Fatalf("the check passed on a .yaml workflow carrying an unreviewed action:\n%s", out)
	}
	if !strings.Contains(out, probeAction) {
		t.Errorf("the check never read %s:\n%s", probeWorkflow, out)
	}
}

// TestTheWorkflowCheckRefusesACommentedOutLintStep covers the class a text scan cannot see: the
// literal stays on the page and the step never runs.
func TestTheWorkflowCheckRefusesACommentedOutLintStep(t *testing.T) {
	for _, step := range []string{
		"Install actionlint",
		"Check the workflow rules",
		"Check the Makefile targets",
		"Check the shipped prose",
	} {
		t.Run(step, func(t *testing.T) {
			sandbox := workflowCheckSandbox(t)
			commentOutStep(t, sandboxWorkflow(sandbox, "pull-request.yml"), step)

			out, err := runWorkflowCheckIn(t, sandbox)
			if err == nil {
				t.Fatalf("the check passed with the %q step commented out:\n%s", step, out)
			}
		})
	}
}

// A pull_request_target workflow holds this repository's token and its API key, so a checkout of
// the branch would run a contributor's code with both. That is the shape the check must refuse.
func TestTheWorkflowCheckRefusesAPullRequestTargetCheckout(t *testing.T) {
	sandbox := workflowCheckSandbox(t)
	path := sandboxWorkflow(sandbox, "pull-request-commands.yml")
	writeSandboxFile(t, path, readSandboxFile(t, path)+
		"      - name: Check out the branch\n        uses: actions/checkout@v7.0.1\n"+
		"        with:\n          ref: ${{ github.event.pull_request.head.sha }}\n")

	out, err := runWorkflowCheckIn(t, sandbox)
	if err == nil {
		t.Fatalf("the check passed on a pull_request_target workflow that checks a branch out:\n%s", out)
	}
	if !strings.Contains(out, "pull_request_target") {
		t.Errorf("the check does not name the trigger that makes the checkout unsafe:\n%s", out)
	}
}

// The scan above finds nothing when no workflow carries the trigger, and finding nothing must not
// read as a pass: the note that tells a maintainer how to run the acceptance tests lives there.
func TestTheWorkflowCheckRefusesAnEmptyPullRequestTargetScan(t *testing.T) {
	sandbox := workflowCheckSandbox(t)
	if err := os.Remove(sandboxWorkflow(sandbox, "pull-request-commands.yml")); err != nil {
		t.Fatalf("remove the note workflow: %v", err)
	}

	out, err := runWorkflowCheckIn(t, sandbox)
	if err == nil {
		t.Fatalf("the check passed with no pull_request_target workflow left to scan:\n%s", out)
	}
}

// The version and the digest travel together, so a commented pin leaves the install step reading
// an empty variable and the checksum comparing nothing.
func TestTheWorkflowCheckRefusesACommentedOutActionlintPin(t *testing.T) {
	for _, pin := range []string{"  ACTIONLINT_VERSION:", "  ACTIONLINT_SHA256:"} {
		t.Run(strings.TrimSpace(pin), func(t *testing.T) {
			sandbox := workflowCheckSandbox(t)
			commentOutLine(t, sandboxWorkflow(sandbox, "pull-request.yml"), pin)

			out, err := runWorkflowCheckIn(t, sandbox)
			if err == nil {
				t.Fatalf("the check passed with %s commented out:\n%s", strings.TrimSpace(pin), out)
			}
		})
	}
}
