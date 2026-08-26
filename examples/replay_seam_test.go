// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build yaml || nodejs || python || dotnet || go || all
// +build yaml nodejs python dotnet go all

package examples

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"

	"github.com/pulumi/providertest/pulumitest"
)

// The proof below deploys one program over the in-process provider with a cassette that holds
// nothing. It reaches no account: the recorder answers every request from the file.
const (
	// vendorHost is the host every Files.com call carries. The request log names it once the
	// provider starts the create.
	vendorHost = "app.files.com"

	// seamSeedBody is the seed the object name of this proof draws from. A replay run reads
	// the seed from a file, and this proof writes the file rather than recording one.
	seamSeedBody = "0000000000000000000000000000000000000000000000000000000000000000\n"
)

// emptyCassetteDir writes the cassette and the seed of one test into a new temporary directory
// and returns the directory. Nothing here writes into the committed cassette directory.
func emptyCassetteDir(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	base := filepath.Join(dir, name)
	// A cassette with no interaction. The version comes from the library, so a new format
	// version fails at the recorder rather than here.
	body := fmt.Sprintf("---\nversion: %d\ninteractions: []\n", cassette.CassetteFormatVersion)
	require.NoError(t, os.WriteFile(base+cassetteSuffix, []byte(body), 0o600))
	require.NoError(t, os.WriteFile(base+seedSuffix, []byte(seamSeedBody), 0o600))
	return dir
}

// requestsToHost returns the entries of a request log that name one host.
func requestsToHost(log []string, host string) []string {
	var found []string
	for _, entry := range log {
		_, target, cut := strings.Cut(entry, " ")
		if !cut {
			continue
		}
		parsed, err := url.Parse(target)
		if err == nil && parsed.Host == host {
			found = append(found, entry)
		}
	}
	return found
}

// TestTheReplaySeamFailsTheStackOnAnEmptyCassette is the end-to-end proof. It runs a real
// stack, so it covers the whole chain at once: the language host reaches the provider this
// process started, the provider's HTTP goes through the recorder, and the error surfaces.
// The stack must fail, because no cassette answers the create.
func TestTheReplaySeamFailsTheStackOnAnEmptyCassette(t *testing.T) {
	t.Setenv(testModeVariable, string(modeReplay))
	// The replay job carries no secret, so this proof runs with an empty credential. The
	// harness supplies the placeholder the provider needs, and no request leaves this machine.
	t.Setenv(apiKeyVariable, "")

	name := cassetteName(t)
	replay, err := newReplayRecorder(replayConfig{mode: modeReplay, dir: emptyCassetteDir(t, name)}, name)
	require.NoError(t, err)
	replay.install(t)

	// The group program is the shortest one here. It creates one resource and takes both of
	// its properties from the stack configuration.
	pt := pulumitest.NewPulumiTest(t, filepath.Join("lifecycle", "group", "base"),
		attachProvider(t),
		credentialSafeStack(),
	)
	pt.SetConfig(t, "groupName", testObjectName(t, "group"))
	pt.SetConfig(t, "groupNotes", "The empty cassette answers no request.")

	_, upErr := pt.UpErr(t)
	require.Error(t, upErr, "an empty cassette answers nothing, so the stack should fail")

	log := replay.requestLog()
	require.NotEmpty(t, log, "the provider should reach the vendor through the recorder")
	vendorCalls := requestsToHost(log, vendorHost)
	require.NotEmptyf(t, vendorCalls,
		"the recorder should see a request to %s, and it saw %v", vendorHost, log)
	t.Logf("the recorder answered %s from the empty cassette", vendorCalls[0])
	require.Containsf(t, upErr.Error(), cassette.ErrInteractionNotFound.Error(),
		"the failure should name the missing interaction, and it reads %v", upErr)
}
