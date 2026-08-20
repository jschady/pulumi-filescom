#!/usr/bin/env bash
# Hand-owned. Do not regenerate with ci-mgmt.

set -uo pipefail

usage() {
  cat <<EOF
NAME
  check-sdk-artifacts.sh - Checks the four SDK artifacts a release publishes.

SYNOPSIS
  $0 <directory> <version>

DESCRIPTION
  Reads the directory holding one restored SDK tree per language and fails when a
  language is missing its publishable artifact, when an artifact names another
  package, or when one was built at a different version. Run it before the release
  binaries publish: a release page carrying binaries that no SDK can be published
  against is a release nobody can consume.

  <version> is the release tag. Each language spells a prerelease differently, so
  only the numeric part is compared.
EOF
}

if [[ "${1:-}" == "help" || "${1:-}" == "-h" || "${1:-}" == "--help" || "$#" -ne 2 ]]; then
  usage
  exit 1
fi

root=$1
version=${2#v}
# A Python wheel spells 0.1.0-alpha.1 as 0.1.0a1, so only the release part is comparable.
core=${version%%-*}
core=${core%%+*}

# A final tag must publish final artifacts. Each language spells a prerelease differently, so a
# prerelease tag keeps the prefix compare and a final tag takes the exact spelling of the release.
if [[ "${version}" == "${core}" ]]; then
  node_tail=""
  wheel_tail="-*"
  sdist_tail=".tar.gz"
  nupkg_tail=".nupkg"
else
  node_tail="*"
  wheel_tail="*"
  sdist_tail="*"
  nupkg_tail="*"
fi

failures=0
fail() {
  echo "FAIL: $*" >&2
  failures=$((failures + 1))
}

# first_match prints the first existing path a glob matched, or nothing. It runs inside a
# command substitution, so it must not be the place a failure is counted.
first_match() {
  local candidate
  for candidate in "$@"; do
    if [[ -e "${candidate}" ]]; then
      printf '%s\n' "${candidate}"
      return
    fi
  done
}

check_nodejs() {
  local manifest="${root}/nodejs/bin/package.json"
  if [[ ! -f "${manifest}" ]]; then
    fail "the nodejs SDK has no staged package manifest at ${manifest}"
    return
  fi
  local name published
  name=$(sed -nE 's/.*"name"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' "${manifest}" | head -1)
  published=$(sed -nE 's/.*"version"[[:space:]]*:[[:space:]]*"([^"]+)".*/\1/p' "${manifest}" | head -1)
  [[ "${name}" == "pulumi-filescom" ]] ||
    fail "the nodejs manifest names ${name} and this repository publishes pulumi-filescom"
  [[ "${published}" == "${core}"${node_tail} ]] ||
    fail "the nodejs manifest carries version ${published} and the release is ${core}"
  echo "ok: nodejs publishes ${name} ${published}"
}

check_python() {
  local wheel sdist name
  wheel=$(first_match "${root}"/python/bin/dist/*.whl)
  sdist=$(first_match "${root}"/python/bin/dist/*.tar.gz)
  if [[ -z "${wheel}" ]]; then
    fail "the python SDK has no wheel under ${root}/python/bin/dist"
    return
  fi
  if [[ -z "${sdist}" ]]; then
    fail "the python SDK has no source distribution under ${root}/python/bin/dist"
    return
  fi
  name=$(basename "${wheel}")
  [[ "${name}" == "pulumi_filescom-"* ]] ||
    fail "the python wheel ${name} is not this repository's distribution"
  [[ "${name}" == "pulumi_filescom-${core}"${wheel_tail} ]] ||
    fail "the python wheel ${name} was not built at ${core}"
  # Both files upload, so both are read. A stale source distribution beside a fresh wheel
  # publishes a prerelease under the release tag.
  local archive
  archive=$(basename "${sdist}")
  [[ "${archive}" == "pulumi_filescom-"* ]] ||
    fail "the python source distribution ${archive} is not this repository's distribution"
  [[ "${archive}" == "pulumi_filescom-${core}"${sdist_tail} ]] ||
    fail "the python source distribution ${archive} was not built at ${core}"
  echo "ok: python publishes ${name} and ${archive}"
}

check_dotnet() {
  local package
  package=$(find "${root}/dotnet" -name '*.nupkg' 2>/dev/null | sort | head -1)
  if [[ -z "${package}" ]]; then
    fail "the dotnet SDK has no built package under ${root}/dotnet"
    return
  fi
  local name
  name=$(basename "${package}")
  [[ "${name}" == "Jschady.Filescom."* ]] ||
    fail "the dotnet package ${name} is not this repository's package"
  [[ "${name}" == "Jschady.Filescom.${core}"${nupkg_tail} ]] ||
    fail "the dotnet package ${name} was not built at ${core}"
  echo "ok: dotnet publishes ${name}"
}

check_go() {
  local module="${root}/go/filescom/go.mod"
  if [[ ! -f "${module}" ]]; then
    fail "the go SDK has no module file at ${module}"
    return
  fi
  local declared
  declared=$(sed -nE 's/^module[[:space:]]+(.*)$/\1/p' "${module}" | head -1)
  # The Go SDK publishes by tag, so the module path is the whole of its identity here.
  [[ "${declared}" == "github.com/jschady/pulumi-filescom/sdk/go/filescom" ]] ||
    fail "the go SDK declares module ${declared}"
  echo "ok: go publishes ${declared}"
}

check_nodejs
check_python
check_dotnet
check_go

if ((failures > 0)); then
  echo "${failures} SDK artifact problem(s); no binary should publish." >&2
  exit 1
fi

echo "The four SDK artifacts are ready for ${core}."
