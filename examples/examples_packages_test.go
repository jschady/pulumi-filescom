// Copyright 2026, Pulumi Corporation.  All rights reserved.
package examples

import (
	"encoding/json"
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
