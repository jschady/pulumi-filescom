// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build yaml || nodejs || python || dotnet || go || all
// +build yaml nodejs python dotnet go all

package examples

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Files-com/files-sdk-go/v3/lib"
	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"

	"github.com/pulumi/providertest/providers"
	pftfbridge "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfbridge"
	pulumirpc "github.com/pulumi/pulumi/sdk/v3/proto/go"

	filescom "github.com/jschady/pulumi-filescom/provider"
	"github.com/jschady/pulumi-filescom/provider/pkg/version"
)

// testModeVariable names the one environment variable that decides how these tests reach the
// vendor. An unset variable means replay, so a run with no key and no configuration replays.
const testModeVariable = "FILESCOM_TEST_MODE"

// testMode is the value of testModeVariable.
type testMode string

const (
	modeReplay testMode = "replay"
	modeRecord testMode = "record"
	modeLive   testMode = "live"
)

// apiKeyVariable names the credential the provider reads when it configures itself. The
// upstream provider refuses an empty value, and the replay job holds no secret, so the harness
// fills the variable in for a replay run.
//
//nolint:gosec // G101: this names an environment variable, and holds no key.
const apiKeyVariable = "FILES_API_KEY"

// recordTarget is the make target that writes a cassette and a seed. Every message about a
// missing file names it, so the reader knows the one command that creates the file.
const recordTarget = "make record_examples"

// seedSuffix names the file that sits next to a cassette and holds the test's seed.
const seedSuffix = ".seed"

// readTestMode turns the raw value of testModeVariable into a mode. An empty value is replay.
func readTestMode(raw string) (testMode, error) {
	switch testMode(raw) {
	case "":
		return modeReplay, nil
	case modeReplay, modeRecord, modeLive:
		return testMode(raw), nil
	default:
		return "", fmt.Errorf("FILESCOM_TEST_MODE is %q, and the modes are replay, record, and live", raw)
	}
}

// replayConfig carries what a recorder needs from outside the test: the mode and the directory
// that holds the cassettes. The mechanism tests build one by hand over a temporary directory.
type replayConfig struct {
	mode testMode
	dir  string
}

// configFor reads the mode out of the environment and points at the committed cassettes.
func configFor(t *testing.T) replayConfig {
	t.Helper()
	mode, err := readTestMode(os.Getenv(testModeVariable))
	require.NoError(t, err)
	return replayConfig{mode: mode, dir: filepath.FromSlash(cassetteDirName)}
}

// testModeFor is the mode the caller runs in. A test that cannot replay reads it to decide.
func testModeFor(t *testing.T) testMode {
	t.Helper()
	return configFor(t).mode
}

// cassetteName is the file name of a test's cassette. A subtest carries the full name of the
// test, and the separator becomes an underscore so the name stays one path segment.
func cassetteName(t *testing.T) string {
	t.Helper()
	return strings.ReplaceAll(t.Name(), "/", "_")
}

// seedSource turns one seed into the bytes a test's object names carry. The Nth call returns
// the same bytes in replay as it returned in record, because the counter drives them.
type seedSource struct {
	mu      sync.Mutex
	seed    []byte
	counter uint64
}

// Bytes returns count deterministic bytes and moves the counter on by one.
func (source *seedSource) Bytes(count int) []byte {
	source.mu.Lock()
	defer source.mu.Unlock()

	drawn := make([]byte, 0, count+sha256.Size)
	for block := uint64(0); len(drawn) < count; block++ {
		var label [16]byte
		binary.BigEndian.PutUint64(label[0:8], source.counter)
		binary.BigEndian.PutUint64(label[8:16], block)
		sum := sha256.Sum256(append(append([]byte{}, source.seed...), label[:]...))
		drawn = append(drawn, sum[:]...)
	}
	source.counter++
	return drawn[:count]
}

// freshSeed draws a new seed. Record writes it to disk; live keeps it in memory.
func freshSeed() ([]byte, error) {
	seed := make([]byte, sha256.Size)
	if _, err := rand.Read(seed); err != nil {
		return nil, err
	}
	return seed, nil
}

// loadSeed returns the seed source of one test. Replay reads the file record wrote, so a
// missing file fails the same way a missing cassette does.
func loadSeed(config replayConfig, name string) (*seedSource, error) {
	path := filepath.Join(config.dir, name) + seedSuffix
	if config.mode == modeReplay {
		raw, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("the seed file %s is missing, so run %q to write it", path, recordTarget)
		}
		if err != nil {
			return nil, err
		}
		seed, err := hex.DecodeString(strings.TrimSpace(string(raw)))
		if err != nil {
			return nil, fmt.Errorf("the seed file %s does not hold hexadecimal bytes", path)
		}
		return &seedSource{seed: seed}, nil
	}

	seed, err := freshSeed()
	if err != nil {
		return nil, err
	}
	if config.mode == modeRecord {
		if err := os.MkdirAll(config.dir, 0o750); err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, []byte(hex.EncodeToString(seed)+"\n"), 0o600); err != nil {
			return nil, err
		}
	}
	return &seedSource{seed: seed}, nil
}

// replayRecorder is the seam between the tests and the vendor. It carries the go-vcr recorder,
// the seed source of the same test, and the log of every request it saw.
type replayRecorder struct {
	mode     testMode
	name     string
	seed     *seedSource
	recorder *recorder.Recorder

	mu  sync.Mutex
	log []string
}

// cassetteFile is the path the recorder reads and writes. The recorder appends the suffix to
// the name it is given, so every message about the file has to append it too.
func (replay *replayRecorder) cassetteFile() string {
	return replay.name + cassetteSuffix
}

// requestLog returns the method and the URL of every request the recorder saw, in order.
func (replay *replayRecorder) requestLog() []string {
	replay.mu.Lock()
	defer replay.mu.Unlock()
	return append([]string{}, replay.log...)
}

// RoundTrip logs the request and hands it to the recorder.
func (replay *replayRecorder) RoundTrip(request *http.Request) (*http.Response, error) {
	replay.mu.Lock()
	replay.log = append(replay.log, request.Method+" "+request.URL.String())
	replay.mu.Unlock()

	if replay.recorder == nil {
		return http.DefaultTransport.RoundTrip(request)
	}
	return replay.recorder.RoundTrip(request)
}

// client is an HTTP client that reaches the vendor through the recorder.
func (replay *replayRecorder) client() *http.Client {
	return &http.Client{Transport: replay}
}

// Stop saves the cassette in record mode and runs the unconsumed-interaction check.
func (replay *replayRecorder) Stop() error {
	if replay.recorder == nil {
		return nil
	}
	return replay.recorder.Stop()
}

// requireReplayed is the stop hook. A recorded interaction that no request matched means the
// cassette and the test drifted apart, so the test fails instead of passing on a stale file.
func (replay *replayRecorder) requireReplayed(interaction *cassette.Interaction) error {
	if replay.mode != modeReplay || interaction.WasReplayed() {
		return nil
	}
	return fmt.Errorf("the cassette %s holds an interaction no request used: %s %s",
		replay.cassetteFile(), interaction.Request.Method, interaction.Request.URL)
}

// newReplayRecorder builds the recorder of one test. Live mode returns a recorder that holds
// only the seed, because a live run reaches the vendor with the transports it already has.
func newReplayRecorder(config replayConfig, name string) (*replayRecorder, error) {
	seed, err := loadSeed(config, name)
	if err != nil {
		return nil, err
	}

	replay := &replayRecorder{
		mode: config.mode,
		name: filepath.Join(config.dir, name),
		seed: seed,
	}
	if config.mode == modeLive {
		return replay, nil
	}

	vcrMode := recorder.ModeReplayOnly
	if config.mode == modeRecord {
		vcrMode = recorder.ModeRecordOnly
	}
	realTransport := lib.DefaultClient.Transport
	if realTransport == nil {
		realTransport = http.DefaultTransport
	}

	built, err := recorder.New(replay.name,
		recorder.WithMode(vcrMode),
		recorder.WithRealTransport(realTransport),
		recorder.WithSkipRequestLatency(true),
		recorder.WithMatcher(matchRequest),
		recorder.WithHook(scrubInteraction, recorder.BeforeSaveHook),
		recorder.WithHook(replay.requireReplayed, recorder.OnRecorderStopHook),
	)
	if err != nil {
		if errors.Is(err, cassette.ErrCassetteNotFound) {
			return nil, fmt.Errorf("the cassette %s is missing, so run %q to write it",
				replay.cassetteFile(), recordTarget)
		}
		return nil, err
	}
	replay.recorder = built
	return replay, nil
}

// supplyReplayCredential fills the credential variable in for a replay run that found it empty.
// The provider configures itself from the environment of this process and refuses an empty key,
// and a replay run answers from a cassette, so the placeholder below reaches no account. The
// cleanup t.Setenv registers puts the environment back after the test.
func supplyReplayCredential(t *testing.T, mode testMode) {
	t.Helper()
	if mode != modeReplay || os.Getenv(apiKeyVariable) != "" {
		return
	}
	t.Setenv(apiKeyVariable, replayPlaceholder)
}

// install puts the recorder in front of both HTTP paths this package uses. The provider builds
// on lib.DefaultClient, and the harness's own vendor calls build on http.DefaultTransport.
// One recorder covers both, so one cassette holds the whole test.
func (replay *replayRecorder) install(t *testing.T) {
	t.Helper()
	supplyReplayCredential(t, replay.mode)
	registerRecorder(t.Name(), replay)

	if replay.recorder == nil {
		t.Cleanup(func() { releaseRecorder(t.Name()) })
		return
	}

	previousLib := lib.DefaultClient.Transport
	previousDefault := http.DefaultTransport
	lib.DefaultClient.Transport = replay
	http.DefaultTransport = replay

	t.Cleanup(func() {
		lib.DefaultClient.Transport = previousLib
		http.DefaultTransport = previousDefault
		releaseRecorder(t.Name())
		if err := replay.Stop(); err != nil {
			t.Errorf("the recorder did not stop cleanly: %v", err)
		}
	})
}

// recorderFor builds the recorder of the calling test and installs it. Call it before the test
// creates a stack or reads the vendor, so every request of the test reaches one cassette.
func recorderFor(t *testing.T) *replayRecorder {
	t.Helper()
	replay, err := newReplayRecorder(configFor(t), cassetteName(t))
	require.NoError(t, err)
	replay.install(t)
	return replay
}

// The registry lets a name helper find the recorder of the test it runs inside, so the helper
// keeps the signature it has today.
var (
	recorderMu        sync.Mutex
	recordersByTest   = map[string]*replayRecorder{}
	noRecorderMessage = "the test %s has no recorder, so call recorderFor(t) before it names an object"
)

func registerRecorder(name string, replay *replayRecorder) {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	recordersByTest[name] = replay
}

func releaseRecorder(name string) {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	delete(recordersByTest, name)
}

func registeredRecorder(name string) *replayRecorder {
	recorderMu.Lock()
	defer recorderMu.Unlock()
	return recordersByTest[name]
}

// nameBytes returns the deterministic bytes the calling test's next object name carries.
func nameBytes(t *testing.T, count int) []byte {
	t.Helper()
	replay := registeredRecorder(t.Name())
	require.NotNilf(t, replay, noRecorderMessage, t.Name())
	return replay.seed.Bytes(count)
}

// matchRequest decides whether a live request is the recorded one. It reads the method, the
// path and the query, and the body of a write. The default matcher compares every header, and
// a scrubbed cassette carries no key header, so it would never match.
func matchRequest(request *http.Request, recorded cassette.Request) bool {
	if request.Method != recorded.Method {
		return false
	}
	recordedURL, err := url.Parse(recorded.URL)
	if err != nil {
		return false
	}
	if request.URL.Path != recordedURL.Path {
		return false
	}
	if request.URL.Query().Encode() != recordedURL.Query().Encode() {
		return false
	}
	switch request.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		body, ok := readAndRestore(request)
		return ok && bodyForMatch(body) == recorded.Body
	default:
		return true
	}
}

// bodyForMatch is the body a comparison reads. The save hook redacts every secret member of a
// recorded body, so the live body passes through the same redaction before it meets the file.
func bodyForMatch(body string) string {
	redacted, _ := redactBody(body)
	return redacted
}

// readAndRestore reads the request body and puts it back, so the matcher can run again and the
// recorder can still send the request.
func readAndRestore(request *http.Request) (string, bool) {
	if request.Body == nil || request.Body == http.NoBody {
		return "", true
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return "", false
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	return string(body), true
}

// contentLengthHeader names the header that measures a body. The save hook rewrites the body,
// and go-vcr replays this header, so the hook rewrites the header with it.
const contentLengthHeader = "Content-Length"

// A cassette holds no credential. The request headers below carry one, and the response header
// carries a session, so the save hook drops all of them.
var (
	scrubbedRequestHeaders  = []string{headerFilesAPIKey, headerAPIKey, headerAuthorization, headerCookie}
	scrubbedResponseHeaders = []string{headerSetCookie}
)

// secretMembers is the set the redaction reads. A vendor spells a member name the way it
// likes, so the lookup folds the case of the name the body carries.
var secretMembers = func() map[string]bool {
	members := map[string]bool{}
	for _, name := range secretMemberNames {
		members[strings.ToLower(name)] = true
	}
	return members
}()

// scrubInteraction runs before the cassette reaches the disk. It drops the credential headers
// and replaces every secret a body carries, on the request and on the response, on every path.
func scrubInteraction(interaction *cassette.Interaction) error {
	dropHeaders(interaction.Request.Headers, scrubbedRequestHeaders)
	dropHeaders(interaction.Response.Headers, scrubbedResponseHeaders)

	interaction.Request.Body, interaction.Request.ContentLength =
		redactAndMeasure(interaction.Request.Body, interaction.Request.ContentLength,
			interaction.Request.Headers)
	interaction.Response.Body, interaction.Response.ContentLength =
		redactAndMeasure(interaction.Response.Body, interaction.Response.ContentLength,
			interaction.Response.Headers)
	return nil
}

// redactAndMeasure replaces every secret in one body and makes the length beside it true. A
// replay builds the response from the recorded length, so a length the redaction left behind
// tells the client to read bytes the cassette does not hold. A body the scrub leaves alone
// keeps the length the recording carries.
func redactAndMeasure(body string, length int64, headers http.Header) (string, int64) {
	redacted, changed := redactBody(body)
	if !changed {
		return body, length
	}
	size := int64(len(redacted))
	setContentLength(headers, size)
	return redacted, size
}

// setContentLength writes the size of the body into the header that measures it. A request or a
// response that sent no such header keeps none, because the scrub adds nothing to a cassette.
func setContentLength(headers http.Header, size int64) {
	for header := range headers {
		if strings.EqualFold(header, contentLengthHeader) {
			headers[header] = []string{strconv.FormatInt(size, 10)}
		}
	}
}

// dropHeaders removes every named header. The names in a cassette carry the canonical spelling
// of the Go HTTP package, so the comparison ignores case.
func dropHeaders(headers http.Header, names []string) {
	for header := range headers {
		for _, dropped := range names {
			if strings.EqualFold(header, dropped) {
				delete(headers, header)
				break
			}
		}
	}
}

// redactBody replaces every secret value in the body and reports whether it replaced one. A
// body the JSON decoder rejects comes back unchanged, because a redaction that guesses is worse.
func redactBody(body string) (string, bool) {
	if strings.TrimSpace(body) == "" {
		return body, false
	}
	var document any
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		return body, false
	}
	redacted, changed := redactSecretFields(document, false)
	if !changed {
		return body, false
	}
	encoded, err := json.Marshal(redacted)
	if err != nil {
		return body, false
	}
	return string(encoded), true
}

// redactSecretFields walks the decoded JSON and replaces every string a secret member carries.
// The secret flag carries into an array, so a member that holds a list of keys loses every
// element of it. It does not carry into a nested object: each member of that object answers
// for its own name.
func redactSecretFields(node any, secret bool) (any, bool) {
	switch value := node.(type) {
	case map[string]any:
		changed := false
		for name, child := range value {
			replaced, childChanged := redactSecretFields(child, secretMembers[strings.ToLower(name)])
			value[name] = replaced
			changed = changed || childChanged
		}
		return value, changed
	case []any:
		changed := false
		for index, child := range value {
			replaced, childChanged := redactSecretFields(child, secret)
			value[index] = replaced
			changed = changed || childChanged
		}
		return value, changed
	case string:
		if secret && value != redactionMarker {
			return redactionMarker, true
		}
		return value, false
	default:
		return node, false
	}
}

// providerVersion is the version the in-process provider reports. Provider() parses it, and the
// linker fills it in for a real build only.
const providerVersion = "0.1.0"

// schemaRelativePath is the committed schema, read from the directory of this package.
const schemaRelativePath = "../provider/cmd/pulumi-resource-filescom/schema.json"

// inProcessProvider builds the provider server the replay and record runs attach to. It runs
// inside the test process, so the recorder the test installed sees the provider's HTTP.
func inProcessProvider(ctx context.Context) (pulumirpc.ResourceProviderServer, error) {
	packageSchema, err := os.ReadFile(schemaRelativePath)
	if err != nil {
		return nil, err
	}
	version.Version = providerVersion
	return pftfbridge.NewProviderServer(ctx, nil, filescom.Provider(),
		pftfbridge.ProviderMetadata{PackageSchema: packageSchema})
}

// programSource adapts a directory to the one-method interface the provider factory takes.
type programSource string

func (source programSource) Source() string { return string(source) }

// debugProvidersEnv starts the in-process provider and returns the environment entry that
// points a program test at it. The provider stops when the test ends.
func debugProvidersEnv(t *testing.T, source string) string {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	factory := providers.ResourceProviderFactory(
		func(providers.PulumiTest) (pulumirpc.ResourceProviderServer, error) {
			return inProcessProvider(ctx)
		})
	port, err := factory(ctx, programSource(source))
	require.NoError(t, err)

	running := map[providers.ProviderName]providers.Port{providers.ProviderName(providerName): port}
	return "PULUMI_DEBUG_PROVIDERS=" + providers.GetDebugProvidersEnv(running)
}
