package filescom

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"

	pftfgen "github.com/pulumi/pulumi-terraform-bridge/v3/pkg/pf/tfgen"
	"github.com/pulumi/pulumi-terraform-bridge/v3/pkg/tfbridge"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag"
	"github.com/pulumi/pulumi/sdk/v3/go/common/diag/colors"

	"github.com/jschady/pulumi-filescom/provider/pkg/version"
)

// Provider() parses version.Version, which the linker fills in for every real build and
// leaves empty under `go test`.
func TestMain(m *testing.M) {
	version.Version = "0.0.1"
	os.Exit(m.Run())
}

const (
	committedSchemaPath = "cmd/pulumi-resource-filescom/schema.json"
	entityListPath      = "entities_expected.txt"
	indexModule         = "filescom:index/"
	wantResources       = 60
	wantDataSources     = 80
	// The bridge emits this provider method for every package it generates.
	providerMethodToken = "pulumi:providers:filescom/terraformConfig"
)

// mappingWarning matches the tfgen lines that mean an entity reached the schema without a
// token, which is the failure `make tfgen` must never print.
var mappingWarning = regexp.MustCompile(`unmapped|missing mapping`)

type schemaProperty struct {
	Type                 string `json:"type"`
	Ref                  string `json:"$ref"`
	Description          string `json:"description"`
	Secret               bool   `json:"secret"`
	WillReplaceOnChanges bool   `json:"willReplaceOnChanges"`
	DefaultInfo          *struct {
		Environment []string `json:"environment"`
	} `json:"defaultInfo"`
	Language struct {
		CSharp struct {
			Name string `json:"name"`
		} `json:"csharp"`
	} `json:"language"`
}

// propertyFacets is the comparable projection of a schemaProperty. It drops the pointer field
// equality would compare by address and the description a fresh generation cannot reproduce.
type propertyFacets struct {
	Type                 string
	Ref                  string
	Secret               bool
	WillReplaceOnChanges bool
	CSharpName           string
}

func (p schemaProperty) facets() propertyFacets {
	return propertyFacets{p.Type, p.Ref, p.Secret, p.WillReplaceOnChanges, p.Language.CSharp.Name}
}

type schemaResource struct {
	Description     string                    `json:"description"`
	Properties      map[string]schemaProperty `json:"properties"`
	InputProperties map[string]schemaProperty `json:"inputProperties"`
}

type schemaFunction struct {
	Description string `json:"description"`
}

type packageSchema struct {
	PluginDownloadURL string `json:"pluginDownloadURL"`
	Config            struct {
		Variables map[string]schemaProperty `json:"variables"`
	} `json:"config"`
	Resources map[string]schemaResource `json:"resources"`
	Functions map[string]schemaFunction `json:"functions"`
}

type entityList struct {
	resources   []string
	dataSources []string
}

type generation struct {
	schema []byte
	stdout string
	stderr string
	err    error
}

var (
	generateOnce   sync.Once
	generateResult generation
)

// schemaGenerate runs the in-memory generator once per test binary. A second run over 140
// entities costs more than every other test in this package combined.
func schemaGenerate(t *testing.T) generation {
	t.Helper()
	generateOnce.Do(func() {
		var out, errs bytes.Buffer
		result, err := pftfgen.GenerateSchema(context.Background(), pftfgen.GenerateSchemaOptions{
			ProviderInfo:    Provider(),
			XInMemoryDocs:   true,
			DiagnosticsSink: diag.DefaultSink(&out, &errs, diag.FormatOptions{Color: colors.Never}),
		})
		generateResult = generation{stdout: out.String(), stderr: errs.String(), err: err}
		if result != nil {
			generateResult.schema = result.ProviderMetadata.PackageSchema
		}
	})
	return generateResult
}

func schemaParse(t *testing.T, raw []byte, source string) packageSchema {
	t.Helper()
	var parsed packageSchema
	if err := json.Unmarshal(raw, &parsed); err != nil {
		t.Fatalf("parse %s: %v", source, err)
	}
	return parsed
}

func schemaCommitted(t *testing.T) packageSchema {
	t.Helper()
	raw, err := os.ReadFile(committedSchemaPath)
	if err != nil {
		t.Fatalf("read %s: %v", committedSchemaPath, err)
	}
	return schemaParse(t, raw, committedSchemaPath)
}

func schemaReadEntityList(t *testing.T) entityList {
	t.Helper()
	raw, err := os.ReadFile(entityListPath)
	if err != nil {
		t.Fatalf("read %s: %v", entityListPath, err)
	}
	var list entityList
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kind, name, ok := strings.Cut(line, " ")
		if !ok {
			t.Fatalf("%s: line %q is not `<kind> <name>`", entityListPath, line)
		}
		switch kind {
		case "resource":
			list.resources = append(list.resources, name)
		case "datasource":
			list.dataSources = append(list.dataSources, name)
		default:
			t.Fatalf("%s: unknown kind %q", entityListPath, kind)
		}
	}
	sort.Strings(list.resources)
	sort.Strings(list.dataSources)
	return list
}

// schemaDocEntities reads the entity names the upstream doc pages imply. The bridge finds
// a page by stripping the "files_" prefix, so the page name is the entity name.
func schemaDocEntities(t *testing.T, dir string) []string {
	t.Helper()
	pages, err := filepath.Glob(filepath.Join("..", "upstream", "docs", dir, "*.md"))
	if err != nil {
		t.Fatalf("glob upstream/docs/%s: %v", dir, err)
	}
	names := make([]string, 0, len(pages))
	for _, page := range pages {
		names = append(names, upstreamPrefix+strings.TrimSuffix(filepath.Base(page), ".md"))
	}
	sort.Strings(names)
	return names
}

func schemaAssertSameSet(t *testing.T, what string, got, want []string) {
	t.Helper()
	missing := schemaDifference(want, got)
	extra := schemaDifference(got, want)
	if len(missing) > 0 || len(extra) > 0 {
		t.Errorf("%s: %d missing %v, %d unexpected %v",
			what, len(missing), schemaHead(missing), len(extra), schemaHead(extra))
	}
}

func schemaDifference(from, remove []string) []string {
	drop := make(map[string]bool, len(remove))
	for _, name := range remove {
		drop[name] = true
	}
	var out []string
	for _, name := range from {
		if !drop[name] {
			out = append(out, name)
		}
	}
	return out
}

func schemaHead(names []string) []string {
	if len(names) > 5 {
		return append(append([]string(nil), names[:5]...), "...")
	}
	return names
}

func schemaSortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// Invariant 1: the bridge accepts the mapping. An error-severity diagnostic here is the
// unresolved-ID and unmapped-entity class of failure that stops `make tfgen`.
func TestSchemaGenerationReportsNoErrors(t *testing.T) {
	got := schemaGenerate(t)

	if got.err != nil {
		t.Fatalf("GenerateSchema: %v\n%s", got.err, got.stderr)
	}
	if got.stderr != "" {
		t.Errorf("GenerateSchema wrote error diagnostics:\n%s", got.stderr)
	}
	if match := mappingWarning.FindString(got.stdout); match != "" {
		t.Errorf("GenerateSchema reported %q:\n%s", match, got.stdout)
	}
	if len(got.schema) == 0 {
		t.Error("GenerateSchema returned an empty package schema")
	}
}

// Invariant 2, first half: the committed entity list is what the upstream pages say.
func TestEntityListMatchesUpstreamDocs(t *testing.T) {
	list := schemaReadEntityList(t)

	schemaAssertSameSet(t, "resources", list.resources, schemaDocEntities(t, "resources"))
	schemaAssertSameSet(t, "data sources", list.dataSources, schemaDocEntities(t, "data-sources"))
	if len(list.resources) != wantResources {
		t.Errorf("entity list holds %d resources, want %d", len(list.resources), wantResources)
	}
	if len(list.dataSources) != wantDataSources {
		t.Errorf("entity list holds %d data sources, want %d", len(list.dataSources), wantDataSources)
	}
}

// The doc pipeline can only describe an entity whose page it can find.
func TestUpstreamDocPageExistsForEveryEntity(t *testing.T) {
	list := schemaReadEntityList(t)
	for dir, names := range map[string][]string{
		"resources":    list.resources,
		"data-sources": list.dataSources,
	} {
		for _, name := range names {
			page := filepath.Join("..", "upstream", "docs", dir,
				strings.TrimPrefix(name, upstreamPrefix)+".md")
			info, err := os.Stat(page)
			if err != nil {
				t.Errorf("%s has no upstream page: %v", name, err)
				continue
			}
			if info.Size() == 0 {
				t.Errorf("%s: upstream page %s is empty", name, page)
			}
		}
	}
}

// Invariant 2, second half: every documented entity carries a token in this module.
func TestProviderTokenizesEveryDocumentedEntity(t *testing.T) {
	list := schemaReadEntityList(t)
	prov := Provider()

	schemaAssertSameSet(t, "mapped resources", schemaSortedKeys(prov.Resources), list.resources)
	schemaAssertSameSet(t, "mapped data sources", schemaSortedKeys(prov.DataSources), list.dataSources)
	for _, name := range list.resources {
		if info := prov.Resources[name]; info == nil || !strings.HasPrefix(string(info.Tok), indexModule) {
			t.Errorf("resource %s has no %s token", name, indexModule)
		}
	}
	for _, name := range list.dataSources {
		if info := prov.DataSources[name]; info == nil || !strings.HasPrefix(string(info.Tok), indexModule) {
			t.Errorf("data source %s has no %s token", name, indexModule)
		}
	}
}

// Invariant 2, third half: the committed schema holds exactly those tokens.
func TestCommittedSchemaHoldsEveryToken(t *testing.T) {
	prov := Provider()
	committed := schemaCommitted(t)

	var resourceToks, functionToks []string
	for _, info := range prov.Resources {
		resourceToks = append(resourceToks, string(info.Tok))
	}
	for _, info := range prov.DataSources {
		functionToks = append(functionToks, string(info.Tok))
	}
	functionToks = append(functionToks, providerMethodToken)
	sort.Strings(resourceToks)
	sort.Strings(functionToks)

	schemaAssertSameSet(t, "schema resources", schemaSortedKeys(committed.Resources), resourceToks)
	schemaAssertSameSet(t, "schema functions", schemaSortedKeys(committed.Functions), functionToks)
}

// Invariant 8: the committed file is a `make tfgen` product, version stripped. CI proves
// byte equality by rerunning tfgen; this proves the committed file is not stale.
func TestCommittedSchemaMatchesAFreshGeneration(t *testing.T) {
	got := schemaGenerate(t)
	if got.err != nil {
		t.Fatalf("GenerateSchema: %v", got.err)
	}
	fresh := schemaParse(t, got.schema, "the fresh generation")
	committed := schemaCommitted(t)

	var keyed map[string]json.RawMessage
	committedRaw, readErr := os.ReadFile(committedSchemaPath)
	if readErr != nil {
		t.Fatalf("read %s: %v", committedSchemaPath, readErr)
	}
	if err := json.Unmarshal(committedRaw, &keyed); err != nil {
		t.Fatalf("parse %s: %v", committedSchemaPath, err)
	}
	if _, found := keyed["version"]; found {
		t.Errorf("%s carries a version key", committedSchemaPath)
	}

	schemaAssertSameSet(t, "resource tokens",
		schemaSortedKeys(committed.Resources), schemaSortedKeys(fresh.Resources))
	schemaAssertSameSet(t, "function tokens",
		schemaSortedKeys(committed.Functions), schemaSortedKeys(fresh.Functions))
	for _, tok := range schemaSortedKeys(fresh.Resources) {
		schemaAssertSameProperties(t, tok+" inputs",
			committed.Resources[tok].InputProperties, fresh.Resources[tok].InputProperties)
		schemaAssertSameProperties(t, tok+" outputs",
			committed.Resources[tok].Properties, fresh.Resources[tok].Properties)
	}
}

// The fresh generation runs with XInMemoryDocs and never reads the upstream pages, so this
// compares the property names and every modelled facet except the description.
func schemaAssertSameProperties(t *testing.T, what string, got, want map[string]schemaProperty) {
	t.Helper()
	schemaAssertSameSet(t, what, schemaSortedKeys(got), schemaSortedKeys(want))
	for _, name := range schemaSortedKeys(want) {
		mine, found := got[name]
		if !found {
			continue
		}
		if theirs := want[name]; mine.facets() != theirs.facets() {
			t.Errorf("%s: %s is %+v, want %+v", what, name, mine.facets(), theirs.facets())
		}
	}
}

// Invariant 3. Every consumer installs the plugin from the server the schema advertises, and the
// upgrade test installs the released baseline from it. The source and the artifact are read as a pair.
func TestTheSchemaAdvertisesThisRepositoryAsTheDownloadServer(t *testing.T) {
	const want = "github://api.github.com/jschady/pulumi-filescom"

	if got := Provider().PluginDownloadURL; got != want {
		t.Errorf("provider/resources.go declares the download server %q, want %q", got, want)
	}
	if got := schemaCommitted(t).PluginDownloadURL; got != want {
		t.Errorf("%s advertises the download server %q, want %q", committedSchemaPath, got, want)
	}
}

func TestProviderApiKeyIsSecretWithEnvVarDefault(t *testing.T) {
	apiKey, found := schemaCommitted(t).Config.Variables["apiKey"]
	if !found {
		t.Fatal("the provider config declares no apiKey")
	}

	if !apiKey.Secret {
		t.Error("provider config apiKey is not secret")
	}
	if apiKey.DefaultInfo == nil || len(apiKey.DefaultInfo.Environment) == 0 {
		t.Fatal("provider config apiKey declares no environment default")
	}
	if got := apiKey.DefaultInfo.Environment[0]; got != "FILES_API_KEY" {
		t.Errorf("provider config apiKey reads %q, want FILES_API_KEY", got)
	}
}

// The upstream attributes carrying a RequiresReplace() plan modifier at the pinned submodule
// revision, read out of the pinned upstream provider source.
var replaceForcingAttributes = []string{
	"files_api_key aws_style_credentials",
	"files_api_key permission_set",
	"files_api_key user_id",
	"files_api_key workspace_id",
	"files_api_key path",
	"files_as2_partner as2_station_id",
	"files_as2_station workspace_id",
	"files_automation workspace_id",
	"files_behavior path",
	"files_behavior behavior",
	"files_bundle_notification bundle_id",
	"files_bundle_notification notify_user_id",
	"files_bundle_notification user_id",
	"files_bundle snapshot_id",
	"files_event_target target_type",
	"files_file_comment path",
	"files_file source",
	"files_file md5",
	"files_file size",
	"files_folder mkdir_parents",
	"files_form_field_set user_id",
	"files_gpg_key workspace_id",
	"files_gpg_key user_id",
	"files_gpg_key generate_expires_at",
	"files_gpg_key generate_keypair",
	"files_gpg_key generate_full_name",
	"files_gpg_key generate_email",
	"files_group workspace_id",
	"files_group_user group_id",
	"files_group_user user_id",
	"files_group_user admin",
	"files_lock path",
	"files_lock timeout",
	"files_lock recursive",
	"files_lock exclusive",
	"files_lock allow_access_by_any_user",
	"files_message_comment_reaction emoji",
	"files_message_comment_reaction user_id",
	"files_message_comment user_id",
	"files_message_reaction emoji",
	"files_message_reaction user_id",
	"files_message user_id",
	"files_notification path",
	"files_notification group_id",
	"files_notification group_ids",
	"files_notification user_id",
	"files_notification username",
	"files_partner_channel partner_id",
	"files_partner_channel workspace_id",
	"files_partner_channel_template workspace_id",
	"files_partner workspace_id",
	"files_partner_site_request host_partner_id",
	"files_partner_site_request guest_site_url",
	"files_permission path",
	"files_permission user_id",
	"files_permission username",
	"files_permission group_id",
	"files_permission group_name",
	"files_permission group_ids",
	"files_permission partner_id",
	"files_permission permission",
	"files_permission recursive",
	"files_permission site_id",
	"files_public_key user_id",
	"files_public_key public_key",
	"files_public_key generate_keypair",
	"files_public_key generate_private_key_password",
	"files_public_key generate_algorithm",
	"files_public_key generate_length",
	"files_remote_mount_backend remote_server_mount_id",
	"files_remote_server_credential workspace_id",
	"files_remote_server_credential copy_values_from_credential_id",
	"files_remote_server workspace_id",
	"files_remote_server user_id",
	"files_request path",
	"files_request destination",
	"files_request user_ids",
	"files_request group_ids",
	"files_secret workspace_id",
	"files_share_group user_id",
	"files_snapshot workspace_id",
	"files_sync workspace_id",
	"files_user_additional_email_recipient user_id",
	"files_user_request name",
	"files_user_request email",
	"files_user_request details",
	"files_user_request company",
}

const wantReplaceForcingInputs = 87

// Invariant 4 both ways: a missing mark and an unexpected one each fail the set comparison. The flag
// comes only from an override (v3.137.0 pf/internal/schemashim/attr_schema.go:85, bridge #818).
func TestReplaceForcingInputsAreMarked(t *testing.T) {
	prov := Provider()
	committed := schemaCommitted(t)

	var want []string
	for _, entry := range replaceForcingAttributes {
		name, attribute, ok := strings.Cut(entry, " ")
		if !ok {
			t.Fatalf("%q is not `<resource> <attribute>`", entry)
		}
		resource, found := prov.Resources[name]
		if !found {
			t.Fatalf("the provider maps no %s", name)
		}
		upstream, found := prov.P.ResourcesMap().GetOk(name)
		if !found {
			t.Fatalf("upstream declares no %s", name)
		}
		if _, found := upstream.Schema().GetOk(attribute); !found {
			t.Errorf("%s no longer declares %s", name, attribute)
			continue
		}
		property := tfbridge.TerraformToPulumiNameV2(attribute, upstream.Schema(), resource.Fields)
		want = append(want, string(resource.Tok)+"."+property)
	}

	var got []string
	for _, token := range schemaSortedKeys(committed.Resources) {
		inputs := committed.Resources[token].InputProperties
		for _, property := range schemaSortedKeys(inputs) {
			if inputs[property].WillReplaceOnChanges {
				got = append(got, token+"."+property)
			}
		}
	}

	sort.Strings(want)
	schemaAssertSameSet(t, "replace-forcing inputs", got, want)
	if len(want) != wantReplaceForcingInputs {
		t.Errorf("the expected list holds %d properties, want %d", len(want), wantReplaceForcingInputs)
	}
}

// The upstream page passes `token` to files_lock, which declares only `path`, and the HCL
// conversion that fails on it drops the getLock example from every SDK.
func TestLockExampleLosesTheUnacceptedArgument(t *testing.T) {
	page, err := os.ReadFile(filepath.Join("..", "upstream", "docs", "data-sources", lockDocPage))
	if err != nil {
		t.Fatalf("read the upstream %s: %v", lockDocPage, err)
	}
	if !bytes.Contains(page, []byte(`token = "token"`)) {
		t.Fatalf("the upstream %s no longer passes a token argument; delete the repair rule",
			lockDocPage)
	}
	rules := Provider().DocRules
	if rules == nil || rules.EditRules == nil {
		t.Fatal("the provider declares no doc edit rules")
	}

	sentinel := tfbridge.DocsEdit{Path: "*", Edit: func(_ string, c []byte) ([]byte, error) { return c, nil }}
	edited := page
	applied := 0
	for _, rule := range rules.EditRules([]tfbridge.DocsEdit{sentinel}) {
		match, err := filepath.Match(rule.Path, lockDocPage)
		if err != nil {
			t.Fatalf("edit rule path %q: %v", rule.Path, err)
		}
		if !match {
			continue
		}
		if edited, err = rule.Edit(lockDocPage, edited); err != nil {
			t.Fatalf("edit rule %q on %s: %v", rule.Path, lockDocPage, err)
		}
		applied++
	}

	if applied < 2 {
		t.Errorf("%d edit rules matched %s, want the default rules plus the repair",
			applied, lockDocPage)
	}
	if bytes.Contains(edited, []byte("token = ")) {
		t.Errorf("the edited %s still passes a token argument", lockDocPage)
	}
	if !bytes.Contains(edited, []byte(`path  = "locked_file"`)) {
		t.Errorf("the edited %s lost the example it was supposed to repair", lockDocPage)
	}
}

// Invariant 5.
func TestApiKeySecretsAreSecret(t *testing.T) {
	token := indexModule + "apiKey:ApiKey"
	resource, found := schemaCommitted(t).Resources[token]
	if !found {
		t.Fatalf("the schema holds no %s", token)
	}

	for _, name := range []string{"key", "awsSecretKey"} {
		property, found := resource.Properties[name]
		if !found {
			t.Errorf("%s has no property %s", token, name)
			continue
		}
		if !property.Secret {
			t.Errorf("%s.%s is not secret", token, name)
		}
	}
}

// The .NET compiler rejects a member named after its enclosing type (CS0542), so no
// resource may carry an output the dotnet codegen would name after the resource class.
func TestNoDotnetMemberIsNamedAfterItsClass(t *testing.T) {
	committed := schemaCommitted(t)
	for _, token := range schemaSortedKeys(committed.Resources) {
		class := token[strings.LastIndex(token, ":")+1:]
		outputs := committed.Resources[token].Properties
		for _, name := range schemaSortedKeys(outputs) {
			member := outputs[name].Language.CSharp.Name
			if member == "" {
				member = strings.ToUpper(name[:1]) + name[1:]
			}
			if member == class {
				t.Errorf("%s.%s becomes the .NET member %s of class %s", token, name, member, class)
			}
		}
	}
}

// These three read as instructions to the wrong tool, so DocRules.EditRules repairs the upstream
// page they come from. Nobody hand-edits the generated schema.
var terraformResidue = []string{"Terraform", "HCL", "terraform import"}

// The bridge writes this function's description in Go for every package it generates, so no
// doc edit rule reaches it. TestTheBridgeMethodStillNamesTerraform pins that fact.
const terraformConfigToken = "pulumi:providers:filescom/terraformConfig"

func schemaAssertNoTerraformResidue(t *testing.T, what, description string) {
	t.Helper()
	for _, word := range terraformResidue {
		at := strings.Index(description, word)
		if at < 0 {
			continue
		}
		start := max(at-60, 0)
		end := min(at+len(word)+60, len(description))
		t.Errorf("%s names %q: ...%s...", what, word, description[start:end])
	}
}

func TestNoDescriptionCarriesTerraformResidue(t *testing.T) {
	committed := schemaCommitted(t)

	for _, token := range schemaSortedKeys(committed.Resources) {
		resource := committed.Resources[token]
		schemaAssertNoTerraformResidue(t, token, resource.Description)
		for _, name := range schemaSortedKeys(resource.InputProperties) {
			schemaAssertNoTerraformResidue(t, token+"."+name+" input",
				resource.InputProperties[name].Description)
		}
		for _, name := range schemaSortedKeys(resource.Properties) {
			schemaAssertNoTerraformResidue(t, token+"."+name,
				resource.Properties[name].Description)
		}
	}
	for _, token := range schemaSortedKeys(committed.Functions) {
		if token == terraformConfigToken {
			continue
		}
		schemaAssertNoTerraformResidue(t, token, committed.Functions[token].Description)
	}
}

// The skip above stays honest only while the bridge keeps writing that description. When this
// test fails, the bridge changed and the skip must go.
func TestTheBridgeMethodStillNamesTerraform(t *testing.T) {
	function, found := schemaCommitted(t).Functions[terraformConfigToken]
	if !found {
		t.Fatalf("the schema holds no %s", terraformConfigToken)
	}
	if !strings.Contains(function.Description, "Terraform") {
		t.Error("the bridge method no longer names Terraform; delete the skip that spares it")
	}
}

// A Dynamic property cannot change its runtime type
// (pulumi/pulumi-terraform-bridge#3122), so the upstream promise of a JSON-encoded string is wrong.
func TestTheBehaviorValueDescriptionRefusesTheEncodedString(t *testing.T) {
	token := indexModule + "behavior:Behavior"
	resource, found := schemaCommitted(t).Resources[token]
	if !found {
		t.Fatalf("the schema holds no %s", token)
	}

	for what, properties := range map[string]map[string]schemaProperty{
		"input":  resource.InputProperties,
		"output": resource.Properties,
	} {
		value, found := properties["value"]
		if !found {
			t.Errorf("%s has no %s property value", token, what)
			continue
		}
		if strings.Contains(value.Description, "May be sent as nested JSON") {
			t.Errorf("the %s description still promises the encoded string: %s",
				what, value.Description)
		}
		if !strings.Contains(value.Description, "3122") {
			t.Errorf("the %s description does not cite the bridge issue: %s",
				what, value.Description)
		}
	}
}

// Invariant 6: an empty description means the bridge never found the upstream page.
func TestEveryResourceHasADescription(t *testing.T) {
	committed := schemaCommitted(t)
	if len(committed.Resources) == 0 {
		t.Fatalf("%s declares no resources", committedSchemaPath)
	}

	for _, token := range schemaSortedKeys(committed.Resources) {
		if strings.TrimSpace(committed.Resources[token].Description) == "" {
			t.Errorf("%s has an empty description", token)
		}
	}
}

// Upstream types "id" as a computed int64. The bridge accepts an integer identity only
// through the string type override, which also stringifies the value at runtime.
func TestIntegerIdsAreTypedAsStrings(t *testing.T) {
	prov := Provider()

	var overridden []string
	for name, info := range prov.Resources {
		field := info.Fields["id"]
		if field == nil {
			continue
		}
		if field.Type != "string" {
			t.Errorf("%s maps id to %q, want string", name, field.Type)
			continue
		}
		if field.Name != "" {
			t.Errorf("%s renames id to %q instead of keeping the identity slot", name, field.Name)
		}
		overridden = append(overridden, name)
	}
	if len(overridden) != 56 {
		t.Errorf("%d resources map an integer id, want 56", len(overridden))
	}
}
