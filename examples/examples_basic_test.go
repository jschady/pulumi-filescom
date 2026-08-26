// Copyright 2026, Pulumi Corporation.  All rights reserved.
//go:build yaml || nodejs || python || dotnet || go || all
// +build yaml nodejs python dotnet go all

package examples

import (
	"maps"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pulumi/pulumi/pkg/v3/testing/integration"
)

// The outputs every basic example declares.
var basicOutputNames = []string{"behaviorId", "groupId"}

// basicExampleOptions gives one basic program a throwaway folder, a unique name per object, and
// the cleanup that runs when the program test fails before its own destroy.
func basicExampleOptions(t *testing.T, program string) integration.ProgramTestOptions {
	t.Helper()

	// The folder comes first, so its delete runs after every cleanup that needs it.
	folder := throwawayFolder(t)
	groupName := testObjectName(t, "group")
	behaviorName := testObjectName(t, "behavior")

	// The two sweeps below are the net under a failed destroy on the account. Every mode
	// registers them: a record run puts their calls in the cassette, and a replay run has to
	// issue the same calls or the cassette keeps an interaction nobody used.
	t.Cleanup(func() { deleteGroupsNamed(t, groupName) })
	t.Cleanup(func() { deleteBehaviorsOn(t, folder) })

	return integration.ProgramTestOptions{
		Dir: filepath.Join(getCwd(t), program),
		Config: map[string]string{
			"folderPath":   folder,
			"groupName":    groupName,
			"behaviorName": behaviorName,
		},
		// The roundtrip writes the checkpoint to a file, and the passphrase that protects it
		// is a published constant, so this suite keeps every checkpoint off the disk.
		SkipExportImport: true,
		ExtraRuntimeValidation: func(t *testing.T, stack integration.RuntimeValidationStackInfo) {
			requireBasicExampleOnTheAccount(t, stack, folder, groupName, behaviorName)
		},
	}
}

// requireBasicExampleOnTheAccount reads both resources back from Files.com. The stack outputs
// alone would pass while the provider returned an id the account never gave it.
func requireBasicExampleOnTheAccount(t *testing.T, stack integration.RuntimeValidationStackInfo,
	folder, groupName, behaviorName string,
) {
	t.Helper()
	require.Equal(t, basicOutputNames, slices.Sorted(maps.Keys(stack.Outputs)),
		"the program should export these outputs and nothing else")

	groupID := requireBasicStringOutput(t, stack, "groupId")
	requireGroupNamed(t, groupID, groupName)

	behavior := requireBehaviorOnPath(t, folder)
	require.Equal(t, strconv.FormatInt(behavior.ID, 10),
		requireBasicStringOutput(t, stack, "behaviorId"),
		"the Pulumi id should be the id the account gives the behavior")
	require.Equal(t, behaviorName, behavior.Name,
		"the behavior on the account should carry the configured name")
	require.Equal(t, "webhook", behavior.Behavior,
		"the program should create a webhook behavior")
}

func requireBasicStringOutput(t *testing.T, stack integration.RuntimeValidationStackInfo, name string) string {
	t.Helper()
	value, ok := stack.Outputs[name].(string)
	require.Truef(t, ok, "the %s stack output should be a string, got %T", name, stack.Outputs[name])
	require.NotEmptyf(t, value, "the %s stack output should not be empty", name)
	return value
}
