// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build yaml || all
// +build yaml all

package examples

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulumi/providertest/pulumitest"
	"github.com/pulumi/providertest/pulumitest/assertpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
)

// The stack itself plus the three resources the combined program creates.
const combinedStackResources = 4

// The outputs the combined program declares. The `key` property holds a live credential, so
// no output carries it.
var combinedOutputNames = []string{"apiKeyDescription", "apiKeyId", "behaviorId", "groupId"}

// TestCombinedStackLifecycle creates a group, a webhook behavior, and an API key in one stack.
// The group id reaches the key description, and the behavior path reaches the key path.
func TestCombinedStackLifecycle(t *testing.T) {
	requireFilesAPIKey(t)
	recorderFor(t)

	baseName := testObjectName(t, "combined")
	groupName := baseName + "-group"
	behaviorName := baseName + "-behavior"
	apiKeyName := baseName + "-key"

	// The folder comes first, so its delete runs after every cleanup that needs it.
	folder := throwawayFolder(t)

	// Every id this run creates, so cleanup deletes the credential even when an assertion
	// fails before the destroy. The name sweeps behind it cover a create we never saw.
	var createdKeyIDs []int64
	t.Cleanup(func() { deleteGroupsNamed(t, baseName) })
	t.Cleanup(func() {
		for _, id := range createdKeyIDs {
			deleteAPIKey(t, id)
		}
		deleteTestAPIKeys(t)
	})
	t.Cleanup(func() { deleteBehaviorsOn(t, folder) })

	pt := pulumitest.NewPulumiTest(t, filepath.Join("lifecycle", "combined", "base"),
		attachProvider(t),
		credentialSafeStack(),
	)
	pt.SetConfig(t, "folderPath", folder)
	pt.SetConfig(t, "groupName", groupName)
	pt.SetConfig(t, "behaviorName", behaviorName)
	pt.SetConfig(t, "apiKeyName", apiKeyName)

	// 1. The first preview plans three creates and reports the key description as unknown,
	// which is the proof that the description reads the group id and not the stack config.
	first := pt.Preview(t, optpreview.Diff())
	logPlan(t, "first", first.StdOut)
	require.Equalf(t, combinedStackResources, first.ChangeSummary[apitype.OpCreate],
		"the first preview should plan the stack and the three resources, got %v", first.ChangeSummary)
	require.Regexpf(t, `\n\s+description\s*: \[unknown\]\n`, first.StdOut,
		"the API key description should be unknown before the group exists")

	// 2. One up creates all three resources and every id is non-empty.
	up := pt.Up(t)
	require.Equal(t, combinedOutputNames, outputNames(up.Outputs),
		"the program should export these outputs and nothing else")
	groupID := requireStringOutput(t, up, "groupId")
	behaviorID := requireStringOutput(t, up, "behaviorId")
	apiKeyID := requireStringOutput(t, up, "apiKeyId")

	requireGroupNamed(t, groupID, groupName)

	behavior := requireBehaviorOnPath(t, folder)
	require.Equal(t, strconv.FormatInt(behavior.ID, 10), behaviorID,
		"the Pulumi id should be the id the account gives the behavior")
	require.Equal(t, behaviorName, behavior.Name,
		"the behavior on the account should carry the configured name")

	key := requireAPIKeyNamed(t, apiKeyName)
	createdKeyIDs = append(createdKeyIDs, key.ID)
	require.Equal(t, strconv.FormatInt(key.ID, 10), apiKeyID,
		"the Pulumi id should be the id the account gives the API key")

	// 3. The group id reaches the API key, in the state and on the account.
	require.Equal(t, groupID, up.Outputs["apiKeyDescription"].Value,
		"the API key description should hold the id the group returned")
	require.Equal(t, groupID, key.Description,
		"the description on the account should hold the id the group returned")

	// 4. The behavior path reaches the API key, and the account took both.
	require.Equal(t, folder, behavior.Path, "the behavior should sit on the throwaway folder")
	require.Equal(t, behavior.Path, key.Path,
		"the write-only key path should reach the account as the behavior path")

	// 5. The state records both references as dependency edges, so the order is not chance.
	groupURN := requireCombinedResource(t, pt, groupToken).URN
	behaviorURN := requireCombinedResource(t, pt, behaviorToken).URN
	stored := requireCombinedResource(t, pt, apiKeyToken)
	require.Contains(t, stored.Dependencies, groupURN, "the API key should depend on the group")
	require.Contains(t, stored.Dependencies, behaviorURN, "the API key should depend on the behavior")
	requirePropertyDependency(t, stored, "description", groupURN)
	requirePropertyDependency(t, stored, "path", behaviorURN)

	// 6. The credential stays secret. The signature is the assertion; the value is never read.
	requireSecretSignature(t, "key", stored.Outputs["key"])
	requireSecretSignature(t, "path", stored.Inputs["path"])

	// 7. The preview after the create is empty.
	requireCombinedNoChanges(t, "second", pt.Preview(t, optpreview.Diff()))

	// 8. Renaming the key is one in-place update, and the stack settles again. The key path is
	// write-only, so an update that left it in the state would show as a diff here.
	renamedKey := apiKeyName + "-renamed"
	pt.SetConfig(t, "apiKeyName", renamedKey)
	renamePreview := pt.Preview(t, optpreview.Diff())
	logPlan(t, "changed key name", renamePreview.StdOut)
	assertpreview.HasNoReplacements(t, renamePreview)
	require.Equalf(t, 1, renamePreview.ChangeSummary[apitype.OpUpdate],
		"renaming the key should plan exactly one update, got %v", renamePreview.ChangeSummary)
	pt.Up(t)

	renamed := requireAPIKeyNamed(t, renamedKey)
	require.Equal(t, key.ID, renamed.ID, "the update should keep the key the create made")
	require.Equal(t, groupID, renamed.Description,
		"the update should leave the group reference on the key")
	require.Equal(t, behavior.Path, renamed.Path,
		"the update should leave the behavior path on the key")
	requireCombinedNoChanges(t, "after the rename", pt.Preview(t, optpreview.Diff()))

	// 9. Destroy takes all three off the state and off the account.
	pt.Destroy(t)
	for _, token := range []string{groupToken, behaviorToken, apiKeyToken} {
		_, found := findResource(t, pt, token)
		require.Falsef(t, found, "the destroyed %s should be gone from the stack state", token)
	}
	requireGroupAbsent(t, groupID)
	requireAPIKeyAbsent(t, key.ID)
	require.Empty(t, sandboxBehaviors(t), "the sandbox should hold no behavior after the destroy")
	require.Empty(t, testAPIKeys(t), "the account should hold no test API key after the run")

	groups, err := listGroups()
	require.NoError(t, err)
	require.Empty(t, groups, "the account should hold zero groups after the run")
	behaviors, err := listBehaviors()
	require.NoError(t, err)
	require.Empty(t, behaviors, "the account should hold zero behaviors after the run")
	require.NotZero(t, currentAPIKeyID(),
		"the run should leave the credential it authenticates with on the account")
}

func requireStringOutput(t *testing.T, up auto.UpResult, name string) string {
	t.Helper()
	value, ok := up.Outputs[name].Value.(string)
	require.Truef(t, ok, "the %s stack output should be a string, got %T", name, up.Outputs[name].Value)
	require.NotEmptyf(t, value, "the %s stack output should not be empty", name)
	return value
}

func requireCombinedResource(t *testing.T, pt *pulumitest.PulumiTest, token string) apitype.ResourceV3 {
	t.Helper()
	stored, found := findResource(t, pt, token)
	require.Truef(t, found, "the stack state should hold one %s", token)
	return stored
}

// requirePropertyDependency reads the edge the engine records per input property. A resource
// dependency alone can come from any property, so the reference needs this one named.
func requirePropertyDependency(t *testing.T, stored apitype.ResourceV3, property string, want resource.URN) {
	t.Helper()
	edges, found := stored.PropertyDependencies[resource.PropertyKey(property)]
	require.Truef(t, found, "the state should record the dependencies of %s, got %v",
		property, stored.PropertyDependencies)
	require.Containsf(t, edges, want, "the %s property should depend on %s", property, want)
}

// requireCombinedNoChanges is assertpreview.HasNoChanges plus the count that keeps it honest:
// an empty change summary satisfies "no changes" without reporting any resource at all.
func requireCombinedNoChanges(t *testing.T, phase string, preview auto.PreviewResult) {
	t.Helper()
	logPlan(t, phase, preview.StdOut)
	require.Equalf(t, combinedStackResources, preview.ChangeSummary[apitype.OpSame],
		"the %s preview should report the stack and the three resources unchanged, got %v",
		phase, preview.ChangeSummary)
	assertpreview.HasNoChanges(t, preview)
}
