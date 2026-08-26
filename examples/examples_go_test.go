// Copyright 2024, Pulumi Corporation.  All rights reserved.
//go:build go || all
// +build go all

package examples

import (
	"testing"

	"github.com/pulumi/pulumi/pkg/v3/testing/integration"
)

func TestBasicGo(t *testing.T) {
	requireFilesAPIKey(t)
	recorderFor(t)

	opts := getGoBaseOptions(t).With(basicExampleOptions(t, "basic-go"))
	attachProviderToProgramTest(t, &opts)
	integration.ProgramTest(t, &opts)
}
