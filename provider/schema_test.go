package filescom

import (
	"bytes"
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
	indexModule         = "filescom:index/"
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

// upstreamResourceSource is what one upstream `<name>_resource.go` declares: the Terraform type
// name its Metadata method builds, the schema kind of every top-level attribute of its Schema
// method, and the top-level attributes a RequiresReplace() plan modifier marks.
type upstreamResourceSource struct {
	typeName       string
	attributeKinds map[string]string
	replaceForcing map[string]bool
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

// schemaReadEntityList answers the entities the upstream doc pages declare. The bridge maps an
// entity only when it finds the page, so the two directories are the list.
func schemaReadEntityList(t *testing.T) entityList {
	t.Helper()
	list := entityList{
		resources:   schemaDocEntities(t, "resources"),
		dataSources: schemaDocEntities(t, "data-sources"),
	}
	if len(list.resources) == 0 || len(list.dataSources) == 0 {
		t.Fatalf("upstream/docs holds %d resource pages and %d data source pages",
			len(list.resources), len(list.dataSources))
	}
	return list
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

// upstreamResourceSources parses every resource file of the pinned upstream submodule. The scan
// stays inside the Schema method, so the prior schema of a state upgrader never reaches it.
func upstreamResourceSources(t *testing.T) []upstreamResourceSource {
	t.Helper()
	pattern := filepath.Join("..", "upstream", "internal", "provider", "*_resource.go")
	files, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(files) == 0 {
		t.Fatalf("%s matched no upstream resource file", pattern)
	}

	// literal drops the address-of a composite literal carries, and answers nil for anything else.
	literal := func(expr ast.Expr) *ast.CompositeLit {
		if address, ok := expr.(*ast.UnaryExpr); ok {
			expr = address.X
		}
		composite, _ := expr.(*ast.CompositeLit)
		return composite
	}
	// member answers the value of one keyed element of a composite literal, and never descends.
	// A nested attribute keeps its own Attributes and PlanModifiers out of reach that way.
	member := func(composite *ast.CompositeLit, key string) ast.Expr {
		if composite == nil {
			return nil
		}
		for _, element := range composite.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if name, ok := pair.Key.(*ast.Ident); ok && name.Name == key {
				return pair.Value
			}
		}
		return nil
	}

	sources := make([]upstreamResourceSource, 0, len(files))
	for _, file := range files {
		parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		// Each file declares one resource, so the method name identifies the declaration.
		methods := map[string]*ast.FuncDecl{}
		for _, declaration := range parsed.Decls {
			if method, ok := declaration.(*ast.FuncDecl); ok && method.Recv != nil {
				methods[method.Name.Name] = method
			}
		}

		source := upstreamResourceSource{
			attributeKinds: map[string]string{},
			replaceForcing: map[string]bool{},
		}
		metadata, found := methods["Metadata"]
		if !found {
			t.Fatalf("%s declares no Metadata method", file)
		}
		// The Metadata method writes `req.ProviderTypeName + "_<name>"`, and the provider type
		// name is the prefix this package already strips off an entity.
		ast.Inspect(metadata.Body, func(node ast.Node) bool {
			sum, ok := node.(*ast.BinaryExpr)
			if !ok || sum.Op != token.ADD {
				return true
			}
			suffix, ok := sum.Y.(*ast.BasicLit)
			if !ok || suffix.Kind != token.STRING {
				return true
			}
			name, err := strconv.Unquote(suffix.Value)
			if err != nil {
				t.Fatalf("%s: the Metadata method holds %s: %v", file, suffix.Value, err)
			}
			source.typeName = upstreamPrefix + strings.TrimPrefix(name, "_")
			return false
		})
		if source.typeName == "" {
			t.Fatalf("%s: the Metadata method sets no Terraform type name", file)
		}

		declare, found := methods["Schema"]
		if !found {
			t.Fatalf("%s declares no Schema method", file)
		}
		var declared *ast.CompositeLit
		ast.Inspect(declare.Body, func(node ast.Node) bool {
			assign, ok := node.(*ast.AssignStmt)
			if !ok || len(assign.Lhs) != 1 || len(assign.Rhs) != 1 {
				return true
			}
			target, ok := assign.Lhs[0].(*ast.SelectorExpr)
			if !ok || target.Sel.Name != "Schema" {
				return true
			}
			declared = literal(assign.Rhs[0])
			if declared != nil {
				return false
			}
			// Two resources build the schema in a helper method and return it.
			call, ok := assign.Rhs[0].(*ast.CallExpr)
			if !ok {
				return false
			}
			callee, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return false
			}
			helper, found := methods[callee.Sel.Name]
			if !found {
				t.Fatalf("%s: the Schema method calls %s, which the file does not declare",
					file, callee.Sel.Name)
			}
			ast.Inspect(helper.Body, func(inner ast.Node) bool {
				returned, ok := inner.(*ast.ReturnStmt)
				if !ok || len(returned.Results) != 1 {
					return true
				}
				declared = literal(returned.Results[0])
				return false
			})
			return false
		})

		attributes := literal(member(declared, "Attributes"))
		if attributes == nil {
			t.Fatalf("%s: the Schema method declares no top-level Attributes map", file)
		}
		for _, element := range attributes.Elts {
			pair, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := pair.Key.(*ast.BasicLit)
			if !ok || key.Kind != token.STRING {
				continue
			}
			attribute, err := strconv.Unquote(key.Value)
			if err != nil {
				t.Fatalf("%s: the attribute key %s: %v", file, key.Value, err)
			}
			declaration := literal(pair.Value)
			if declaration == nil {
				t.Fatalf("%s: the attribute %s is not a schema literal", file, attribute)
			}
			kind, ok := declaration.Type.(*ast.SelectorExpr)
			if !ok {
				t.Fatalf("%s: the attribute %s names no schema kind", file, attribute)
			}
			source.attributeKinds[attribute] = kind.Sel.Name
			modifiers := literal(member(declaration, "PlanModifiers"))
			if modifiers == nil {
				continue
			}
			for _, modifier := range modifiers.Elts {
				call, ok := modifier.(*ast.CallExpr)
				if !ok {
					continue
				}
				if callee, ok := call.Fun.(*ast.SelectorExpr); ok &&
					callee.Sel.Name == "RequiresReplace" {
					source.replaceForcing[attribute] = true
				}
			}
		}
		sources = append(sources, source)
	}
	return sources
}

// upstreamReplaceForcingAttributes answers the `<type> <attribute>` entries the upstream source
// marks. `markReplaceForcingInputs` in provider/resources.go walks the same top level, so the
// committed schema must mark exactly these inputs.
func upstreamReplaceForcingAttributes(t *testing.T) []string {
	t.Helper()
	var entries []string
	for _, source := range upstreamResourceSources(t) {
		for _, attribute := range schemaSortedKeys(source.replaceForcing) {
			entries = append(entries, source.typeName+" "+attribute)
		}
	}
	sort.Strings(entries)
	return entries
}

// upstreamIntegerIDResources answers the resources whose top-level `id` attribute is not a string.
// `mapIntegerIDs` in provider/resources.go overrides the Pulumi type for exactly these.
func upstreamIntegerIDResources(t *testing.T) []string {
	t.Helper()
	var names []string
	for _, source := range upstreamResourceSources(t) {
		if kind, found := source.attributeKinds["id"]; found && kind != "StringAttribute" {
			names = append(names, source.typeName)
		}
	}
	sort.Strings(names)
	return names
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

// The doc pipeline can only describe an entity whose page it can find, and an empty page
// describes nothing.
func TestUpstreamDocPageExistsForEveryEntity(t *testing.T) {
	for _, dir := range []string{"resources", "data-sources"} {
		names := schemaDocEntities(t, dir)
		if len(names) == 0 {
			t.Fatalf("upstream/docs/%s holds no page", dir)
		}
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

// Invariant 2, first half: every documented entity carries a token in this module.
func TestProviderTokenizesEveryDocumentedEntity(t *testing.T) {
	resources := schemaDocEntities(t, "resources")
	dataSources := schemaDocEntities(t, "data-sources")
	if len(resources) == 0 || len(dataSources) == 0 {
		t.Fatalf("upstream/docs holds %d resource pages and %d data source pages",
			len(resources), len(dataSources))
	}
	prov := Provider()

	schemaAssertSameSet(t, "mapped resources", schemaSortedKeys(prov.Resources), resources)
	schemaAssertSameSet(t, "mapped data sources", schemaSortedKeys(prov.DataSources), dataSources)
	for _, name := range resources {
		if info := prov.Resources[name]; info == nil || !strings.HasPrefix(string(info.Tok), indexModule) {
			t.Errorf("resource %s has no %s token", name, indexModule)
		}
	}
	for _, name := range dataSources {
		if info := prov.DataSources[name]; info == nil || !strings.HasPrefix(string(info.Tok), indexModule) {
			t.Errorf("data source %s has no %s token", name, indexModule)
		}
	}
}

// Invariant 2, second half: the committed schema holds exactly those tokens.
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

// Invariant 4 both ways: a missing mark and an unexpected one each fail the set comparison. The flag
// comes only from an override (v3.137.0 pf/internal/schemashim/attr_schema.go:85, bridge #818).
func TestReplaceForcingInputsAreMarked(t *testing.T) {
	prov := Provider()
	committed := schemaCommitted(t)

	entries := upstreamReplaceForcingAttributes(t)
	if len(entries) == 0 {
		t.Fatal("the upstream source marks no attribute with RequiresReplace()")
	}

	var want []string
	for _, entry := range entries {
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
	if len(overridden) == 0 {
		t.Fatal("the provider overrides no integer id")
	}

	sort.Strings(overridden)
	schemaAssertSameSet(t, "integer-id resources", overridden, upstreamIntegerIDResources(t))
}
