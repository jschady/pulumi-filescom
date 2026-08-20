// Copyright 2024, Pulumi Corporation.  All rights reserved.
//go:build nodejs || all
// +build nodejs all

package examples

import (
	"testing"

	"github.com/pulumi/pulumi/pkg/v3/testing/integration"
)

func TestBasicTs(t *testing.T) {
	requireFilesAPIKey(t)

	opts := getJSBaseOptions(t).With(basicExampleOptions(t, "basic-ts"))
	integration.ProgramTest(t, &opts)
}
