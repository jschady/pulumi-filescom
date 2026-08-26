// Copyright 2024, Pulumi Corporation.  All rights reserved.
package examples

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/testing/integration"
)

func getJSBaseOptions(t *testing.T) integration.ProgramTestOptions {
	t.Helper()
	base := getBaseOptions(t)
	baseJS := base.With(integration.ProgramTestOptions{
		Dependencies: []string{
			"pulumi-filescom",
		},
	})

	return baseJS
}

func getPythonBaseOptions(t *testing.T) integration.ProgramTestOptions {
	t.Helper()
	base := getBaseOptions(t)
	basePython := base.With(integration.ProgramTestOptions{
		Dependencies: []string{
			filepath.Join("..", "sdk", "python", "bin"),
		},
	})

	return basePython
}

func getGoBaseOptions(t *testing.T) integration.ProgramTestOptions {
	t.Helper()
	goDepRoot := os.Getenv("PULUMI_GO_DEP_ROOT")
	if goDepRoot == "" {
		var err error
		goDepRoot, err = filepath.Abs("../..")
		require.NoError(t, err)
	}
	rootSdkPath, err := filepath.Abs("../sdk/go/filescom")
	require.NoError(t, err)

	base := getBaseOptions(t)
	baseJS := base.With(integration.ProgramTestOptions{
		Dependencies: []string{
			fmt.Sprintf("github.com/jschady/pulumi-filescom/sdk/go/filescom=%s", rootSdkPath),
		},
		Env: []string{
			fmt.Sprintf("PULUMI_GO_DEP_ROOT=%s", goDepRoot),
		},
	})

	return baseJS
}

func getCSBaseOptions(t *testing.T) integration.ProgramTestOptions {
	t.Helper()
	base := getBaseOptions(t)
	baseJS := base.With(integration.ProgramTestOptions{
		Dependencies: []string{
			"Jschady.Filescom",
		},
	})

	return baseJS
}

func getCwd(t *testing.T) string {
	cwd, err := os.Getwd()
	if err != nil {
		t.FailNow()
	}

	return cwd
}

// liveModeName is the value of FILESCOM_TEST_MODE that runs the legs against the account. This file
// compiles under every tag and the mode helpers do not, so it carries the one value it reads.
const liveModeName = "live"

func getBaseOptions(t *testing.T) integration.ProgramTestOptions {
	t.Helper()
	binPath, err := filepath.Abs("../bin")
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("Using binPath %s\n", binPath)
	return integration.ProgramTestOptions{
		LocalProviders: []integration.LocalDependency{
			{
				Package: "filescom",
				Path:    binPath,
			},
		},
		// A record run and a replay run install one recorder for the whole process, so the legs
		// run one at a time. A live run keeps the parallel legs. The variable is read here by
		// name because this file compiles under every tag and the mode helpers do not.
		NoParallel: os.Getenv("FILESCOM_TEST_MODE") != liveModeName,
	}
}

// TestTheLegsRunOneAtATimeUnlessLive pins the option that keeps the recorded legs serial. Parallel
// legs would write each other's calls into one cassette.
func TestTheLegsRunOneAtATimeUnlessLive(t *testing.T) {
	for mode, serial := range map[string]bool{"": true, "replay": true, "record": true, liveModeName: false} {
		t.Setenv("FILESCOM_TEST_MODE", mode)
		require.Equalf(t, serial, getBaseOptions(t).NoParallel, "the mode %q", mode)
	}
}
