package filescom

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const (
	nodejsLanguage     = "nodejs"
	pythonLanguage     = "python"
	dotnetLanguage     = "dotnet"
	bridgeMetadataPath = "cmd/pulumi-resource-filescom/bridge-metadata.json"
	providerBinaryName = "pulumi-resource-filescom"
	logoRepoPath       = "docs/logo.svg"
	// The branch the logo URL reads the file from.
	logoBranchSegment = "/main/"
)

// The four SDKs this provider ships, and the directory that holds the sources of each. The
// bridge generates them from the committed schema; no other language tree belongs under sdk/.
var sdkSourceDirs = map[string]string{
	dotnetLanguage: "sdk/dotnet",
	"go":           "sdk/go/filescom",
	nodejsLanguage: "sdk/nodejs",
	pythonLanguage: "sdk/python",
}

// The file extension that holds the resource declarations of each SDK.
var sdkSourceExtensions = map[string]string{
	dotnetLanguage: ".cs",
	"go":           ".go",
	nodejsLanguage: ".ts",
	pythonLanguage: ".py",
}

// Directories a generator or a build writes under an SDK. A token that only appears in one of
// these is a build product, not a committed source.
var sdkBuildDirs = map[string]bool{
	"bin": true, "obj": true, "dist": true, "build": true, "node_modules": true, ".venv": true,
}

// TestTheEmbeddedMetadataCoversEveryEntity reads the bytes the compiler embedded. The alias
// history lives here, an entity missing from it loses its aliases, and nothing else fails.
func TestTheEmbeddedMetadataCoversEveryEntity(t *testing.T) {
	var embedded struct {
		AutoAliasing *struct {
			Resources   map[string]json.RawMessage `json:"resources"`
			DataSources map[string]json.RawMessage `json:"datasources"`
		} `json:"auto-aliasing"`
	}
	if err := json.Unmarshal(metadata, &embedded); err != nil {
		t.Fatalf("parse the embedded metadata: %v", err)
	}
	if embedded.AutoAliasing == nil {
		t.Fatalf("the embedded metadata holds no alias history: %d bytes", len(metadata))
	}

	list := schemaReadEntityList(t)
	schemaAssertSameSet(t, "alias history resources",
		schemaSortedKeys(embedded.AutoAliasing.Resources), list.resources)
	schemaAssertSameSet(t, "alias history data sources",
		schemaSortedKeys(embedded.AutoAliasing.DataSources), list.dataSources)

	// The bridge reads the file next to schema.json, so the name it carries must be that file.
	info := Provider().MetadataInfo
	if info == nil {
		t.Fatal("Provider() passes no metadata to the bridge")
	}
	beside := filepath.Join(filepath.Dir(committedSchemaPath), info.Path)
	if beside != bridgeMetadataPath {
		t.Errorf("the bridge reads the metadata from %s and the compiler embeds %s",
			beside, bridgeMetadataPath)
	}
	if _, err := os.Stat(beside); err != nil {
		t.Errorf("the bridge reads the metadata from %s, which does not exist: %v", beside, err)
	}
}

// TestTheBuiltProviderServesTheCommittedSchema asks the built binary for its schema, which is
// the path every Pulumi program and every SDK generation reads.
func TestTheBuiltProviderServesTheCommittedSchema(t *testing.T) {
	binary := filepath.Join(repoRoot(t), "bin", providerBinaryName)
	if _, err := os.Stat(binary); err != nil {
		t.Skipf("run `make provider` first: %s is missing", binary)
	}
	if _, err := exec.LookPath("pulumi"); err != nil {
		t.Skip("the Pulumi CLI is not on PATH, so no program can ask the binary for its schema")
	}

	cmd := exec.Command("pulumi", "package", "get-schema", binary)
	cmd.Dir = repoRoot(t)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pulumi package get-schema %s: %v", binary, err)
	}

	served := schemaParse(t, out, "the schema the built provider serves")
	committed := schemaCommitted(t)
	schemaAssertSameSet(t, "served resources",
		schemaSortedKeys(served.Resources), schemaSortedKeys(committed.Resources))
	schemaAssertSameSet(t, "served functions",
		schemaSortedKeys(served.Functions), schemaSortedKeys(committed.Functions))
}

// TestTheLogoTheSchemaAdvertisesIsInTheRepo follows the URL the registry reads back to the
// file. The registry fetches it from the default branch, so an untracked file serves nothing.
func TestTheLogoTheSchemaAdvertisesIsInTheRepo(t *testing.T) {
	var advertised struct {
		LogoURL    string `json:"logoUrl"`
		Repository string `json:"repository"`
	}
	raw, err := os.ReadFile(committedSchemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", committedSchemaPath, err)
	}
	if err := json.Unmarshal(raw, &advertised); err != nil {
		t.Fatalf("parse %s: %v", committedSchemaPath, err)
	}

	prov := Provider()
	if advertised.LogoURL != prov.LogoURL {
		t.Errorf("%s advertises the logo at %s and resources.go names %s",
			committedSchemaPath, advertised.LogoURL, prov.LogoURL)
	}

	parsed, err := url.Parse(advertised.LogoURL)
	if err != nil {
		t.Fatalf("parse the logo URL %s: %v", advertised.LogoURL, err)
	}
	if parsed.Scheme != "https" {
		t.Errorf("the logo URL uses the %s scheme: %s", parsed.Scheme, advertised.LogoURL)
	}
	owner := strings.TrimPrefix(advertised.Repository, "https://github.com/")
	if !strings.HasPrefix(parsed.Path, "/"+owner+logoBranchSegment) {
		t.Errorf("the logo URL path %s does not read from %s%s", parsed.Path, owner, logoBranchSegment)
	}

	_, relative, found := strings.Cut(parsed.Path, logoBranchSegment)
	if !found {
		t.Fatalf("the logo URL names no branch: %s", advertised.LogoURL)
	}
	if relative != logoRepoPath {
		t.Errorf("the logo URL reads %s and this repo keeps the logo at %s", relative, logoRepoPath)
	}
	info, err := os.Stat(filepath.Join(repoRoot(t), relative))
	if err != nil {
		t.Fatalf("the logo URL reads %s, which does not exist: %v", relative, err)
	}
	if info.Size() == 0 {
		t.Errorf("%s is empty, so the registry would show nothing", relative)
	}
	if tracked := mustGit(t, repoRoot(t), "ls-files", "--error-unmatch", relative); tracked != relative {
		t.Errorf("git does not track %s, so the branch serves no file there", relative)
	}
}

// TestTheConverterInstallPrecedesTheSchemaGeneration guards the order the example conversion
// needs: the schema run shells out to the converter and may not fetch a plugin on demand.
func TestTheConverterInstallPrecedesTheSchemaGeneration(t *testing.T) {
	root := repoRoot(t)

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	if err != nil {
		t.Fatalf("read the Makefile: %v", err)
	}
	if !strings.Contains(string(makefile), ".make/converter_plugin") {
		t.Error("the Makefile does not make the schema depend on the converter plugin")
	}
	for _, line := range strings.Split(string(makefile), "\n") {
		if !strings.HasPrefix(line, ".make/schema:") || strings.Contains(line, "export") {
			continue
		}
		if !strings.Contains(line, ".make/converter_plugin") {
			t.Errorf("the schema rule does not require the converter plugin: %s", line)
		}
	}

	for _, workflow := range workflowFiles(t) {
		body, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatalf("read %s: %v", workflow, err)
		}
		name := filepath.Base(workflow)
		installs := linesOf(string(body), "make converter_plugin")
		for _, schemaStep := range linesOf(string(body), "make tfgen") {
			earlier := -1
			for _, converterStep := range installs {
				// An install in an earlier job ran on another runner and left nothing behind.
				if converterStep < schemaStep && sameJob(string(body), converterStep, schemaStep) {
					earlier = converterStep
				}
			}
			if earlier < 0 {
				t.Errorf("%s generates the schema on line %d with no converter install before it",
					name, schemaStep+1)
			}
		}
	}
}

// TestTheSDKTreeAndTheSDKTargetsAgree pins the state this repository holds: every language has
// committed sources, and every language's generate target runs rather than refusing.
func TestTheSDKTreeAndTheSDKTargetsAgree(t *testing.T) {
	if missing := missingSDKLanguages(t); len(missing) > 0 {
		t.Fatalf("the SDK tree has no sources for %s", strings.Join(missing, ", "))
	}
	for _, language := range sortedLanguages() {
		out, err := runMake(t, "-n", "generate_"+language)
		if err != nil {
			t.Errorf("make generate_%s refuses while %s holds sources: %s",
				language, sdkSourceDirs[language], recipeOutput(out))
			continue
		}
		// A dry run prints a recipe without running it, so a target that only exits non-zero
		// still reports success. The generator it would run is what proves the target is live.
		if !strings.Contains(out, "gen-sdk") || !strings.Contains(out, "--language "+language) {
			t.Errorf("make generate_%s runs no SDK generator: %s", language, recipeOutput(out))
		}
	}
}

// TestGeneratingTheSDKsLeavesTheTreeClean regenerates the schema and the four SDKs. A
// difference means the committed sources are not what the generators produce.
func TestGeneratingTheSDKsLeavesTheTreeClean(t *testing.T) {
	if testing.Short() {
		t.Skip("regenerating the schema and the four SDKs takes minutes, which -short excludes")
	}

	for _, target := range []string{"tfgen", "generate_sdks"} {
		if out, err := runMake(t, target); err != nil {
			t.Fatalf("make %s: %v\n%s", target, err, out)
		}
	}

	if changed := changedPaths(t, repoRoot(t)); len(changed) > 0 {
		t.Errorf("generating the schema and the SDKs changed %d committed paths:\n%s",
			len(changed), strings.Join(changed, "\n"))
	}
}

// TestTheGeneratedCheckTargetNamesALiveTest reads the pattern `make test_generated` passes to
// `go test -run`, which exits zero when it matches nothing, so a rename would go unseen.
func TestTheGeneratedCheckTargetNamesALiveTest(t *testing.T) {
	out, err := runMake(t, "-n", "test_generated")
	if err != nil {
		t.Fatalf("make -n test_generated: %v\n%s", err, out)
	}
	var name string
	fields := strings.Fields(out)
	for position, field := range fields {
		if field == "-run" && position+1 < len(fields) {
			name = fields[position+1]
		}
	}
	if name == "" {
		t.Fatalf("make test_generated passes no -run pattern: %s", recipeOutput(out))
	}

	sources, err := filepath.Glob(filepath.Join(repoRoot(t), "provider", "*_test.go"))
	if err != nil {
		t.Fatalf("glob the provider tests: %v", err)
	}
	for _, source := range sources {
		body, err := os.ReadFile(source)
		if err != nil {
			t.Fatalf("read %s: %v", source, err)
		}
		if strings.Contains(string(body), "func "+name+"(") {
			return
		}
	}
	t.Errorf("make test_generated runs `-run %s` and no test under provider/ declares it", name)
}

// TestTheEmbedContractHolds runs the check that reads the committed schema and the embed
// directive together. It had no caller, so a broken embed contract would surface at build time.
func TestTheEmbedContractHolds(t *testing.T) {
	out, err := runMake(t, "schema_embed")
	if err != nil {
		t.Fatalf("make schema_embed: %v\n%s", err, out)
	}
	if !strings.Contains(out, committedSchemaPath) {
		t.Errorf("make schema_embed reports nothing about %s: %s", committedSchemaPath, out)
	}
}

// TestEveryGenerationStepIsFollowedByAWorktreeCheck reads the workflows. `make test_provider`
// excludes the regeneration check, so an automated runner has to compare the tree after it.
func TestEveryGenerationStepIsFollowedByAWorktreeCheck(t *testing.T) {
	var checked int
	for _, workflow := range workflowFiles(t) {
		body, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatalf("read %s: %v", workflow, err)
		}
		name := filepath.Base(workflow)
		for _, step := range []string{"make tfgen", "make generate_"} {
			// Every occurrence, not the first: a second generation step in the same file
			// would otherwise be read through the check that follows the first.
			for _, generation := range linesOf(string(body), step) {
				checked++
				if !comparesTheTreeAfter(string(body), generation) {
					t.Errorf("%s runs `%s` on line %d and never compares the tree with the commit",
						name, step, generation+1)
				}
			}
		}
	}
	if checked == 0 {
		t.Error("no workflow generates the schema or an SDK, so nothing would catch a stale tree")
	}
}

// TestTheWorktreeCheckReadRejectsAWorkflowWithoutOne strips the comparison out of a real
// workflow body. Without this the read above would pass on a workflow that checks nothing.
func TestTheWorktreeCheckReadRejectsAWorkflowWithoutOne(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", "main.yml"))
	if err != nil {
		t.Fatalf("read main.yml: %v", err)
	}
	found := linesOf(string(body), "make tfgen")
	if len(found) == 0 {
		t.Fatal("main.yml generates no schema, so there is nothing to strip a check from")
	}
	generation := found[0]
	if !comparesTheTreeAfter(string(body), generation) {
		t.Fatal("main.yml compares no tree, so stripping the comparison proves nothing")
	}

	var kept []string
	for _, line := range strings.Split(string(body), "\n") {
		if strings.Contains(line, worktreeScript) || strings.Contains(line, "make test_generated") {
			continue
		}
		kept = append(kept, line)
	}
	if comparesTheTreeAfter(strings.Join(kept, "\n"), generation) {
		t.Error("the read finds a comparison after every comparison line was removed")
	}

	// Prepending the comparison puts the generation step one line lower and the only
	// comparison above it, which is the arrangement a whole-file search would accept.
	earlier := append([]string{"          ./" + worktreeScript}, kept...)
	if comparesTheTreeAfter(strings.Join(earlier, "\n"), generation+1) {
		t.Error("the read counts a comparison that runs before the generation step")
	}
}

// TestTheWorktreeReadNamesRenamedAndSpacedPaths builds the two shapes a whitespace split gets
// wrong: a rename, which carries two paths and an arrow, and a path holding a space.
func TestTheWorktreeReadNamesRenamedAndSpacedPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sdk", "nodejs"), 0o755); err != nil {
		t.Fatalf("create the fixture tree: %v", err)
	}
	for _, name := range []string{"old name.ts", "package.json"} {
		if err := os.WriteFile(filepath.Join(dir, "sdk", "nodejs", name), []byte("x\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	mustGit(t, dir, "init", "-q", ".")
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "-c", "commit.gpgsign=false", "-c", "user.name=sdk",
		"-c", "user.email=sdk@example.invalid", "commit", "-qm", "fixture")
	mustGit(t, dir, "mv", "sdk/nodejs/old name.ts", "sdk/nodejs/new name.ts")
	if err := os.WriteFile(filepath.Join(dir, "sdk", "nodejs", "package.json"), []byte("y\n"), 0o600); err != nil {
		t.Fatalf("rewrite package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sdk", "nodejs", "untracked file.ts"), []byte("z\n"), 0o600); err != nil {
		t.Fatalf("write the untracked file: %v", err)
	}

	got := changedPaths(t, dir)
	sort.Strings(got)
	want := []string{
		"sdk/nodejs/new name.ts",
		"sdk/nodejs/package.json",
		"sdk/nodejs/untracked file.ts",
	}
	schemaAssertSameSet(t, "changed paths", got, want)
}

// changedPaths lists the paths git reports as changed in dir. The NUL-separated form quotes
// nothing, so a path holding a space survives, and a rename names its destination first.
func changedPaths(t *testing.T, dir string) []string {
	t.Helper()
	cmd := exec.Command("git", "status", "--porcelain", "-z")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git status --porcelain -z in %s: %v", dir, err)
	}

	var paths []string
	records := strings.Split(string(out), "\x00")
	for index := 0; index < len(records); index++ {
		record := records[index]
		// Two status letters, a space, then the path.
		if len(record) < 4 {
			continue
		}
		if record[0] == 'R' || record[0] == 'C' {
			index++
		}
		paths = append(paths, record[3:])
	}
	return paths
}

// TestEverySDKCarriesEveryEntity reads the token of every mapped entity out of the four SDKs.
// A generator that drops one leaves no other trace, and the language builds still pass.
func TestEverySDKCarriesEveryEntity(t *testing.T) {
	requireOneSDKPerLanguage(t)

	list := schemaReadEntityList(t)
	prov := Provider()
	var want []string
	for _, name := range list.resources {
		info := prov.Resources[name]
		if info == nil {
			t.Errorf("the provider maps no resource %s, so no SDK can carry it", name)
			continue
		}
		want = append(want, string(info.Tok))
	}
	for _, name := range list.dataSources {
		info := prov.DataSources[name]
		if info == nil {
			t.Errorf("the provider maps no data source %s, so no SDK can carry it", name)
			continue
		}
		want = append(want, string(info.Tok))
	}
	sort.Strings(want)

	for _, language := range sortedLanguages() {
		found := tokensInSDK(t, sdkSourceDirs[language], sdkSourceExtensions[language], want)
		if missing := schemaDifference(want, found); len(missing) > 0 {
			t.Errorf("the %s SDK carries %d of %d entities, missing %v",
				language, len(found), len(want), schemaHead(missing))
		}
	}
}

func missingSDKLanguages(t *testing.T) []string {
	t.Helper()
	root := repoRoot(t)
	var missing []string
	for _, language := range sortedLanguages() {
		info, err := os.Stat(filepath.Join(root, sdkSourceDirs[language]))
		if err != nil || !info.IsDir() {
			missing = append(missing, language)
		}
	}
	return missing
}

// requireOneSDKPerLanguage reads the language trees under sdk/. A fifth tree is a generator
// nobody asked for, and a second tree for one language would carry a second copy of a token.
func requireOneSDKPerLanguage(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(repoRoot(t), "sdk"))
	if err != nil {
		t.Fatalf("read sdk/: %v", err)
	}
	var got []string
	for _, entry := range entries {
		if entry.IsDir() {
			got = append(got, entry.Name())
		}
	}
	sort.Strings(got)
	schemaAssertSameSet(t, "sdk language trees", got, sortedLanguages())

	// The generator writes a .gitattributes beside the package, so only directories count.
	packages, err := os.ReadDir(filepath.Join(repoRoot(t), "sdk", "go"))
	if err != nil {
		t.Fatalf("read sdk/go: %v", err)
	}
	var goPackages []string
	for _, entry := range packages {
		if entry.IsDir() {
			goPackages = append(goPackages, "sdk/go/"+entry.Name())
		}
	}
	schemaAssertSameSet(t, "sdk/go packages", goPackages, []string{sdkSourceDirs["go"]})
}

// tokensInSDK returns the wanted tokens that a committed source file of the SDK carries. A
// generated file quotes the token of every resource and every function it declares.
func tokensInSDK(t *testing.T, dir, extension string, want []string) []string {
	t.Helper()
	seen := map[string]bool{}
	root := filepath.Join(repoRoot(t), dir)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if sdkBuildDirs[entry.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != extension {
			return nil
		}
		//nolint:gosec // G122: reads this repo's own generated SDK; no untrusted symlink to race.
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, token := range want {
			if !seen[token] && bytes.Contains(body, []byte(token)) {
				seen[token] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	found := make([]string, 0, len(seen))
	for token := range seen {
		found = append(found, token)
	}
	sort.Strings(found)
	return found
}

func runMake(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("make", args...)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// recipeOutput drops the lines make writes about its own failure, so what remains is what the
// recipe itself said. A recipe that only exits non-zero leaves nothing here.
func recipeOutput(out string) string {
	var kept []string
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "make:") || strings.HasPrefix(line, "make[") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// comparesTheTreeAfter reports whether a step below line reads the worktree back.
func comparesTheTreeAfter(body string, line int) bool {
	lines := strings.Split(body, "\n")
	for index := line + 1; index < len(lines); index++ {
		// The next job runs on another runner, so its comparison reads another tree.
		if _, isJob := jobHeader(lines[index]); isJob {
			return false
		}
		for _, check := range []string{worktreeScript, "make test_generated"} {
			if strings.Contains(lines[index], check) {
				return true
			}
		}
	}
	return false
}

// linesOf returns the index of every line that holds needle. Reading only the first hides a
// second generation step that nothing compares the tree after.
func linesOf(body, needle string) []int {
	var found []int
	for index, line := range strings.Split(body, "\n") {
		if strings.Contains(line, needle) {
			found = append(found, index)
		}
	}
	return found
}

// workflowFiles lists the workflow files in both spellings GitHub runs. An empty list means a
// scan read nothing.
func workflowFiles(t *testing.T) []string {
	t.Helper()
	var found []string
	for _, pattern := range []string{"*.yml", "*.yaml"} {
		matched, err := filepath.Glob(filepath.Join(repoRoot(t), ".github", "workflows", pattern))
		if err != nil {
			t.Fatalf("glob the workflows: %v", err)
		}
		found = append(found, matched...)
	}
	if len(found) == 0 {
		t.Fatal("the repository holds no workflow files")
	}
	return found
}

func sortedLanguages() []string {
	languages := make([]string, 0, len(sdkSourceDirs))
	for language := range sdkSourceDirs {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

// jobHeader returns the job a line declares. A job name sits at one indent level, ends the line
// with a colon and holds no space.
func jobHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(strings.TrimSuffix(line, ":"))
	if !strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "   ") ||
		!strings.HasSuffix(line, ":") || trimmed == "" || strings.Contains(trimmed, " ") {
		return "", false
	}
	return trimmed, true
}

// sameJob reports whether two lines sit in one job. A job header between them puts the earlier
// step on another runner, where nothing it wrote survives.
func sameJob(body string, from, to int) bool {
	lines := strings.Split(body, "\n")
	for index := from + 1; index <= to && index < len(lines); index++ {
		if _, isJob := jobHeader(lines[index]); isJob {
			return false
		}
	}
	return true
}

// jobBlocks splits a workflow into its jobs. A step reads as covered by the wrong job when the
// whole file is searched, and only the job that holds a step provides that step's tools.
func jobBlocks(body string) map[string]string {
	blocks := map[string]string{}
	name := ""
	var lines []string
	for _, line := range strings.Split(body, "\n") {
		if _, isJob := jobHeader(line); isJob {
			if name != "" {
				blocks[name] = strings.Join(lines, "\n")
			}
			name, _ = jobHeader(line)
			lines = nil
			continue
		}
		if name != "" {
			lines = append(lines, line)
		}
	}
	if name != "" {
		blocks[name] = strings.Join(lines, "\n")
	}
	return blocks
}

// TestTheServedSchemaCheckRunsWhereTheProviderExists reads the workflows. The check asks the built
// binary for its schema and skips without it, so a job lacking either tool reads a skip as a pass.
func TestTheServedSchemaCheckRunsWhereTheProviderExists(t *testing.T) {
	const servedSchemaTest = "TestTheBuiltProviderServesTheCommittedSchema"
	var ran int
	for _, workflow := range workflowFiles(t) {
		body, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatalf("read %s: %v", workflow, err)
		}
		for job, block := range jobBlocks(string(body)) {
			if !strings.Contains(block, servedSchemaTest) {
				continue
			}
			ran++
			for _, needed := range []string{"Install the pinned Pulumi CLI", "Restore the provider binaries"} {
				if !strings.Contains(block, needed) {
					t.Errorf("%s job %s runs %s without the step %q, so the check skips",
						filepath.Base(workflow), job, servedSchemaTest, needed)
				}
			}
		}
	}
	if ran == 0 {
		t.Errorf("no workflow job runs %s, so nothing compares the served schema with the commit",
			servedSchemaTest)
	}
}

// TestTheSDKJobsRegenerateEveryLanguage reads the matrix. One local command regenerates all four
// SDKs; CI splits that across this matrix, so a language missing from it is never regenerated.
func TestTheSDKJobsRegenerateEveryLanguage(t *testing.T) {
	for _, name := range []string{"pull-request.yml", "main.yml", "release.yml"} {
		body, err := os.ReadFile(filepath.Join(repoRoot(t), ".github", "workflows", name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		block, found := jobBlocks(string(body))["build_sdks"]
		if !found {
			t.Errorf("%s declares no build_sdks job", name)
			continue
		}
		schemaAssertSameSet(t, name+" build_sdks languages", matrixLanguages(block), sortedLanguages())
	}
}

// matrixLanguages reads the language list out of a job's build matrix.
func matrixLanguages(block string) []string {
	var found []string
	inside := false
	for _, line := range strings.Split(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "language:" {
			inside = true
			continue
		}
		if !inside {
			continue
		}
		if !strings.HasPrefix(trimmed, "- ") {
			break
		}
		found = append(found, strings.TrimPrefix(trimmed, "- "))
	}
	sort.Strings(found)
	return found
}
