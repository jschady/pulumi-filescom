package filescom

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const worktreeScript = "scripts/check-worktree-clean.sh"

// fixtureRepo builds a git repository holding one committed file.
func fixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "generated.txt"), []byte("first\n"), 0o600); err != nil {
		t.Fatalf("write the fixture file: %v", err)
	}
	mustGit(t, dir, "init", "-q", ".")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "-c", "commit.gpgsign=false", "-c", "user.name=generated",
		"-c", "user.email=generated@example.invalid", "commit", "-qm", "fixture")
	return dir
}

func runWorktreeCheck(t *testing.T, dir string) (string, error) {
	t.Helper()
	//nolint:gosec // G204: the path is this repository's own check script, joined from two
	// constants. No caller passes a name in.
	cmd := exec.Command(filepath.Join(repoRoot(t), worktreeScript))
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestTheWorktreeCheckPassesOnACleanTree is the state every generation step has to leave. The
// committed artifacts are what the generators produce, so regenerating changes nothing.
func TestTheWorktreeCheckPassesOnACleanTree(t *testing.T) {
	out, err := runWorktreeCheck(t, fixtureRepo(t))
	if err != nil {
		t.Fatalf("the check failed on a clean tree: %v\n%s", err, out)
	}
}

// TestTheWorktreeCheckFailsOnEveryShapeOfChange covers the three a generator produces: a
// rewritten file, a new one, and a deleted one.
func TestTheWorktreeCheckFailsOnEveryShapeOfChange(t *testing.T) {
	for name, change := range map[string]func(dir string) error{
		"a rewritten file": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "generated.txt"), []byte("second\n"), 0o600)
		},
		"a new file": func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("new\n"), 0o600)
		},
		"a deleted file": func(dir string) error {
			return os.Remove(filepath.Join(dir, "generated.txt"))
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := fixtureRepo(t)
			if err := change(dir); err != nil {
				t.Fatalf("apply the change: %v", err)
			}
			out, err := runWorktreeCheck(t, dir)
			if err == nil {
				t.Fatalf("the check passed after %s:\n%s", name, out)
			}
			if !strings.Contains(out, "generated.txt") && !strings.Contains(out, "extra.txt") {
				t.Errorf("the check does not name the changed path:\n%s", out)
			}
		})
	}
}

// TestTheWorkflowCheckReadsEveryWorkflow runs the script holding the workflow rules, so any rule it
// carries fails here, and reads back its file list: a workflow it never names goes unscanned.
func TestTheWorkflowCheckReadsEveryWorkflow(t *testing.T) {
	cmd := exec.Command("./.github/workflows/check-workflows.sh")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check-workflows.sh: %v\n%s", err, out)
	}

	var reported []string
	for _, line := range strings.Split(string(out), "\n") {
		if after, found := strings.CutPrefix(line, "ok: read "); found {
			reported = strings.Fields(after)
		}
	}
	if len(reported) == 0 {
		t.Fatalf("check-workflows.sh does not report which files it read:\n%s", out)
	}

	var present []string
	for _, workflow := range workflowFiles(t) {
		present = append(present, filepath.Base(workflow))
	}
	schemaAssertSameSet(t, "workflows the check reads", reported, present)
}
