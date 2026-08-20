#!/usr/bin/env bash
# Hand-owned. Asserts the pinning, secret and publishing rules of the four workflows.
# Run it from anywhere; the lint job runs it on every pull request.

set -uo pipefail

cd "$(dirname "$0")" || exit 1

FAILURES=0

fail() {
  echo "FAIL: $*" >&2
  FAILURES=$((FAILURES + 1))
}

# check runs one check and prints a pass line only when the check added no failure.
check() {
  local label=$1 before=${FAILURES}
  "$2"
  if ((FAILURES == before)); then
    echo "ok: ${label}"
  fi
}

# Every workflow file, read from the directory, in both spellings GitHub runs. A fixed list or a
# single glob leaves a workflow unscanned: nothing reads its action pins, its secrets or its triggers.
WORKFLOWS=()
while IFS= read -r workflow; do
  WORKFLOWS+=("${workflow}")
done < <(ls -1 ./*.yml ./*.yaml 2>/dev/null | sed 's|^\./||' | sort)

# The four this repository is built around. Any other file is scanned by the same rules.
REQUIRED_WORKFLOWS=(pull-request.yml main.yml release.yml upstream-drift.yml)

# Every action these workflows can call, at the version this repository pins.
ALLOWED_USES=(
  "actions/checkout@v7.0.1"
  "actions/setup-go@v7.0.0"
  "actions/setup-node@v7.0.0"
  "actions/setup-python@v7.0.0"
  "actions/setup-dotnet@v6.0.0"
  "actions/upload-artifact@v7.0.1"
  "actions/download-artifact@v8.0.1"
  "NuGet/login@v1.2.0"
  "goreleaser/goreleaser-action@v7.2.3"
  "pypa/gh-action-pypi-publish@v1.14.2"
  "pulumi/provider-version-action@v2.0.0"
  "pulumi/publish-go-sdk-action@v1.2.0"
)

# Infrastructure that belongs to the Pulumi organization. None of it is ours.
FORBIDDEN_REFERENCES=(
  "esc-action"
  "pulumi-ubuntu-8core"
  "api.pulumi-staging.io"
  "get.pulumi.com"
  "pulumi/scripts"
  "ci-mgmt"
)

# A step that matches one of these publishes a package.
PUBLISH_MARKERS=(
  "npm publish"
  "gh-action-pypi-publish"
  "dotnet nuget push"
  "publish-go-sdk-action"
  "release --clean"
)

# Every secret these workflows may read. Anything else is an unreviewed credential.
ALLOWED_SECRETS=(
  "GITHUB_TOKEN"
  "NPM_TOKEN"
  "NUGET_PUBLISH_KEY"
  "FILES_API_KEY"
)

CREDENTIAL_GATE="github.event.pull_request.head.repo.full_name == github.repository"

# The one comparison every generation step runs after itself.
WORKTREE_SCRIPT="./scripts/check-worktree-clean.sh"

# The actionlint release the lint jobs install. Bump the version and the digest together.
ACTIONLINT_PIN=1.7.12
ACTIONLINT_SHA256_PIN=8aca8db96f1b94770f1b0d72b6dddcb1ebb8123cb3712530b08cc387b349a3d8

# Every command the lint job runs against the repository rules.
LINT_JOB_SCRIPTS=(
  "./.github/workflows/check-workflows.sh"
  "./scripts/check-make-targets.sh"
  "./scripts/check-internal-references.sh"
  "make lint_prose"
)

contains() {
  local needle=$1
  shift
  local candidate
  for candidate in "$@"; do
    [[ "${candidate}" == "${needle}" ]] && return 0
  done
  return 1
}

require_file() {
  if [[ -f "$1" ]]; then
    return 0
  fi
  fail "$1 does not exist"
  return 1
}

# trigger_block prints the on: section of a workflow, without the jobs.
trigger_block() {
  sed -n '/^on:/,/^[a-z]/{ /^jobs:/q; p; }' "$1"
}

# lint_job prints the body of the lint job, without the jobs that follow it.
lint_job() {
  awk '/^  lint:$/ { inside = 1; next } inside && /^  [a-z_]+:$/ { exit } inside' "$1"
}

# uncommented drops the lines YAML drops. A commented-out step is not a step, and a check that
# reads the file as text would otherwise accept one.
uncommented() {
  grep -v '^[[:space:]]*#'
}

check_files_exist() {
  local file
  if ((${#WORKFLOWS[@]} == 0)); then
    fail "the workflow directory holds no .yml file, so every check below would read nothing"
    return
  fi
  for file in "${REQUIRED_WORKFLOWS[@]}"; do
    require_file "${file}" || true
  done
  echo "ok: read ${WORKFLOWS[*]}"
}

check_action_pins() {
  local line file ref found=0
  while IFS= read -r line; do
    file=${line%%:*}
    ref=$(printf '%s' "${line#*uses:}" | tr -d '\r' | sed -E 's/^[[:space:]]*//; s/[[:space:]].*//; s/"//g')
    [[ -z "${ref}" ]] && continue
    found=$((found + 1))
    if [[ "${ref}" != *"@"* ]]; then
      fail "${file}: uses ${ref} carries no version"
      continue
    fi
    if ! contains "${ref}" "${ALLOWED_USES[@]}"; then
      fail "${file}: uses ${ref} is not one of the pinned actions"
    fi
  done < <(grep -H 'uses:' "${WORKFLOWS[@]}" 2>/dev/null)
  if ((found == 0)); then
    fail "no uses: line found in any workflow"
  fi
}

check_no_forbidden_references() {
  local needle hit scanned
  # A grep that reads no file finds no hit, so count the lines before trusting it.
  scanned=$(cat "${WORKFLOWS[@]}" 2>/dev/null | wc -l | tr -d ' ')
  if ((scanned == 0)); then
    fail "no workflow content to scan for Pulumi organization infrastructure"
    return
  fi
  for needle in "${FORBIDDEN_REFERENCES[@]}"; do
    hit=$(grep -Hn -F -- "${needle}" "${WORKFLOWS[@]}" 2>/dev/null)
    if [[ -n "${hit}" ]]; then
      fail "reference to ${needle}: ${hit}"
    fi
  done
}

check_release_triggers_on_tags_only() {
  require_file release.yml || return
  local block unwanted
  block=$(trigger_block release.yml)
  grep -q "v\*\.\*\.\*" <<<"${block}" || fail "release.yml does not trigger on v*.*.* tags"
  grep -q "tags:" <<<"${block}" || fail "release.yml has no tags: filter"
  for unwanted in pull_request schedule workflow_dispatch branches; do
    if grep -q "${unwanted}" <<<"${block}"; then
      fail "release.yml also triggers on ${unwanted}; the tag push is its only trigger"
    fi
  done
}

check_main_ignores_tags() {
  require_file main.yml || return
  local block
  block=$(trigger_block main.yml)
  grep -q "tags-ignore:" <<<"${block}" || fail "main.yml has no tags-ignore filter"
  grep -q -- "- v\*" <<<"${block}" || fail "main.yml does not ignore the v* tags"
  grep -q -- "- sdk/\*\*" <<<"${block}" || fail "main.yml does not ignore the sdk/** tags"
}

check_publishing_only_on_release() {
  require_file release.yml || return
  local file marker hit
  for file in "${WORKFLOWS[@]}"; do
    [[ "${file}" == "release.yml" ]] && continue
    require_file "${file}" || continue
    for marker in "${PUBLISH_MARKERS[@]}"; do
      hit=$(grep -Hn -F -- "${marker}" "${file}")
      if [[ -n "${hit}" ]]; then
        fail "${marker} runs outside the release tag trigger: ${hit}"
      fi
    done
  done
  for marker in "${PUBLISH_MARKERS[@]}"; do
    grep -qF -- "${marker}" release.yml || fail "release.yml never runs ${marker}"
  done
}

check_credential_gate() {
  local file
  for file in pull-request.yml main.yml; do
    require_file "${file}" || continue
    grep -qF -- "${CREDENTIAL_GATE}" "${file}" ||
      fail "${file}: the credential job carries no fork gate"
    grep -q "env.FILES_API_KEY == ''" "${file}" ||
      fail "${file}: nothing reports the skip when FILES_API_KEY is absent"
  done
}

check_worktree_clean_steps() {
  local file count guards
  for file in pull-request.yml main.yml release.yml; do
    require_file "${file}" || continue
    count=$(grep -c "Check the worktree is clean" "${file}")
    if ((count < 2)); then
      fail "${file}: ${count} worktree-clean steps; the schema and each SDK need one"
    fi
    # A step name alone proves nothing, so each one must run the shared check.
    guards=$(grep -c "${WORKTREE_SCRIPT}" "${file}")
    if ((guards < count)); then
      fail "${file}: ${count} worktree-clean steps but ${guards} run ${WORKTREE_SCRIPT}"
    fi
  done
  # One copy, so a fix to the comparison reaches every step that runs it.
  for file in "${WORKFLOWS[@]}"; do
    require_file "${file}" || continue
    if grep -q "git status --porcelain" "${file}"; then
      fail "${file} compares the tree inline rather than running ${WORKTREE_SCRIPT}"
    fi
  done
}

# A pull_request_target workflow runs with this repository's token and its secrets. Checking the
# branch out would run a contributor's code there. https://gh.io/securely-using-pull_request_target
check_no_checkout_under_pull_request_target() {
  local file privileged=0
  for file in "${WORKFLOWS[@]}"; do
    require_file "${file}" || continue
    grep -q "pull_request_target" <<<"$(trigger_block "${file}")" || continue
    privileged=$((privileged + 1))
    if grep -qF "uses: actions/checkout" "${file}"; then
      fail "${file} triggers on pull_request_target and checks a branch out"
    fi
  done
  if ((privileged == 0)); then
    fail "no workflow triggers on pull_request_target, so this scan read nothing and the note that tells a maintainer how to run the acceptance tests is gone"
  fi
}

check_secret_names() {
  local line file name found=0
  while IFS= read -r line; do
    file=${line%%:*}
    found=$((found + 1))
    name=$(printf '%s' "${line}" | sed -E 's/.*secrets\.([A-Z_]+).*/\1/')
    if ! contains "${name}" "${ALLOWED_SECRETS[@]}"; then
      fail "${file}: secret ${name} is not one of the allowed secrets"
    fi
  done < <(grep -Ho 'secrets\.[A-Z_]\+' "${WORKFLOWS[@]}" 2>/dev/null | sort -u)
  # Every workflow reads GITHUB_TOKEN, so zero hits means the scan read nothing.
  if ((found == 0)); then
    fail "no secret reference found in any workflow"
  fi
}

check_lint_job_wiring() {
  local file body live read_lines script
  for file in pull-request.yml main.yml; do
    require_file "${file}" || continue
    live=$(uncommented <"${file}")
    body=$(lint_job "${file}" | uncommented)
    # An awk range that matched nothing greps clean, so count the lines before trusting them.
    read_lines=$(grep -c . <<<"${body}")
    if ((read_lines < 5)); then
      fail "${file}: the lint job body read ${read_lines} lines"
      continue
    fi
    for script in "${LINT_JOB_SCRIPTS[@]}"; do
      grep -qF -- "${script}" <<<"${body}" ||
        fail "${file}: the lint job never runs ${script}"
    done
    grep -qF "name: Install actionlint" <<<"${body}" ||
      fail "${file}: the lint job does not install actionlint"
    grep -qF "actionlint_\${ACTIONLINT_VERSION}_linux_amd64.tar.gz" <<<"${body}" ||
      fail "${file}: the install step does not download the pinned release tarball"
    grep -qF "sha256sum -c -" <<<"${body}" ||
      fail "${file}: the install step does not verify the tarball checksum"
    grep -qF "ACTIONLINT_VERSION: ${ACTIONLINT_PIN}" <<<"${live}" ||
      fail "${file}: ACTIONLINT_VERSION is not pinned to ${ACTIONLINT_PIN}"
    grep -qF "ACTIONLINT_SHA256: ${ACTIONLINT_SHA256_PIN}" <<<"${live}" ||
      fail "${file}: ACTIONLINT_SHA256 is not pinned to the release digest"
    # An install step placed after this script runs would leave the check below skipping.
    awk '/Install actionlint/ { i = NR } /check-workflows.sh/ { c = NR } END { exit !(i && c && i < c) }' <<<"${body}" ||
      fail "${file}: the lint job runs check-workflows.sh before it installs actionlint"
  done
}

# check_actionlint reports its own status: the skip must not read as a pass.
check_actionlint() {
  if command -v actionlint >/dev/null 2>&1; then
    if actionlint "${WORKFLOWS[@]}"; then
      echo "ok: actionlint reads every workflow without a complaint"
    else
      fail "actionlint reported a problem"
    fi
  elif [[ "${CI:-}" == "true" ]]; then
    fail "actionlint is absent under CI=true; the lint job's install step is what puts it on PATH"
  else
    echo "skip: actionlint is not installed"
  fi
}

check_files_exist
check "every uses: line carries a pin from the allowed list" check_action_pins
check "no workflow reaches into Pulumi organization infrastructure" check_no_forbidden_references
check "release.yml triggers only on the v*.*.* tags" check_release_triggers_on_tags_only
check "main.yml ignores the v* and sdk/** tags" check_main_ignores_tags
check "only release.yml publishes" check_publishing_only_on_release
check "the credential jobs gate on the fork and on the API key" check_credential_gate
check "the schema job and the SDK jobs check the worktree" check_worktree_clean_steps
check "no pull_request_target workflow checks a branch out" check_no_checkout_under_pull_request_target
check "every secret is one the workflows may read" check_secret_names
check "the lint job installs the pinned actionlint and runs the rule scripts" check_lint_job_wiring
check_actionlint

if ((FAILURES > 0)); then
  echo "${FAILURES} check(s) failed." >&2
  exit 1
fi

echo "All workflow checks passed."
