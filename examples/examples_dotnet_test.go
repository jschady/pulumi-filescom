// Copyright 2024, Pulumi Corporation.  All rights reserved.
//go:build dotnet || all
// +build dotnet all

package examples

import (
	"testing"

	"github.com/pulumi/pulumi/pkg/v3/testing/integration"
)

func TestBasicCs(t *testing.T) {
	requireFilesAPIKey(t)
	recorderFor(t)

	opts := getCSBaseOptions(t).With(basicExampleOptions(t, "basic-cs"))
	attachProviderToProgramTest(t, &opts)
	integration.ProgramTest(t, &opts)
}
