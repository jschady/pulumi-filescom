#!/usr/bin/env bash
# Hand-owned. Do not regenerate with ci-mgmt.

set -uo pipefail

usage() {
  cat <<EOF
NAME
  check-internal-references.sh - Checks the tracked files for references to private build notes.

SYNOPSIS
  $0 [help]

DESCRIPTION
  This repository was built against planning documents that stay private. A comment or a
  message that cites one of them sends a reader to a document they cannot open.

  A tracked file states the reason itself, or it cites something public: an upstream issue
  URL, a file and line of a pinned public repository, or a vendor document.

  Add a name to ALLOWED only when a real word collides with one of the patterns.
EOF
}

if [[ "${1:-}" == "help" || "${1:-}" == "-h" || "${1:-}" == "--help" ]]; then
  usage
  exit 0
fi

cd "$(dirname "$0")/.." || exit 1

# Every pattern is spelled with a bracket around one letter, so the script never carries the
# name it looks for and passes its own check.
NOTES='[S]PEC[ ]*[0-9§]|§[0-9]'
NOTES="${NOTES}"'|[A]RB-[0-9]|[C]HUNKS|[d]ecisions[.]md|[r]ules[.]md|[.]orchestrator'
# A build chunk is a letter and a number, with an optional name after a dash. A review adds R in
# front, a gate uses G with its number, and a spike puts S between the letter and the number.
NOTES="${NOTES}"'|(^|[^A-Za-z0-9_])([R]?[AB][0-9]{1,2}[a-z]?(-[a-z]+)?|[AB]S[0-9]|[C][0-9]|[G][0-9]{1,2})([^A-Za-z0-9_]|$)'
# The writing standard, prefixed and bare. The three letters alone would match "system".
NOTES="${NOTES}"'|([aA][sS][dD][ -]?)?[sS][tT][eE][ -]?100|simplified[ -]technical[ -]english'

# Files.com names a storage vendor and a file-transfer protocol that read like chunk ids.
ALLOWED='Backblaze B2|AS2'
# An empty list makes sed refuse its expression, and every line would then read as clean.
[[ -n "${ALLOWED}" ]] || { echo "ALLOWED is empty, so the check would accept every line." >&2; exit 1; }

hits=$(git grep -n -I -E "${NOTES}" -- . 2>/dev/null)

failures=0
while IFS= read -r hit; do
  [[ -z "${hit}" ]] && continue
  # Remove the collisions first: a line that only matched one of them is not a reference.
  if ! sed -E "s/${ALLOWED}//g" <<<"${hit}" | grep -E "${NOTES}" >/dev/null; then
    continue
  fi
  # One data file holds the whole vendor document on a single line. Report the head of it.
  echo "FAIL: ${hit:0:200}" >&2
  failures=$((failures + 1))
done <<<"${hits}"

scanned=$(git ls-files | wc -l | tr -d ' ')
echo "read ${scanned} tracked files"

if ((failures > 0)); then
  echo "RESULT: ${failures} line(s) name a document that the reader cannot open" >&2
  exit 1
fi

echo "ok: no tracked file names the private build notes"
