// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build knownissue
// +build knownissue

package examples

import (
	"encoding/json"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulumi/providertest/pulumitest"
	"github.com/pulumi/providertest/pulumitest/assertpreview"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
)

// The same userIds in a different order should plan no diff. Held out of `make test_examples`,
// run with `-tags all,knownissue`: the plugin framework never runs semantic equality in a plan.
func TestKnownIssueGroupUserIDsReorderPlansNoDiff(t *testing.T) {
	requireFilesAPIKey(t)

	name := testObjectName(t, "group")
	t.Cleanup(func() { deleteGroupsNamed(t, name) })

	memberA, memberB := twoUserIDs(t)

	pt := pulumitest.NewPulumiTest(t, filepath.Join("lifecycle", "group", "with-members"),
		attachProvider(t),
		credentialSafeStack(),
	)
	pt.SetConfig(t, "groupName", name)
	pt.SetConfig(t, "groupNotes", "The order-insensitive membership check.")
	pt.SetConfig(t, "userIds", memberA+","+memberB)
	pt.Up(t)

	pt.SetConfig(t, "userIds", memberB+","+memberA)
	assertpreview.HasNoChanges(t, pt.Preview(t, optpreview.Diff()))

	pt.Destroy(t)
}

// twoUserIDs picks the members for assertion 5. Disabled accounts come first so the
// brief membership this test creates lands on nobody who is logging in.
func twoUserIDs(t *testing.T) (string, string) {
	t.Helper()
	status, body, err := filesAPI(http.MethodGet, "/users", url.Values{"per_page": []string{"100"}})
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, status, "listing users returned HTTP %d: %s", status, body)

	var users []struct {
		ID       int64 `json:"id"`
		Disabled bool  `json:"disabled"`
	}
	require.NoError(t, json.Unmarshal(body, &users))

	var ids, active []string
	for _, user := range users {
		if user.Disabled {
			ids = append(ids, strconv.FormatInt(user.ID, 10))
		} else {
			active = append(active, strconv.FormatInt(user.ID, 10))
		}
	}
	ids = append(ids, active...)
	if reason := skipReasonForUserCount(len(ids)); reason != "" {
		t.Skip(reason)
	}
	return ids[0], ids[1]
}
