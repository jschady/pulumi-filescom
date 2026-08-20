// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build yaml || all
// +build yaml all

package examples

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulumi/providertest"
	"github.com/pulumi/providertest/optproviderupgrade"
	"github.com/pulumi/providertest/pulumitest"
	"github.com/pulumi/providertest/pulumitest/assertpreview"
	"github.com/pulumi/providertest/pulumitest/opttest"
)

// The released version the upgrade runs from. A repository variable carries it, and it exists
// only once a release does, so an unset variable is the normal state before the first release.
const upgradeBaselineVar = "UPGRADE_BASELINE_VERSION"

// The program the upgrade covers. A group carries no secret and no dynamically typed property,
// so a difference the preview reports is the upgrade rather than a known limitation.
var upgradeProgram = filepath.Join("lifecycle", "group", "base")

// TestUpgradeFromTheReleasedProvider deploys with the released provider, then previews the same
// stack with this build. A property this build renamed becomes a change the user already owns.
func TestUpgradeFromTheReleasedProvider(t *testing.T) {
	baseline := os.Getenv(upgradeBaselineVar)
	if baseline == "" {
		t.Skipf("%s names the released provider to upgrade from and is not set", upgradeBaselineVar)
	}
	requireFilesAPIKey(t)

	groupName := testObjectName(t, "group")
	t.Cleanup(func() { deleteGroupsNamed(t, groupName) })

	installBaselinePlugin(t, baseline)

	pt := pulumitest.NewPulumiTest(t, upgradeProgram,
		attachProvider(t),
		opttest.SkipInstall(),
	)
	pt.SetConfig(t, "groupName", groupName)
	pt.SetConfig(t, "groupNotes", "Upgrade coverage for the Files.com bridge.")

	preview := providertest.PreviewProviderUpgrade(t, pt, providerName, baseline,
		optproviderupgrade.CacheDir(upgradeCacheDir(t)))
	assertpreview.HasNoChanges(t, preview)
}

// installBaselinePlugin downloads the released provider. The harness runs a plain `pulumi plugin
// install`, which reads a registry holding no community plugin, so the server is named here first.
func installBaselinePlugin(t *testing.T, baseline string) {
	t.Helper()
	//nolint:gosec // G204: the arguments are a constant, a repository variable, and the
	// server the committed schema advertises. Each reaches the CLI as one argument.
	cmd := exec.Command("pulumi", "plugin", "install", "resource", providerName, baseline,
		"--server", pluginDownloadURL(t))
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "install the %s baseline plugin: %s", baseline, out)
}

// pluginDownloadURL reads the server the committed schema advertises. That is the same server
// every consumer of a released SDK reaches, so the baseline comes from where a user's would.
func pluginDownloadURL(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "provider", "cmd",
		"pulumi-resource-"+providerName, "schema.json"))
	require.NoError(t, err, "read the committed schema")

	var advertised struct {
		PluginDownloadURL string `json:"pluginDownloadURL"`
	}
	require.NoError(t, json.Unmarshal(raw, &advertised), "parse the committed schema")
	require.NotEmpty(t, advertised.PluginDownloadURL,
		"the schema advertises no download server, so no consumer can install the plugin")
	return advertised.PluginDownloadURL
}

// upgradeCacheDir names where the harness records the baseline. It writes an exported stack and a
// gRPC log there, both holding the API key, so the directory is a temporary one, not examples/.
func upgradeCacheDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// TestTheUpgradeBaselineRecordingStaysOutOfTheRepository reads the harness default against the one
// this suite passes. The default sits under examples/ and takes a decrypted stack export.
func TestTheUpgradeBaselineRecordingStaysOutOfTheRepository(t *testing.T) {
	fallback := providertest.GetUpgradeCacheDir(filepath.Base(upgradeProgram), "0.0.0")
	require.False(t, filepath.IsAbs(fallback),
		"the harness default should be a path inside the package, got %s", fallback)

	chosen := upgradeCacheDir(t)
	require.True(t, filepath.IsAbs(chosen), "the recording directory should be absolute")

	cwd, err := os.Getwd()
	require.NoError(t, err)
	require.False(t, strings.HasPrefix(chosen, cwd+string(filepath.Separator)),
		"the recording directory %s sits inside %s", chosen, cwd)
}
