#!/usr/bin/env sh
set -eu

dist_dir="${1:-dist}"
checksum_file="$dist_dir/checksums.txt"

if [ ! -d "$dist_dir" ]; then
  echo "distribution directory not found: $dist_dir" >&2
  exit 1
fi

tmp_file="$(mktemp)"
file_list="$tmp_file.files"
trap 'rm -f "$tmp_file" "$file_list"' EXIT INT TERM

find "$dist_dir" -type f \( -name '*.tar.gz' -o -name '*.zip' \) | sort > "$file_list"

if [ ! -s "$file_list" ]; then
  echo "no release archives found in $dist_dir" >&2
  exit 1
fi

while IFS= read -r file; do
  rel="${file#"$dist_dir"/}"
  if command -v sha256sum >/dev/null 2>&1; then
    hash="$(sha256sum "$file" | awk '{print $1}')"
  else
    hash="$(shasum -a 256 "$file" | awk '{print $1}')"
  fi
  printf '%s  %s\n' "$hash" "$rel"
done < "$file_list" > "$tmp_file"

mv "$tmp_file" "$checksum_file"
echo "Wrote $checksum_file"
