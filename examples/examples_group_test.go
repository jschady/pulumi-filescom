// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build yaml || all
// +build yaml all

package examples

import (
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulumi/providertest/pulumitest"
	"github.com/pulumi/providertest/pulumitest/assertpreview"
	"github.com/pulumi/providertest/pulumitest/changesummary"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

//nolint:gosec // The value is the Pulumi type token of the resource, not a credential.
const groupToken = "filescom:index/group:Group"

// The account holds no workspaces, so no live id can move here. The assertion is about
// the replace decision, which the bridge makes during preview without calling the API.
const absentWorkspaceID = "999999999"

var decimalID = regexp.MustCompile(`^[0-9]+$`)

// TestGroupLifecycle walks create, the second preview, a name update in place, a workspaceId
// replace, and destroy. The userIds reorder stays in TestKnownIssueGroupUserIDsReorderPlansNoDiff.
func TestGroupLifecycle(t *testing.T) {
	requireFilesAPIKey(t)

	createdName := testObjectName(t, "group")
	renamedName := createdName + "-renamed"

	// Registered before the stack exists so that it runs after the stack teardown.
	t.Cleanup(func() { deleteGroupsNamed(t, createdName) })

	// Each lifecycle test creates its own throwaway folder. A group carries no path, so
	// this call is what proves the helper the later lifecycle tests build on.
	throwawayFolder(t)

	pt := pulumitest.NewPulumiTest(t, filepath.Join("lifecycle", "group", "base"),
		attachProvider(t),
		credentialSafeStack(),
	)
	pt.SetConfig(t, "groupName", createdName)
	pt.SetConfig(t, "groupNotes", "Lifecycle coverage for the Files.com bridge.")

	// 1. Create succeeds and the output id is non-empty.
	up := pt.Up(t)
	groupID, ok := up.Outputs["groupId"].Value.(string)
	require.True(t, ok, "the groupId stack output should be a string, got %T", up.Outputs["groupId"].Value)
	require.NotEmpty(t, groupID, "the created group should carry an id")
	require.Regexpf(t, decimalID, groupID,
		"the bridge should map the upstream integer id to a decimal string, got %q", groupID)
	requireGroupNamed(t, groupID, createdName)

	// 2. The immediate second preview reports no changes.
	assertpreview.HasNoChanges(t, pt.Preview(t))

	// 3. Changing name is an in-place update and the refreshed name matches.
	pt.SetConfig(t, "groupName", renamedName)
	renamePreview := pt.Preview(t)
	assertpreview.HasNoReplacements(t, renamePreview)
	require.Equalf(t, 1, renamePreview.ChangeSummary[apitype.OpUpdate],
		"renaming should plan exactly one update, got %v", renamePreview.ChangeSummary)
	pt.Up(t)
	pt.Refresh(t)
	refreshed := requireGroupResource(t, pt)
	require.Equal(t, renamedName, refreshed.Outputs["name"], "the refreshed name should match the new name")

	// 4. Changing workspaceId is a replace.
	pt.UpdateSource(t, "lifecycle", "group", "with-workspace")
	pt.SetConfig(t, "workspaceId", absentWorkspaceID)
	workspacePreview := pt.Preview(t)
	workspaceSummary := changesummary.ChangeSummary(workspacePreview.ChangeSummary)
	replacements := workspaceSummary.WhereOpEquals(
		apitype.OpReplace, apitype.OpCreateReplacement, apitype.OpDeleteReplaced)
	require.NotEmptyf(t, *replacements,
		"changing workspaceId should plan a replace, got %v", workspacePreview.ChangeSummary)

	// 5. Reordering userIds is not a diff. Preserved whole, and held out of this suite, in
	// TestKnownIssueGroupUserIDsReorderPlansNoDiff: the plan path skips semantic equality.

	// 6. Destroy succeeds and the re-preview reports the resource absent.
	pt.Destroy(t)
	_, found := findResource(t, pt, groupToken)
	require.False(t, found, "the destroyed group should be gone from the stack state")
	rebuild := pt.Preview(t)
	rebuildSummary := changesummary.ChangeSummary(rebuild.ChangeSummary)
	require.NotZerof(t, rebuild.ChangeSummary[apitype.OpCreate],
		"the preview after destroy should plan to create the group again, got %v", rebuild.ChangeSummary)
	require.Emptyf(t, *rebuildSummary.WhereOpNotEquals(apitype.OpCreate),
		"the preview after destroy should plan creates only, got %v", rebuild.ChangeSummary)
	requireGroupAbsent(t, groupID)

	leftovers, err := listGroups()
	require.NoError(t, err)
	t.Logf("groups on the account after the run: %d", len(leftovers))
	require.Empty(t, leftovers, "the account should hold zero groups after the run")
}

func requireGroupResource(t *testing.T, pt *pulumitest.PulumiTest) apitype.ResourceV3 {
	t.Helper()
	resource, found := findResource(t, pt, groupToken)
	require.Truef(t, found, "the stack state should hold one %s", groupToken)
	return resource
}

func requireGroupAbsent(t *testing.T, id string) {
	t.Helper()
	groups, err := listGroups()
	require.NoError(t, err)
	for _, group := range groups {
		require.NotEqualf(t, id, strconv.FormatInt(group.ID, 10), "group %s should be gone", id)
	}
}
