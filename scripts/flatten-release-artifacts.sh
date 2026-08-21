#!/usr/bin/env sh
set -eu

source_dir="${1:-dist/artifacts}"
release_dir="${2:-dist/release}"

case "$release_dir" in
  ""|"/"|".")
    echo "refusing unsafe release output directory: $release_dir" >&2
    exit 1
    ;;
esac

if [ ! -d "$source_dir" ]; then
  echo "artifact source directory not found: $source_dir" >&2
  exit 1
fi

rm -rf "$release_dir"
mkdir -p "$release_dir"

archive_list="$(mktemp)"
copied_names="$(mktemp)"
trap 'rm -f "$archive_list" "$copied_names"' EXIT INT TERM

find "$source_dir" -type f \( -name '*.tar.gz' -o -name '*.zip' \) | sort > "$archive_list"

if [ ! -s "$archive_list" ]; then
  echo "no release archives found in $source_dir" >&2
  exit 1
fi

while IFS= read -r archive; do
  name="$(basename "$archive")"
  if grep -Fx "$name" "$copied_names" >/dev/null 2>&1; then
    echo "release archive basename collision: $name" >&2
    exit 1
  fi
  cp "$archive" "$release_dir/$name"
  printf '%s\n' "$name" >> "$copied_names"
done < "$archive_list"

./scripts/checksums.sh "$release_dir"
./scripts/validate-release-artifacts.sh "$release_dir"
