#!/usr/bin/env sh
set -eu

dist_dir="${1:-dist}"
checksum_file="$dist_dir/checksums.txt"

if [ ! -d "$dist_dir" ]; then
  echo "distribution directory not found: $dist_dir" >&2
  exit 1
fi

if [ ! -f "$checksum_file" ]; then
  echo "checksum file not found: $checksum_file" >&2
  exit 1
fi

asset_list="$(mktemp)"
checksum_paths="$(mktemp)"
trap 'rm -f "$asset_list" "$checksum_paths"' EXIT INT TERM

find "$dist_dir" -type f \( -name '*.tar.gz' -o -name '*.zip' -o -name '*.spdx.json' \) | sort | while IFS= read -r file; do
  printf '%s\n' "${file#"$dist_dir"/}"
done > "$asset_list"

if [ ! -s "$asset_list" ]; then
  echo "no release assets found in $dist_dir" >&2
  exit 1
fi

awk 'NF >= 2 { print $2 }' "$checksum_file" | sort > "$checksum_paths"

while IFS= read -r rel; do
  count="$(awk -v path="$rel" '$0 == path { count++ } END { print count + 0 }' "$checksum_paths")"
  if [ "$count" -ne 1 ]; then
    echo "release asset $rel has $count checksum entries; expected exactly 1" >&2
    exit 1
  fi
done < "$asset_list"

while IFS= read -r rel; do
  if ! grep -Fx "$rel" "$asset_list" >/dev/null 2>&1; then
    echo "checksum entry does not match a release asset: $rel" >&2
    exit 1
  fi
done < "$checksum_paths"

while read -r expected rel; do
  file="$dist_dir/$rel"
  if [ ! -f "$file" ]; then
    echo "checksummed release asset not found: $rel" >&2
    exit 1
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$file" | awk '{print $1}')"
  else
    actual="$(shasum -a 256 "$file" | awk '{print $1}')"
  fi
  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for $rel" >&2
    exit 1
  fi
done < "$checksum_file"

echo "Validated release assets in $dist_dir"
