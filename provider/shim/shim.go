// Package shim reaches upstream's internal/provider; only a module path nested inside the
// upstream module path makes that import legal.
package shim

import (
	"github.com/Files-com/terraform-provider-files/internal/provider"
	tfpf "github.com/hashicorp/terraform-plugin-framework/provider"
)

func NewProvider(version string) tfpf.Provider { return provider.New(version)() }
