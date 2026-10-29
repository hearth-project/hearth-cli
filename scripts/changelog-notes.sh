#!/usr/bin/env bash
# Print the CHANGELOG.md section of one version (without its heading):
#   scripts/changelog-notes.sh 0.2.0
# Matches headings like "## 0.2.0 — 2026-12-01". Prints nothing when the
# version has no section, so the release falls back to generated notes.
set -euo pipefail
cd "$(dirname "$0")/.."
version=${1:?usage: scripts/changelog-notes.sh <version>}
awk -v v="$version" '
  /^## / { if (found) exit; split($0, f, " "); if (f[2] == v) { found = 1; next } }
  found { print }
' CHANGELOG.md | sed -e '/./,$!d'
