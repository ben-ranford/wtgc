#!/usr/bin/env sh
set -eu

source_dir="${1:-dist/artifacts}"
release_dir="${2:-dist/release}"

if [ ! -d "$source_dir" ]; then
  echo "artifact source directory not found: $source_dir" >&2
  exit 1
fi

source_real="$(cd "$source_dir" && pwd -P)"
if [ -d "$release_dir" ]; then
  release_real="$(cd "$release_dir" && pwd -P)"
else
  release_parent="$(dirname "$release_dir")"
  release_base="$(basename "$release_dir")"
  if [ -d "$release_parent" ]; then
    release_parent_real="$(cd "$release_parent" && pwd -P)"
    release_real="$release_parent_real/$release_base"
  else
    echo "release output parent directory not found: $release_parent" >&2
    exit 1
  fi
fi

case "$release_real" in
  "$source_real"|"$source_real"/*)
    echo "refusing release output directory inside artifact source: $release_dir" >&2
    exit 1
    ;;
  *)
    ;;
esac
case "$source_real" in
  "$release_real"/*)
    echo "refusing release output directory that contains artifact source: $release_dir" >&2
    exit 1
    ;;
  *)
    ;;
esac

./scripts/managed-output.sh reset "$release_dir"

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
