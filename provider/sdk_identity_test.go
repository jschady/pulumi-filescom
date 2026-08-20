package filescom

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"
)

// The name this provider is published under in each language registry. A code generator left
// to its defaults claims `@pulumi/...` and `Pulumi....`, which are Pulumi's own scope.
const (
	npmPackageName     = "pulumi-filescom"
	pypiPackageName    = "pulumi-filescom"
	nugetPackageID     = "Jschady.Filescom"
	nugetRootNamespace = "Jschady"
	goImportBasePath   = "github.com/jschady/pulumi-filescom/sdk/go/filescom"
)

// A distribution name treats runs of "-", "_" and "." as one hyphen, so pulumi_filescom and
// pulumi-filescom are the same package. https://peps.python.org/pep-0503/#normalized-names
var pypiSeparators = regexp.MustCompile(`[-_.]+`)

// The upstream repository the bridge derives from the package name, and the one Files.com
// actually publishes. https://github.com/Files-com/terraform-provider-files
const (
	derivedUpstreamRepoName = "terraform-provider-filescom"
	realUpstreamRepoName    = "terraform-provider-files"
)

// The icon the NuGet package carries, and the source image it is copied from. NuGet reads the
// leading bytes, so an SVG or an error page named logo.png is rejected at push.
const (
	dotnetIconPath       = "sdk/dotnet/logo.png"
	dotnetIconSourcePath = "docs/logo.png"
)

var pngMagic = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// The license the upstream provider is distributed under. The bridge writes the npm and PyPI
// readmes from it and assumes MPL 2.0 when the provider names nothing.
const upstreamLicenseName = "MIT"

// schemaLanguage is the per-language block each SDK generator reads out of the schema. The
// names below are the only ones these tests assert on.
type schemaLanguage struct {
	CSharp struct {
		RootNamespace string `json:"rootNamespace"`
	} `json:"csharp"`
	Go struct {
		ImportBasePath string `json:"importBasePath"`
	} `json:"go"`
	NodeJS struct {
		PackageName string `json:"packageName"`
		Readme      string `json:"readme"`
	} `json:"nodejs"`
	Python struct {
		Readme string `json:"readme"`
	} `json:"python"`
}

func schemaLanguageBlock(t *testing.T) schemaLanguage {
	t.Helper()
	raw, err := os.ReadFile(committedSchemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", committedSchemaPath, err)
	}
	var parsed struct {
		Language schemaLanguage `json:"language"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse %s: %v", committedSchemaPath, err)
	}
	return parsed.Language
}

// TestTheSchemaCarriesThePublishedPackageNames reads the names the generators will bake into
// the SDK manifests. The schema is the only place a name can be corrected before generation.
func TestTheSchemaCarriesThePublishedPackageNames(t *testing.T) {
	language := schemaLanguageBlock(t)
	if language.NodeJS.PackageName != npmPackageName {
		t.Errorf("the schema names the npm package %q and this provider publishes %q",
			language.NodeJS.PackageName, npmPackageName)
	}
	if language.CSharp.RootNamespace != nugetRootNamespace {
		t.Errorf("the schema names the .NET root namespace %q and this provider publishes under %q",
			language.CSharp.RootNamespace, nugetRootNamespace)
	}
	if language.Go.ImportBasePath != goImportBasePath {
		t.Errorf("the schema names the Go import base path %q and the SDK module is %q",
			language.Go.ImportBasePath, goImportBasePath)
	}
}

// TestTheProviderInfoNamesThePublishedPackages reads the three fields the schema's language
// block is generated from. A regression there stays invisible until the next generation.
func TestTheProviderInfoNamesThePublishedPackages(t *testing.T) {
	prov := Provider()
	if prov.JavaScript == nil || prov.CSharp == nil || prov.Golang == nil {
		t.Fatal("Provider() passes no per-language settings to the bridge")
	}
	if prov.JavaScript.PackageName != npmPackageName {
		t.Errorf("resources.go names the npm package %q and this provider publishes %q",
			prov.JavaScript.PackageName, npmPackageName)
	}
	if prov.CSharp.RootNamespace != nugetRootNamespace {
		t.Errorf("resources.go names the .NET root namespace %q and this provider publishes under %q",
			prov.CSharp.RootNamespace, nugetRootNamespace)
	}
	if prov.Golang.ImportBasePath != goImportBasePath {
		t.Errorf("resources.go names the Go import base path %q and the SDK module is %q",
			prov.Golang.ImportBasePath, goImportBasePath)
	}
}

// TestTheSDKReadmesNameTheUpstreamLicense reads the readme the bridge bakes into the schema and
// the license file it describes. Those readmes are the npm and PyPI pages a user lands on.
func TestTheSDKReadmesNameTheUpstreamLicense(t *testing.T) {
	license, err := os.ReadFile(filepath.Join(repoRoot(t), "upstream", "LICENSE"))
	if err != nil {
		t.Fatalf("read the upstream license: %v", err)
	}
	if !strings.Contains(string(license), upstreamLicenseName+" License") {
		t.Fatalf("upstream is not distributed under the %s license: %s",
			upstreamLicenseName, strings.SplitN(string(license), "\n", 2)[0])
	}

	switch named := Provider().TFProviderLicense; {
	case named == nil:
		t.Error("resources.go names no upstream license, so the readmes take the bridge default")
	case string(*named) != upstreamLicenseName:
		t.Errorf("resources.go names the upstream license %s and upstream ships %s",
			*named, upstreamLicenseName)
	}

	language := schemaLanguageBlock(t)
	for name, readme := range map[string]string{
		"npm":  language.NodeJS.Readme,
		"PyPI": language.Python.Readme,
	} {
		if readme == "" {
			t.Errorf("the schema gives the %s package no readme", name)
			continue
		}
		if !strings.Contains(readme, "["+upstreamLicenseName+"]") {
			t.Errorf("the %s readme does not say the upstream provider is %s: %s",
				name, upstreamLicenseName, readme)
		}
	}
}

// TestTheGeneratedTextLinksTheUpstreamRepositoryThatExists reads every place the generator names
// the upstream repository. Those links reach the registry page, the npm page, and the PyPI page.
func TestTheGeneratedTextLinksTheUpstreamRepositoryThatExists(t *testing.T) {
	for source, raw := range map[string][]byte{
		committedSchemaPath:    mustReadFile(t, committedSchemaPath),
		"the generated schema": schemaGenerate(t).schema,
	} {
		if len(raw) == 0 {
			t.Fatalf("%s is empty", source)
		}
		if count := bytes.Count(raw, []byte(derivedUpstreamRepoName)); count != 0 {
			t.Errorf("%s links %s %d times, and that repository does not exist",
				source, derivedUpstreamRepoName, count)
		}
		if !bytes.Contains(raw, []byte(realUpstreamRepoName)) {
			t.Errorf("%s names no upstream repository at all", source)
		}
	}
}

// TestTheUpstreamLinkCorrectionStillHasWorkToDo runs the correction over the link the bridge
// derives. It goes red once the bridge derives the real repository: delete the correction then.
func TestTheUpstreamLinkCorrectionStillHasWorkToDo(t *testing.T) {
	info := Provider()
	derived := "https://" + info.GetGitHubHost() + "/" + info.GetGitHubOrg() +
		"/terraform-provider-" + info.Name
	if !strings.HasSuffix(derived, derivedUpstreamRepoName) {
		t.Fatalf("the bridge now derives %s; the correction below no longer describes it", derived)
	}

	spec := pschema.PackageSpec{
		Attribution: "based on " + derived + ".",
		Language: map[string]pschema.RawMessage{
			nodejsLanguage: pschema.RawMessage(`{"readme":"derived from ` + derived + `"}`),
		},
	}
	corrected := correctUpstreamLinks(&spec)
	if corrected == 0 {
		t.Fatal("the correction changed nothing, so the generated text keeps the derived link")
	}
	if strings.Contains(spec.Attribution, derivedUpstreamRepoName) {
		t.Errorf("the attribution still links the derived repository: %s", spec.Attribution)
	}
	if bytes.Contains(spec.Language[nodejsLanguage], []byte(derivedUpstreamRepoName)) {
		t.Errorf("the nodejs readme still links the derived repository: %s",
			spec.Language[nodejsLanguage])
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

// TestTheDotnetPackageIconIsTheMarkThisRepoAuthored compares the packaged icon with the source
// image. The generator writes whatever the logo URL returns, so an error page reaches the package.
func TestTheDotnetPackageIconIsTheMarkThisRepoAuthored(t *testing.T) {
	source, err := os.ReadFile(filepath.Join(repoRoot(t), dotnetIconSourcePath))
	if err != nil {
		t.Fatalf("read %s: %v", dotnetIconSourcePath, err)
	}
	packaged, err := os.ReadFile(filepath.Join(repoRoot(t), dotnetIconPath))
	if err != nil {
		t.Fatalf("read %s: %v", dotnetIconPath, err)
	}

	// NuGet reads the leading bytes rather than the extension and rejects anything else.
	if !bytes.HasPrefix(packaged, pngMagic) {
		t.Errorf("%s is not a PNG: it starts %q", dotnetIconPath, head(packaged))
	}
	if !bytes.Equal(source, packaged) {
		t.Errorf("%s holds %d bytes and %s holds %d, so the package ships a different mark",
			dotnetIconSourcePath, len(source), dotnetIconPath, len(packaged))
	}

	project := filepath.Join("sdk", "dotnet", nugetPackageID+".csproj")
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), project))
	if err != nil {
		t.Fatalf("read %s: %v", project, err)
	}
	if !strings.Contains(string(raw), "<PackageIcon>"+filepath.Base(dotnetIconPath)+"</PackageIcon>") {
		t.Errorf("%s names no package icon, so nothing reads %s", project, dotnetIconPath)
	}
}

// TestTheDotnetGenerationRestoresThePackageIcon reads the recipe. The generator replaces its
// language directory whole, so the icon has to be written back after every generation.
func TestTheDotnetGenerationRestoresThePackageIcon(t *testing.T) {
	out, err := runMake(t, "-n", "generate_dotnet")
	if err != nil {
		t.Fatalf("make -n generate_dotnet: %v\n%s", err, out)
	}
	generated := strings.Index(out, "gen-sdk")
	restored := strings.Index(out, dotnetIconSourcePath)
	if generated < 0 {
		t.Fatalf("make generate_dotnet runs no generator: %s", recipeOutput(out))
	}
	if restored < 0 {
		t.Fatalf("make generate_dotnet never writes %s back: %s",
			dotnetIconPath, recipeOutput(out))
	}
	if restored < generated {
		t.Errorf("make generate_dotnet restores the icon before the generator overwrites it: %s",
			recipeOutput(out))
	}
}

func head(body []byte) []byte {
	if len(body) > 16 {
		return body[:16]
	}
	return body
}

// firstSubmatch reads one value out of an SDK manifest. A manifest that stopped carrying the
// field would otherwise compare equal to an empty string.
func firstSubmatch(t *testing.T, rel string, pattern *regexp.Regexp) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	found := pattern.FindStringSubmatch(string(raw))
	if found == nil {
		t.Fatalf("%s carries nothing matching %s", rel, pattern)
	}
	return found[1]
}

// TestEachSDKManifestCarriesItsPublishedName reads the four manifests a package manager reads.
// These are the names a user installs, and no other file in the SDK states them.
func TestEachSDKManifestCarriesItsPublishedName(t *testing.T) {
	npm := firstSubmatch(t, "sdk/nodejs/package.json", regexp.MustCompile(`"name":\s*"([^"]+)"`))
	if npm != npmPackageName {
		t.Errorf("the npm manifest names the package %q and this provider publishes %q", npm, npmPackageName)
	}

	pypi := firstSubmatch(t, "sdk/python/pyproject.toml", regexp.MustCompile(`(?m)^\s*name\s*=\s*"([^"]+)"`))
	if normalized := strings.ToLower(pypiSeparators.ReplaceAllString(pypi, "-")); normalized != pypiPackageName {
		t.Errorf("the Python manifest names the distribution %q, which normalizes to %q, and this provider publishes %q",
			pypi, normalized, pypiPackageName)
	}

	projects, err := filepath.Glob(filepath.Join(repoRoot(t), "sdk", "dotnet", "*.csproj"))
	if err != nil {
		t.Fatalf("glob the .NET project files: %v", err)
	}
	var ids []string
	for _, project := range projects {
		ids = append(ids, strings.TrimSuffix(filepath.Base(project), ".csproj"))
	}
	schemaAssertSameSet(t, "NuGet package ids", ids, []string{nugetPackageID})

	namespace := firstSubmatch(t, "sdk/dotnet/Provider.cs", regexp.MustCompile(`(?m)^namespace\s+(\S+)`))
	if namespace != nugetPackageID {
		t.Errorf("the .NET sources declare namespace %s and the package id is %s", namespace, nugetPackageID)
	}

	module := firstSubmatch(t, "sdk/go/filescom/go.mod", regexp.MustCompile(`(?m)^module\s+(\S+)`))
	if module != goImportBasePath {
		t.Errorf("the Go SDK module is %s and the schema tells programs to import %s", module, goImportBasePath)
	}
}
