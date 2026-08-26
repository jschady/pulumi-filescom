#!/usr/bin/env bash
# Hand-owned. Do not regenerate with ci-mgmt.

set -uo pipefail

usage() {
  cat <<EOF
NAME
  check-make-targets.sh - Checks the Makefile against the agreed target vocabulary.

SYNOPSIS
  $0 [help]

DESCRIPTION
  Both providers answer to the same target names. This script checks that every
  name has a rule, that the shared exports are set, and that the version guard
  rejects a leading "v".
EOF
}

if [[ "${1:-}" == "help" || "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

cd "$(dirname "$0")/.." || exit 1
repo_root=$(pwd)

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

# The shared vocabulary, then the three names this track adds.
targets=(
  ensure tidy lint lint_fix lint_prose provider generate_schema codegen sdk
  generate_nodejs generate_python generate_dotnet generate_go
  build_nodejs build_python build_dotnet build_go
  install_nodejs_sdk install_python_sdk install_dotnet_sdk install_go_sdk
  test_provider test_integration test_examples test_knownissue compile_examples docs
  record_examples test_examples_live
  release_snapshot clean
  tfgen schema_embed upstream
  test_generated converter_plugin prepare_local_workspace
)

exports=(
  PULUMI_IGNORE_AMBIENT_PLUGINS
  PULUMI_DISABLE_AUTOMATIC_PLUGIN_ACQUISITION
  PULUMI_HOME
  PULUMI_LOCAL_NUGET
  PULUMI_BACKEND_URL
  PULUMI_CONFIG_PASSPHRASE
)

check_targets() {
  for target in "${targets[@]}"; do
    # A target name that matches a directory (provider, docs, sdk, upstream) makes an
    # absent rule look successful, so read the rule out of the Makefile itself.
    if ! grep -qE "^${target}[[:space:]]*:" Makefile; then
      fail "the Makefile declares no rule for ${target}"
      continue
    fi
    if ! out=$(make -n "${target}" 2>&1); then
      fail "make -n ${target} exited non-zero: ${out}"
    fi
  done
}

check_exports() {
  for name in "${exports[@]}"; do
    if ! grep -qE "^export[[:space:]]+${name}[[:space:]]*=" Makefile; then
      fail "the Makefile does not export ${name}"
    fi
  done
  # The local backend refuses to open a bucket whose directory is missing, so `pulumi stack
  # init` fails unless the Makefile creates the directory PULUMI_BACKEND_URL names.
  local state_dir
  state_dir=$(sed -nE 's|^export[[:space:]]+PULUMI_BACKEND_URL[[:space:]]*=[[:space:]]*file://.*/||p' Makefile)
  if [[ -z "${state_dir}" ]]; then
    fail "the Makefile does not point PULUMI_BACKEND_URL at a local directory"
    return
  fi
  # The recipe is read here because a directory an earlier run left behind would answer for a
  # mkdir that is no longer there.
  if ! grep -E '^_ :=.*mkdir -p' Makefile | grep -F -- "${state_dir}" >/dev/null; then
    fail "the Makefile does not create ${state_dir}, the directory PULUMI_BACKEND_URL names"
  fi
  make -n help >/dev/null 2>&1
  if [[ ! -d "${state_dir}" ]]; then
    fail "make leaves the directory PULUMI_BACKEND_URL names missing"
  fi
}

check_version_discipline() {
  local out
  if ! grep -q 'pulumictl convert-version --language generic' Makefile; then
    fail "the Makefile does not read the build version through pulumictl"
  fi
  # -B forces the recipe to print. Without it a current bin/ answers "Nothing to be done".
  out=$(make -Bn provider 2>&1)
  if [[ "${out}" != *"-X github.com/jschady/pulumi-filescom/provider/pkg/version.Version="* ]]; then
    fail "make provider does not set the one version symbol: ${out}"
  fi
  if out=$(make -n provider PROVIDER_VERSION=v1.2.3 2>&1); then
    fail "a build version starting with v must be rejected: ${out}"
  fi
}

check_test_commands() {
  local out
  out=$(make -n test_provider 2>&1)
  for flag in "go test" "-race" "-short"; do
    if [[ "${out}" != *"${flag}"* ]]; then
      fail "make test_provider must run ${flag}: ${out}"
    fi
  done
  out=$(make -n tidy 2>&1)
  if [[ "${out}" != *"go mod tidy"* ]]; then
    fail "make tidy must run go mod tidy: ${out}"
  fi
}

# One command has to reach all three checks, so read them out of what ensure runs.
check_ensure_reaches_every_check() {
  local out needle
  out=$(make -n ensure 2>&1)
  for needle in "go mod tidy" "golangci-lint" "-short"; do
    if [[ "${out}" != *"${needle}"* ]]; then
      fail "make ensure must reach ${needle}: ${out}"
    fi
  done
}

# This track has no vendor integration tier, so the target must refuse rather than
# report an empty run as a pass.
check_no_integration_tier() {
  local out
  if out=$(make test_integration 2>&1); then
    fail "test_integration has no tier to run here, so it must not exit 0: ${out}"
    return
  fi
  if [[ "${out}" != *"test_examples"* ]]; then
    fail "make test_integration must send the reader to make test_examples: ${out}"
  fi
}

# The workflows pass LANGUAGE to compile_examples and TESTTAGS to test_examples.
check_example_tags() {
  local out
  out=$(make -n compile_examples LANGUAGE=nodejs 2>&1)
  if [[ "${out}" != *"-tags=nodejs"* ]]; then
    fail "make compile_examples must honor LANGUAGE: ${out}"
  fi
  out=$(make -n compile_examples TESTTAGS=dotnet 2>&1)
  if [[ "${out}" != *"-tags=dotnet"* ]]; then
    fail "make compile_examples must honor TESTTAGS: ${out}"
  fi
  out=$(make -n test_examples LANGUAGE=python 2>&1)
  if [[ "${out}" != *"-tags=python"* ]]; then
    fail "make test_examples must honor LANGUAGE: ${out}"
  fi
  out=$(make -n test_examples TESTTAGS=go 2>&1)
  if [[ "${out}" != *"-tags=go"* ]]; then
    fail "make test_examples must honor TESTTAGS: ${out}"
  fi
  # The acceptance command runs this target with no override, so the default decides how many
  # languages a maintainer's /run-acceptance-tests actually covers.
  out=$(make -n test_examples 2>&1)
  if [[ "${out}" != *"-tags=all"* ]]; then
    fail "make test_examples with no override must run every language: ${out}"
  fi
}

# A cached go-test result replays a live pass as a fresh one, and a retained run directory
# holds the checkpoint with every created secret in it.
check_example_run_hygiene() {
  local target out
  for target in test_examples record_examples test_examples_live test_knownissue; do
    out=$(make -n "${target}" 2>&1)
    if [[ "${out}" != *"-count=1"* ]]; then
      fail "make ${target} must forbid the test cache with -count=1: ${out}"
    fi
    if [[ "${out}" != *"PULUMITEST_RETAIN_FILES_ON_FAILURE=false"* ]]; then
      fail "make ${target} must run with PULUMITEST_RETAIN_FILES_ON_FAILURE=false: ${out}"
    fi
  done
}

# A record run and a live run reach the account, so each one has to refuse an empty key before
# it starts. The mode each recipe sets decides whether the run reads a cassette or the account.
check_credentialed_example_targets() {
  local pair target mode out
  for pair in record_examples:record test_examples_live:live; do
    target=${pair%%:*}
    mode=${pair#*:}
    out=$(make -n "${target}" 2>&1)
    if [[ "${out}" != *'test -n "$FILES_API_KEY"'* ]]; then
      fail "make ${target} must read FILES_API_KEY before it runs the tests: ${out}"
    fi
    if [[ "${out}" != *"exit 1"* ]]; then
      fail "make ${target} must stop when FILES_API_KEY is empty: ${out}"
    fi
    if [[ "${out}" != *"FILESCOM_TEST_MODE=${mode}"* ]]; then
      fail "make ${target} must run in the ${mode} mode: ${out}"
    fi
  done
  # The gated tests record what the account does. A replay run would report a cassette nobody
  # wrote instead of the limitation each test carries.
  out=$(make -n test_knownissue 2>&1)
  if [[ "${out}" != *"FILESCOM_TEST_MODE=live"* ]]; then
    fail "make test_knownissue must run in the live mode: ${out}"
  fi
  # An unset mode variable is the replay mode, and replay is what the pull request runs.
  out=$(make -n test_examples 2>&1)
  if [[ "${out}" == *"FILESCOM_TEST_MODE"* ]]; then
    fail "make test_examples must leave FILESCOM_TEST_MODE unset, which reads the cassettes: ${out}"
  fi
}

# The repository ignores ambient plugins, so a provider on PATH would never be found. Both
# example harnesses name the binary instead, and a PATH entry here would only mislead.
check_no_ambient_plugin_discovery() {
  local hits
  # `make -n` never prints a target-scoped export, so this reads the Makefile itself.
  hits=$(grep -nE 'export[[:space:]]+PATH[[:space:]]*:?=' Makefile)
  if [[ -n "${hits}" ]]; then
    fail "the Makefile must not put the provider on PATH: ${hits}"
  fi
}

# `go test -run` that matches nothing exits 0, so the runner would go quiet if a gated test
# were renamed. Every gated test carries the prefix the runner selects.
check_knownissue_runner() {
  local out file name
  out=$(make -n test_knownissue 2>&1)
  if [[ "${out}" != *"-tags=knownissue,all"* ]]; then
    fail "make test_knownissue must build with both tags: ${out}"
  fi
  if [[ "${out}" != *"-run '^TestKnownIssue'"* ]]; then
    fail "make test_knownissue must select the gated tests: ${out}"
  fi
  local found=0
  while IFS= read -r file; do
    found=1
    while IFS= read -r name; do
      if [[ "${name}" != TestKnownIssue* ]]; then
        fail "${file} declares ${name}, which make test_knownissue never runs"
      fi
    done < <(grep -oE '^func (Test[A-Za-z0-9_]*)' "${file}" | sed 's/^func //')
  done < <(grep -lE '^//go:build .*\bknownissue\b' examples/*.go)
  if ((found == 0)); then
    fail "no gated test file carries the knownissue build tag"
  fi
  # A file that loses the build tag drops out of the runner while its tests still read as
  # gated, and the scan above never opens it, so the same pair is read the other way here.
  while IFS= read -r file; do
    if ! grep -qE '^//go:build .*\bknownissue\b' "${file}"; then
      fail "${file} declares a gated test without the knownissue build tag"
    fi
  done < <(grep -lE '^func TestKnownIssue' examples/*.go)
}

# Each language leg has to reach its own program, or a broken example would pass unseen.
check_compile_examples_reaches_every_program() {
  local language program out
  for language in nodejs:basic-ts python:basic-py dotnet:basic-cs go:basic-go; do
    program=${language#*:}
    out=$(make -n compile_examples LANGUAGE="${language%%:*}" 2>&1)
    if [[ "${out}" != *"examples/${program}"* ]]; then
      fail "make compile_examples LANGUAGE=${language%%:*} must compile examples/${program}: ${out}"
    fi
  done
}

# Without a package pattern go test reads only the top directory, so a nested
# example package would never run.
check_examples_cover_every_package() {
  local target out
  for target in compile_examples test_examples; do
    out=$(make -n "${target}" 2>&1)
    if [[ "${out}" != *"./..."* ]]; then
      fail "make ${target} must name every example package: ${out}"
    fi
  done
}

# provider/shim is a second module. One golangci-lint run cannot reach into it.
check_lint_covers_every_module() {
  local target out dir before after
  for target in lint lint_fix; do
    before=$(shasum Makefile)
    out=$(make "${target}" GOLANGCI_LINT='sh -c pwd --' 2>&1)
    after=$(shasum Makefile)
    for dir in provider provider/shim examples examples/basic-go; do
      if ! grep -Fxq "${repo_root}/${dir}" <<<"${out}"; then
        fail "make ${target} does not run the linter in ${dir}: ${out}"
      fi
    done
    # The target hides the go:embed markers while it runs, then restores them. It must
    # touch only the files under provider/, never the Makefile that holds the same token.
    if [[ "${before}" != "${after}" ]]; then
      fail "make ${target} rewrote the Makefile"
    fi
  done
}

check_upstream_target() {
  local out
  if ! out=$(make upstream 2>&1); then
    fail "make upstream must check out the submodule: ${out}"
    return
  fi
  if [[ ! -f upstream/go.mod ]]; then
    fail "make upstream left the submodule empty"
  fi
}

# A target CI runs but this list never exercises is a target nobody checks.
check_vocabulary_covers_every_target_ci_runs() {
  local invoked
  while IFS= read -r invoked; do
    if ! printf '%s\n' "${targets[@]}" | grep -x "${invoked}" >/dev/null; then
      fail "the workflows run make ${invoked} and this check never exercises it"
    fi
    # Both spellings below, because GitHub runs a workflow named .yaml as readily as one
    # named .yml, and a bare `make` with no target must not enter the comparison as an empty name.
  done < <(cat .github/workflows/*.yml .github/workflows/*.yaml 2>/dev/null |
    sed 's/\${{ matrix.language }}/nodejs/g' |
    grep -o 'run: make [a-z_]\+' | sed 's/^run: make //' | sort -u)
}

# One tool name, so an override reaches every Go command rather than only the test ones.
check_go_commands_share_one_binary() {
  local out
  out=$(make -n compile_examples GO=stubgo 2>&1)
  if [[ "${out}" != *"stubgo vet"* ]]; then
    fail "make compile_examples does not run vet through GO: ${out}"
  fi
  out=$(make -n test_provider GO=stubgo 2>&1)
  if [[ "${out}" != *"stubgo test"* ]]; then
    fail "make test_provider does not run the tests through GO: ${out}"
  fi
}

check_no_dead_version_script() {
  if [[ -e scripts/get-versions.sh ]]; then
    fail "scripts/get-versions.sh emits variables no consumer reads"
  fi
}

check_targets
check_exports
check_version_discipline
check_test_commands
check_ensure_reaches_every_check
check_no_integration_tier
check_example_tags
check_example_run_hygiene
check_credentialed_example_targets
check_no_ambient_plugin_discovery
check_knownissue_runner
check_compile_examples_reaches_every_program
check_examples_cover_every_package
check_lint_covers_every_module
check_upstream_target
check_vocabulary_covers_every_target_ci_runs
check_go_commands_share_one_binary
check_no_dead_version_script

if ((failures > 0)); then
  echo "${failures} check(s) failed." >&2
  exit 1
fi
echo "All Makefile checks passed."
