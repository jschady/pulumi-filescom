package filescom

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Every tool version this repository pins lives in more than one file, and nothing regenerates
// the copies. The tests below read each copy and fail when two of them disagree.

// pinsToolVersion answers the version `.tool-versions` pins for one tool.
func pinsToolVersion(t *testing.T, name string) string {
	t.Helper()
	body := mustReadFile(t, filepath.Join(repoRoot(t), ".tool-versions"))
	for _, line := range strings.Split(string(body), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == name {
			return fields[1]
		}
	}
	t.Fatalf(".tool-versions pins no %s version", name)
	return ""
}

// pinsWorkflowEnv answers the value each workflow gives one key of its top-level `env:` block,
// keyed by the path of the workflow. A workflow that names the key nowhere is absent from the
// answer. If no workflow names the key, a rename left the key behind, so the helper stops.
func pinsWorkflowEnv(t *testing.T, key string) map[string]string {
	t.Helper()
	root := repoRoot(t)
	values := map[string]string{}
	for _, path := range workflowFiles(t) {
		rel, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relative path of %s: %v", path, err)
		}
		//nolint:gosec // G304: the path comes from a glob of this repository's own workflows.
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		inEnv := false
		for _, line := range strings.Split(string(body), "\n") {
			if strings.TrimRight(line, " \t") == "env:" {
				inEnv = true
				continue
			}
			if !inEnv {
				continue
			}
			// The block ends at the next line that starts in the first column.
			if line != "" && !strings.HasPrefix(line, " ") {
				break
			}
			if name, value, cut := strings.Cut(strings.TrimSpace(line), ":"); cut && name == key {
				values[rel] = strings.Trim(strings.TrimSpace(value), `"'`)
			}
		}
	}
	if len(values) == 0 {
		t.Fatalf("no workflow under .github/workflows names %s", key)
	}
	return values
}

// pinsGoModRequire answers the version one go.mod requires of one module. The answer keeps the
// `v` prefix the file writes, and an `// indirect` marker changes nothing. A file writes a
// require inside a `require (` block, or alone on a `require` line, so the scan reads both.
func pinsGoModRequire(t *testing.T, path, module string) string {
	t.Helper()
	body := mustReadFile(t, filepath.Join(repoRoot(t), path))
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == "require" {
			fields = fields[1:]
		}
		if len(fields) >= 2 && fields[0] == module {
			return fields[1]
		}
	}
	t.Fatalf("%s requires no %s", path, module)
	return ""
}

// pinsGoDirective answers the `go` directive of one go.mod. The directive starts in the first
// column and every require line is indented, so the scan reads the directive alone.
func pinsGoDirective(t *testing.T, path string) string {
	t.Helper()
	body := mustReadFile(t, filepath.Join(repoRoot(t), path))
	for _, line := range strings.Split(string(body), "\n") {
		if version, found := strings.CutPrefix(line, "go "); found {
			return strings.TrimSpace(version)
		}
	}
	t.Fatalf("%s carries no go directive", path)
	return ""
}

// pinsGoModules answers every `go.mod` this repository tracks, as a repo-relative path. The tree
// is the authority, so a module a later commit adds joins the tests below on its own. `git
// ls-files` reads the index: it lists the upstream submodule as one gitlink, not as a file tree,
// and it lists no node_modules directory, because no commit tracks one.
func pinsGoModules(t *testing.T) []string {
	t.Helper()
	var paths []string
	for _, line := range strings.Split(mustGit(t, repoRoot(t), "ls-files", "--", "go.mod", "*/go.mod"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		paths = append(paths, line)
	}
	if len(paths) == 0 {
		t.Fatal("the repository holds no go.mod files")
	}
	return paths
}

// TestOnePulumiVersionReachesEveryPin reads every copy of the Pulumi version. A copy the rest
// left behind builds the provider against one SDK and runs the workflows against another. The
// test reads the modules `pinsGoModules` finds, so a new module joins the comparison on its own.
func TestOnePulumiVersionReachesEveryPin(t *testing.T) {
	const pulumi = "github.com/pulumi/pulumi/"

	pins := [][2]string{{".tool-versions", pinsToolVersion(t, "pulumi")}}

	workflows := pinsWorkflowEnv(t, "PULUMI_VERSION")
	for _, path := range slices.Sorted(maps.Keys(workflows)) {
		pins = append(pins, [2]string{path, workflows[path]})
	}

	// A module that requires no Pulumi module carries no copy to compare. provider/shim/go.mod
	// is such a module: it builds against the Terraform plugin framework alone. The version must
	// start with `v`, so a `replace` line, which writes `=>` in that field, stays out.
	root := repoRoot(t)
	fromModules := 0
	for _, path := range pinsGoModules(t) {
		body := mustReadFile(t, filepath.Join(root, path))
		for _, line := range strings.Split(string(body), "\n") {
			fields := strings.Fields(line)
			if len(fields) > 0 && fields[0] == "require" {
				fields = fields[1:]
			}
			if len(fields) < 2 || !strings.HasPrefix(fields[0], pulumi) || !strings.HasPrefix(fields[1], "v") {
				continue
			}
			pins = append(pins, [2]string{path + " " + fields[0], strings.TrimPrefix(fields[1], "v")})
			fromModules++
		}
	}
	if fromModules == 0 {
		t.Fatalf("no go.mod under the repository requires a %s module", pulumi)
	}

	var read, disagree []string
	for index, pin := range pins {
		read = append(read, pin[0])
		if index > 0 && pin[1] != pins[0][1] {
			disagree = append(disagree, pin[0]+" pins "+pin[1])
		}
	}
	if len(disagree) > 0 {
		t.Errorf("%s pins the Pulumi version %s, and %s. The copies must agree. The test read %s.",
			pins[0][0], pins[0][1], strings.Join(disagree, "; "), strings.Join(read, ", "))
	}
}

// TestOnePulumictlVersionReachesEveryPin reads every copy of the pulumictl version. The
// workflows write the tag, and `.tool-versions` writes the same version with no `v`.
func TestOnePulumictlVersionReachesEveryPin(t *testing.T) {
	pins := [][2]string{{".tool-versions", pinsToolVersion(t, "pulumictl")}}

	workflows := pinsWorkflowEnv(t, "PULUMICTL_VERSION")
	for _, path := range slices.Sorted(maps.Keys(workflows)) {
		pins = append(pins, [2]string{path, strings.TrimPrefix(workflows[path], "v")})
	}

	var read, disagree []string
	for index, pin := range pins {
		read = append(read, pin[0])
		if index > 0 && pin[1] != pins[0][1] {
			disagree = append(disagree, pin[0]+" pins "+pin[1])
		}
	}
	if len(disagree) > 0 {
		t.Errorf("%s pins the pulumictl version %s, and %s. The copies must agree. The test read %s.",
			pins[0][0], pins[0][1], strings.Join(disagree, "; "), strings.Join(read, ", "))
	}
}

// TestOneGoDirectiveReachesEveryModule reads the `go` directive of every module this repository
// tracks. A module left behind compiles under rules the others dropped.
func TestOneGoDirectiveReachesEveryModule(t *testing.T) {
	var pins [][2]string
	for _, path := range pinsGoModules(t) {
		pins = append(pins, [2]string{path, pinsGoDirective(t, path)})
	}

	var read, disagree []string
	for index, pin := range pins {
		read = append(read, pin[0])
		if index > 0 && pin[1] != pins[0][1] {
			disagree = append(disagree, pin[0]+" carries "+pin[1])
		}
	}
	if len(disagree) > 0 {
		t.Errorf("%s carries the go directive %s, and %s. The copies must agree. The test read %s.",
			pins[0][0], pins[0][1], strings.Join(disagree, "; "), strings.Join(read, ", "))
	}
}

// TestTheToolchainGoVersionMatchesTheGoDirective compares the Go toolchain `.tool-versions`
// installs with the directive the provider module asks for. TestOneGoDirectiveReachesEveryModule
// covers the other modules. A patch release apart is fine. A minor release apart builds this
// repository under a language version the modules do not ask for.
func TestTheToolchainGoVersionMatchesTheGoDirective(t *testing.T) {
	majorMinor := func(source, version string) string {
		fields := strings.SplitN(version, ".", 3)
		if len(fields) < 2 {
			t.Fatalf("%s names the version %s, which carries no major.minor", source, version)
		}
		return fields[0] + "." + fields[1]
	}

	const toolchain = ".tool-versions"
	const module = "provider/go.mod"

	installed := pinsToolVersion(t, "golang")
	directive := pinsGoDirective(t, module)
	if majorMinor(toolchain, installed) != majorMinor(module, directive) {
		t.Errorf("%s installs Go %s, and %s asks for %s; the major.minor must agree",
			toolchain, installed, module, directive)
	}
}
