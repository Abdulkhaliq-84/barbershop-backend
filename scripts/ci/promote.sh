#!/usr/bin/env bash
# Called only by serialized CD. No force pushes; immutable version tag conflicts
# fail before touching any alias. Repository tag rules also protect other writers.
set -euo pipefail
: "${IMAGE:?}" "${DIGEST:?}" "${TAG:?}" "${GITHUB_SHA:?}"
[[ "$TAG" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] || { echo 'Invalid release tag' >&2; exit 1; }
minor="${BASH_REMATCH[1]}.${BASH_REMATCH[2]}"
[[ "$DIGEST" =~ ^sha256:[a-f0-9]{64}$ ]] || { echo 'Invalid image digest' >&2; exit 1; }
[[ "$GITHUB_SHA" =~ ^[a-f0-9]{40}$ ]] || exit 1
# Resolve both lightweight and annotated remote tags; never trust a stale checkout.
refs=$(git ls-remote --exit-code origin "refs/tags/$TAG" "refs/tags/$TAG^{}")
commit=$(awk '$2 ~ /\^\{\}$/ {print $1; found=1} END {if (!found) print first} NR==1 {first=$1}' <<< "$refs")
[[ "$commit" == "$GITHUB_SHA" ]] || { echo 'Release tag does not identify this commit' >&2; exit 1; }
source=$(bash scripts/ci/image-digest.sh "$IMAGE:sha-$GITHUB_SHA")
[[ "$source" == "$DIGEST" ]] || { echo 'Commit image digest mismatch' >&2; exit 1; }
existing=$(bash scripts/ci/image-digest.sh "$IMAGE:$TAG")
if [[ -n "$existing" && "$existing" != "$DIGEST" ]]; then
  echo 'Refusing to overwrite existing release image tag' >&2
  exit 1
fi
if [[ -z "$existing" ]]; then
  docker buildx imagetools create --prefer-index=false --tag "$IMAGE:$TAG" "$IMAGE@$DIGEST"
fi
[[ "$(bash scripts/ci/image-digest.sh "$IMAGE:$TAG")" == "$DIGEST" ]] || { echo 'Promoted digest mismatch' >&2; exit 1; }
# Rerunning an old delivery may finish its immutable version, but cannot roll
# the moving channels back after a newer commit reaches main.
head=$(git ls-remote --exit-code origin refs/heads/main | awk '{print $1}')
if [[ "$head" == "$GITHUB_SHA" ]]; then
  docker buildx imagetools create --prefer-index=false --tag "$IMAGE:$minor" --tag "$IMAGE:latest" "$IMAGE@$DIGEST"
fi
