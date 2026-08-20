// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build yaml || nodejs || python || dotnet || go || all
// +build yaml nodejs python dotnet go all

package examples

import (
	"context"
	"crypto/rand"
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
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/pulumi/providertest/pulumitest"
	"github.com/pulumi/providertest/pulumitest/opttest"
	"github.com/pulumi/pulumi/sdk/v3/go/common/apitype"
)

const (
	filesAPIBase = "https://app.files.com/api/rest/v1"

	// Every vendor object these tests create carries this prefix, so the sweep can find it.
	testObjectPrefix = "pulumi-test-"

	// Every throwaway folder these tests create lives under this root. Nothing outside it is touched.
	throwawayRoot = "pulumi-filescom-test"

	providerName = "filescom"
)

// The poll bounds of the sandbox-root create. Files.com deletes a folder in a background job,
// so a root delete from an earlier run can still hold the name. Poll to a deadline, never sleep.
const (
	rootCreateTimeout = 2 * time.Minute
	rootPollInterval  = 2 * time.Second
)

func TestMain(m *testing.M) {
	sweepLeakedAPIKeys()
	sweepLeakedBehaviors()
	sweepLeakedGroups()
	if err := createThrowawayRoot(); err != nil {
		fmt.Fprintf(os.Stderr, "the sandbox root is not ready: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	removeThrowawayRoot()
	os.Exit(code)
}

// requireFilesAPIKey skips the caller when the credential is absent. The skip names the variable.
func requireFilesAPIKey(t *testing.T) {
	t.Helper()
	if os.Getenv("FILES_API_KEY") == "" {
		t.Skip("FILES_API_KEY is not set")
	}
}

// listQuery asks a Files.com list endpoint for one page. The page holds far more entries than
// a test account can carry, so no caller has to follow the pagination cursor.
func listQuery() url.Values {
	return url.Values{"per_page": []string{"1000"}}
}

// filesAPI issues one Files.com REST call. The key is read fresh from the environment on
// every call, so a rotation takes effect, and it is never logged, written, or put in a message.
func filesAPI(method, path string, query url.Values) (int, []byte, error) {
	key := os.Getenv("FILES_API_KEY")
	if key == "" {
		return 0, nil, errors.New("FILES_API_KEY is not set")
	}

	target := filesAPIBase + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-FilesAPI-Key", key)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	return resp.StatusCode, body, err
}

// Warning: ExportStack runs `pulumi stack export --show-secrets`, so the deployment below
// holds every secret in clear. Never log a deployment, a resource, or a property value.
func findResource(t *testing.T, pt *pulumitest.PulumiTest, token string) (apitype.ResourceV3, bool) {
	t.Helper()
	var deployment apitype.DeploymentV3
	require.NoError(t, json.Unmarshal(pt.ExportStack(t).Deployment, &deployment))
	for _, resource := range deployment.Resources {
		if string(resource.Type) == token {
			return resource, true
		}
	}
	return apitype.ResourceV3{}, false
}

func randomHex(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 4)
	_, err := rand.Read(buf)
	require.NoError(t, err, "generating a random object suffix")
	return hex.EncodeToString(buf)
}

func testObjectName(t *testing.T, kind string) string {
	t.Helper()
	return testObjectPrefix + kind + "-" + randomHex(t)
}

func attachProvider(t *testing.T) opttest.Option {
	t.Helper()
	return opttest.AttachProviderBinary(providerName, providerBinDir(t))
}

// stackOption adapts a function to the option interface, which carries no exported adapter.
type stackOption func(*opttest.Options)

func (apply stackOption) Apply(options *opttest.Options) { apply(options) }

// credentialSafeStack is the option every stack in this package takes. DisableGrpcLog keeps
// FILES_API_KEY and every created secret out of the gRPC log pulumitest retains on failure.
func credentialSafeStack() opttest.Option {
	return stackOption(func(options *opttest.Options) {
		opttest.SkipInstall().Apply(options)
		opttest.DisableGrpcLog().Apply(options)
	})
}

// TestTheCredentialSafeStackSwitchesTheGrpcLogOff pins what the option does, not only where the
// source scan finds it. Without DisableGrpcLog the log holds FILES_API_KEY and every new secret.
func TestTheCredentialSafeStackSwitchesTheGrpcLogOff(t *testing.T) {
	var options opttest.Options
	credentialSafeStack().Apply(&options)
	require.True(t, options.DisableGrpcLog, "the credential-safe stack should switch the gRPC log off")
	require.True(t, options.SkipInstall, "the credential-safe stack should skip the install step")
}

// providerBinDir names the directory AttachProviderBinary joins the plugin name onto. The
// binary is checked here so a missing build fails with this message, not inside the stack.
func providerBinDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs("../bin")
	require.NoError(t, err)
	binary := filepath.Join(dir, "pulumi-resource-"+providerName)
	_, err = os.Stat(binary)
	require.NoErrorf(t, err, "run `make provider` first: %s is missing", binary)
	return dir
}

// createThrowawayRoot creates the sandbox root once for the package. Creating and deleting
// the root per test raced the next test's mkdir against the pending delete job.
func createThrowawayRoot() error {
	if os.Getenv("FILES_API_KEY") == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), rootCreateTimeout)
	defer cancel()
	for {
		status, body, err := filesAPI(http.MethodPost, "/folders/"+throwawayRoot, nil)
		if err != nil {
			return err
		}
		if status == http.StatusOK || status == http.StatusCreated || throwawayRootExists() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("creating %s returned HTTP %d: %s", throwawayRoot, status, body)
		case <-time.After(rootPollInterval):
		}
	}
}

// throwawayRootExists reads the sandbox root. A create that reports a conflict is a success
// when a crashed run left the root behind, so the read decides and the status alone does not.
func throwawayRootExists() bool {
	status, _, err := filesAPI(http.MethodGet, "/folders/"+throwawayRoot, nil)
	return err == nil && status == http.StatusOK
}

// removeThrowawayRoot deletes the sandbox root once, after every test finished with it.
func removeThrowawayRoot() {
	if os.Getenv("FILES_API_KEY") == "" {
		return
	}
	status, body, err := deleteThrowawayFolder(throwawayRoot)
	if err != nil || (status >= 300 && status != http.StatusNotFound) {
		fmt.Fprintf(os.Stderr, "teardown: deleting %s returned HTTP %d %v: %s\n",
			throwawayRoot, status, err, body)
	}
}

// throwawayFolder creates the per-test folder under the package root. Call it before the
// stack exists, so its cleanup runs after the teardown that deletes the resources.
func throwawayFolder(t *testing.T) string {
	t.Helper()
	folder := throwawayRoot + "/" + randomHex(t)

	// No mkdir_parents: TestMain owns the root, so a missing parent is a defect to see here,
	// not a folder to recreate under a root that a background delete is still removing.
	status, body, err := filesAPI(http.MethodPost, "/folders/"+folder, nil)
	require.NoError(t, err, "creating the throwaway folder")
	require.Truef(t, status == http.StatusOK || status == http.StatusCreated,
		"creating folder %s returned HTTP %d: %s", folder, status, body)

	t.Cleanup(func() { deleteFolder(t, folder) })
	return folder
}

// isThrowawayPath reports whether folder is the test sandbox or a folder under it. A
// recursive delete never leaves the sandbox, whatever else the account holds.
func isThrowawayPath(folder string) bool {
	if folder == throwawayRoot {
		return true
	}
	if !strings.HasPrefix(folder, throwawayRoot+"/") {
		return false
	}
	for _, segment := range strings.Split(folder, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

// usersNeededForMembership is the account size the gated membership assertion needs. The
// assertion reorders a list of two members, so one user cannot carry it.
const usersNeededForMembership = 2

// skipReasonForUserCount decides whether the account can carry the membership assertion. An
// empty reason means the assertion runs; a failed read of the account is a failure, not a skip.
func skipReasonForUserCount(count int) string {
	if count >= usersNeededForMembership {
		return ""
	}
	return fmt.Sprintf("the membership assertion needs %d users on the account; it lists %d",
		usersNeededForMembership, count)
}

// TestTheUserCountSkipFiresOnlyWhenTheAccountLacksASecondUser pins the decision the gated
// membership test makes. A skip with enough users would hide the limitation it records.
func TestTheUserCountSkipFiresOnlyWhenTheAccountLacksASecondUser(t *testing.T) {
	for _, count := range []int{usersNeededForMembership, 3, 50} {
		require.Emptyf(t, skipReasonForUserCount(count),
			"%d users are enough, so the assertion should run", count)
	}
	for _, count := range []int{0, 1} {
		reason := skipReasonForUserCount(count)
		require.NotEmptyf(t, reason, "%d users cannot carry the assertion", count)
		require.Containsf(t, reason, "2 users",
			"the skip reason should name the precondition, got %q", reason)
	}
}

// isTestObjectPrefix reports whether prefix can only ever match names these tests created.
func isTestObjectPrefix(prefix string) bool {
	return strings.HasPrefix(prefix, testObjectPrefix)
}

// TestCleanupGuardsRejectPathsAndPrefixesTheseTestsDoNotOwn covers the two strings that
// reach a destructive call on an account holding live data.
func TestCleanupGuardsRejectPathsAndPrefixesTheseTestsDoNotOwn(t *testing.T) {
	insidePaths := []string{throwawayRoot, throwawayRoot + "/1a2b3c4d", throwawayRoot + "/1a2b3c4d/nested"}
	outsidePaths := []string{"", "/", ".", "..", "Other Folder", throwawayRoot + "-other",
		"/" + throwawayRoot, throwawayRoot + "/../Other Folder", throwawayRoot + "//",
		throwawayRoot + "/.", throwawayRoot + "/nested/.."}
	for _, folder := range insidePaths {
		require.Truef(t, isThrowawayPath(folder), "%q is the throwaway root or under it", folder)
	}
	for _, folder := range outsidePaths {
		require.Falsef(t, isThrowawayPath(folder), "%q is outside the throwaway root", folder)
	}

	for _, prefix := range []string{testObjectPrefix, testObjectPrefix + "group-1a2b3c4d"} {
		require.Truef(t, isTestObjectPrefix(prefix), "%q names only objects these tests create", prefix)
	}
	for _, prefix := range []string{"", "pulumi", "Finance", "-pulumi-test-", " " + testObjectPrefix} {
		require.Falsef(t, isTestObjectPrefix(prefix), "%q can match an object these tests never created", prefix)
	}
}

// deleteThrowawayFolder is the one place a recursive folder delete is built. The path guard
// is load-bearing: an unguarded value here would aim the delete outside the sandbox.
func deleteThrowawayFolder(folder string) (int, []byte, error) {
	if !isThrowawayPath(folder) {
		return 0, nil, fmt.Errorf("the folder %q is outside %s, so this suite does not delete it",
			folder, throwawayRoot)
	}
	return filesAPI(http.MethodDelete, "/files/"+folder,
		url.Values{"recursive": []string{"true"}})
}

// deleteFolder tolerates an already-deleted folder so cleanup never fails a test. A path
// outside the throwaway root is a defect, not an "already gone", so it fails loudly.
func deleteFolder(t *testing.T, folder string) {
	t.Helper()
	if !isThrowawayPath(folder) {
		t.Errorf("the folder %q is outside %s, so this suite does not delete it", folder, throwawayRoot)
		return
	}
	status, body, err := deleteThrowawayFolder(folder)
	if err != nil {
		t.Logf("cleanup: deleting folder %s: %v", folder, err)
		return
	}
	if status >= 300 && status != http.StatusNotFound {
		t.Logf("cleanup: deleting folder %s returned HTTP %d: %s", folder, status, body)
	}
}

type filesGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func listGroups() ([]filesGroup, error) {
	status, body, err := filesAPI(http.MethodGet, "/groups", listQuery())
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("listing groups returned HTTP %d: %s", status, body)
	}
	var groups []filesGroup
	if err := json.Unmarshal(body, &groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func deleteGroup(id int64) (int, error) {
	status, _, err := filesAPI(http.MethodDelete, fmt.Sprintf("/groups/%d", id), nil)
	return status, err
}

func requireGroupNamed(t *testing.T, id, name string) {
	t.Helper()
	groups, err := listGroups()
	require.NoError(t, err)
	for _, group := range groups {
		if strconv.FormatInt(group.ID, 10) == id {
			require.Equal(t, name, group.Name, "the group on the account should carry the configured name")
			return
		}
	}
	t.Fatalf("the account lists no group with id %s", id)
}

type filesBehavior struct {
	ID       int64  `json:"id"`
	Path     string `json:"path"`
	Behavior string `json:"behavior"`
	Name     string `json:"name"`
	Value    any    `json:"value"`
}

func listBehaviors() ([]filesBehavior, error) {
	status, body, err := filesAPI(http.MethodGet, "/behaviors", listQuery())
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("listing behaviors returned HTTP %d: %s", status, body)
	}
	var behaviors []filesBehavior
	if err := json.Unmarshal(body, &behaviors); err != nil {
		return nil, err
	}
	return behaviors, nil
}

// sandboxBehaviors returns the behaviors inside the throwaway root. The path guard is what
// keeps every behavior delete off a folder these tests do not own.
func sandboxBehaviors(t *testing.T) []filesBehavior {
	t.Helper()
	behaviors, err := listBehaviors()
	require.NoError(t, err)
	var inside []filesBehavior
	for _, behavior := range behaviors {
		if isThrowawayPath(behavior.Path) {
			inside = append(inside, behavior)
		}
	}
	return inside
}

func requireBehaviorOnPath(t *testing.T, folder string) filesBehavior {
	t.Helper()
	var matches []filesBehavior
	for _, behavior := range sandboxBehaviors(t) {
		if behavior.Path == folder {
			matches = append(matches, behavior)
		}
	}
	require.Lenf(t, matches, 1, "the account should list one behavior on %s", folder)
	return matches[0]
}

// behaviorsToSweep picks the behaviors a cleanup may delete. Two guards apply: the behavior
// sits inside the sandbox root, and one folder the caller named holds it.
func behaviorsToSweep(behaviors []filesBehavior, folders []string) []filesBehavior {
	var sweepable []filesBehavior
	for _, behavior := range behaviors {
		if !isThrowawayPath(behavior.Path) || behavior.ID <= 0 {
			continue
		}
		for _, folder := range folders {
			if behavior.Path == folder {
				sweepable = append(sweepable, behavior)
				break
			}
		}
	}
	return sweepable
}

// TestTheBehaviorSweepStaysInsideTheFoldersItIsGiven covers the selection behind every behavior
// delete. The example legs run in parallel, so one cleanup meets the folder a sibling still uses.
func TestTheBehaviorSweepStaysInsideTheFoldersItIsGiven(t *testing.T) {
	mine := throwawayRoot + "/1a2b3c4d"
	sibling := throwawayRoot + "/5e6f7a8b"
	behaviors := []filesBehavior{
		{ID: 1, Path: mine},
		{ID: 2, Path: sibling},
		{ID: 3, Path: throwawayRoot},
		{ID: 4, Path: "Finance"},
		{ID: 5, Path: mine + "-other"},
		{ID: 0, Path: mine},
		{ID: -1, Path: mine},
	}

	require.Equal(t, []int64{1}, sweptBehaviorIDs(behaviors, mine),
		"a cleanup should reach the behavior on its own folder and no other")
	require.Equal(t, []int64{1, 2}, sweptBehaviorIDs(behaviors, mine, sibling),
		"a test that owns two folders should reach both")
	require.Empty(t, sweptBehaviorIDs(behaviors, "Finance"),
		"a folder outside the sandbox should reach no behavior")
	require.Empty(t, sweptBehaviorIDs(behaviors),
		"a cleanup that names no folder should reach no behavior")
}

func sweptBehaviorIDs(behaviors []filesBehavior, folders ...string) []int64 {
	var ids []int64
	for _, behavior := range behaviorsToSweep(behaviors, folders) {
		ids = append(ids, behavior.ID)
	}
	return ids
}

// deleteBehaviorsOn removes the behaviors on the folders one test owns. Registered with
// t.Cleanup before the stack exists, it is the net under a failed `pulumi destroy`.
func deleteBehaviorsOn(t *testing.T, folders ...string) {
	t.Helper()
	for _, folder := range folders {
		if !isThrowawayPath(folder) {
			t.Errorf("the folder %q is outside %s, so this suite does not sweep it", folder, throwawayRoot)
			return
		}
	}
	behaviors, err := listBehaviors()
	if err != nil {
		t.Logf("cleanup: listing behaviors: %v", err)
		return
	}
	for _, behavior := range behaviorsToSweep(behaviors, folders) {
		status, _, err := filesAPI(http.MethodDelete, fmt.Sprintf("/behaviors/%d", behavior.ID), nil)
		if err != nil {
			t.Logf("cleanup: deleting behavior %d: %v", behavior.ID, err)
		} else if status >= 300 && status != http.StatusNotFound {
			t.Logf("cleanup: deleting behavior %d returned HTTP %d", behavior.ID, status)
		}
	}
}

// deleteGroupsNamed removes every group whose name starts with prefix. Registered with
// t.Cleanup before the stack is created, it is the net under a failed `pulumi destroy`.
func deleteGroupsNamed(t *testing.T, prefix string) {
	t.Helper()
	if !isTestObjectPrefix(prefix) {
		t.Errorf("the group sweep prefix %q must start with %s", prefix, testObjectPrefix)
		return
	}
	groups, err := listGroups()
	if err != nil {
		t.Logf("cleanup: listing groups: %v", err)
		return
	}
	for _, group := range groups {
		if !strings.HasPrefix(group.Name, prefix) {
			continue
		}
		status, err := deleteGroup(group.ID)
		if err != nil {
			t.Logf("cleanup: deleting group %d: %v", group.ID, err)
		} else if status >= 300 && status != http.StatusNotFound {
			t.Logf("cleanup: deleting group %d returned HTTP %d", group.ID, status)
		}
	}
}

// sweepLeakedAPIKeys deletes prefixed keys a crashed run left behind, with no age filter: the
// Files.com API key list carries a creation time this suite does not need to trust.
func sweepLeakedAPIKeys() {
	if os.Getenv("FILES_API_KEY") == "" {
		return
	}
	keys, err := listAPIKeys()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sweep: listing API keys: %v\n", err)
		return
	}
	currentID := currentAPIKeyID()
	for _, key := range keys {
		if !isSweepableAPIKey(key, currentID) {
			continue
		}
		status, err := deleteAPIKeyByID(key.ID)
		fmt.Fprintf(os.Stderr, "sweep: deleting the leaked API key %d: HTTP %d %v\n",
			key.ID, status, err)
	}
}

// sweepLeakedBehaviors deletes behaviors a crashed run left behind, with no age filter: the
// Files.com behavior model carries no timestamp (files-sdk-go v3.3.233, behavior.go).
func sweepLeakedBehaviors() {
	if os.Getenv("FILES_API_KEY") == "" {
		return
	}
	behaviors, err := listBehaviors()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sweep: listing behaviors: %v\n", err)
		return
	}
	for _, behavior := range behaviors {
		// The path guard keeps the sweep off the folders these tests never created, and the
		// id guard keeps an unset id out of the delete path it would otherwise build.
		if !isThrowawayPath(behavior.Path) || behavior.ID <= 0 {
			continue
		}
		status, _, err := filesAPI(http.MethodDelete, fmt.Sprintf("/behaviors/%d", behavior.ID), nil)
		fmt.Fprintf(os.Stderr, "sweep: deleting leaked behavior %d on %s: HTTP %d %v\n",
			behavior.ID, behavior.Path, status, err)
	}
}

// sweepLeakedGroups deletes groups a crashed run left behind, with no age filter: the
// Files.com group model carries no timestamp (files-sdk-go v3.3.233, group.go).
func sweepLeakedGroups() {
	if os.Getenv("FILES_API_KEY") == "" {
		return
	}
	groups, err := listGroups()
	if err != nil {
		fmt.Fprintf(os.Stderr, "sweep: listing groups: %v\n", err)
		return
	}
	for _, group := range groups {
		if !strings.HasPrefix(group.Name, testObjectPrefix) {
			continue
		}
		status, err := deleteGroup(group.ID)
		fmt.Fprintf(os.Stderr, "sweep: deleting leaked group %d: HTTP %d %v\n", group.ID, status, err)
	}
}

// filesAPIKey holds the contract keys of an API key entry. The credential fields of the
// vendor model are left out on purpose, so no code path can print one.
type filesAPIKey struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	PermissionSet string `json:"permission_set"`
	Path          string `json:"path"`
}

// listAPIKeys reads the account's API keys. No error here carries the response body: an API
// key entry can hold the credential itself, and a message reaches logs and files.
func listAPIKeys() ([]filesAPIKey, error) {
	status, body, err := filesAPI(http.MethodGet, "/api_keys", listQuery())
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("listing API keys returned HTTP %d", status)
	}
	var keys []filesAPIKey
	if err := json.Unmarshal(body, &keys); err != nil {
		return nil, errors.New("the API key list did not decode")
	}
	return keys, nil
}

// currentAPIKeyID reads the id of the key FILES_API_KEY names, so no sweep can delete the
// credential this suite runs on. Zero means the read failed and the name guard stands alone.
func currentAPIKeyID() int64 {
	status, body, err := filesAPI(http.MethodGet, "/api_key", nil)
	if err != nil || status != http.StatusOK {
		return 0
	}
	var current filesAPIKey
	if err := json.Unmarshal(body, &current); err != nil {
		return 0
	}
	return current.ID
}

// isSweepableAPIKey reports whether a delete may reach this key. The name guard keeps the
// sweep off keys these tests never created; currentID keeps it off the credential in use.
func isSweepableAPIKey(key filesAPIKey, currentID int64) bool {
	if key.ID <= 0 || key.ID == currentID {
		return false
	}
	return isTestObjectPrefix(key.Name)
}

// TestTheAPIKeySweepRefusesKeysTheseTestsDoNotOwn covers the guard on the one destructive
// call in this file. A leaked API key is a live credential, so the guard is tested, not read.
func TestTheAPIKeySweepRefusesKeysTheseTestsDoNotOwn(t *testing.T) {
	const currentID = int64(4242)

	sweepable := []filesAPIKey{
		{ID: 7, Name: testObjectPrefix + "apikey-1a2b3c4d"},
		{ID: 7, Name: testObjectPrefix + "apikey-1a2b3c4d-renamed"},
	}
	for _, key := range sweepable {
		require.Truef(t, isSweepableAPIKey(key, currentID), "%q is a key these tests created", key.Name)
	}

	refused := []filesAPIKey{
		{ID: currentID, Name: testObjectPrefix + "apikey-1a2b3c4d"},
		{ID: 0, Name: testObjectPrefix + "apikey-1a2b3c4d"},
		{ID: -1, Name: testObjectPrefix + "apikey-1a2b3c4d"},
		{ID: 7, Name: ""},
		{ID: 7, Name: "admin"},
		{ID: 7, Name: "pulumi"},
		{ID: 7, Name: " " + testObjectPrefix + "apikey-1a2b3c4d"},
		{ID: 7, Name: "Pulumi-Test-apikey-1a2b3c4d"},
	}
	for _, key := range refused {
		require.Falsef(t, isSweepableAPIKey(key, currentID),
			"the key id %d named %q is not one these tests created", key.ID, key.Name)
	}

	// A failed current-key read leaves the name guard on its own, which is still enough.
	require.True(t, isSweepableAPIKey(filesAPIKey{ID: 7, Name: testObjectPrefix + "apikey-1a2b3c4d"}, 0))
	require.False(t, isSweepableAPIKey(filesAPIKey{ID: 7, Name: "admin"}, 0))
}

// deleteAPIKeyByID is the one place an API key delete is built. The id guard is load-bearing:
// the account-wide `/api_key` path deletes the credential the suite runs on.
func deleteAPIKeyByID(id int64) (int, error) {
	if id <= 0 {
		return 0, fmt.Errorf("the API key delete refuses the id %d", id)
	}
	status, _, err := filesAPI(http.MethodDelete, fmt.Sprintf("/api_keys/%d", id), nil)
	return status, err
}

// deleteAPIKey removes one API key inside a test. An id of zero or less is a defect in the
// caller, not an "already gone", so it fails the test loudly.
func deleteAPIKey(t *testing.T, id int64) {
	t.Helper()
	if id <= 0 {
		t.Errorf("the API key cleanup refuses the id %d", id)
		return
	}
	status, err := deleteAPIKeyByID(id)
	if err != nil {
		t.Logf("cleanup: deleting API key %d: %v", id, err)
		return
	}
	if status >= 300 && status != http.StatusNotFound {
		t.Logf("cleanup: deleting API key %d returned HTTP %d", id, status)
	}
}
