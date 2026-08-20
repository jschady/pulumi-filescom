package filescom

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The rule scripts this package runs. Each carries checks that exist nowhere else, so a test
// that stops invoking one takes those checks out of `make test_provider` without failing it.
var ruleScripts = []string{
	"./.github/workflows/check-workflows.sh",
	"./scripts/check-make-targets.sh",
	"./scripts/check-internal-references.sh",
}

// TestEveryRuleScriptIsRunByATest reads the test sources for the invocation, so deleting or
// renaming a caller fails here rather than leaving the script unrun until a pull request.
func TestEveryRuleScriptIsRunByATest(t *testing.T) {
	root := repoRoot(t)
	sources, err := filepath.Glob(filepath.Join(root, "provider", "*_test.go"))
	if err != nil {
		t.Fatalf("glob the provider tests: %v", err)
	}
	if len(sources) == 0 {
		t.Fatal("the provider package holds no test sources, so this read proves nothing")
	}

	for _, script := range ruleScripts {
		if _, err := os.Stat(filepath.Join(root, strings.TrimPrefix(script, "./"))); err != nil {
			t.Errorf("%s does not exist: %v", script, err)
			continue
		}
		called := false
		for _, source := range sources {
			body, err := os.ReadFile(source)
			if err != nil {
				t.Fatalf("read %s: %v", source, err)
			}
			if strings.Contains(string(body), `exec.Command("`+script+`")`) {
				called = true
				break
			}
		}
		if !called {
			t.Errorf("no test under provider/ runs %s, so its checks run nowhere in this package",
				script)
		}
	}
}
