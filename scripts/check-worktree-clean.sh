#!/usr/bin/env bash
# Hand-owned. Do not regenerate with ci-mgmt.
#
# Fails when a generation step changed a file the repository tracks. Run it from the
# repository, straight after every step that regenerates the schema or an SDK. The
# committed schema and the committed SDKs carry no build version, so a generation at
# any version leaves the tree untouched and there is nothing to exclude here.

set -uo pipefail

changed=$(git status --porcelain --ignore-submodules=all)

if [[ -n "${changed}" ]]; then
  echo "${changed}"
  echo "The generation step changed tracked files. Run the same make target and commit the result." >&2
  exit 1
fi

echo "The generated files match the commit."
