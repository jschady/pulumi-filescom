// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build yaml || all
// +build yaml all

package examples

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulumi/providertest/pulumitest"
	"github.com/pulumi/providertest/pulumitest/assertpreview"
	"github.com/pulumi/providertest/pulumitest/changesummary"
	"github.com/pulumi/pulumi/sdk/v3/go/auto"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optrefresh"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

//nolint:gosec // The value is the Pulumi type token of the resource, not a credential.
const behaviorToken = "filescom:index/behavior:Behavior"

// The webhook target of the probe value. It resolves nowhere and no test uploads a file,
// so the behavior never fires.
const probeWebhookURL = "https://example.com/pulumi-filescom-behavior-probe"

// The second behavior type of assertion 6, and the retention its value carries. The shape
// comes from upstream/docs/resources/behavior.md, the file expiration example.
const (
	replacementBehaviorType = "file_expiration"
	retentionDays           = 30
)

var (
	baseHeaders  = map[string]string{"x-pulumi-filescom-probe": "v1"}
	extraHeaders = map[string]string{"x-pulumi-filescom-probe": "v1", "x-pulumi-filescom-probe-2": "v2"}
)

// TestBehaviorLifecycle walks create, the second preview, a refresh, a header added in place,
// a path replace, a behavior replace, and destroy. It is the probe for the Dynamic `value`.
func TestBehaviorLifecycle(t *testing.T) {
	requireFilesAPIKey(t)
	recorderFor(t)

	name := testObjectName(t, "behavior")
	folderA := throwawayFolder(t)
	folderB := throwawayFolder(t)

	// Registered after the folders and before the stack, so it runs between the stack
	// teardown and the folder deletes.
	t.Cleanup(func() { deleteBehaviorsOn(t, folderA, folderB) })

	pt := pulumitest.NewPulumiTest(t, filepath.Join("lifecycle", "behavior", "base"),
		attachProvider(t),
		credentialSafeStack(),
	)
	pt.SetConfig(t, "behaviorName", name)
	pt.SetConfig(t, "behaviorPath", folderA)
	pt.SetConfig(t, "behaviorType", "webhook")

	// 1. Create with behavior, path, and the four-key Dynamic value.
	up := pt.Up(t)
	behaviorID, ok := up.Outputs["behaviorId"].Value.(string)
	require.Truef(t, ok, "the behaviorId stack output should be a string, got %T", up.Outputs["behaviorId"].Value)
	require.NotEmpty(t, behaviorID, "the created behavior should carry an id")
	t.Logf("the bridge reports the behavior id as %q", behaviorID)

	created := requireBehaviorOnPath(t, folderA)
	// The static bridge gives the upstream integer id as the Pulumi id, where the dynamic
	// bridge gave the string "missing ID". The docs quote this, so it is asserted.
	require.Equal(t, strconv.FormatInt(created.ID, 10), behaviorID,
		"the Pulumi id should be the id the account gives the behavior")
	require.Equal(t, "webhook", created.Behavior, "the behavior on the account should be a webhook")
	require.Equal(t, name, created.Name, "the behavior on the account should carry the configured name")
	require.Equal(t, wantValueJSON(t, baseHeaders), valueJSON(t, created.Value),
		"the value on the account should carry the four keys the program sent")
	require.Equal(t, wantValueJSON(t, baseHeaders), valueJSON(t, up.Outputs["behaviorValue"].Value),
		"the value in the stack outputs should carry the four keys the program sent")

	// 2. The immediate second preview reports no changes. This is the map-versus-object
	// recovery test: a Dynamic value the bridge cannot recover shows up here as a diff.
	requireNoChanges(t, "second", pt.Preview(t, optpreview.Diff()))

	// 3. A refresh reports no changes, and so does the preview after it.
	refresh := pt.Refresh(t, optrefresh.ExpectNoChanges(), optrefresh.Diff())
	logPlan(t, "refresh", refresh.StdOut)
	requireOnlySameResources(t, refresh.Summary.ResourceChanges)
	requireNoChanges(t, "after the refresh", pt.Preview(t, optpreview.Diff()))

	// 4. Adding a header to value is an in-place update the refresh can see.
	pt.UpdateSource(t, "lifecycle", "behavior", "extra-header")
	headerPreview := pt.Preview(t, optpreview.Diff())
	logPlan(t, "added header", headerPreview.StdOut)
	assertpreview.HasNoReplacements(t, headerPreview)
	require.Equalf(t, 1, headerPreview.ChangeSummary[apitype.OpUpdate],
		"adding a header should plan exactly one update, got %v", headerPreview.ChangeSummary)
	pt.Up(t)
	pt.Refresh(t)
	refreshed := requireBehaviorResource(t, pt)
	require.Equal(t, wantValueJSON(t, extraHeaders), valueJSON(t, refreshed.Outputs["value"]),
		"the refreshed value should carry the new header")
	require.Equal(t, wantValueJSON(t, extraHeaders), valueJSON(t, requireBehaviorOnPath(t, folderA).Value),
		"the value on the account should carry the new header")
	requireNoChanges(t, "after the header update", pt.Preview(t, optpreview.Diff()))

	// 5. Changing path is a replace. An in-place update here is the quiet failure to catch:
	// state would claim a path the server never took.
	pt.SetConfig(t, "behaviorPath", folderB)
	requireReplacePlan(t, "changed path", pt.Preview(t, optpreview.Diff()))
	pt.Up(t)
	moved := requireBehaviorOnPath(t, folderB)
	require.Equal(t, wantValueJSON(t, extraHeaders), valueJSON(t, moved.Value),
		"the replacement behavior should carry the same value")
	require.Len(t, sandboxBehaviors(t), 1, "the replace should leave one behavior in the sandbox")

	// 6. Changing behavior is a replace. The first preview keeps the webhook value, so the
	// replace is attributable to the behavior property alone, and restoring the type is clean.
	pt.SetConfig(t, "behaviorType", replacementBehaviorType)
	requireReplacePlan(t, "changed behavior", pt.Preview(t, optpreview.Diff()))
	pt.SetConfig(t, "behaviorType", "webhook")
	requireNoChanges(t, "restored behavior", pt.Preview(t, optpreview.Diff()))

	// Then the replace is applied, with the value the second type takes. A preview alone
	// would not show whether the replacement create reaches the account.
	pt.SetConfig(t, "behaviorType", replacementBehaviorType)
	pt.UpdateSource(t, "lifecycle", "behavior", "file-expiration")
	requireReplacePlan(t, "changed behavior with its own value", pt.Preview(t, optpreview.Diff()))
	pt.Up(t)
	retyped := requireBehaviorOnPath(t, folderB)
	require.Equal(t, replacementBehaviorType, retyped.Behavior,
		"the replacement on the account should carry the second behavior type")
	require.NotEqual(t, moved.ID, retyped.ID, "the replacement should carry a new id")
	require.EqualValues(t, retentionDays, retentionValue(t, retyped.Value),
		"the replacement on the account should carry the retention the program set")
	require.Len(t, sandboxBehaviors(t), 1, "the replace should leave one behavior in the sandbox")
	requireNoChanges(t, "after the behavior replace", pt.Preview(t, optpreview.Diff()))

	// 7. Destroy succeeds and takes the behavior off the account.
	pt.Destroy(t)
	_, found := findResource(t, pt, behaviorToken)
	require.False(t, found, "the destroyed behavior should be gone from the stack state")
	require.Empty(t, sandboxBehaviors(t), "the sandbox should hold no behavior after the destroy")

	leftovers, err := listBehaviors()
	require.NoError(t, err)
	t.Logf("behaviors on the account after the run: %d", len(leftovers))
	require.Empty(t, leftovers, "the account should hold zero behaviors after the run")
}

func requireBehaviorResource(t *testing.T, pt *pulumitest.PulumiTest) apitype.ResourceV3 {
	t.Helper()
	resource, found := findResource(t, pt, behaviorToken)
	require.Truef(t, found, "the stack state should hold one %s", behaviorToken)
	return resource
}

// valueJSON reduces a Dynamic value to urls, method, triggers and headers. Marshalling sorts
// the keys, so two reduced values compare as strings that read back in a failure message.
func valueJSON(t *testing.T, raw any) string {
	t.Helper()
	object, ok := raw.(map[string]any)
	require.Truef(t, ok, "the behavior value should be an object, got %T: %v", raw, raw)

	reduced := map[string]any{}
	for _, key := range []string{"urls", "method", "triggers", "headers"} {
		value, present := object[key]
		require.Truef(t, present, "the behavior value should carry %q, got the keys %v", key, sortedKeys(object))
		reduced[key] = value
	}
	encoded, err := json.Marshal(reduced)
	require.NoError(t, err)
	return string(encoded)
}

// retentionValue reads the one key the file expiration value of assertion 6 is asserted on.
func retentionValue(t *testing.T, raw any) any {
	t.Helper()
	object, ok := raw.(map[string]any)
	require.Truef(t, ok, "the file expiration value should be an object, got %T: %v", raw, raw)
	days, present := object["days_to_retain"]
	require.Truef(t, present, "the file expiration value should carry the retention, got the keys %v",
		sortedKeys(object))
	return days
}

func wantValueJSON(t *testing.T, headers map[string]string) string {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"urls":     []string{probeWebhookURL},
		"method":   "POST",
		"triggers": []string{"create"},
		"headers":  headers,
	})
	require.NoError(t, err)
	return string(encoded)
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// requireReplacePlan fails unless the preview replaces the behavior. A plain update instead
// of a replace is the failure to catch: the server never moves a behavior in place.
func requireReplacePlan(t *testing.T, phase string, preview auto.PreviewResult) {
	t.Helper()
	logPlan(t, phase, preview.StdOut)
	summary := changesummary.ChangeSummary(preview.ChangeSummary)
	replacements := summary.WhereOpEquals(
		apitype.OpReplace, apitype.OpCreateReplacement, apitype.OpDeleteReplaced)
	require.NotEmptyf(t, *replacements, "the change should plan a replace, got %v", preview.ChangeSummary)
	require.Zerof(t, preview.ChangeSummary[apitype.OpUpdate],
		"the change should plan no in-place update, got %v", preview.ChangeSummary)
}

// Pulumi counts the stack itself beside the resources, so a program declaring one resource
// reports two unchanged. The combined program reports four for its three resources.
const singleResourceStackSame = 2

// requireNoChanges is assertpreview.HasNoChanges plus the count that keeps it honest: an empty
// change summary reports no resource at all, and every caller runs a one-resource program.
func requireNoChanges(t *testing.T, phase string, preview auto.PreviewResult) {
	t.Helper()
	logPlan(t, phase, preview.StdOut)
	require.Equalf(t, singleResourceStackSame, preview.ChangeSummary[apitype.OpSame],
		"the %s preview should report the stack and its one resource unchanged, got %v",
		phase, preview.ChangeSummary)
	assertpreview.HasNoChanges(t, preview)
}

func logPlan(t *testing.T, phase string, plan string) {
	t.Helper()
	t.Logf("the %s plan:\n%s", phase, plan)
}

func requireOnlySameResources(t *testing.T, changes *map[string]int) {
	t.Helper()
	require.NotNil(t, changes, "the operation should report its resource changes")
	for operation, count := range *changes {
		if count == 0 {
			continue
		}
		require.Equalf(t, string(apitype.OpSame), operation,
			"the operation should report only unchanged resources, got %v", *changes)
	}
}
