package shim

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"testing"

	tfpf "github.com/hashicorp/terraform-plugin-framework/provider"
)

const testVersion = "0.0.0-test"

func TestNewProviderReturnsLiveFilesProvider(t *testing.T) {
	p := NewProvider(testVersion)
	if p == nil {
		t.Fatal("NewProvider returned a nil provider.Provider")
	}
	if v := reflect.ValueOf(p); v.Kind() == reflect.Pointer && v.IsNil() {
		t.Fatal("NewProvider returned an interface holding a nil pointer")
	}

	// TypeName is the prefix ProviderInfo.ResourcePrefix must match; drift here silently breaks
	// token mapping and upstream doc lookup, so pin it here rather than only in resources.go.
	resp := &tfpf.MetadataResponse{}
	p.Metadata(context.Background(), tfpf.MetadataRequest{}, resp)
	if resp.TypeName != "files" {
		t.Errorf("TypeName = %q, want %q", resp.TypeName, "files")
	}
	if resp.Version != testVersion {
		t.Errorf("Version = %q, want %q", resp.Version, testVersion)
	}
}

func TestSchemaMarksAPIKeySensitive(t *testing.T) {
	resp := &tfpf.SchemaResponse{}
	NewProvider(testVersion).Schema(context.Background(), tfpf.SchemaRequest{}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Schema returned diagnostics: %v", resp.Diagnostics)
	}

	apiKey, ok := resp.Schema.Attributes["api_key"]
	if !ok {
		t.Fatalf("schema has no api_key attribute; attributes are %v",
			slices.Sorted(maps.Keys(resp.Schema.Attributes)))
	}
	if !apiKey.IsSensitive() {
		t.Error("api_key attribute is not marked sensitive")
	}
}
