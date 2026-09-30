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

// A behavior created with a JSON-encoded string value should move to the wrapped object. Held
// out of `make test_examples` and run with `-tags all,knownissue`. Today the preview fails with
// "can't unmarshal tftypes.Object[...] into *string": the state holds a string and the program
// sends an object. Cause: pulumi/pulumi-terraform-bridge#3122. The test passes once the bridge
// fixes it.
func TestKnownIssueBehaviorValueChangesFromAJSONStringToAnObject(t *testing.T) {
	// The known issue is what the provider does with the state the account gave it, so this
	// test runs live. No cassette records a failure nobody has fixed yet.
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

	pt.UpdateSource(t, "lifecycle", "behavior", "value-json-string-wrapped")
	logPlan(t, "wrapped value", pt.Preview(t, optpreview.Diff()).StdOut)
	pt.Up(t)
	pt.Destroy(t)
}
