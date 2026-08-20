package filescom

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The version each generator writes into the committed sources. The committed schema carries no
// version, so every one of these is a placeholder and generation does not depend on the build.
const (
	nodejsVersionPlaceholder = "${VERSION}"
	pythonVersionPlaceholder = "0.0.0"
)

// TestTheCommittedSDKSourcesCarryNoBuildVersion reads the four manifests. A build version in the
// committed tree makes every regeneration a diff, and the worktree check in CI then fails.
func TestTheCommittedSDKSourcesCarryNoBuildVersion(t *testing.T) {
	npm := firstSubmatch(t, "sdk/nodejs/package.json", regexp.MustCompile(`"version":\s*"([^"]+)"`))
	if npm != nodejsVersionPlaceholder {
		t.Errorf("the npm manifest carries version %q and the committed tree holds %q",
			npm, nodejsVersionPlaceholder)
	}

	pypi := firstSubmatch(t, "sdk/python/pyproject.toml",
		regexp.MustCompile(`(?m)^\s*version\s*=\s*"([^"]+)"`))
	if pypi != pythonVersionPlaceholder {
		t.Errorf("the Python manifest carries version %q and the committed tree holds %q",
			pypi, pythonVersionPlaceholder)
	}

	project := filepath.Join("sdk", "dotnet", nugetPackageID+".csproj")
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), project))
	if err != nil {
		t.Fatalf("read %s: %v", project, err)
	}
	if strings.Contains(string(raw), "<Version>") {
		t.Errorf("%s declares a Version element; the build passes the version instead", project)
	}

	for _, manifest := range pluginManifests(t) {
		var declared struct {
			Version string `json:"version"`
		}
		body, err := os.ReadFile(filepath.Join(repoRoot(t), manifest))
		if err != nil {
			t.Fatalf("read %s: %v", manifest, err)
		}
		if err := json.Unmarshal(body, &declared); err != nil {
			t.Fatalf("parse %s: %v", manifest, err)
		}
		if declared.Version != "" {
			t.Errorf("%s pins the plugin at version %q, so the SDK stops matching its provider",
				manifest, declared.Version)
		}
	}
}

// TestEachSDKBuildStampsTheVersionItPublishes reads each build recipe. The staged copy under the
// SDK's own bin/ is what a publish uploads, and it is the only place the version belongs.
func TestEachSDKBuildStampsTheVersionItPublishes(t *testing.T) {
	dotnet := recipeFor(t, "build_dotnet")
	stated := firstGroup(t, dotnet, regexp.MustCompile(`echo "([^"]+)" > version.txt`))
	if !strings.Contains(dotnet, "-p:Version="+stated) {
		t.Errorf("make build_dotnet writes version %s into version.txt and packs a different one: %s",
			stated, recipeOutput(dotnet))
	}
	if stated == "1.0.0" {
		t.Error("make build_dotnet stamps the .NET default rather than the build version")
	}

	nodejs := recipeFor(t, "build_nodejs")
	if !strings.Contains(nodejs, nodejsVersionPlaceholder) || !strings.Contains(nodejs, "bin/package.json") {
		t.Errorf("make build_nodejs does not replace the version placeholder in the staged manifest: %s",
			recipeOutput(nodejs))
	}

	python := recipeFor(t, "build_python")
	if !strings.Contains(python, "bin/pyproject.toml") {
		t.Errorf("make build_python does not stamp the version into the staged manifest: %s",
			recipeOutput(python))
	}
	if !strings.Contains(python, pythonVersionPlaceholder) {
		t.Errorf("make build_python does not replace the placeholder version %s: %s",
			pythonVersionPlaceholder, recipeOutput(python))
	}
}

// TestTheGoSDKTakesItsVersionFromThePublishedTag reads the release workflow. The Go module proxy
// reads a module's version from its tag, so nothing in the tree can carry it.
func TestTheGoSDKTakesItsVersionFromThePublishedTag(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	if !strings.Contains(string(body), "pulumi/publish-go-sdk-action") {
		t.Fatal("release.yml publishes no Go SDK, so nothing tags the module")
	}
	if !strings.Contains(string(body), "version: ${{ steps.version.outputs.version }}") {
		t.Error("the Go SDK publish step does not take the version the release tag carries")
	}

	if strings.Contains(recipeFor(t, "build_go"), "-p:Version=") {
		t.Error("make build_go stamps a version the module proxy would ignore")
	}
}

// TestTheDotnetExampleResolvesTheLocallyBuiltSDK reads the version range the example asks for
// against the version the local build produces. A prerelease needs a range that admits one.
func TestTheDotnetExampleResolvesTheLocallyBuiltSDK(t *testing.T) {
	makefile, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	if err != nil {
		t.Fatalf("read the Makefile: %v", err)
	}
	built := firstGroup(t, string(makefile), regexp.MustCompile(`(?m)^PROVIDER_VERSION \?= (\S+)`))
	if !strings.Contains(built, "-") {
		t.Skipf("the default build version %s is not a prerelease, so any range resolves it", built)
	}

	project := filepath.Join("examples", "basic-cs", "basic-cs.csproj")
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), project))
	if err != nil {
		t.Fatalf("read %s: %v", project, err)
	}
	pattern := regexp.MustCompile(`Include="` + nugetPackageID + `" Version="([^"]+)"`)
	requested := firstGroup(t, string(raw), pattern)
	if !strings.Contains(requested, "-") {
		t.Errorf("%s asks for %s version %q, which excludes the prerelease %s the local build produces",
			project, nugetPackageID, requested, built)
	}
}

// pluginManifests lists every pulumi-plugin.json the committed SDK sources hold. The build
// directories carry copies, and only the committed ones are regenerated.
func pluginManifests(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	var found []string
	for _, dir := range sdkSourceDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if sdkBuildDirs[entry.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			if entry.Name() == "pulumi-plugin.json" {
				relative, relErr := filepath.Rel(root, path)
				if relErr != nil {
					return relErr
				}
				found = append(found, relative)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(found) == 0 {
		t.Fatal("the committed SDKs hold no plugin manifest")
	}
	return found
}

// recipeFor returns what make would run for the target without running it.
func recipeFor(t *testing.T, target string) string {
	t.Helper()
	out, err := runMake(t, "-n", target)
	if err != nil {
		t.Fatalf("make -n %s: %v\n%s", target, err, out)
	}
	return out
}

func firstGroup(t *testing.T, body string, pattern *regexp.Regexp) string {
	t.Helper()
	found := pattern.FindStringSubmatch(body)
	if found == nil {
		t.Fatalf("nothing matches %s", pattern)
	}
	return found[1]
}
