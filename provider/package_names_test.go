package filescom

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const packageNameScript = "scripts/check-package-names.sh"

// The environment variables the check reads its three registries from. They exist so the
// decision the check makes about a status code can be exercised without the real registries.
var packageNameRegistryVars = []string{
	"PACKAGE_CHECK_NPM_URL",
	"PACKAGE_CHECK_PYPI_URL",
	"PACKAGE_CHECK_NUGET_URL",
}

// registryStub answers every request with one status and records the paths it was asked for.
type registryStub struct {
	server *httptest.Server
	mu     sync.Mutex
	paths  []string
}

func newRegistryStub(t *testing.T, status func(path string) int) *registryStub {
	t.Helper()
	stub := &registryStub{}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.paths = append(stub.paths, r.URL.Path)
		stub.mu.Unlock()
		w.WriteHeader(status(r.URL.Path))
	}))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *registryStub) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.paths...)
}

// runPackageNameCheck runs the check against the stub and returns its combined output.
func runPackageNameCheck(t *testing.T, stub *registryStub) (string, error) {
	t.Helper()
	cmd := exec.Command("./" + packageNameScript)
	cmd.Dir = repoRoot(t)
	cmd.Env = os.Environ()
	for _, name := range packageNameRegistryVars {
		cmd.Env = append(cmd.Env, name+"="+stub.server.URL)
	}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestThePackageNameCheckFailsOnATakenName is the case the release gate exists for. Publishing
// under a name this repository did not claim hands the next version to whoever holds it.
func TestThePackageNameCheckFailsOnATakenName(t *testing.T) {
	for _, taken := range []string{npmPackageName, pypiPackageName, strings.ToLower(nugetPackageID)} {
		t.Run(taken, func(t *testing.T) {
			stub := newRegistryStub(t, func(path string) int {
				if strings.Contains(path, taken) {
					return http.StatusOK
				}
				return http.StatusNotFound
			})
			out, err := runPackageNameCheck(t, stub)
			if err == nil {
				t.Fatalf("the check passed while %s was taken:\n%s", taken, out)
			}
			if !strings.Contains(out, taken) {
				t.Errorf("the check does not name the taken package %s:\n%s", taken, out)
			}
		})
	}
}

// TestThePackageNameCheckFailsWhenARegistryCannotBeRead keeps a broken read out of the answer.
// A registry that returns 500 has said nothing about who holds the name.
func TestThePackageNameCheckFailsWhenARegistryCannotBeRead(t *testing.T) {
	stub := newRegistryStub(t, func(string) int { return http.StatusInternalServerError })
	out, err := runPackageNameCheck(t, stub)
	if err == nil {
		t.Fatalf("the check passed while no registry answered:\n%s", out)
	}
	if !strings.Contains(out, "500") {
		t.Errorf("the check does not report the status it read:\n%s", out)
	}
}

// TestThePackageNameCheckProbesTheNamesThisRepositoryPublishes reads the paths the check asked
// for. A registry that has never seen the name answers 404, which is the state a release needs.
func TestThePackageNameCheckProbesTheNamesThisRepositoryPublishes(t *testing.T) {
	stub := newRegistryStub(t, func(string) int { return http.StatusNotFound })
	out, err := runPackageNameCheck(t, stub)
	if err != nil {
		t.Fatalf("the check failed while every name was free: %v\n%s", err, out)
	}

	want := []string{
		"/" + npmPackageName,
		"/pypi/" + pypiPackageName + "/json",
		"/v3-flatcontainer/" + strings.ToLower(nugetPackageID) + "/index.json",
	}
	schemaAssertSameSet(t, "registry paths the check reads", stub.seen(), want)
	// A set comparison passes on a repeated read, so the count is read as well.
	if len(stub.seen()) != len(want) {
		t.Errorf("the check made %d requests to reach %d registries: %v",
			len(stub.seen()), len(want), stub.seen())
	}
}

// TestThePackageNameCheckRunsNowhereAutomatically reads the workflows. Every name is taken the
// moment the first release publishes, so a scheduled run of this check would fail from then on.
func TestThePackageNameCheckRunsNowhereAutomatically(t *testing.T) {
	for _, workflow := range workflowFiles(t) {
		body, err := os.ReadFile(workflow)
		if err != nil {
			t.Fatalf("read %s: %v", workflow, err)
		}
		if strings.Contains(string(body), filepath.Base(packageNameScript)) {
			t.Errorf("%s runs %s, which fails once the names are claimed",
				filepath.Base(workflow), packageNameScript)
		}
	}
}
