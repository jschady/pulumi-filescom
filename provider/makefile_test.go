package filescom

import (
	"os/exec"
	"testing"
)

// The checks live in a script so they can also run before this module compiles.
func TestMakeTargetVocabulary(t *testing.T) {
	cmd := exec.Command("./scripts/check-make-targets.sh")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("check-make-targets.sh: %v\n%s", err, out)
	}
}
