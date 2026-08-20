#!/usr/bin/env bash
# Hand-owned. Do not regenerate with ci-mgmt.

set -uo pipefail

usage() {
  cat <<EOF
NAME
  check-package-names.sh - Checks that the package names this repository publishes are free.

SYNOPSIS
  $0 [help]

DESCRIPTION
  Reads three package registries and fails when a name is already taken. Every
  request is a read. Run this immediately before the first publish: a name that
  was free when the release was prepared can be taken by the time it ships, and
  publishing under a name we do not hold gives the next version to whoever does.

  Nothing runs this automatically. Every name is taken by our own packages once
  the first release publishes, so a scheduled run would fail from then on.

  The three URLs below are overridable so the decision this script makes about a
  status code can be exercised without reaching the real registries.
EOF
}

if [[ "${1:-}" == "help" || "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

NPM_URL="${PACKAGE_CHECK_NPM_URL:-https://registry.npmjs.org}"
PYPI_URL="${PACKAGE_CHECK_PYPI_URL:-https://pypi.org}"
NUGET_URL="${PACKAGE_CHECK_NUGET_URL:-https://api.nuget.org}"

# Each entry is "<registry>|<package>|<url>". The package name is what the check reports.
probes=(
  "npm|pulumi-filescom|${NPM_URL}/pulumi-filescom"
  "PyPI|pulumi-filescom|${PYPI_URL}/pypi/pulumi-filescom/json"
  "NuGet|jschady.filescom|${NUGET_URL}/v3-flatcontainer/jschady.filescom/index.json"
)

failures=0

for probe in "${probes[@]}"; do
  registry=${probe%%|*}
  rest=${probe#*|}
  package=${rest%%|*}
  url=${rest#*|}

  status=$(curl -s -o /dev/null -w '%{http_code}' --max-time 30 "${url}")
  case "${status}" in
    404)
      echo "ok: ${package} is free on ${registry}"
      ;;
    200)
      echo "FAIL: ${package} is already published on ${registry}" >&2
      failures=$((failures + 1))
      ;;
    *)
      # A registry that did not answer has said nothing about who holds the name.
      echo "FAIL: ${registry} answered ${status} for ${package}; the name is unread" >&2
      failures=$((failures + 1))
      ;;
  esac
done

if ((failures > 0)); then
  echo "${failures} package name(s) could not be claimed." >&2
  exit 1
fi

echo "All package names are free."
