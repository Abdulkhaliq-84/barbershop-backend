#!/usr/bin/env bash
set -euo pipefail
: "${GITHUB_SHA:?}" "${GITHUB_OUTPUT:?}"
# --points-at handles annotated tags too. List published releases once; an API
# outage fails closed instead of silently skipping a release.
published=$(gh api --paginate "repos/$GITHUB_REPOSITORY/releases" --jq '.[] | select(.draft == false and .prerelease == false) | .tag_name')
tags=$(git tag --points-at "$GITHUB_SHA" --list 'v*')
selected=''
while IFS= read -r tag; do
  [[ -n "$tag" ]] || continue
  [[ "$tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo 'Invalid release tag' >&2; exit 1; }
  if grep -Fxq -- "$tag" <<< "$published"; then
    [[ -z "$selected" ]] || { echo 'Ambiguous release tags for commit' >&2; exit 1; }
    selected="$tag"
  fi
done <<< "$tags"
if [[ -n "${CREATED_TAG:-}" && "$selected" != "$CREATED_TAG" ]]; then
  echo 'Created release tag does not match a published release on this commit' >&2
  exit 1
fi
[[ -z "$selected" ]] || echo "tag=$selected" >> "$GITHUB_OUTPUT"
