package filescom

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const (
	releaseConfigPath = ".goreleaser.yml"
	// The one symbol a release build stamps, and the template that names the archives.
	versionSymbol       = "github.com/jschady/pulumi-filescom/provider/pkg/version.Version"
	archiveNameTemplate = "{{ .Binary }}-{{ .Tag }}-{{ .Os }}-{{ .Arch }}"
)

// archiveCountPattern reads the count the snapshot job compares against. Searching the job for
// the number alone matches the arm64 in the same step, which pins nothing.
var archiveCountPattern = regexp.MustCompile(`-ne ([0-9]+)`)

// The sections the release config may hold. Anything else uploads somewhere, signs something,
// or builds an image, and none of that belongs in a provider release.
var releaseConfigSections = []string{
	"archives", "builds", "changelog", "project_name", "release", "version",
}

// releaseConfig holds the release config split into its top-level sections.
type releaseConfig struct {
	sections map[string][]string
	body     string
}

func readReleaseConfig(t *testing.T) releaseConfig {
	t.Helper()
	path := filepath.Join(repoRoot(t), releaseConfigPath)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", releaseConfigPath, err)
	}
	config := releaseConfig{sections: map[string][]string{}, body: string(raw)}
	current := ""
	for _, line := range strings.Split(config.body, "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "-") {
			current = strings.TrimSuffix(strings.SplitN(line, ":", 2)[0], ":")
			config.sections[current] = []string{line}
			continue
		}
		if current == "" {
			t.Fatalf("%s opens with an indented line: %s", releaseConfigPath, line)
		}
		config.sections[current] = append(config.sections[current], line)
	}
	if len(config.sections) == 0 {
		t.Fatalf("%s holds no sections", releaseConfigPath)
	}
	return config
}

// listUnder returns the items of the `key:` list inside the section, in file order.
func (c releaseConfig) listUnder(t *testing.T, section, key string) []string {
	t.Helper()
	lines, ok := c.sections[section]
	if !ok {
		t.Fatalf("%s holds no %s section", releaseConfigPath, section)
	}
	var items []string
	indent := -1
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == key+":" {
			indent = len(line) - len(strings.TrimLeft(line, " "))
			continue
		}
		if indent < 0 {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") ||
			len(line)-len(strings.TrimLeft(line, " ")) <= indent {
			break
		}
		items = append(items, strings.Trim(strings.TrimPrefix(trimmed, "- "), `"`))
	}
	if indent < 0 {
		t.Fatalf("the %s section declares no %s list", section, key)
	}
	return items
}

// valueUnder returns the value of the `key:` scalar inside the section.
func (c releaseConfig) valueUnder(t *testing.T, section, key string) string {
	t.Helper()
	lines, ok := c.sections[section]
	if !ok {
		t.Fatalf("%s holds no %s section", releaseConfigPath, section)
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if after, found := strings.CutPrefix(trimmed, key+":"); found {
			return strings.Trim(strings.TrimSpace(after), `"`)
		}
	}
	t.Fatalf("the %s section sets no %s", section, key)
	return ""
}

// TestTheReleaseConfigStampsOneVersionSymbol reads the linker flags. A second -X would give the
// binary a second version to disagree with, and the plugin loader reads only the one.
func TestTheReleaseConfigStampsOneVersionSymbol(t *testing.T) {
	config := readReleaseConfig(t)

	if got := config.valueUnder(t, "version", "version"); got != "2" {
		t.Errorf("%s declares schema version %s and the tool reads 2", releaseConfigPath, got)
	}
	if got := config.valueUnder(t, "project_name", "project_name"); got != "pulumi-filescom" {
		t.Errorf("the project name is %s", got)
	}

	ldflags := config.listUnder(t, "builds", "ldflags")
	var stamps []string
	for _, flag := range ldflags {
		if strings.HasPrefix(flag, "-X") {
			stamps = append(stamps, flag)
		}
	}
	if len(stamps) != 1 {
		t.Fatalf("the release build sets %d version symbols: %v", len(stamps), stamps)
	}
	symbol, value, found := strings.Cut(strings.TrimSpace(strings.TrimPrefix(stamps[0], "-X")), "=")
	if !found {
		t.Fatalf("the linker flag %s assigns nothing", stamps[0])
	}
	if symbol != versionSymbol {
		t.Errorf("the release build stamps %s and the provider reads %s", symbol, versionSymbol)
	}
	if value != "{{.Tag}}" {
		t.Errorf("the release build stamps %s rather than the tag it builds", value)
	}
}

// TestTheReleaseConfigCoversEveryPlatform reads the platform lists the plugin download needs.
// A missing pair leaves that platform with no plugin at all.
func TestTheReleaseConfigCoversEveryPlatform(t *testing.T) {
	config := readReleaseConfig(t)
	schemaAssertSameSet(t, "release operating systems",
		config.listUnder(t, "builds", "goos"), []string{"darwin", "linux", "windows"})
	schemaAssertSameSet(t, "release architectures",
		config.listUnder(t, "builds", "goarch"), []string{"amd64", "arm64"})
	schemaAssertSameSet(t, "release build environment",
		config.listUnder(t, "builds", "env"), []string{"CGO_ENABLED=0"})

	if got := config.valueUnder(t, "builds", "binary"); got != providerBinaryName {
		t.Errorf("the release builds the binary %s and the plugin loader runs %s",
			got, providerBinaryName)
	}
	if got := config.valueUnder(t, "builds", "dir"); got != "provider" {
		t.Errorf("the release builds from %s and the provider module is provider/", got)
	}
	main := config.valueUnder(t, "builds", "main")
	if main != "./cmd/"+providerBinaryName+"/" {
		t.Errorf("the release builds %s and the provider entry point is ./cmd/%s/",
			main, providerBinaryName)
	}
}

// TestTheReleaseArchivesCarryTheNameThePluginLoaderAsks derives every archive name from the
// config. The plugin download reads the name, so a template change breaks every install.
func TestTheReleaseArchivesCarryTheNameThePluginLoaderAsks(t *testing.T) {
	config := readReleaseConfig(t)

	if got := config.valueUnder(t, "archives", "name_template"); got != archiveNameTemplate {
		t.Errorf("the archive template is %s and the plugin loader reads %s",
			got, archiveNameTemplate)
	}
	schemaAssertSameSet(t, "archive formats",
		config.listUnder(t, "archives", "formats"), []string{"tar.gz"})

	schemaAssertSameSet(t, "archive names", expectedArchiveNames(t, "v9.9.9"), []string{
		"pulumi-resource-filescom-v9.9.9-darwin-amd64.tar.gz",
		"pulumi-resource-filescom-v9.9.9-darwin-arm64.tar.gz",
		"pulumi-resource-filescom-v9.9.9-linux-amd64.tar.gz",
		"pulumi-resource-filescom-v9.9.9-linux-arm64.tar.gz",
		"pulumi-resource-filescom-v9.9.9-windows-amd64.tar.gz",
		"pulumi-resource-filescom-v9.9.9-windows-arm64.tar.gz",
	})
}

// TestTheReleaseConfigUploadsNowhereAndSignsNothing reads the section list. An upload target or
// a signing hook would send this build somewhere nobody reviewed.
func TestTheReleaseConfigUploadsNowhereAndSignsNothing(t *testing.T) {
	config := readReleaseConfig(t)

	schemaAssertSameSet(t, "release config sections",
		schemaSortedKeys(config.sections), releaseConfigSections)
	for _, forbidden := range []string{"blobs", "signs", "binary_signs", "notarize", "hooks"} {
		if strings.Contains(config.body, forbidden+":") {
			t.Errorf("%s declares %s, which uploads or signs outside this repository",
				releaseConfigPath, forbidden)
		}
	}
	if got := config.valueUnder(t, "changelog", "disable"); got != "true" {
		t.Errorf("the changelog section reads %s and this repository writes no changelog", got)
	}
	if got := config.valueUnder(t, "release", "prerelease"); got != "auto" {
		t.Errorf("the release section marks prereleases %s and a suffixed tag needs auto", got)
	}
}

// expectedArchiveNames renders the archive template over every platform pair the config lists.
func expectedArchiveNames(t *testing.T, tag string) []string {
	t.Helper()
	config := readReleaseConfig(t)
	replacer := strings.NewReplacer(
		"{{ .Binary }}", config.valueUnder(t, "builds", "binary"),
		"{{ .Tag }}", tag,
	)
	template := config.valueUnder(t, "archives", "name_template")
	var names []string
	for _, goos := range config.listUnder(t, "builds", "goos") {
		for _, goarch := range config.listUnder(t, "builds", "goarch") {
			name := strings.NewReplacer("{{ .Os }}", goos, "{{ .Arch }}", goarch).
				Replace(replacer.Replace(template))
			names = append(names, name+".tar.gz")
		}
	}
	sort.Strings(names)
	return names
}

// TestTheSnapshotJobCountsEveryArchive reads the job that exercises the release build. It built the
// binaries and stopped, so a wrong archive template reached a real release before anyone read it.
func TestTheSnapshotJobCountsEveryArchive(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "main.yml"))
	if err != nil {
		t.Fatalf("read main.yml: %v", err)
	}
	job, found := jobBlocks(string(body))["snapshot"]
	if !found {
		t.Fatal("main.yml declares no snapshot job")
	}
	if !strings.Contains(job, "release --snapshot") {
		t.Error("the snapshot job builds the binaries without archiving them")
	}

	expected := strconv.Itoa(len(expectedArchiveNames(t, "v0.0.0")))
	counted := archiveCountPattern.FindStringSubmatch(job)
	if counted == nil {
		t.Fatal("the snapshot job compares the archive count with nothing")
	}
	if counted[1] != expected {
		t.Errorf("the snapshot job accepts %s archives and the config builds %s",
			counted[1], expected)
	}
	if !strings.Contains(job, "artifacts.json") {
		t.Error("the snapshot job reads no list of what the release produced")
	}
}
