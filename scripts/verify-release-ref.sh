#!/usr/bin/env sh
set -eu

tag="${1:?release tag is required}"
sha="${2:?release SHA is required}"

tag_commit="$(git rev-parse --verify --end-of-options "${tag}^{commit}")" || {
  echo "release tag does not resolve to a commit: $tag" >&2
  exit 1
}
sha_commit="$(git rev-parse --verify --end-of-options "${sha}^{commit}")" || {
  echo "release SHA does not resolve to a commit: $sha" >&2
  exit 1
}

if [ "$tag_commit" != "$sha_commit" ]; then
  echo "release tag $tag points to $tag_commit, not requested SHA $sha_commit" >&2
  exit 1
fi

echo "Verified $tag points to $sha_commit"
