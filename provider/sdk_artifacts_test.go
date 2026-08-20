package filescom

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const sdkArtifactScript = "scripts/check-sdk-artifacts.sh"

// The publishable artifact of each SDK, as the release job lays them out after downloading the
// four build artifacts. The Go SDK publishes by tag, so its artifact is the module itself.
func sdkArtifactFixture(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, body string) {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create %s: %v", filepath.Dir(rel), err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("nodejs/bin/package.json",
		`{"name": "`+npmPackageName+`", "version": "`+version+`"}`)
	write("python/bin/dist/pulumi_filescom-"+version+"-py3-none-any.whl", "wheel")
	write("python/bin/dist/pulumi_filescom-"+version+".tar.gz", "sdist")
	write("dotnet/bin/Debug/"+nugetPackageID+"."+version+".nupkg", "package")
	write("go/filescom/go.mod", "module "+goImportBasePath+"\n")
	return dir
}

func runSDKArtifactCheck(t *testing.T, dir, version string) (string, error) {
	t.Helper()
	cmd := exec.Command("./"+sdkArtifactScript, dir, version)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestTheSDKArtifactCheckPassesOnACompleteSet is the state a release needs before any binary
// reaches the release page.
func TestTheSDKArtifactCheckPassesOnACompleteSet(t *testing.T) {
	out, err := runSDKArtifactCheck(t, sdkArtifactFixture(t, "0.1.0"), "v0.1.0")
	if err != nil {
		t.Fatalf("the check failed on a complete set: %v\n%s", err, out)
	}
}

// TestTheSDKArtifactCheckAcceptsAPrereleaseSpelling covers the versions that differ per language.
// A Python wheel spells a prerelease 0.1.0a1 and the tag spells it 0.1.0-alpha.1.
func TestTheSDKArtifactCheckAcceptsAPrereleaseSpelling(t *testing.T) {
	dir := sdkArtifactFixture(t, "0.1.0-alpha.1")
	if err := os.Rename(
		filepath.Join(dir, "python", "bin", "dist", "pulumi_filescom-0.1.0-alpha.1-py3-none-any.whl"),
		filepath.Join(dir, "python", "bin", "dist", "pulumi_filescom-0.1.0a1-py3-none-any.whl"),
	); err != nil {
		t.Fatalf("rename the wheel: %v", err)
	}
	out, err := runSDKArtifactCheck(t, dir, "v0.1.0-alpha.1")
	if err != nil {
		t.Fatalf("the check failed on a prerelease set: %v\n%s", err, out)
	}
}

// A final tag must publish final artifacts. The version compare read a prefix, so a stale
// prerelease build answered for the release and the release page would carry it.
func TestTheSDKArtifactCheckRefusesAPrereleaseArtifactForAFinalTag(t *testing.T) {
	// The Go SDK is absent here: the module proxy reads its version from the tag, so the tree
	// carries none to be stale.
	for language, makeStale := range map[string]func(t *testing.T, dir string){
		nodejsLanguage: func(t *testing.T, dir string) {
			manifest := filepath.Join(dir, "nodejs", "bin", "package.json")
			body := `{"name": "` + npmPackageName + `", "version": "0.1.0-alpha.1"}`
			if err := os.WriteFile(manifest, []byte(body), 0o600); err != nil {
				t.Fatalf("rewrite the manifest: %v", err)
			}
		},
		pythonLanguage: func(t *testing.T, dir string) {
			dist := filepath.Join(dir, "python", "bin", "dist")
			renameOne(t, dist, "pulumi_filescom-0.1.0-py3-none-any.whl",
				"pulumi_filescom-0.1.0a1-py3-none-any.whl")
		},
		dotnetLanguage: func(t *testing.T, dir string) {
			debug := filepath.Join(dir, "dotnet", "bin", "Debug")
			renameOne(t, debug, nugetPackageID+".0.1.0.nupkg", nugetPackageID+".0.1.0-alpha.1.nupkg")
		},
	} {
		t.Run(language, func(t *testing.T) {
			dir := sdkArtifactFixture(t, "0.1.0")
			makeStale(t, dir)
			out, err := runSDKArtifactCheck(t, dir, "v0.1.0")
			if err == nil {
				t.Fatalf("the check passed on a %s prerelease artifact under a final tag:\n%s",
					language, out)
			}
			if !strings.Contains(out, language) {
				t.Errorf("the check does not name the stale %s SDK:\n%s", language, out)
			}
		})
	}
}

// TestTheSDKArtifactCheckFailsOnAMissingLanguage is what the gate exists for. Binaries that
// publish while one SDK is missing leave a release nobody can consume from that language.
func TestTheSDKArtifactCheckFailsOnAMissingLanguage(t *testing.T) {
	for _, language := range sortedLanguages() {
		t.Run(language, func(t *testing.T) {
			dir := sdkArtifactFixture(t, "0.1.0")
			if err := os.RemoveAll(filepath.Join(dir, language)); err != nil {
				t.Fatalf("remove the %s tree: %v", language, err)
			}
			out, err := runSDKArtifactCheck(t, dir, "v0.1.0")
			if err == nil {
				t.Fatalf("the check passed with no %s SDK:\n%s", language, out)
			}
			if !strings.Contains(out, language) {
				t.Errorf("the check does not name the missing %s SDK:\n%s", language, out)
			}
		})
	}
}

// TestTheSDKArtifactCheckFailsOnADisagreeingVersion is the defect the .NET package carried: the
// project declared no version. One language at a time, or two unread ones hide behind the first.
func TestTheSDKArtifactCheckFailsOnADisagreeingVersion(t *testing.T) {
	// The Go SDK is absent here: the module proxy reads its version from the tag, so the tree
	// carries none to disagree with.
	for language, misbuild := range map[string]func(t *testing.T, dir string){
		nodejsLanguage: func(t *testing.T, dir string) {
			manifest := filepath.Join(dir, "nodejs", "bin", "package.json")
			body := `{"name": "` + npmPackageName + `", "version": "1.0.0"}`
			if err := os.WriteFile(manifest, []byte(body), 0o600); err != nil {
				t.Fatalf("rewrite the manifest: %v", err)
			}
		},
		pythonLanguage: func(t *testing.T, dir string) {
			dist := filepath.Join(dir, "python", "bin", "dist")
			renameOne(t, dist, "pulumi_filescom-0.1.0-py3-none-any.whl",
				"pulumi_filescom-1.0.0-py3-none-any.whl")
		},
		dotnetLanguage: func(t *testing.T, dir string) {
			debug := filepath.Join(dir, "dotnet", "bin", "Debug")
			renameOne(t, debug, nugetPackageID+".0.1.0.nupkg", nugetPackageID+".1.0.0.nupkg")
		},
	} {
		t.Run(language, func(t *testing.T) {
			dir := sdkArtifactFixture(t, "0.1.0")
			misbuild(t, dir)
			out, err := runSDKArtifactCheck(t, dir, "v0.1.0")
			if err == nil {
				t.Fatalf("the check passed on a %s artifact built at 1.0.0:\n%s", language, out)
			}
			if !strings.Contains(out, "1.0.0") || !strings.Contains(out, "0.1.0") {
				t.Errorf("the check does not report both the version it read and the release:\n%s", out)
			}
		})
	}
}

// TestTheSDKArtifactCheckFailsOnAWheelWithoutASourceDistribution covers the second file the
// Python upload carries. A release that ships no source distribution cannot be built from source.
func TestTheSDKArtifactCheckFailsOnAWheelWithoutASourceDistribution(t *testing.T) {
	dir := sdkArtifactFixture(t, "0.1.0")
	if err := os.Remove(filepath.Join(dir, "python", "bin", "dist", "pulumi_filescom-0.1.0.tar.gz")); err != nil {
		t.Fatalf("remove the source distribution: %v", err)
	}
	out, err := runSDKArtifactCheck(t, dir, "v0.1.0")
	if err == nil {
		t.Fatalf("the check passed with a wheel and no source distribution:\n%s", out)
	}
	if !strings.Contains(out, "source distribution") {
		t.Errorf("the check does not name the missing source distribution:\n%s", out)
	}
}

func renameOne(t *testing.T, dir, from, to string) {
	t.Helper()
	if err := os.Rename(filepath.Join(dir, from), filepath.Join(dir, to)); err != nil {
		t.Fatalf("rename %s: %v", from, err)
	}
}

// The upload carries the wheel and the source distribution, and the check read the version of the
// wheel alone. A stale source distribution beside a fresh wheel publishes under the release tag.
func TestTheSDKArtifactCheckRefusesAStaleSourceDistribution(t *testing.T) {
	dist := filepath.Join("python", "bin", "dist")
	dir := sdkArtifactFixture(t, "0.1.0")
	renameOne(t, dir, filepath.Join(dist, "pulumi_filescom-0.1.0.tar.gz"),
		filepath.Join(dist, "pulumi_filescom-0.1.0a1.tar.gz"))

	out, err := runSDKArtifactCheck(t, dir, "v0.1.0")
	if err == nil {
		t.Fatalf("the check passed on a prerelease source distribution under a final tag:\n%s", out)
	}
	if !strings.Contains(out, "python") {
		t.Errorf("the check does not name the stale SDK:\n%s", out)
	}
}

// TestTheSDKArtifactCheckFailsOnAForeignPackageName reads the identity, not only the layout. A
// tree carrying somebody else's name publishes to somebody else's package.
func TestTheSDKArtifactCheckFailsOnAForeignPackageName(t *testing.T) {
	dir := sdkArtifactFixture(t, "0.1.0")
	manifest := filepath.Join(dir, "nodejs", "bin", "package.json")
	if err := os.WriteFile(manifest, []byte(`{"name": "@pulumi/filescom", "version": "0.1.0"}`), 0o600); err != nil {
		t.Fatalf("rewrite the manifest: %v", err)
	}
	out, err := runSDKArtifactCheck(t, dir, "v0.1.0")
	if err == nil {
		t.Fatalf("the check passed on a manifest naming another package:\n%s", out)
	}
	if !strings.Contains(out, npmPackageName) {
		t.Errorf("the check does not name the package this repository publishes:\n%s", out)
	}
}

// TestNoBinaryPublishesBeforeTheSDKSetIsConfirmed walks the release job graph. A binary on the
// release page that no SDK can be published against is the failure this ordering prevents.
func TestNoBinaryPublishesBeforeTheSDKSetIsConfirmed(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatalf("read release.yml: %v", err)
	}
	jobs := jobBlocks(string(body))

	var gate string
	for name, block := range jobs {
		if strings.Contains(block, sdkArtifactScript) {
			gate = name
		}
	}
	if gate == "" {
		t.Fatalf("no release job runs %s, so nothing confirms the SDK set", sdkArtifactScript)
	}

	publish, found := jobs["publish_provider"]
	if !found {
		t.Fatal("release.yml declares no publish_provider job")
	}
	if !reaches(jobs, "publish_provider", gate, map[string]bool{}) {
		t.Errorf("publish_provider needs %v and none of them reaches %s",
			jobNeeds(publish), gate)
	}
}

// reaches reports whether one job depends on another, directly or through its own needs.
func reaches(jobs map[string]string, from, target string, seen map[string]bool) bool {
	if seen[from] {
		return false
	}
	seen[from] = true
	for _, need := range jobNeeds(jobs[from]) {
		if need == target || reaches(jobs, need, target, seen) {
			return true
		}
	}
	return false
}

// jobNeeds reads the jobs one job waits for, in either the inline or the list spelling.
func jobNeeds(block string) []string {
	var needs []string
	inList := false
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "needs:":
			inList = true
		case strings.HasPrefix(trimmed, "needs: ["):
			for _, name := range strings.Split(strings.Trim(strings.TrimPrefix(trimmed, "needs: ["), "]"), ",") {
				needs = append(needs, strings.TrimSpace(name))
			}
		case strings.HasPrefix(trimmed, "needs: "):
			needs = append(needs, strings.TrimPrefix(trimmed, "needs: "))
		case inList && strings.HasPrefix(trimmed, "- "):
			needs = append(needs, strings.TrimPrefix(trimmed, "- "))
		default:
			inList = false
		}
	}
	return needs
}
