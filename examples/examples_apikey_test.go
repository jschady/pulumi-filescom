// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build yaml || all
// +build yaml all

package examples

import (
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulumi/providertest/pulumitest"
	"github.com/pulumi/providertest/pulumitest/assertpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource/sig"
)

//nolint:gosec // The value is the Pulumi type token of the resource, not a credential.
const apiKeyToken = "filescom:index/apiKey:ApiKey"

// The two permission sets the Files.com console offers. The restricted set is created first,
// so a full-access key exists only across the last two assertions.
const (
	createdPermissionSet     = "files_only"
	replacementPermissionSet = "full"
)

// TestApiKeyLifecycle walks create, the second preview, a name update, a permissionSet
// replace, and destroy. Only the key's secret signature is asserted, never the value itself.
func TestApiKeyLifecycle(t *testing.T) {
	requireFilesAPIKey(t)
	recorderFor(t)

	createdName := testObjectName(t, "apikey")
	renamedName := createdName + "-renamed"

	// Every id this run creates, so cleanup deletes the credential even when an assertion
	// fails before the destroy. The name sweep behind it covers a create we never saw.
	var createdIDs []int64
	t.Cleanup(func() {
		for _, id := range createdIDs {
			deleteAPIKey(t, id)
		}
		deleteTestAPIKeys(t)
	})

	// The key takes a path under the throwaway folder. Files.com documents the
	// path as a restriction for office_integration keys, so this asserts the bridge, not the server.
	folder := throwawayFolder(t)

	pt := pulumitest.NewPulumiTest(t, filepath.Join("lifecycle", "apikey", "base"),
		attachProvider(t),
		credentialSafeStack(),
	)
	pt.SetConfig(t, "apiKeyName", createdName)
	pt.SetConfig(t, "apiKeyPath", folder)
	pt.SetConfig(t, "apiKeyPermissionSet", createdPermissionSet)

	// 1. Create with a name and a path under the throwaway folder succeeds.
	up := pt.Up(t)
	require.Equal(t, []string{"apiKeyId"}, outputNames(up.Outputs),
		"the program should export the id and nothing else")
	apiKeyID, ok := up.Outputs["apiKeyId"].Value.(string)
	require.Truef(t, ok, "the apiKeyId stack output should be a string, got %T", up.Outputs["apiKeyId"].Value)
	require.NotEmpty(t, apiKeyID, "the created API key should carry an id")

	created := requireAPIKeyNamed(t, createdName)
	createdIDs = append(createdIDs, created.ID)
	require.Equal(t, strconv.FormatInt(created.ID, 10), apiKeyID,
		"the Pulumi id should be the id the account gives the API key")
	require.Equal(t, createdPermissionSet, created.PermissionSet,
		"the key on the account should carry the configured permission set")
	require.Equal(t, folder, created.Path,
		"the write-only path should reach the account as the throwaway folder")

	// 2. The output key is marked secret in the state. The signature is the assertion; the
	// value is never read. The write-only path is secret too, which is bridge #3201.
	stored := requireAPIKeyResource(t, pt)
	requireSecretSignature(t, "key", stored.Outputs["key"])
	requireSecretSignature(t, "path", stored.Inputs["path"])

	// 3. The immediate second preview reports no changes. A perpetual diff here is bridge
	// #3201: the write-only path is in the state inputs and the refresh nullifies it.
	requireNoChanges(t, "second", pt.Preview(t, optpreview.Diff()))

	// 4. Changing the name is an in-place update.
	pt.SetConfig(t, "apiKeyName", renamedName)
	renamePreview := pt.Preview(t, optpreview.Diff())
	logPlan(t, "changed name", renamePreview.StdOut)
	assertpreview.HasNoReplacements(t, renamePreview)
	require.Equalf(t, 1, renamePreview.ChangeSummary[apitype.OpUpdate],
		"renaming should plan exactly one update, got %v", renamePreview.ChangeSummary)
	pt.Up(t)
	renamed := requireAPIKeyNamed(t, renamedName)
	require.Equal(t, created.ID, renamed.ID, "the update should keep the key the create made")
	require.Equal(t, createdPermissionSet, renamed.PermissionSet,
		"the update should leave the permission set alone")
	t.Logf("the account reports the write-only path as %q after the in-place update", renamed.Path)
	requireNoChanges(t, "after the rename", pt.Preview(t, optpreview.Diff()))

	// 5. Changing the permission set is a replace. The replace is applied, not previewed
	// alone, so the assertion covers the create the replacement leg performs.
	pt.SetConfig(t, "apiKeyPermissionSet", replacementPermissionSet)
	requireReplacePlan(t, "changed permission set", pt.Preview(t, optpreview.Diff()))
	pt.Up(t)
	replaced := requireAPIKeyNamed(t, renamedName)
	createdIDs = append(createdIDs, replaced.ID)
	require.NotEqual(t, created.ID, replaced.ID, "the replacement should carry a new id")
	require.Equal(t, replacementPermissionSet, replaced.PermissionSet,
		"the replacement on the account should carry the second permission set")
	require.Equal(t, folder, replaced.Path,
		"the replacement create should carry the write-only path through again")
	requireAPIKeyAbsent(t, created.ID)
	require.Len(t, testAPIKeys(t), 1, "the replace should leave one test key on the account")
	requireNoChanges(t, "after the permission set replace", pt.Preview(t, optpreview.Diff()))

	// 6. Destroy succeeds and the key is gone from the account.
	pt.Destroy(t)
	_, found := findResource(t, pt, apiKeyToken)
	require.False(t, found, "the destroyed API key should be gone from the stack state")
	requireAPIKeyAbsent(t, replaced.ID)
	require.Empty(t, testAPIKeys(t), "the account should hold no test API key after the run")
	require.NotZero(t, currentAPIKeyID(),
		"the run should leave the credential it authenticates with on the account")
}

// testAPIKeys returns the keys on the account that these tests created.
func testAPIKeys(t *testing.T) []filesAPIKey {
	t.Helper()
	keys, err := listAPIKeys()
	require.NoError(t, err)
	currentID := currentAPIKeyID()
	var mine []filesAPIKey
	for _, key := range keys {
		if isSweepableAPIKey(key, currentID) {
			mine = append(mine, key)
		}
	}
	return mine
}

// deleteTestAPIKeys removes every API key these tests created. Registered with t.Cleanup
// before the stack exists, it is the net under a failed `pulumi destroy`.
func deleteTestAPIKeys(t *testing.T) {
	t.Helper()
	keys, err := listAPIKeys()
	if err != nil {
		t.Logf("cleanup: listing API keys: %v", err)
		return
	}
	currentID := currentAPIKeyID()
	for _, key := range keys {
		if !isSweepableAPIKey(key, currentID) {
			continue
		}
		deleteAPIKey(t, key.ID)
	}
}

func requireAPIKeyNamed(t *testing.T, name string) filesAPIKey {
	t.Helper()
	keys, err := listAPIKeys()
	require.NoError(t, err)
	var matches []filesAPIKey
	for _, key := range keys {
		if key.Name == name {
			matches = append(matches, key)
		}
	}
	require.Lenf(t, matches, 1, "the account should list one API key named %s", name)
	return matches[0]
}

func requireAPIKeyAbsent(t *testing.T, id int64) {
	t.Helper()
	keys, err := listAPIKeys()
	require.NoError(t, err)
	for _, key := range keys {
		require.NotEqualf(t, id, key.ID, "the API key %d should be gone from the account", id)
	}
}

func requireAPIKeyResource(t *testing.T, pt *pulumitest.PulumiTest) apitype.ResourceV3 {
	t.Helper()
	resource, found := findResource(t, pt, apiKeyToken)
	require.Truef(t, found, "the stack state should hold one %s", apiKeyToken)
	return resource
}

// requireSecretSignature fails unless the state marks the property as a Pulumi secret. It
// reads the signature and the key names only, so the value never reaches a message.
func requireSecretSignature(t *testing.T, property string, value any) {
	t.Helper()
	object, ok := value.(map[string]any)
	require.Truef(t, ok, "the %s property should be a secret object in the state, got %T",
		property, value)
	require.Equalf(t, sig.Secret, object[sig.Key],
		"the %s property should carry the Pulumi secret signature, got the keys %v",
		property, sortedKeys(object))
}

// outputNames lists the stack output names. Only the names are returned: a stack output that
// held the API key would put its value in this process and in `pulumi stack output`.
func outputNames(outputs auto.OutputMap) []string {
	names := make([]string, 0, len(outputs))
	for name := range outputs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
