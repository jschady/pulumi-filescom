// Copyright 2016-2024, Pulumi Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package filescom

import (
	"bytes"
	"context"
	"path"
	"reflect"
	"strings"

	// embed serves the bridge-metadata.json directive below.
	_ "embed"

	"github.com/Files-com/terraform-provider-files/shim"
	pfprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	pfresource "github.com/hashicorp/terraform-plugin-framework/resource"
	pfschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"

	pfbridge "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfbridge"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge/tokens"
	tfshim "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfshim"
	pschema "github.com/pulumi/pulumi/pkg/v3/codegen/schema"

	"github.com/jschady/pulumi-filescom/provider/pkg/version"
)

const (
	mainPkg        = "filescom"
	mainMod        = "index"
	upstreamPrefix = "files_"
	// Files.com publishes terraform-provider-files. The bridge derives the repository from the
	// package name instead. https://github.com/Files-com/terraform-provider-files
	upstreamRepoSuffix = "files"
	lockDocPage        = "lock.md"
	behaviorDocPage    = "behavior.md"
	credentialDocPage  = "remote_server_credential.md"
)

//go:embed cmd/pulumi-resource-filescom/bridge-metadata.json
var metadata []byte

// The bridge assumes the upstream provider is MPL 2.0 and upstream/LICENSE is the MIT License.
// It reads the value through a pointer, so the correction needs an addressable home.
var upstreamLicense = tfbridge.MITLicenseType

// Provider returns additional overlaid schema and metadata associated with the provider.
func Provider() tfbridge.ProviderInfo {
	upstream := shim.NewProvider(version.Version)
	prov := tfbridge.ProviderInfo{
		P:       pfbridge.ShimProvider(upstream),
		Name:    mainPkg,
		Version: version.Version,
		// GetResourcePrefix falls back to Name. Left unset, the doc lookup strips
		// "filescom_" from every entity name and finds no upstream page.
		ResourcePrefix:    "files",
		GitHubOrg:         "Files-com",
		DisplayName:       "Files.com",
		Publisher:         "jschady",
		Description:       "A Pulumi package to create and manage Files.com resources.",
		Keywords:          []string{"pulumi", "filescom", "files.com", "category/cloud"},
		License:           "Apache-2.0",
		TFProviderLicense: &upstreamLicense,
		Homepage:          "https://www.files.com",
		Repository:        "https://github.com/jschady/pulumi-filescom",
		LogoURL:           "https://raw.githubusercontent.com/jschady/pulumi-filescom/main/docs/logo.svg",
		PluginDownloadURL: "github://api.github.com/jschady/pulumi-filescom",
		MetadataInfo:      tfbridge.NewProviderMetadata(metadata),

		// Only the PF bridge reads this; EnableAccurateBridgePreview is deprecated and SDKv2-only.
		EnableAccuratePFBridgePreview: true,

		// One rule per upstream page defect, never a blanket rewrite. Each page name matches under
		// both docs/resources and docs/data-sources, which carry the same prose.
		DocRules: &tfbridge.DocRuleInfo{
			EditRules: func(defaults []tfbridge.DocsEdit) []tfbridge.DocsEdit {
				return append(defaults,
					tfbridge.DocsEdit{Path: lockDocPage, Edit: dropLockExampleToken},
					tfbridge.DocsEdit{Path: behaviorDocPage, Edit: correctBehaviorValueEncoding},
					tfbridge.DocsEdit{Path: credentialDocPage, Edit: dropCredentialTerraformMention},
				)
			},
		},

		// Each rename answers a CS0542 the .NET build reported: the compiler rejects a member
		// named after its enclosing type, and each of these four repeats its resource name.
		Resources: map[string]*tfbridge.ResourceInfo{
			"files_automation": {Fields: map[string]*tfbridge.SchemaInfo{
				"automation": {CSharpName: "AutomationType"},
			}},
			"files_behavior": {Fields: map[string]*tfbridge.SchemaInfo{
				"behavior": {CSharpName: "BehaviorType"},
			}},
			"files_permission": {Fields: map[string]*tfbridge.SchemaInfo{
				"permission": {CSharpName: "PermissionType"},
			}},
			"files_public_key": {Fields: map[string]*tfbridge.SchemaInfo{
				"public_key": {CSharpName: "PublicKeyContents"},
			}},
		},

		Config: map[string]*tfbridge.SchemaInfo{
			"api_key": {
				Secret:  tfbridge.True(),
				Default: &tfbridge.DefaultInfo{EnvVars: []string{"FILES_API_KEY"}},
			},
		},

		JavaScript: &tfbridge.JavaScriptInfo{
			// Unscoped: @pulumi is Pulumi's own npm scope.
			PackageName:          "pulumi-filescom",
			RespectSchemaVersion: true,
		},
		Python: &tfbridge.PythonInfo{
			RespectSchemaVersion: true,
			PyProject:            struct{ Enabled bool }{true},
		},
		Golang: &tfbridge.GolangInfo{
			ImportBasePath: path.Join(
				"github.com/jschady/pulumi-filescom/sdk/",
				tfbridge.GetModuleMajorVersion(version.Version),
				"go",
				mainPkg,
			),
			GenerateResourceContainerTypes: true,
			GenerateExtraInputTypes:        true,
			RespectSchemaVersion:           true,
		},
		CSharp: &tfbridge.CSharpInfo{
			// Pulumi is the first-party NuGet prefix, so the package id is Jschady.Filescom.
			RootNamespace:        "Jschady",
			RespectSchemaVersion: true,
			// Use a wildcard import so NuGet will prefer the latest possible version.
			PackageReferences: map[string]string{
				"Pulumi": "3.*",
			},
		},
	}

	mapIntegerIDs(&prov)

	prov.MustComputeTokens(tokens.SingleModule(upstreamPrefix, mainMod,
		tokens.MakeStandard(mainPkg)))

	prov.MustApplyAutoAliases()
	prov.SetAutonaming(255, "-")
	markReplaceForcingInputs(&prov, upstream)
	addUpstreamLinkCorrection(&prov)

	return prov
}

// TfgenProvider adds the upstream doc root, which only schema generation reads. Resolving it in
// Provider() would panic the resource binary, which Pulumi starts in the user's program directory.
func TfgenProvider() tfbridge.ProviderInfo {
	prov := Provider()
	prov.UpstreamRepoPath = upstreamRepoPath()
	return prov
}

// The bridge admits upstream's computed int64 "id" only through this type override, which
// also decodes it as a string at runtime. Must run before MustComputeTokens.
func mapIntegerIDs(prov *tfbridge.ProviderInfo) {
	if prov.Resources == nil {
		prov.Resources = map[string]*tfbridge.ResourceInfo{}
	}
	prov.P.ResourcesMap().Range(func(name string, upstream tfshim.Resource) bool {
		id, found := upstream.Schema().GetOk("id")
		if !found || id.Type() == tfshim.TypeString {
			return true
		}
		entry := prov.Resources[name]
		if entry == nil {
			entry = &tfbridge.ResourceInfo{}
			prov.Resources[name] = entry
		}
		if entry.Fields == nil {
			entry.Fields = map[string]*tfbridge.SchemaInfo{}
		}
		entry.Fields["id"] = &tfbridge.SchemaInfo{Type: "string"}
		return true
	})
}

// The shim answers ForceNew() with a hardcoded false (pulumi/pulumi-terraform-bridge#818) and the
// bridge warns on SchemaInfo.ForceNew, so the flag lands here, after MustComputeTokens sets tokens.
func markReplaceForcingInputs(prov *tfbridge.ProviderInfo, upstream pfprovider.Provider) {
	ctx := context.Background()
	var provider pfprovider.MetadataResponse
	upstream.Metadata(ctx, pfprovider.MetadataRequest{}, &provider)

	forcing := map[string][]string{}
	for _, newResource := range upstream.Resources(ctx) {
		var named pfresource.MetadataResponse
		var declared pfresource.SchemaResponse
		resource := newResource()
		resource.Metadata(ctx, pfresource.MetadataRequest{ProviderTypeName: provider.TypeName}, &named)
		resource.Schema(ctx, pfresource.SchemaRequest{}, &declared)

		entry, mapped := prov.Resources[named.TypeName]
		if !mapped {
			continue
		}
		fields := prov.P.ResourcesMap().Get(named.TypeName).Schema()
		for attribute, declaration := range declared.Schema.Attributes {
			if !attributeRequiresReplace(declaration) {
				continue
			}
			token := string(entry.Tok)
			forcing[token] = append(forcing[token],
				tfbridge.TerraformToPulumiNameV2(attribute, fields, entry.Fields))
		}
	}

	prov.SchemaPostProcessor = func(spec *pschema.PackageSpec) {
		for token, properties := range forcing {
			inputs := spec.Resources[token].InputProperties
			for _, name := range properties {
				input, declared := inputs[name]
				if !declared {
					panic(token + " declares no input property " + name)
				}
				input.WillReplaceOnChanges = true
				inputs[name] = input
			}
		}
	}
}

// The framework exposes plan modifiers only as opaque interface values, so the RequiresReplace
// family is recognizable by the unexported type name its three constructors all return.
func attributeRequiresReplace(declaration pfschema.Attribute) bool {
	value := reflect.ValueOf(declaration)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return false
	}
	modifiers := value.FieldByName("PlanModifiers")
	if !modifiers.IsValid() || modifiers.Kind() != reflect.Slice {
		return false
	}
	for i := range modifiers.Len() {
		name := reflect.TypeOf(modifiers.Index(i).Interface()).Name()
		if strings.Contains(strings.ToLower(name), "requiresreplace") {
			return true
		}
	}
	return false
}

// The data-sources/lock.md example passes `token`, which files_lock does not declare, so the HCL
// conversion drops the example. resources/lock.md matches the same name and misses on purpose.
func dropLockExampleToken(_ string, content []byte) ([]byte, error) {
	return bytes.Replace(content, []byte("\n  token = \"token\""), nil, 1), nil
}

// A Dynamic property cannot change its runtime type (pulumi/pulumi-terraform-bridge#3122), so the
// second encoding upstream offers creates the behavior and then fails every later plan.
func correctBehaviorValueEncoding(_ string, content []byte) ([]byte, error) {
	const promise = "May be sent as nested JSON or a single JSON-encoded string.  " +
		"If using XML encoding for the API call, this data must be sent as a JSON-encoded string."
	const rule = "Write this property as nested JSON.  A JSON-encoded string creates the " +
		"behavior, and then every later plan fails.  The bridge cannot change the runtime " +
		"type of a Dynamic property (pulumi/pulumi-terraform-bridge#3122)."
	return bytes.ReplaceAll(content, []byte(promise), []byte(rule)), nil
}

// The upstream page tells the reader to reach for Terraform, which is the wrong tool for a
// Pulumi program.
func dropCredentialTerraformMention(_ string, content []byte) ([]byte, error) {
	const mention = "It also enhances security by allowing you to use Terraform or APIs for " +
		"Remote Server management without having to worry about credential exposure."
	const replacement = "It also improves security.  You can manage the Remote Servers with " +
		"Pulumi or with the API, and the credential stays in the vault."
	return bytes.ReplaceAll(content, []byte(mention), []byte(replacement)), nil
}

// addUpstreamLinkCorrection chains onto the post-processor already set. The bridge calls one
// function, so a plain assignment here would drop whatever ran before it.
func addUpstreamLinkCorrection(prov *tfbridge.ProviderInfo) {
	earlier := prov.SchemaPostProcessor
	prov.SchemaPostProcessor = func(spec *pschema.PackageSpec) {
		if earlier != nil {
			earlier(spec)
		}
		correctUpstreamLinks(spec)
	}
}

// correctUpstreamLinks rewrites the upstream repository the bridge names in the attribution and in
// the readmes it writes for npm and PyPI, and returns how many strings it changed.
func correctUpstreamLinks(spec *pschema.PackageSpec) int {
	derived := "terraform-provider-" + mainPkg
	published := "terraform-provider-" + upstreamRepoSuffix
	changed := 0
	if corrected := strings.ReplaceAll(spec.Attribution, derived, published); corrected != spec.Attribution {
		spec.Attribution = corrected
		changed++
	}
	for language, block := range spec.Language {
		corrected := bytes.ReplaceAll(block, []byte(derived), []byte(published))
		if !bytes.Equal(corrected, block) {
			spec.Language[language] = corrected
			changed++
		}
	}
	return changed
}
