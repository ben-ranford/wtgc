#!/usr/bin/env sh
set -eu

tag="${1:?release tag is required}"
asset_dir="${2:?asset directory is required}"

if [ ! -d "$asset_dir" ]; then
  echo "asset directory not found: $asset_dir" >&2
  exit 1
fi

asset_list="$(mktemp)"
asset_names="$(mktemp)"
remote_assets="$(mktemp)"
download_dir="$(mktemp -d)"
trap 'rm -f "$asset_list" "$asset_names" "$remote_assets"; rm -rf "$download_dir"' EXIT INT TERM

find "$asset_dir" -type f \( -name 'checksums.txt' -o -name '*.tar.gz' -o -name '*.zip' -o -name '*.spdx.json' \) | sort > "$asset_list"
if [ ! -s "$asset_list" ]; then
  echo "no release assets found in $asset_dir" >&2
  exit 1
fi

while IFS= read -r asset; do
  basename "$asset"
done < "$asset_list" | sort > "$asset_names"

duplicate_name="$(uniq -d "$asset_names" | sed -n '1p')"
if [ -n "$duplicate_name" ]; then
  echo "release asset basename collision: $duplicate_name" >&2
  exit 1
fi

hash_file() {
  file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  else
    shasum -a 256 "$file" | awk '{print $1}'
  fi
}

asset_exists() {
  release_tag="$1"
  asset_name="$2"
  gh release view "$release_tag" --json assets --jq '.assets[].name' | grep -Fx "$asset_name" >/dev/null 2>&1
}

verify_existing_asset() {
  release_tag="$1"
  asset="$2"
  asset_name="$(basename "$asset")"
  rm -f "$download_dir/$asset_name"
  gh release download "$release_tag" --pattern "$asset_name" --dir "$download_dir"
  local_hash="$(hash_file "$asset")"
  remote_hash="$(hash_file "$download_dir/$asset_name")"
  rm -f "$download_dir/$asset_name"
  if [ "$local_hash" != "$remote_hash" ]; then
    echo "release asset already exists with different content: $asset_name" >&2
    exit 1
  fi
}

if ! gh release view "$tag" >/dev/null 2>&1; then
  gh release create "$tag" --draft --generate-notes --verify-tag
fi

is_draft="$(gh release view "$tag" --json isDraft --jq .isDraft)"
gh release view "$tag" --json assets --jq '.assets[].name' | sort > "$remote_assets"

while IFS= read -r remote_asset; do
  if ! grep -Fx "$remote_asset" "$asset_names" >/dev/null 2>&1; then
    echo "release contains unexpected existing asset: $remote_asset" >&2
    exit 1
  fi
done < "$remote_assets"

while IFS= read -r asset; do
  name="$(basename "$asset")"
  if asset_exists "$tag" "$name"; then
    verify_existing_asset "$tag" "$asset"
  elif [ "$is_draft" = "true" ]; then
    gh release upload "$tag" "$asset"
  else
    echo "published release is missing expected asset and cannot be mutated: $name" >&2
    exit 1
  fi
done < "$asset_list"

if [ "$is_draft" = "true" ]; then
  gh release edit "$tag" --draft=false
else
  echo "Published release assets already match local assets."
fi
