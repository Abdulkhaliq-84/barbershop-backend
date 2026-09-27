#!/usr/bin/env bash
# Print an existing manifest digest; only a registry's explicit missing-manifest
# response means absence. Authentication/network/server failures stop delivery.
set -euo pipefail
err=$(mktemp)
trap 'rm -f "$err"' EXIT
if digest=$(docker buildx imagetools inspect "$1" --format '{{.Manifest.Digest}}' 2>"$err"); then
  [[ "$digest" =~ ^sha256:[a-f0-9]{64}$ ]] || { echo 'Invalid registry digest' >&2; exit 1; }
  printf '%s\n' "$digest"
elif grep -Eq '(manifest unknown|: not found)$' "$err"; then
  exit 0
else
  cat "$err" >&2
  exit 1
fi
