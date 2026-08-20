# Hand-owned. Do not regenerate with ci-mgmt.

PACK := filescom
ORG := jschady
PROJECT := github.com/$(ORG)/pulumi-$(PACK)
PROVIDER_PATH := provider
VERSION_PATH := $(PROVIDER_PATH)/pkg/version.Version
CODEGEN := pulumi-tfgen-$(PACK)
PROVIDER := pulumi-resource-$(PACK)
TESTPARALLELISM := 4
GOTESTARGS := ""
TESTTAGS ?= all
# One tool name for every Go command, so an override reaches vet as well as test.
GO ?= go
GOTEST ?= $(GO) test
GOLANGCI_LINT ?= golangci-lint

# The workflows pass LANGUAGE to compile_examples and TESTTAGS to test_examples.
LANGUAGE ?=
EXAMPLE_TAGS = $(if $(LANGUAGE),$(LANGUAGE),$(TESTTAGS))
WORKING_DIR := $(shell pwd)
PULUMI_PROVIDER_BUILD_PARALLELISM ?=
PULUMI_CONVERT := 1
PULUMI_MISSING_DOCS_ERROR := false
# tfgen shells out to `pulumi convert`, and PULUMI_DISABLE_AUTOMATIC_PLUGIN_ACQUISITION
# forbids fetching on demand, so the converter has to be installed before the schema run.
CONVERTER_TERRAFORM_VERSION := 1.3.0

# Every module the repository may carry. The wildcard drops a path this checkout lacks.
GO_MODULES := $(dir $(wildcard provider/go.mod provider/shim/go.mod examples/go.mod examples/basic-go/go.mod sdk/go/$(PACK)/go.mod))
LINT_MODULES := $(dir $(wildcard provider/go.mod provider/shim/go.mod examples/go.mod examples/basic-go/go.mod))

# Every test file in examples/ sits behind a build tag, so the linter reads none of them
# without this list.
LINT_BUILD_TAGS := all,knownissue

# Files that lint_prose reads. Override to check one file: make lint_prose PROSE_FILES=x.md
PROSE_FILES ?=

# Override during CI using `make [TARGET] PROVIDER_VERSION=""` or by setting a PROVIDER_VERSION environment variable
# Local & branch builds will just used this fixed default version unless specified
PROVIDER_VERSION ?= 0.1.0-alpha.0+dev
VERSION_GENERIC = $(shell pulumictl convert-version --language generic --version "$(PROVIDER_VERSION)")

# Check version doesn't start with a "v" - this is a common mistake
ifeq ($(shell echo $(PROVIDER_VERSION) | cut -c1),v)
$(error PROVIDER_VERSION should not start with a "v")
endif

# Strips debug information from the provider binary to reduce its size and speed up builds
LDFLAGS_STRIP_SYMBOLS=-s -w
LDFLAGS_PROJ_VERSION=-X $(PROJECT)/$(VERSION_PATH)=$(VERSION_GENERIC)
LDFLAGS_UPSTREAM_VERSION=
LDFLAGS_EXTRAS=
LDFLAGS=$(LDFLAGS_PROJ_VERSION) $(LDFLAGS_UPSTREAM_VERSION) $(LDFLAGS_EXTRAS) $(LDFLAGS_STRIP_SYMBOLS)

# The passphrase protects throwaway local stacks only. It is not a secret.
export PULUMI_IGNORE_AMBIENT_PLUGINS = true
export PULUMI_DISABLE_AUTOMATIC_PLUGIN_ACQUISITION = true
export PULUMI_HOME = $(WORKING_DIR)/.pulumi
export PULUMI_LOCAL_NUGET = $(WORKING_DIR)/nuget
# The local backend refuses to open a bucket whose directory is missing, so the mkdir below
# creates the directory this URL names.
export PULUMI_BACKEND_URL = file://$(WORKING_DIR)/.pulumi-state
export PULUMI_CONFIG_PASSPHRASE = ci-local-passphrase

# A `.make/<name>` sentinel file records when a target last ran, so make can skip it.
# Each phony target delegates to its `.make/` twin, which touches the sentinel at the end.
_ := $(shell mkdir -p .make bin .pulumi/bin .pulumi-state)

# Build the provider and all SDKs and install ready for testing
build: provider build_sdks install_sdks

# Keep aliases for old targets to stay backwards compatible
development: build
only_build: build
# Prepare the workspace for building the provider and SDKs
prepare_local_workspace: upstream
# Creates all generated files which need to be committed
generate: generate_sdks schema
generate_sdks: generate_dotnet generate_go generate_nodejs generate_python
build_sdks: build_dotnet build_go build_nodejs build_python
install_sdks: install_dotnet_sdk install_go_sdk install_nodejs_sdk install_python_sdk
sdk: generate_sdks build_sdks install_sdks
.PHONY: development only_build build generate generate_sdks build_sdks install_sdks sdk

ensure: tidy lint test_provider
.PHONY: ensure

help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Main targets"
	@echo "  build (default)  Build the provider and the four SDKs, then install them for testing."
	@echo "  ensure           Run tidy, lint, and test_provider."
	@echo "  tidy             Tidy every Go module in the repository."
	@echo "  tfgen            Generate the schema from the upstream provider."
	@echo "  provider         Build the provider binary into bin/."
	@echo "  sdk              Generate, build, and install the four SDKs."
	@echo "  clean            Delete the generated files and the local state."
	@echo ""
	@echo "Checks"
	@echo "  lint             Run the Go linter over provider/."
	@echo "  lint_fix         Run the Go linter and fix what it can."
	@echo "  lint_prose       Check the shipped prose."
	@echo "  test_provider    Run the provider tests. These need no network."
	@echo "  compile_examples Compile the example programs. This deploys nothing."
	@echo "  test_examples    Run the example programs against the live API."
	@echo "  test_knownissue  Run the gated tests. Each one records a limitation and fails."
	@echo ""
	@echo "Per language, where [language] is dotnet, go, nodejs, or python"
	@echo "  generate_[language]    Generate the SDK sources for committing."
	@echo "  build_[language]       Build the SDK to check it."
	@echo "  install_[language]_sdk Install the SDK for testing."
	@echo ""
	@echo "More targets"
	@echo "  codegen generate_schema schema schema_embed converter_plugin upstream docs release_snapshot debug_tfgen"
	@echo ""
.PHONY: help

GEN_PULUMI_CONVERT_EXAMPLES_CACHE_DIR := $(WORKING_DIR)/.pulumi/examples-cache

# gen-sdk replaces its language directory wholesale, so no committed file may live inside one
# unless a recipe writes it back afterwards. The Go module files are the only such file.
gen_sdk = pulumi package gen-sdk $(WORKING_DIR)/bin/$(PROVIDER) --language $(1) -o $(WORKING_DIR)/sdk

# Read from the provider so the SDK never claims a different Go or Pulumi version.
SDK_GO_VERSION = $(shell cd $(PROVIDER_PATH) && go list -m -f '{{.GoVersion}}')
SDK_PULUMI_VERSION = $(shell cd $(PROVIDER_PATH) && go list -m -f '{{.Version}}' github.com/pulumi/pulumi/sdk/v3)

# Each build target stages its publishable artifact under the SDK's own `bin/`, which git
# ignores. Nothing a build writes lands in the committed tree.
# The generator downloads the schema's logo URL and writes the response body to logo.png,
# whatever it is. https://github.com/pulumi/pulumi/issues/13589
generate_dotnet: bin/$(PROVIDER)
	$(call gen_sdk,dotnet)
	cp docs/logo.png sdk/dotnet/logo.png
# The generated project declares no version, so a plain build packs the .NET default of 1.0.0.
build_dotnet: DOTNET_VERSION := $(shell pulumictl convert-version --language dotnet --version "$(PROVIDER_VERSION)")
build_dotnet: generate_dotnet
	cd sdk/dotnet && echo "$(DOTNET_VERSION)" > version.txt && dotnet build -p:Version=$(DOTNET_VERSION)
# A package the feed still holds from an earlier build version outranks the one just built.
install_dotnet_sdk: build_dotnet
	mkdir -p $(PULUMI_LOCAL_NUGET)
	rm -f $(PULUMI_LOCAL_NUGET)/*.nupkg
	find sdk/dotnet/bin -name '*.nupkg' -exec cp -p {} $(PULUMI_LOCAL_NUGET) \;
.PHONY: generate_dotnet build_dotnet install_dotnet_sdk

generate_go: bin/$(PROVIDER)
	$(call gen_sdk,go)
	cd sdk/go/$(PACK) && \
		go mod init $(PROJECT)/sdk/go/$(PACK) && \
		go mod edit -go=$(SDK_GO_VERSION) -require=github.com/pulumi/pulumi/sdk/v3@$(SDK_PULUMI_VERSION) && \
		go mod tidy
build_go: generate_go
	cd sdk/go/$(PACK) && go build ./...
install_go_sdk: build_go
	@echo "A Go program reaches sdk/go/$(PACK) by its module path or a replace directive."
.PHONY: generate_go build_go install_go_sdk

generate_nodejs: bin/$(PROVIDER)
	$(call gen_sdk,nodejs)
# The generated package.json carries a $${VERSION} placeholder that npm cannot parse, so the
# staged copy is the one that gets the build version.
build_nodejs: generate_nodejs
	cd sdk/nodejs && \
		yarn install --no-progress && \
		yarn run tsc && \
		cp ../../README.md ../../LICENSE bin/ && \
		sed -e 's/$${VERSION}/$(VERSION_GENERIC)/g' package.json > bin/package.json
install_nodejs_sdk: build_nodejs
	-yarn unlink --cwd sdk/nodejs/bin
	yarn link --cwd sdk/nodejs/bin
.PHONY: generate_nodejs build_nodejs install_nodejs_sdk

generate_python: bin/$(PROVIDER)
	$(call gen_sdk,python)
# pyproject.toml names a README the generator does not write and carries a placeholder version,
# so the wheel is built from a staged copy that has both.
build_python: PYPI_VERSION := $(shell pulumictl convert-version --language python --version "$(PROVIDER_VERSION)")
build_python: generate_python
	cd sdk/python && \
		rm -rf bin venv && \
		python3 -m venv venv && \
		./venv/bin/python -m pip install --quiet build && \
		mkdir -p bin && \
		cp -R pulumi_$(PACK) bin/ && \
		cp ../../README.md bin/ && \
		sed -e 's/^  version = "0.0.0"$$/  version = "$(PYPI_VERSION)"/' pyproject.toml > bin/pyproject.toml && \
		cd bin && ../venv/bin/python -m build --outdir dist .
install_python_sdk: build_python
	@ls sdk/python/bin/dist/*.whl
.PHONY: generate_python build_python install_python_sdk

clean:
	-yarn unlink --cwd sdk/nodejs/bin
	rm -rf sdk/dotnet sdk/nodejs sdk/go sdk/python
	rm -rf bin/*
	rm -rf .make/*
	rm -rf .pulumi-state
	rm -rf "$(GEN_PULUMI_CONVERT_EXAMPLES_CACHE_DIR)"
	if dotnet nuget list source | grep "$(WORKING_DIR)/nuget"; then \
		dotnet nuget remove source "$(WORKING_DIR)/nuget" \
	; fi
.PHONY: clean

tidy:
	for dir in $(GO_MODULES); do (cd $$dir && go mod tidy) || exit 1; done
.PHONY: tidy

# golangci-lint reads one module at a time, so the nested shim module needs its own run.
lint_one = (cd $(1) && $(GOLANGCI_LINT) run -c $(WORKING_DIR)/.golangci.yml --build-tags $(LINT_BUILD_TAGS) $(2)) || LINT_EXIT=$$?;

# The linter detaches a `go:embed` comment from its var, so the marker hides during the run.
define lint_modules
	if git grep -ql 'go:embed' -- provider; then git grep -l 'go:embed' -- provider | xargs perl -i -pe 's/go:embed/ goembed/g'; fi
	LINT_EXIT=0; \
	$(foreach dir,$(LINT_MODULES),$(call lint_one,$(dir),$(1))) \
	if git grep -ql 'goembed' -- provider; then git grep -l 'goembed' -- provider | xargs perl -i -pe 's/ goembed/go:embed/g'; fi; \
	exit $$LINT_EXIT
endef

lint: upstream
	$(call lint_modules,)

# `lint_fix` is a utility target meant to be run manually.
lint_fix: upstream
	$(call lint_modules,--fix)

lint_prose:
	./scripts/lint-prose.sh $(PROSE_FILES)
.PHONY: lint lint_fix lint_prose

build_provider_cmd = set -x; \
cd provider && GOOS=$(1) GOARCH=$(2) go mod download && \
GOOS=$(1) GOARCH=$(2) CGO_ENABLED=0 go build -v $(PULUMI_PROVIDER_BUILD_PARALLELISM) -o "$(3)" -ldflags "$(LDFLAGS)" $(PROJECT)/$(PROVIDER_PATH)/cmd/$(PROVIDER)

provider: bin/$(PROVIDER)

# `make provider_no_deps` skips the schema, so it cannot produce a release binary.
provider_no_deps:
	$(call build_provider_cmd,$(shell go env GOOS),$(shell go env GOARCH),$(WORKING_DIR)/bin/$(PROVIDER))
bin/$(PROVIDER): .make/schema
	$(call build_provider_cmd,$(shell go env GOOS),$(shell go env GOARCH),$(WORKING_DIR)/bin/$(PROVIDER))
.PHONY: provider provider_no_deps

test: test_examples
.PHONY: test

test_provider:
	cd provider && $(GOTEST) -race -short ./...
.PHONY: test_provider

# This check regenerates the schema and the four SDKs, which needs the Pulumi CLI and the
# converter plugin, so `-short` keeps it out of test_provider.
test_generated:
	cd provider && $(GOTEST) -count=1 -run TestGeneratingTheSDKsLeavesTheTreeClean ./...
.PHONY: test_generated

# This repository has no vendor integration tier. The bridge exposes no in-process
# lifecycle harness, so the vendor calls happen in test_examples instead.
test_integration:
	@echo "make test_integration runs nothing here. Run make test_examples for the vendor tests."; exit 1
.PHONY: test_integration

# A cached pass of a live run reads exactly like a fresh one, so the count switch forbids the
# cache. The retain switch keeps a failed run's checkpoint, which holds secrets, off the disk.
example_test_env = PULUMITEST_RETAIN_FILES_ON_FAILURE=false
example_test_flags = -count=1 -v -tags=$(1) -parallel $(TESTPARALLELISM) -timeout 2h
test_examples:
	cd examples && $(example_test_env) $(GOTEST) $(call example_test_flags,$(EXAMPLE_TAGS)) ./... $(value GOTESTARGS)
.PHONY: test_examples

# The gated tests record a limitation, so they fail on purpose and stay out of test_examples.
# The `all` tag rides along because the shared helpers behind it are what they call.
KNOWNISSUE_TAGS := knownissue,all
test_knownissue:
	cd examples && $(example_test_env) $(GOTEST) $(call example_test_flags,$(KNOWNISSUE_TAGS)) -run '^TestKnownIssue' ./... $(value GOTESTARGS)
.PHONY: test_knownissue

EXAMPLE_LANGUAGES := nodejs python dotnet go
COMPILE_EXAMPLE_TARGETS = $(if $(filter all,$(EXAMPLE_TAGS)),$(addprefix compile_,$(addsuffix _example,$(EXAMPLE_LANGUAGES))),compile_$(EXAMPLE_TAGS)_example)

# Compiles the example programs against the installed SDKs and deploys nothing. `go vet` builds
# the test package without running it, so no TestMain reaches the vendor API here.
compile_examples: $(COMPILE_EXAMPLE_TARGETS)
	cd examples && $(GO) vet -tags=$(EXAMPLE_TAGS) ./...

compile_nodejs_example:
	cd examples/basic-ts && yarn install --no-progress && \
		{ yarn link pulumi-filescom || { echo "Run make install_nodejs_sdk first."; exit 1; }; } && \
		yarn run tsc --noEmit

compile_python_example:
	cd examples/basic-py && rm -rf venv && python3 -m venv venv && \
		./venv/bin/python -m pip install --quiet -r requirements.txt ../../sdk/python/bin && \
		./venv/bin/python -c "import pulumi_filescom" && \
		./venv/bin/python -m compileall -q __main__.py

compile_dotnet_example:
	cd examples/basic-cs && dotnet build

compile_go_example:
	cd examples/basic-go && go build -o /dev/null ./...
.PHONY: compile_examples compile_nodejs_example compile_python_example compile_dotnet_example compile_go_example

docs:
	@test -f docs/_index.md || { echo "make docs needs docs/_index.md. Restore it from the repository."; exit 1; }
	@echo "The registry pages here are hand-written. The bridge converts the upstream pages during make tfgen."
.PHONY: docs

release_snapshot:
	goreleaser release --snapshot --clean
.PHONY: release_snapshot

tfgen: schema
generate_schema: schema
codegen: tfgen generate_schema
schema: .make/schema
# Kept for backwards compatibility. It does have dependencies.
tfgen_no_deps: .make/schema
.make/schema: export PULUMI_CONVERT := $(PULUMI_CONVERT)
.make/schema: export PULUMI_CONVERT_EXAMPLES_CACHE_DIR := $(GEN_PULUMI_CONVERT_EXAMPLES_CACHE_DIR)
.make/schema: export PULUMI_MISSING_DOCS_ERROR := $(PULUMI_MISSING_DOCS_ERROR)
.make/schema: bin/$(CODEGEN) .make/upstream .make/converter_plugin
	$(WORKING_DIR)/bin/$(CODEGEN) schema --out provider/cmd/$(PROVIDER)
	@touch $@
tfgen_build_only: bin/$(CODEGEN)
bin/$(CODEGEN): provider/*.go provider/go.* .make/upstream
	@test -f provider/upstream_path.go || { echo "$@ needs provider/upstream_path.go. Restore it from the repository."; exit 1; }
	(cd provider && go build $(PULUMI_PROVIDER_BUILD_PARALLELISM) -o $(WORKING_DIR)/bin/$(CODEGEN) -ldflags "$(LDFLAGS_PROJ_VERSION) $(LDFLAGS_EXTRAS)" $(PROJECT)/$(PROVIDER_PATH)/cmd/$(CODEGEN))
# The compiler embeds the schema through the go:embed line in main.go, so this target
# checks that contract instead of running a generator of its own.
schema_embed:
	@test -s provider/cmd/$(PROVIDER)/schema.json || { echo "make schema_embed needs provider/cmd/$(PROVIDER)/schema.json. Run make tfgen."; exit 1; }
	@grep -q '"resources"' provider/cmd/$(PROVIDER)/schema.json || { echo "provider/cmd/$(PROVIDER)/schema.json describes no resources. Run make tfgen."; exit 1; }
	@grep -q 'go:embed schema.json' provider/cmd/$(PROVIDER)/main.go || { echo "provider/cmd/$(PROVIDER)/main.go no longer embeds schema.json."; exit 1; }
	@echo "provider/cmd/$(PROVIDER)/schema.json is embedded by provider/cmd/$(PROVIDER)/main.go."
.PHONY: tfgen generate_schema codegen schema tfgen_no_deps tfgen_build_only schema_embed

# Install the pinned converter the example conversion needs.
converter_plugin: .make/converter_plugin
.make/converter_plugin:
	pulumi plugin install converter terraform $(CONVERTER_TERRAFORM_VERSION)
	@touch $@
.PHONY: converter_plugin

# Check out the pinned upstream submodule, if it exists
upstream: .make/upstream
# Re-run when the pinned upstream commit changes.
.make/upstream: $(shell ./scripts/upstream.sh file_target)
	./scripts/upstream.sh init
	@touch $@
.PHONY: upstream

# Start debug server for tfgen
debug_tfgen:
	dlv  --listen=:2345 --headless=true --api-version=2  exec $(WORKING_DIR)/bin/$(CODEGEN) -- schema --out provider/cmd/$(PROVIDER)
.PHONY: debug_tfgen
