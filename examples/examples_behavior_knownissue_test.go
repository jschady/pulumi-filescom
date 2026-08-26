// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build knownissue
// +build knownissue

package examples

import (
	"path/filepath"
	"testing"

	"github.com/pulumi/providertest/pulumitest"
	"github.com/pulumi/pulumi/sdk/v3/go/auto/optpreview"
)

// The second `value` encoding the description promises. Held out of `make test_examples` and
// run with `-tags all,knownissue`. Cause: pulumi/pulumi-terraform-bridge#3122, open.
func TestKnownIssueBehaviorValueAsAJSONEncodedString(t *testing.T) {
	// The known issue is what the account stores, so this test reads the account. No cassette
	// records a failure nobody has fixed yet.
	requireLiveProvider(t)
	requireFilesAPIKey(t)
	recorderFor(t)

	folder := throwawayFolder(t)
	t.Cleanup(func() { deleteBehaviorsOn(t, folder) })

	pt := pulumitest.NewPulumiTest(t, filepath.Join("lifecycle", "behavior", "value-json-string"),
		attachProvider(t),
		credentialSafeStack(),
	)
	pt.SetConfig(t, "behaviorName", testObjectName(t, "behavior"))
	pt.SetConfig(t, "behaviorPath", folder)

	pt.Up(t)
	t.Logf("the account stored %v", requireBehaviorOnPath(t, folder).Value)
	requireNoChanges(t, "second", pt.Preview(t, optpreview.Diff()))
	pt.Destroy(t)
}
