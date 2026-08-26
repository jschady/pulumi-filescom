// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build yaml || nodejs || python || dotnet || go || all
// +build yaml nodejs python dotnet go all

package examples

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Files-com/files-sdk-go/v3/lib"
	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
)

// The fake vendor answers the two paths this proof needs: a read, and a write that returns an
// API key. No test here reaches Files.com, and no key exists in this repository.
const (
	fakeFolderPath = "/api/rest/v1/folders/probe"
	//nolint:gosec // G101: this names a URL path of the fake server, and holds no key.
	fakeAPIKeyPath = "/api/rest/v1/api_keys"

	// placeholderKeyValue stands in for the key a real response carries. The scrub replaces
	// it, so the assertions below can look for it in the file and find nothing.
	placeholderKeyValue = "not-a-real-key"

	// apiKeyRequestBody is the write body. It holds a key value, because the scrub covers the
	// request body of an API key path as well as the response body.
	apiKeyRequestBody = `{"name":"pulumi-test-apikey","key":"` + placeholderKeyValue + `"}`

	// probeCassette is the cassette name every test here records into its own temporary
	// directory. Nothing in this file writes into the committed cassette directory.
	probeCassette = "probe"
)

// fakeVendor starts the stand-in server. The caller closes it, so a replay test can prove the
// calls come from the file and not from the network.
func fakeVendor(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Set-Cookie", "session=not-a-real-session")
		switch request.URL.Path {
		case fakeFolderPath:
			fmt.Fprint(writer, `{"path":"probe"}`)
		case fakeAPIKeyPath:
			fmt.Fprintf(writer, `[{"id":7,"name":"pulumi-test-apikey","key":%q}]`, placeholderKeyValue)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// callVendor issues one request through the client. withKey adds the credential header the
// harness sends, so a test can put the header in front of a cassette that does not hold it.
func callVendor(client *http.Client, method, target, body string, withKey bool) (string, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, target, reader)
	if err != nil {
		return "", err
	}
	if withKey {
		request.Header.Set(headerFilesAPIKey, placeholderKeyValue)
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(response.Body)
	return string(answer), err
}

// recordProbe records the read and the write against the fake server, then stops the recorder.
func recordProbe(t *testing.T, dir, base string, withKey bool) *replayRecorder {
	t.Helper()
	replay, err := newReplayRecorder(replayConfig{mode: modeRecord, dir: dir}, probeCassette)
	require.NoError(t, err)

	_, err = callVendor(replay.client(), http.MethodGet, base+fakeFolderPath, "", withKey)
	require.NoError(t, err, "the recorded read should reach the fake server")
	_, err = callVendor(replay.client(), http.MethodPost, base+fakeAPIKeyPath, apiKeyRequestBody, withKey)
	require.NoError(t, err, "the recorded write should reach the fake server")
	require.NoError(t, replay.Stop())
	return replay
}

// TestTheRecorderWritesACassetteAndASeedWithNoCredential covers the record half of the round
// trip. The file it writes is the file a person commits, so it holds no header and no key.
func TestTheRecorderWritesACassetteAndASeedWithNoCredential(t *testing.T) {
	server := fakeVendor(t)
	dir := t.TempDir()

	replay := recordProbe(t, dir, server.URL, true)

	require.FileExists(t, replay.cassetteFile(), "record mode should write the cassette")
	require.FileExists(t, filepath.Join(dir, probeCassette)+seedSuffix, "record mode should write the seed")

	raw, err := os.ReadFile(replay.cassetteFile())
	require.NoError(t, err)
	written := strings.ToLower(string(raw))

	for _, header := range append(scrubbedRequestHeaders, scrubbedResponseHeaders...) {
		require.NotContainsf(t, written, strings.ToLower(header)+":",
			"the cassette should carry no %s header", header)
	}
	require.NotContains(t, written, placeholderKeyValue, "the cassette should carry no key value")
	require.Contains(t, written, strings.ToLower(redactionMarker),
		"the API key bodies should carry the redaction")

	// The file the save hook wrote is the file a person commits, so the hygiene scan has to
	// read it clean. A scan that reported a recorded cassette would fail every pull request.
	findings, err := scanCassettes(dir)
	require.NoError(t, err)
	require.Emptyf(t, findings, "the recorded cassette should pass the scan:\n%s",
		strings.Join(findings, "\n"))
}

// TestTheReplayServesTheRecordedCallsAfterTheServerStops covers the replay half. The server is
// closed before the replay runs, so a call that reached the network would fail.
func TestTheReplayServesTheRecordedCallsAfterTheServerStops(t *testing.T) {
	server := fakeVendor(t)
	base := server.URL
	dir := t.TempDir()

	recordProbe(t, dir, base, false)
	server.Close()

	replay, err := newReplayRecorder(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
	require.NoError(t, err)

	folder, err := callVendor(replay.client(), http.MethodGet, base+fakeFolderPath, "", false)
	require.NoError(t, err, "the read should come from the cassette")
	require.Contains(t, folder, `"path":"probe"`)

	keys, err := callVendor(replay.client(), http.MethodPost, base+fakeAPIKeyPath, apiKeyRequestBody, false)
	require.NoError(t, err, "the write should come from the cassette")
	require.Contains(t, keys, redactionMarker, "the replayed body should carry the redacted key")

	require.NoError(t, replay.Stop(), "the replay consumed every interaction")
}

// TestARequestCarryingTheKeyHeaderMatchesAScrubbedCassette covers the matcher. The default
// matcher compares every header, so a scrubbed cassette would never match a live request.
func TestARequestCarryingTheKeyHeaderMatchesAScrubbedCassette(t *testing.T) {
	server := fakeVendor(t)
	base := server.URL
	dir := t.TempDir()

	recorded := recordProbe(t, dir, base, true)
	server.Close()

	raw, err := os.ReadFile(recorded.cassetteFile())
	require.NoError(t, err)
	require.NotContains(t, strings.ToLower(string(raw)), strings.ToLower(headerFilesAPIKey)+":",
		"the cassette under test should lack the key header")

	replay, err := newReplayRecorder(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
	require.NoError(t, err)

	folder, err := callVendor(replay.client(), http.MethodGet, base+fakeFolderPath, "", true)
	require.NoError(t, err, "a request that carries the key header should match")
	require.Contains(t, folder, `"path":"probe"`)

	_, err = callVendor(replay.client(), http.MethodPost, base+fakeAPIKeyPath, apiKeyRequestBody, true)
	require.NoError(t, err, "a write that carries the key header should match")
	require.NoError(t, replay.Stop())
}

// TestAnExtraCallInReplayFailsWithTheNotFoundError covers strict replay. A request the cassette
// does not hold returns the go-vcr error, and the caller sees it.
func TestAnExtraCallInReplayFailsWithTheNotFoundError(t *testing.T) {
	server := fakeVendor(t)
	base := server.URL
	dir := t.TempDir()

	recordProbe(t, dir, base, false)
	server.Close()

	replay, err := newReplayRecorder(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
	require.NoError(t, err)
	_, err = callVendor(replay.client(), http.MethodGet, base+fakeFolderPath, "", false)
	require.NoError(t, err)
	_, err = callVendor(replay.client(), http.MethodPost, base+fakeAPIKeyPath, apiKeyRequestBody, false)
	require.NoError(t, err)

	_, err = callVendor(replay.client(), http.MethodGet, base+"/api/rest/v1/folders/unrecorded", "", false)
	require.Error(t, err, "a call the cassette does not hold should fail")
	require.ErrorIs(t, err, cassette.ErrInteractionNotFound)
}

// TestAnUnconsumedCassetteInteractionFailsAtStop covers the stop hook. A cassette that holds more than
// the test used means the two drifted apart, and a silent pass would hide it.
func TestAnUnconsumedCassetteInteractionFailsAtStop(t *testing.T) {
	server := fakeVendor(t)
	base := server.URL
	dir := t.TempDir()

	recordProbe(t, dir, base, false)
	server.Close()

	replay, err := newReplayRecorder(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
	require.NoError(t, err)
	_, err = callVendor(replay.client(), http.MethodGet, base+fakeFolderPath, "", false)
	require.NoError(t, err)

	err = replay.Stop()
	require.Error(t, err, "the unused write should fail the stop")
	require.Contains(t, err.Error(), replay.cassetteFile())
	require.Contains(t, err.Error(), fakeAPIKeyPath)
}

// TestAMissingCassetteOrSeedNamesThePathAndTheTarget covers the loud failure a run gets before
// anyone records. The seed is read first, so both messages are checked in order.
func TestAMissingCassetteOrSeedNamesThePathAndTheTarget(t *testing.T) {
	dir := t.TempDir()

	// The command is spelled out here. Reading it back from the constant would assert nothing.
	const command = "make record_examples"
	require.Equal(t, command, recordTarget, "the messages should name the target that records")

	replay, err := newReplayRecorder(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
	require.Nil(t, replay)
	require.Error(t, err)
	require.Contains(t, err.Error(), filepath.Join(dir, probeCassette)+seedSuffix)
	require.Contains(t, err.Error(), command)

	require.NoError(t, os.WriteFile(filepath.Join(dir, probeCassette)+seedSuffix,
		[]byte(strings.Repeat("ab", 32)+"\n"), 0o600))

	replay, err = newReplayRecorder(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
	require.Nil(t, replay)
	require.Error(t, err)
	require.Contains(t, err.Error(), filepath.Join(dir, probeCassette)+cassetteSuffix)
	require.Contains(t, err.Error(), command)
}

// TestAnUnknownModeNamesTheVariableAndEveryMode covers the mode reader. An unset variable is
// replay, so a run with no configuration and no key replays.
func TestAnUnknownModeNamesTheVariableAndEveryMode(t *testing.T) {
	mode, err := readTestMode("bogus")
	require.Error(t, err)
	require.Empty(t, mode)
	for _, part := range []string{"FILESCOM_TEST_MODE", "replay", "record", "live"} {
		require.Containsf(t, err.Error(), part, "the message should name %s", part)
	}
	require.Equal(t, "FILESCOM_TEST_MODE", testModeVariable, "the workflows read this name")

	accepted := map[string]testMode{
		"":                 modeReplay,
		string(modeReplay): modeReplay,
		string(modeRecord): modeRecord,
		string(modeLive):   modeLive,
	}
	for raw, want := range accepted {
		got, err := readTestMode(raw)
		require.NoErrorf(t, err, "%q is a mode", raw)
		require.Equal(t, want, got)
	}
}

// TestTheModeAndTheCassetteDirectoryComeFromTheEnvironment covers the one switch a run has. An
// unset variable is replay, so a pull request with no key and no configuration replays.
func TestTheModeAndTheCassetteDirectoryComeFromTheEnvironment(t *testing.T) {
	t.Setenv(testModeVariable, string(modeRecord))
	config := configFor(t)
	require.Equal(t, modeRecord, config.mode)
	require.Equal(t, filepath.FromSlash(cassetteDirName), config.dir,
		"a run should read the committed cassettes")
	require.Equal(t, modeRecord, testModeFor(t))

	t.Setenv(testModeVariable, "")
	require.Equal(t, modeReplay, testModeFor(t), "an unset variable means replay")
}

// TestTheRecorderForALiveTestWritesNothingAndKeepsTheTransports covers the live mode of the
// helper every test calls. A live run reaches the vendor, so it holds a seed and nothing else.
func TestTheRecorderForALiveTestWritesNothingAndKeepsTheTransports(t *testing.T) {
	t.Setenv(testModeVariable, string(modeLive))
	beforeLib := lib.DefaultClient.Transport
	beforeDefault := http.DefaultTransport

	replay := recorderFor(t)
	require.Equal(t, modeLive, replay.mode)
	require.Same(t, beforeLib, lib.DefaultClient.Transport, "a live run keeps the SDK client it has")
	require.Same(t, beforeDefault, http.DefaultTransport, "a live run keeps the default transport")
	require.NoFileExists(t, filepath.Join(filepath.FromSlash(cassetteDirName), cassetteName(t))+seedSuffix,
		"a live run writes no seed")
	require.Len(t, nameBytes(t, 4), 4, "a live test still draws its names from a seed")
	require.Empty(t, replay.requestLog(), "the live recorder saw no request")
}

// TestTheHarnessSuppliesTheCredentialOfAReplayRun covers the one credential the harness writes.
// The provider refuses an empty key when it configures itself, and the replay job holds no
// secret, so a replay run that left the variable empty would fail every stack before the
// recorder saw a request.
func TestTheHarnessSuppliesTheCredentialOfAReplayRun(t *testing.T) {
	t.Setenv(apiKeyVariable, "")
	dir := emptyCassetteDir(t, probeCassette)

	t.Run("replay fills the empty variable in", func(t *testing.T) {
		replay, err := newReplayRecorder(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
		require.NoError(t, err)
		replay.install(t)
		require.Equal(t, replayPlaceholder, os.Getenv(apiKeyVariable),
			"a replay run should carry the placeholder the provider accepts")
	})
	require.Empty(t, os.Getenv(apiKeyVariable), "the cleanup should put the variable back")

	t.Run("replay keeps the value the machine carries", func(t *testing.T) {
		t.Setenv(apiKeyVariable, placeholderKeyValue)
		replay, err := newReplayRecorder(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
		require.NoError(t, err)
		replay.install(t)
		require.Equal(t, placeholderKeyValue, os.Getenv(apiKeyVariable),
			"a replay run should keep the value it found")
	})

	// A record run and a live run reach the account, so a placeholder there would send the
	// vendor a credential that no account carries.
	for _, mode := range []testMode{modeRecord, modeLive} {
		t.Run(string(mode)+" writes no placeholder", func(t *testing.T) {
			replay, err := newReplayRecorder(replayConfig{mode: mode, dir: t.TempDir()}, probeCassette)
			require.NoError(t, err)
			replay.install(t)
			require.Emptyf(t, os.Getenv(apiKeyVariable),
				"%s reaches the account, so the harness supplies no credential", mode)
		})
	}
}

// TestTheRecorderInstallsItselfOnBothClientsAndRestoresThem covers the capture seam. The
// provider builds on lib.DefaultClient, and the harness's own calls build on the default
// transport, so one recorder has to sit in front of both.
func TestTheRecorderInstallsItselfOnBothClientsAndRestoresThem(t *testing.T) {
	server := fakeVendor(t)
	dir := t.TempDir()

	beforeLib := lib.DefaultClient.Transport
	beforeDefault := http.DefaultTransport

	var installed *replayRecorder
	t.Run("installed", func(t *testing.T) {
		replay, err := newReplayRecorder(replayConfig{mode: modeRecord, dir: dir}, probeCassette)
		require.NoError(t, err)
		replay.install(t)
		installed = replay

		require.Same(t, replay, lib.DefaultClient.Transport, "the SDK client should reach the recorder")
		require.Same(t, replay, http.DefaultTransport, "the harness calls should reach the recorder")

		_, err = callVendor(http.DefaultClient, http.MethodGet, server.URL+fakeFolderPath, "", true)
		require.NoError(t, err)
		require.Equal(t, []string{http.MethodGet + " " + server.URL + fakeFolderPath}, replay.requestLog(),
			"the recorder should log the method and the URL of every request")
		require.NoFileExists(t, replay.cassetteFile(),
			"the recorder writes the cassette when it stops, and it has not stopped yet")
	})

	require.Same(t, beforeLib, lib.DefaultClient.Transport, "the cleanup should restore the SDK client")
	require.Same(t, beforeDefault, http.DefaultTransport, "the cleanup should restore the default transport")

	// A record run writes the file at the stop, so the file proves the cleanup stopped the
	// recorder. The stop is what fails a replay run that leaves an interaction unused.
	require.FileExists(t, installed.cassetteFile(), "the cleanup should stop the recorder")
}

// TestTheSeedRepeatsTheSameNameBytesInReplay covers determinism. The Nth name of a test has to
// come out the same in replay as it did in record, or no cassette would ever match.
func TestTheSeedRepeatsTheSameNameBytesInReplay(t *testing.T) {
	dir := t.TempDir()

	written, err := loadSeed(replayConfig{mode: modeRecord, dir: dir}, probeCassette)
	require.NoError(t, err)
	read, err := loadSeed(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
	require.NoError(t, err)

	require.Equal(t, written.Bytes(4), read.Bytes(4), "the first name should repeat")
	second := written.Bytes(4)
	require.Equal(t, second, read.Bytes(4), "the second name should repeat")
	require.NotEqual(t, written.Bytes(6), read.Bytes(4), "a longer draw is a different string")

	live, err := loadSeed(replayConfig{mode: modeLive, dir: dir}, "live-probe")
	require.NoError(t, err)
	require.Len(t, live.Bytes(4), 4)
	require.NoFileExists(t, filepath.Join(dir, "live-probe")+seedSuffix, "live mode writes no seed")
}

// TestTheNameBytesComeFromTheRecorderOfTheRunningTest covers the registry a name helper reads.
// Without it a name helper could not find the seed of the test it runs inside.
func TestTheNameBytesComeFromTheRecorderOfTheRunningTest(t *testing.T) {
	dir := t.TempDir()
	replay, err := newReplayRecorder(replayConfig{mode: modeRecord, dir: dir}, probeCassette)
	require.NoError(t, err)
	replay.install(t)

	mirror := &seedSource{seed: replay.seed.seed}
	first := nameBytes(t, 4)
	require.Equal(t, mirror.Bytes(4), first)
	second := nameBytes(t, 4)
	require.Equal(t, mirror.Bytes(4), second)
	require.NotEqual(t, first, second, "the counter should move the bytes on")
}

// TestTheReplayModesAttachTheInProcessProvider covers the server the replay and record runs
// attach to. It needs no key, because a plugin info call reads no vendor.
func TestTheReplayModesAttachTheInProcessProvider(t *testing.T) {
	entry := debugProvidersEnv(t, getCwd(t))
	name, ports, found := strings.Cut(entry, "=")
	require.True(t, found, "the entry should read as name=value")
	require.Equal(t, "PULUMI_DEBUG_PROVIDERS", name)
	require.Truef(t, strings.HasPrefix(ports, providerName+":"),
		"the entry should name the provider and its port, got %q", ports)

	_, port, found := strings.Cut(ports, ":")
	require.True(t, found)
	require.NotEmpty(t, port, "the provider should report the port it listens on")
}

// TestTheCassetteScrubDropsTheHeadersAndTheKeyBodies covers the save hook on its own, so a
// change to the header list or to the redaction shows up here and not only in a whole cassette.
func TestTheCassetteScrubDropsTheHeadersAndTheKeyBodies(t *testing.T) {
	responseBody := fmt.Sprintf(`[{"id":7,"key":%q}]`, placeholderKeyValue)
	interaction := &cassette.Interaction{
		Request: cassette.Request{
			Method:        http.MethodPost,
			URL:           "https://app.files.com" + fakeAPIKeyPath,
			Body:          apiKeyRequestBody,
			ContentLength: int64(len(apiKeyRequestBody)),
			Headers: http.Header{
				headerFilesAPIKey:   {placeholderKeyValue},
				headerAuthorization: {"Bearer " + placeholderKeyValue},
				headerCookie:        {"session=1"},
				"Content-Type":      {"application/json"},
				contentLengthHeader: {strconv.Itoa(len(apiKeyRequestBody))},
			},
		},
		Response: cassette.Response{
			Code:          http.StatusOK,
			Body:          responseBody,
			ContentLength: int64(len(responseBody)),
			Headers: http.Header{
				headerSetCookie:     {"session=1"},
				contentLengthHeader: {strconv.Itoa(len(responseBody))},
			},
		},
	}
	require.Equal(t, []string{"X-Filesapi-Key", "X-Api-Key", "Authorization", "Cookie"},
		scrubbedRequestHeaders, "the save hook drops these request headers")
	require.Equal(t, []string{"Set-Cookie"}, scrubbedResponseHeaders)
	require.Equal(t, "REDACTED", redactionMarker)

	require.NoError(t, scrubInteraction(interaction))

	for _, header := range scrubbedRequestHeaders {
		require.Emptyf(t, interaction.Request.Headers.Get(header), "the %s header should be gone", header)
	}
	require.Empty(t, interaction.Response.Headers.Get(headerSetCookie))
	require.Equal(t, "application/json", interaction.Request.Headers.Get("Content-Type"),
		"the scrub should keep the headers that carry no credential")

	require.ElementsMatch(t, credentialHeaders,
		append(append([]string{}, scrubbedRequestHeaders...), scrubbedResponseHeaders...),
		"the hygiene scan and the save hook should cover the same header names")

	require.NotContains(t, interaction.Request.Body, placeholderKeyValue)
	require.NotContains(t, interaction.Response.Body, placeholderKeyValue)
	require.Contains(t, interaction.Request.Body, redactionMarker)
	require.Contains(t, interaction.Response.Body, redactionMarker)

	// The redaction changes the size of a body, and go-vcr replays the length it reads beside
	// the body. A stale length tells the client to read bytes the cassette does not hold.
	requireLengthMeasuresBody(t, "request", interaction.Request.Body,
		interaction.Request.ContentLength, interaction.Request.Headers)
	requireLengthMeasuresBody(t, "response", interaction.Response.Body,
		interaction.Response.ContentLength, interaction.Response.Headers)

	// The scrub reads no path: a secret member carries a secret wherever the vendor returns it.
	elsewhere := &cassette.Interaction{
		Request:  cassette.Request{Method: http.MethodGet, URL: "https://app.files.com" + fakeFolderPath},
		Response: cassette.Response{Body: `{"folder":{"awsSecretKey":"` + placeholderKeyValue + `"}}`},
	}
	require.NoError(t, scrubInteraction(elsewhere))
	require.NotContains(t, elsewhere.Response.Body, placeholderKeyValue,
		"a secret nested under a folder path should carry the redaction")
	require.Contains(t, elsewhere.Response.Body, redactionMarker)

	// A body the JSON decoder rejects survives, because a redaction that guesses is worse.
	broken := &cassette.Interaction{
		Request:  cassette.Request{Method: http.MethodGet, URL: "https://app.files.com" + fakeFolderPath},
		Response: cassette.Response{Body: `not json {"password":"` + placeholderKeyValue + `"}`},
	}
	require.NoError(t, scrubInteraction(broken))
	require.Contains(t, broken.Response.Body, placeholderKeyValue,
		"a body the decoder rejects should come back unchanged")
}

// requireLengthMeasuresBody fails when the length beside a body does not measure that body.
// The side names the half of the interaction under test, so one message reads on its own.
func requireLengthMeasuresBody(t *testing.T, side, body string, length int64, headers http.Header) {
	t.Helper()
	require.Equalf(t, int64(len(body)), length,
		"the %s content length should measure the body the scrub wrote", side)
	require.Equalf(t, strconv.Itoa(len(body)), headers.Get(contentLengthHeader),
		"the %s %s header should measure the body the scrub wrote", side, contentLengthHeader)
}

// TestTheScrubReplacesEverySecretMemberOnEveryPath covers the whole member list, on the request
// and on the response. One name left out of the walk is one credential a cassette carries.
func TestTheScrubReplacesEverySecretMemberOnEveryPath(t *testing.T) {
	require.NotEmpty(t, secretMemberNames, "the scrub should read a member list")

	// The loop below draws its cases from the list the scrub reads, so a name that leaves the
	// list leaves the loop with it. The names are spelled out here, and a drop goes red.
	//
	//nolint:gosec // G101: each of these names a JSON member, and holds no key.
	required := []string{
		"key", "api_key", "apiKey", "awsSecretKey", "aws_secret_key", "secret",
		"password", "token", "authorization", "basicAuth", "basic_auth",
	}
	require.Subset(t, secretMemberNames, required,
		"the scrub and the scan should cover every member name a body carries a secret under")

	for _, member := range secretMemberNames {
		interaction := &cassette.Interaction{
			Request: cassette.Request{
				Method: http.MethodPost,
				URL:    "https://app.files.com" + fakeFolderPath,
				Body:   fmt.Sprintf(`{"outer":[{%q:%q}]}`, member, placeholderKeyValue),
			},
			Response: cassette.Response{
				Body: fmt.Sprintf(`{%q:%q}`, strings.ToUpper(member), placeholderKeyValue),
			},
		}
		require.NoError(t, scrubInteraction(interaction))
		require.NotContainsf(t, interaction.Request.Body, placeholderKeyValue,
			"the %s member inside an array should carry the redaction", member)
		require.NotContainsf(t, interaction.Response.Body, placeholderKeyValue,
			"the %s member should match whatever case the vendor spells it in", member)
	}

	// A vendor returns a set of keys as a list, and a list can hold a list. The walk carries
	// the name of the member into every element, or the cassette keeps a credential.
	lists := &cassette.Interaction{
		Request: cassette.Request{
			Method: http.MethodPost,
			URL:    "https://app.files.com" + fakeFolderPath,
			Body: fmt.Sprintf(`{"key":[%q,%q],"outer":{"token":[[%q]],"name":"probe"}}`,
				placeholderKeyValue, placeholderKeyValue, placeholderKeyValue),
		},
		Response: cassette.Response{Body: fmt.Sprintf(`[{"api_key":[%q]}]`, placeholderKeyValue)},
	}
	require.NoError(t, scrubInteraction(lists))
	require.NotContains(t, lists.Request.Body, placeholderKeyValue,
		"every element of a list under a secret member should carry the redaction")
	require.Equal(t, 3, strings.Count(lists.Request.Body, redactionMarker),
		"the walk should replace each element it meets, one for one")
	require.NotContains(t, lists.Response.Body, placeholderKeyValue,
		"a list under a secret member of a listed object should carry the redaction too")

	// A member the list does not name keeps its value, so the redaction stays readable. The
	// length of a body the scrub left alone stays the length the recording carries, and a
	// chunked answer carries no length at all.
	kept := &cassette.Interaction{
		Request:  cassette.Request{Method: http.MethodGet, URL: "https://app.files.com" + fakeFolderPath},
		Response: cassette.Response{Body: `{"name":"` + placeholderKeyValue + `"}`, ContentLength: -1},
	}
	require.NoError(t, scrubInteraction(kept))
	require.Contains(t, kept.Response.Body, placeholderKeyValue,
		"the scrub should reach the members the list names and no other")
	require.EqualValues(t, -1, kept.Response.ContentLength,
		"a body the scrub left alone should keep the length the recording carries")
}

// TestTheCassetteMatcherReadsTheMethodThePathTheQueryAndTheBody covers each half of the matcher.
func TestTheCassetteMatcherReadsTheMethodThePathTheQueryAndTheBody(t *testing.T) {
	// The save hook redacted the recorded body, so the live body meets the redacted form.
	savedBody, _ := redactBody(apiKeyRequestBody)
	require.Contains(t, savedBody, redactionMarker)
	recorded := cassette.Request{
		Method: http.MethodPost,
		URL:    "https://app.files.com/api/rest/v1/api_keys?per_page=1000",
		Body:   savedBody,
	}

	match := func(method, target, body string) bool {
		var reader io.Reader
		if body != "" {
			reader = strings.NewReader(body)
		}
		request, err := http.NewRequestWithContext(context.Background(), method, target, reader)
		require.NoError(t, err)
		return matchRequest(request, recorded)
	}

	require.True(t, match(http.MethodPost, recorded.URL, apiKeyRequestBody), "the same call should match")
	require.True(t, match(http.MethodPost, "https://other.example/api/rest/v1/api_keys?per_page=1000",
		apiKeyRequestBody), "the host is not part of the match")
	require.False(t, match(http.MethodPut, recorded.URL, apiKeyRequestBody), "another method is another call")
	require.False(t, match(http.MethodPost, "https://app.files.com/api/rest/v1/groups?per_page=1000",
		apiKeyRequestBody), "another path is another call")
	require.False(t, match(http.MethodPost, "https://app.files.com/api/rest/v1/api_keys?per_page=1",
		apiKeyRequestBody), "another query is another call")
	require.False(t, match(http.MethodPost, recorded.URL, `{"name":"other"}`), "another body is another write")
	require.True(t, match(http.MethodPost, recorded.URL,
		`{"key":"another-placeholder","name":"pulumi-test-apikey"}`),
		"the key value is redacted on both sides, so it cannot decide the match")

	// A read carries no body, so the two assertions below rest on the path alone.
	read := cassette.Request{Method: http.MethodGet, URL: "https://app.files.com" + fakeFolderPath}
	matchRead := func(target string) bool {
		request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
		require.NoError(t, err)
		return matchRequest(request, read)
	}
	require.True(t, matchRead("https://app.files.com"+fakeFolderPath),
		"a read matches on the method, the path and the query")
	require.False(t, matchRead("https://app.files.com/api/rest/v1/folders/other"),
		"another path is another read")

	// The redaction reads no path either, so a write to any endpoint meets a redacted file.
	folder := cassette.Request{
		Method: http.MethodPost,
		URL:    "https://app.files.com" + fakeFolderPath,
		Body:   `{"awsSecretKey":"` + redactionMarker + `","name":"probe"}`,
	}
	write, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"https://app.files.com"+fakeFolderPath,
		strings.NewReader(`{"name":"probe","awsSecretKey":"`+placeholderKeyValue+`"}`))
	require.NoError(t, err)
	require.True(t, matchRequest(write, folder),
		"the secret is redacted on both sides, so it cannot decide the match")
}

// TestTheStopHookPassesInRecordMode covers the one mode where an unplayed interaction is
// normal. Every interaction a record run holds is new, so the hook has to stay quiet.
func TestTheStopHookPassesInRecordMode(t *testing.T) {
	unplayed := &cassette.Interaction{Request: cassette.Request{Method: http.MethodGet, URL: fakeFolderPath}}

	recording := &replayRecorder{mode: modeRecord, name: filepath.Join(t.TempDir(), probeCassette)}
	require.NoError(t, recording.requireReplayed(unplayed), "record mode records, so nothing is replayed")

	replaying := &replayRecorder{mode: modeReplay, name: filepath.Join(t.TempDir(), probeCassette)}
	require.Error(t, replaying.requireReplayed(unplayed), "replay mode should refuse an unused interaction")

	live := &replayRecorder{mode: modeLive, name: filepath.Join(t.TempDir(), probeCassette)}
	require.NoError(t, live.requireReplayed(unplayed), "a live run reads no cassette")
}

// TestTheCassetteNameOfASubtestStaysOnePathSegment covers the layout rule.
func TestTheCassetteNameOfASubtestStaysOnePathSegment(t *testing.T) {
	require.Equal(t, "TestTheCassetteNameOfASubtestStaysOnePathSegment", cassetteName(t))
	t.Run("child", func(t *testing.T) {
		require.Equal(t, "TestTheCassetteNameOfASubtestStaysOnePathSegment_child", cassetteName(t))
		require.NotContains(t, cassetteName(t), "/")
	})
}

// TestTheReplayNotFoundErrorCarriesTheTextTheProofLooksFor pins the text a failing stack shows.
// The end-to-end proof reads the output of `pulumi up` and looks for it.
func TestTheReplayNotFoundErrorCarriesTheTextTheProofLooksFor(t *testing.T) {
	require.Equal(t, "requested interaction not found", cassette.ErrInteractionNotFound.Error())
	require.ErrorIs(t, fmt.Errorf("the provider failed: %w", cassette.ErrInteractionNotFound),
		cassette.ErrInteractionNotFound)
}

// recordOneDelete writes a cassette that holds one delete and nothing else. It is the shape a
// per-test cleanup leaves behind: the last call of the test, made after every assertion.
func recordOneDelete(t *testing.T, dir, base string) {
	t.Helper()
	recorded, err := newReplayRecorder(replayConfig{mode: modeRecord, dir: dir}, probeCassette)
	require.NoError(t, err)
	_, err = callVendor(recorded.client(), http.MethodDelete, base+fakeFolderPath, "", true)
	require.NoError(t, err, "the recorded delete should reach the fake server")
	require.NoError(t, recorded.Stop())
}

// TestAPerTestCleanupReachesTheCassetteInReplay covers the cleanups a lifecycle test registers
// after recorderFor. Go runs them before the recorder stops, so the delete meets the cassette
// and the strict stop check passes.
func TestAPerTestCleanupReachesTheCassetteInReplay(t *testing.T) {
	server := fakeVendor(t)
	base := server.URL
	dir := t.TempDir()

	recordOneDelete(t, dir, base)
	server.Close()

	var installed *replayRecorder
	ran := t.Run("the cleanup runs", func(t *testing.T) {
		replay, err := newReplayRecorder(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
		require.NoError(t, err)
		replay.install(t)
		installed = replay

		// This is the shape of deleteGroupsNamed and deleteBehaviorsOn: registered after the
		// recorder, so it runs first and the recorder still holds the cassette open.
		t.Cleanup(func() {
			_, err := callVendor(http.DefaultClient, http.MethodDelete, base+fakeFolderPath, "", true)
			require.NoError(t, err, "the cleanup should read the delete out of the cassette")
		})
	})

	require.True(t, ran, "the cleanup consumed the recorded delete, so the stop should pass")
	require.Equal(t, []string{http.MethodDelete + " " + base + fakeFolderPath}, installed.requestLog(),
		"the cleanup should reach the vendor through the recorder")
}

// TestACleanupTheModeGateSkipsFailsTheStop is the mutation behind the test above. A gate that
// registers the cleanup outside replay leaves the recorded delete unused, and a silent pass
// would hide a replay run that did less than the record run did.
func TestACleanupTheModeGateSkipsFailsTheStop(t *testing.T) {
	server := fakeVendor(t)
	base := server.URL
	dir := t.TempDir()

	recordOneDelete(t, dir, base)
	server.Close()

	replay, err := newReplayRecorder(replayConfig{mode: modeReplay, dir: dir}, probeCassette)
	require.NoError(t, err)

	// The gate skips the cleanup in replay, so no request reaches the recorder. Nothing else
	// differs from the test above, and the stop below is the whole consequence.
	err = replay.Stop()
	require.Error(t, err, "the delete the gate skipped should fail the stop")
	require.Contains(t, err.Error(), http.MethodDelete)
	require.Contains(t, err.Error(), fakeFolderPath)
	require.Contains(t, err.Error(), replay.cassetteFile())
}
