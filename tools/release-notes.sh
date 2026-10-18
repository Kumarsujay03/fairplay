#!/bin/sh
# Prints the CHANGELOG.md section for a version, e.g. tools/release-notes.sh v0.1.5
v="${1#v}"
awk -v v="$v" '
  /^## / { if (on) exit; if ($2 == v) { on = 1; next } }
  on { print }
' CHANGELOG.md | sed '/./,$!d'
