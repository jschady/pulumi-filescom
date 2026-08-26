// Copyright 2026, Pulumi Corporation.  All rights reserved.
package examples

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestTheHarnessInstallsThePackagesTheSDKsPublish reads each identity out of the committed SDK
// and fails when the dependency the example harness installs no longer matches it.
func TestTheHarnessInstallsThePackagesTheSDKsPublish(t *testing.T) {
	t.Run("nodejs", func(t *testing.T) {
		var manifest struct {
			Name string `json:"name"`
		}
		raw, err := os.ReadFile(filepath.Join("..", "sdk", "nodejs", "package.json"))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &manifest))
		require.Equal(t, []string{manifest.Name}, getJSBaseOptions(t).Dependencies,
			"the JavaScript leg should install the package sdk/nodejs/package.json names")
	})

	t.Run("python", func(t *testing.T) {
		dependencies := getPythonBaseOptions(t).Dependencies
		require.Len(t, dependencies, 1, "the Python leg should install one package")
		require.FileExists(t, filepath.Join(filepath.Dir(dependencies[0]), "pyproject.toml"),
			"the Python leg should install the package build under sdk/python")
	})

	t.Run("go", func(t *testing.T) {
		dependencies := getGoBaseOptions(t).Dependencies
		require.Len(t, dependencies, 1, "the Go leg should replace one module")
		module, _, found := strings.Cut(dependencies[0], "=")
		require.Truef(t, found, "the Go dependency %q should read as module=path", dependencies[0])
		require.Equal(t, goSDKModulePath(t), module,
			"the Go leg should replace the module sdk/go/filescom/go.mod declares")
	})

	t.Run("dotnet", func(t *testing.T) {
		dependencies := getCSBaseOptions(t).Dependencies
		require.Len(t, dependencies, 1, "the .NET leg should install one package")
		require.FileExists(t, filepath.Join("..", "sdk", "dotnet", dependencies[0]+".csproj"),
			"the .NET leg should install the package sdk/dotnet builds")
	})
}

// The scan below reads Go source, and this file holds the pattern it searches for.
const packagesSourceName = "examples_packages_test.go"

// The upgrade harness sanitizes a gRPC log it never opened when the log is off, and panics on the
// nil (providertest v0.7.0 previewProviderUpgrade.go:34), so its one stack keeps the log on.
const (
	upgradeSourceName   = "examples_upgrade_test.go"
	upgradeStacksExempt = 1
)

// TestEveryStackTakesTheCredentialSafeOption reads this package's own source. A stack built
// without the option writes FILES_API_KEY and every created secret into a retained debug log.
func TestEveryStackTakesTheCredentialSafeOption(t *testing.T) {
	sources, err := filepath.Glob("*_test.go")
	require.NoError(t, err)

	checked, exempt := 0, 0
	for _, source := range sources {
		if filepath.Base(source) == packagesSourceName {
			continue
		}
		raw, err := os.ReadFile(source)
		require.NoError(t, err)
		for _, call := range stackCalls(string(raw)) {
			// The exemption is one stack, not one file: a second stack here goes red below.
			if filepath.Base(source) == upgradeSourceName {
				exempt++
				continue
			}
			checked++
			require.Containsf(t, call, "credentialSafeStack()",
				"a stack in %s is built without the credential-safe option", source)
		}
	}
	require.NotZero(t, checked, "this package should build at least one stack")
	require.Equalf(t, upgradeStacksExempt, exempt,
		"%s should build %d stack the harness forbids the option on", upgradeSourceName, upgradeStacksExempt)
}

// stackCalls returns the argument text of every pulumitest.NewPulumiTest call in source.
func stackCalls(source string) []string {
	const opening = "pulumitest.NewPulumiTest("

	var calls []string
	for index := strings.Index(source, opening); index >= 0; index = strings.Index(source, opening) {
		source = source[index+len(opening):]
		depth, end := 1, len(source)
		for position, character := range source {
			if character == '(' {
				depth++
			}
			if character == ')' {
				depth--
			}
			if depth == 0 {
				end = position
				break
			}
		}
		calls = append(calls, source[:end])
		source = source[end:]
	}
	return calls
}

// goSDKModulePath reads the module path the committed Go SDK declares. The path doubles as the
// import path every Go example uses, so a rename has to reach both the harness and the programs.
func goSDKModulePath(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "sdk", "go", "filescom", "go.mod"))
	require.NoError(t, err)
	for _, line := range strings.Split(string(raw), "\n") {
		if path, found := strings.CutPrefix(strings.TrimSpace(line), "module "); found {
			return strings.TrimSpace(path)
		}
	}
	t.Fatal("sdk/go/filescom/go.mod declares no module path")
	return ""
}

// The tests below run against the built provider binary. A replay run starts the provider in
// this process instead, so each one takes the live guard and skips.
const (
	knownIssuePattern = "*_knownissue_test.go"
	upgradeTestName   = "TestUpgradeFromTheReleasedProvider"
	liveGuardCall     = "requireLiveProvider(t)"
)

// knownIssueTests is how many tests the known-issue files declare between them. The count keeps
// a new test from joining one of those files without the guard.
const knownIssueTests = 2

// TestEveryLiveOnlyTestTakesTheLiveGuard reads this package's own source. A test that lost the
// guard fails a replay run over a binary it cannot start, and the failure names no fix.
func TestEveryLiveOnlyTestTakesTheLiveGuard(t *testing.T) {
	sources, err := filepath.Glob(knownIssuePattern)
	require.NoError(t, err)
	require.NotEmpty(t, sources, "this package should hold at least one known-issue file")

	checked := 0
	for _, source := range sources {
		raw, err := os.ReadFile(source)
		require.NoErrorf(t, err, "reading %s", source)
		for name, body := range testBodies(string(raw)) {
			checked++
			require.Containsf(t, body, liveGuardCall,
				"the test %s in %s runs with no live guard", name, source)
		}
	}
	require.Equal(t, knownIssueTests, checked,
		"the known-issue files should declare this many tests")

	raw, err := os.ReadFile(upgradeSourceName)
	require.NoError(t, err)
	body, found := testBodies(string(raw))[upgradeTestName]
	require.Truef(t, found, "%s should declare %s", upgradeSourceName, upgradeTestName)
	require.Containsf(t, body, liveGuardCall, "%s runs with no live guard", upgradeTestName)
}

// testBodies returns the name and the source text of every test one file declares. The text of
// a test runs to the next declaration, which covers the calls the test opens with.
func testBodies(source string) map[string]string {
	const opening = "\nfunc "

	bodies := map[string]string{}
	for _, block := range strings.Split(source, opening) {
		name, _, found := strings.Cut(block, "(")
		if !found || !strings.HasPrefix(name, "Test") {
			continue
		}
		bodies[name] = block
	}
	return bodies
}

// The capture seam replaces two transports the whole process shares, so two tests in flight at
// once would write into one recorder. The call below is spelled in two pieces, so this file
// does not hold the text the scan searches for.
var parallelCall = "t.Parallel" + "("

// parallelCalls returns one finding for every parallel call the test sources under dir make.
// The scan takes a directory, so the proof below can read a copy of these sources.
func parallelCalls(dir string) ([]string, error) {
	sources, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}

	var findings []string
	for _, source := range sources {
		raw, err := os.ReadFile(source)
		if err != nil {
			return nil, err
		}
		for index, line := range strings.Split(string(raw), "\n") {
			if strings.Contains(line, parallelCall) {
				findings = append(findings,
					fmt.Sprintf("%s line %d starts a parallel test", source, index+1))
			}
		}
	}
	return findings, nil
}

// TestNoTestInThisPackageRunsInParallel reads this package's own source. One process holds one
// lib.DefaultClient and one http.DefaultTransport, and the recorder of a test owns both.
func TestNoTestInThisPackageRunsInParallel(t *testing.T) {
	findings, err := parallelCalls(".")
	require.NoError(t, err)
	require.Emptyf(t, findings, "a test in this package runs in parallel:\n%s",
		strings.Join(findings, "\n"))
}

// TestTheParallelScanRejectsAPlantedCall copies the sources and plants the call in the copy. A
// scan nobody watched fail reports a serial package whether or not the package is serial.
func TestTheParallelScanRejectsAPlantedCall(t *testing.T) {
	dir := t.TempDir()
	sources, err := filepath.Glob("*_test.go")
	require.NoError(t, err)
	require.NotEmpty(t, sources, "this package should hold test sources to copy")
	for _, source := range sources {
		raw, err := os.ReadFile(source)
		require.NoError(t, err)
		copied := filepath.Join(dir, filepath.Base(source))
		//nolint:gosec // G703: the directory is t.TempDir and the name is the base of a
		// source this package already holds, so the write stays inside the temporary copy.
		require.NoError(t, os.WriteFile(copied, raw, 0o600))
	}

	findings, err := parallelCalls(dir)
	require.NoError(t, err)
	require.Empty(t, findings, "the copied sources should carry no parallel call")

	planted := filepath.Join(dir, packagesSourceName)
	raw, err := os.ReadFile(planted)
	require.NoError(t, err)
	call := "\nfunc TestThePlantedLeg(t *testing.T) {\n\t" + parallelCall + ")\n}\n"
	//nolint:gosec // G703: the path is t.TempDir joined with a constant name.
	require.NoError(t, os.WriteFile(planted, append(raw, []byte(call)...), 0o600))

	findings, err = parallelCalls(dir)
	require.NoError(t, err)
	require.Len(t, findings, 1, "the planted call should be the one finding")
	require.Contains(t, findings[0], packagesSourceName)

	// A directory that holds no test source reports nothing, so the scan fails on a call and
	// never on a path.
	empty, err := parallelCalls(filepath.Join(dir, "absent"))
	require.NoError(t, err)
	require.Empty(t, empty)
}
