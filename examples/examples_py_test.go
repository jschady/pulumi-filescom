// Copyright 2024, Pulumi Corporation.  All rights reserved.
//go:build python || all
// +build python all

package examples

import (
	"testing"

	"github.com/pulumi/pulumi/pkg/v3/testing/integration"
)

func TestBasicPy(t *testing.T) {
	requireFilesAPIKey(t)

	opts := getPythonBaseOptions(t).With(basicExampleOptions(t, "basic-py"))
	integration.ProgramTest(t, &opts)
}
