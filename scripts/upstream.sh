#!/usr/bin/env bash
# Hand-owned. Do not regenerate with ci-mgmt.

set -euo pipefail

usage() {
  cat <<EOF
NAME
  upstream.sh - Checks out the pinned upstream submodule.

SYNOPSIS
  $0 <init|check|file_target|help>

COMMANDS
  init         Check out the upstream submodule at the commit this repo pins.
  check        Check that the 3 upstream pins hold the same version.
  file_target  Print the path make depends on to decide whether to re-init.
  help         Print this message.

DESCRIPTION
  This repo carries no patch series. The upstream provider is used as
  published, pinned to an exact tag by the 'upstream' submodule gitlink. To
  move to a new upstream release, check out the new tag inside 'upstream',
  then commit the changed gitlink.

  The upstream version is pinned in 3 places: the 'upstream' gitlink,
  'provider/go.mod', and 'provider/shim/go.mod'. Move all 3 together.
EOF
}

init() {
  if [[ ! -d upstream ]]; then
    echo "No 'upstream' directory detected. Skipping init."
    exit 0
  fi
  git submodule update --force --init upstream
}

UPSTREAM_MODULE=github.com/Files-com/terraform-provider-files
PIN_FILES=(provider/go.mod provider/shim/go.mod)

# Prints the upstream version one go.mod requires, or nothing when it names none.
gomod_pin() {
  awk -v module="${UPSTREAM_MODULE}" '$1 == module { print $2; exit }' "$1"
}

# Fails when the gitlink tag and the 2 go.mod requires do not all agree.
check() {
  local tag file pin failures=0

  if [[ ! -e upstream/.git ]]; then
    echo "The upstream submodule is not checked out. Run 'make upstream', then try again." >&2
    exit 1
  fi

  tag=$(git -C upstream describe --tags --exact-match HEAD 2>/dev/null || true)
  if [[ -z "${tag}" ]]; then
    echo "The upstream submodule points at a commit with no tag." >&2
    echo "Check out a release tag inside upstream. Commit the changed gitlink." >&2
    exit 1
  fi
  echo "The upstream gitlink tag is ${tag}."

  for file in "${PIN_FILES[@]}"; do
    if [[ ! -f "${file}" ]]; then
      echo "The ${file} file does not exist." >&2
      failures=$((failures + 1))
      continue
    fi
    pin=$(gomod_pin "${file}")
    if [[ -z "${pin}" ]]; then
      echo "The ${file} file names no ${UPSTREAM_MODULE} version." >&2
      failures=$((failures + 1))
      continue
    fi
    if [[ "${pin}" != "${tag}" ]]; then
      echo "The ${file} file pins ${pin}. It must match the upstream gitlink tag." >&2
      failures=$((failures + 1))
      continue
    fi
    echo "The ${file} file pins ${pin}."
  done

  if ((failures > 0)); then
    echo "${failures} upstream pin(s) disagree. Move all 3 pins to the same version." >&2
    exit 1
  fi
  echo "The 3 upstream pins agree at ${tag}."
}

# Prints the file make watches. Touches it when the gitlink moved, so make re-inits.
file_target() {
  path=.git/modules/upstream/HEAD
  if [[ ! -f "${path}" ]]; then
    exit 0
  fi
  desired_commit=$(git ls-tree HEAD upstream | cut -d ' ' -f3 | cut -f1 || true)
  current_commit=$(cat "${path}")
  if [[ "${desired_commit}" != "${current_commit}" ]]; then
    touch "${path}"
  fi
  echo "${path}"
}

case "${1:-}" in
  init) init ;;
  check) check ;;
  file_target) file_target ;;
  help | -h | --help) usage ;;
  *)
    echo "Error: unknown command \"${1:-}\"." >&2
    usage >&2
    exit 1
    ;;
esac
