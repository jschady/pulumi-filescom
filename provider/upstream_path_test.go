package filescom

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// behaviorDoc proves the resolved path is the upstream doc root the bridge reads,
// not merely some directory that happens to hold a docs/ child.
const behaviorDoc = "docs/resources/behavior.md"

// startupTimeout bounds the wait for the plugin's port line, which a healthy binary
// prints immediately. The version stamp matches the one the Makefile links in.
const (
	startupTimeout = 30 * time.Second
	versionStamp   = "-X github.com/jschady/pulumi-filescom/provider/pkg/version.Version=0.0.1"
)

func TestUpstreamRepoPathFromRepoRoot(t *testing.T) {
	t.Chdir(repoRoot(t))

	got := upstreamRepoPath()

	if got != upstreamDirName {
		t.Errorf("upstreamRepoPath() = %q, want %q", got, upstreamDirName)
	}
	upstreamAssertBehaviorDoc(t, got)
}

// The package directory is provider/, which is where a hand-run tfgen starts.
func TestUpstreamRepoPathFromProviderDir(t *testing.T) {
	want := filepath.Join("..", upstreamDirName)

	got := upstreamRepoPath()

	if got != want {
		t.Errorf("upstreamRepoPath() = %q, want %q", got, want)
	}
	upstreamAssertBehaviorDoc(t, got)
}

func TestUpstreamRepoPathPanicsWhenSubmoduleAbsent(t *testing.T) {
	t.Chdir(t.TempDir())

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("upstreamRepoPath() returned instead of panicking with no upstream checkout")
		}
		message := fmt.Sprint(recovered)
		if !strings.Contains(message, "git submodule update --init") {
			t.Errorf("panic %q does not name `git submodule update --init`", message)
		}
	}()

	upstreamRepoPath()
}

// A registered but uninitialized submodule leaves an empty upstream/ behind. Accepting it
// would hand the bridge a doc root with no pages and lose every description silently.
func TestUpstreamRepoPathPanicsWhenDocsAbsentFromEmptySubmodule(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir(upstreamDirName, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", upstreamDirName, err)
	}

	defer func() {
		recovered := recover()
		if recovered == nil {
			t.Fatal("upstreamRepoPath() accepted an upstream directory with no docs/")
		}
		if message := fmt.Sprint(recovered); !strings.Contains(message, "git submodule update --init") {
			t.Errorf("panic %q does not name `git submodule update --init`", message)
		}
	}()

	upstreamRepoPath()
}

// The doc root belongs to schema generation alone. Left on Provider(), it costs the runtime
// binary a panic; dropped from TfgenProvider(), the bridge silently loses every description.
func TestOnlyTheTfgenProviderCarriesTheDocRoot(t *testing.T) {
	if got := Provider().UpstreamRepoPath; got != "" {
		t.Errorf("Provider() resolves the doc root to %q; the resource binary must not", got)
	}

	got := TfgenProvider().UpstreamRepoPath
	if got == "" {
		t.Fatal("TfgenProvider() declares no doc root; the bridge would download a module instead")
	}
	upstreamAssertBehaviorDoc(t, got)
}

// Pulumi launches a resource plugin with the user's program directory as its working directory
// (pkg/v3@v3.258.0 resource/plugin/plugin.go:599 sets cmd.Dir), where no upstream checkout exists.
func TestResourceBinaryServesFromAForeignDirectory(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pulumi-resource-filescom")
	build := exec.Command("go", "build", "-ldflags", versionStamp, "-o", binary,
		"./cmd/pulumi-resource-filescom")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the resource binary: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	//nolint:gosec // G204: the path is this test's own build output.
	plugin := exec.CommandContext(ctx, binary)
	plugin.Dir = t.TempDir()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer reader.Close()
	plugin.Stdout, plugin.Stderr = writer, writer

	if err := plugin.Start(); err != nil {
		writer.Close()
		t.Fatalf("start the resource binary: %v", err)
	}
	defer func() { _ = plugin.Process.Kill(); _ = plugin.Wait() }()
	writer.Close()

	greeting := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		if scanner.Scan() {
			greeting <- scanner.Text()
		}
		close(greeting)
	}()

	var first string
	select {
	case line, spoke := <-greeting:
		if !spoke {
			t.Fatalf("the resource binary exited from %s without a word", plugin.Dir)
		}
		first = line
	case <-ctx.Done():
		t.Fatalf("the resource binary said nothing within %s from %s", startupTimeout, plugin.Dir)
	}

	if _, err := strconv.Atoi(strings.TrimSpace(first)); err != nil {
		t.Errorf("the resource binary did not serve from %s; it said %q", plugin.Dir, first)
	}
}

// The split is only safe while the tfgen entry point keeps the guard the resource binary lost.
// Wired to Provider() instead of TfgenProvider(), tfgen regenerates every description away.
func TestTfgenBinaryRefusesAForeignDirectory(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "pulumi-tfgen-filescom")
	build := exec.Command("go", "build", "-ldflags", versionStamp, "-o", binary,
		"./cmd/pulumi-tfgen-filescom")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the tfgen binary: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout)
	defer cancel()
	//nolint:gosec // G204: the path is this test's own build output.
	generate := exec.CommandContext(ctx, binary, "schema")
	generate.Dir = t.TempDir()
	out, err := generate.CombinedOutput()

	if err == nil {
		t.Fatalf("the tfgen binary generated a schema from %s with no upstream checkout", generate.Dir)
	}
	if !strings.Contains(string(out), "git submodule update --init") {
		t.Errorf("the tfgen binary failed from %s without naming the submodule: %s", generate.Dir, out)
	}
}

func upstreamAssertBehaviorDoc(t *testing.T, repoPath string) {
	t.Helper()
	doc := filepath.Join(repoPath, filepath.FromSlash(behaviorDoc))
	if _, err := os.Stat(doc); err != nil {
		t.Errorf("resolved %q but %s is missing: %v", repoPath, doc, err)
	}
}
