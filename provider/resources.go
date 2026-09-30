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
	"regexp"
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
					tfbridge.DocsEdit{Path: behaviorDocPage, Edit: describeTheWrappedBehaviorValue},
					tfbridge.DocsEdit{Path: behaviorDocPage, Edit: dropBehaviorTableReference},
					tfbridge.DocsEdit{Path: behaviorDocPage, Edit: pointBehaviorValueAtTheExamples},
					tfbridge.DocsEdit{Path: behaviorDocPage, Edit: dropBehaviorDetailsReference},
					tfbridge.DocsEdit{Path: behaviorDocPage, Edit: repairBehaviorValueFormatSpan},
					tfbridge.DocsEdit{Path: behaviorDocPage, Edit: describeBehaviorValueFormat},
					tfbridge.DocsEdit{Path: behaviorDocPage, Edit: quoteBehaviorValueKeys},
					tfbridge.DocsEdit{Path: behaviorDocPage, Edit: cautionBehaviorImportOfANestedValue},
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

// The upstream value description points at per-type sections that the Pulumi page does not have,
// and it does not say that the value keys keep the API's snake_case names. A snake_case word
// between spaces becomes a per-language span, so "snake_case" ends its sentence.
func describeTheWrappedBehaviorValue(_ string, content []byte) ([]byte, error) {
	const value = "Settings for this behavior. Wrap the value under the selected behavior name. " +
		"See the Behavior sections above for fields and examples."
	const wrapped = "Settings for this behavior, wrapped under the behavior name, for example " +
		"`{ file_expiration: { days_to_retain: 30, delete_empty_folders: false } }`. " +
		"Keep the API's keys in snake_case."
	return bytes.ReplaceAll(content, []byte(value), []byte(wrapped)), nil
}

// The upstream page points at a table of options for each behavior type, and no page has one.
func dropBehaviorTableReference(_ string, content []byte) ([]byte, error) {
	const table = " The exact options for each behavior type are explained in the table below."
	return bytes.ReplaceAll(content, []byte(table), nil), nil
}

// The upstream page says each behavior type shows its fields, and only the examples do.
func pointBehaviorValueAtTheExamples(_ string, content []byte) ([]byte, error) {
	const shown = "The accepted fields and an example are shown with each behavior type."
	const examples = "The Behavior resource examples show the value for each behavior type."
	return bytes.ReplaceAll(content, []byte(shown), []byte(examples)), nil
}

// The upstream page points below at which behaviors non-admins can see or set, and no page lists them.
func dropBehaviorDetailsReference(_ string, content []byte) ([]byte, error) {
	const details = " All the details are below."
	return bytes.ReplaceAll(content, []byte(details), nil), nil
}

// The bridge turns `value_format ` inside a code span into a span of its own, which takes the
// opening backtick and leaves the closing one outside. The rewrite also says the shape the way
// the value_format description does.
func repairBehaviorValueFormatSpan(_ string, content []byte) ([]byte, error) {
	const setting = "Set `value_format = \"typed\"` to return the future typed shape under the " +
		"selected behavior name."
	const split = "Set `value_format` to `\"typed\"` to return it wrapped under the behavior name."
	return bytes.ReplaceAll(content, []byte(setting), []byte(split)), nil
}

// The upstream value_format description names the Terraform path files_behavior.value. A bare
// `typed` becomes `Typed` in .NET. The date is a Files.com plan; upstream checks no date.
func describeBehaviorValueFormat(_ string, content []byte) ([]byte, error) {
	const format = "Set to `typed` to return the future files_behavior.value output shape before it " +
		"becomes the default on March 1, 2027. Omit this attribute to keep the current output until then."
	const typed = "Set to `\"typed\"` to return `value` wrapped under the behavior name. " +
		"Files.com plans to make this the default on March 1, 2027."
	return bytes.ReplaceAll(content, []byte(format), []byte(typed)), nil
}

// Importing a behavior panics the bridge when a nested object sits under the Dynamic value, and
// the upstream Import section offers the command without that condition. The rule appends the
// condition at the end of the section, after the command.
func cautionBehaviorImportOfANestedValue(_ string, content []byte) ([]byte, error) {
	const heading = "\n## Import\n"
	// A code span around a single word such as value becomes a per-language span that .NET
	// capitalizes, so the sentence carries none.
	const crash = "\nImporting a behavior crashes the provider if its value holds a nested object, " +
		"such as webhook headers.\n"
	start := bytes.Index(content, []byte(heading))
	if start < 0 {
		return content, nil
	}
	body := start + len(heading)
	end := len(content)
	if next := bytes.Index(content[body:], []byte("\n## ")); next >= 0 {
		end = body + next
	}
	return append(append(content[:end:end], crash...), content[end:]...), nil
}

var (
	behaviorValueStart = regexp.MustCompile(`^\s+value\s*=\s*\{\s*$`)
	behaviorValueKey   = regexp.MustCompile(`^(\s+)([a-z0-9_]+)(\s*=)`)
)

// The example converter camelCases each key of an object whose type it does not know, and value
// is Dynamic, so the API would receive fileExpiration. It keeps every key of an object that has a
// quoted key (pulumi/pulumi-converter-terraform#451).
func quoteBehaviorValueKeys(_ string, content []byte) ([]byte, error) {
	var out bytes.Buffer
	depth := 0
	for _, line := range bytes.SplitAfter(content, []byte("\n")) {
		if depth > 0 {
			line = behaviorValueKey.ReplaceAll(line, []byte(`$1"$2"$3`))
		}
		if depth > 0 || behaviorValueStart.Match(line) {
			depth += bytes.Count(line, []byte("{")) - bytes.Count(line, []byte("}"))
		}
		out.Write(line)
	}
	return out.Bytes(), nil
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
