#!/usr/bin/env sh
set -eu

dist_dir="${1:-dist}"
checksum_file="$dist_dir/checksums.txt"

if [ ! -d "$dist_dir" ]; then
  echo "distribution directory not found: $dist_dir" >&2
  exit 1
fi

tmp_file="$(mktemp)"
trap 'rm -f "$tmp_file"' EXIT INT TERM

find "$dist_dir" -maxdepth 1 -type f ! -name 'checksums.txt' | sort | while IFS= read -r file; do
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file"
  else
    shasum -a 256 "$file"
  fi
done > "$tmp_file"

mv "$tmp_file" "$checksum_file"
echo "Wrote $checksum_file"
