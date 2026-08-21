#!/usr/bin/env sh
set -eu

marker_name=".wtgc-managed-output"
marker_value="managed by wtgc build automation"
repo_root="$(pwd -P)"

die() {
  echo "$*" >&2
  exit 1
}

is_empty_dir() {
  dir="$1"
  set -- "$dir"/* "$dir"/.[!.]* "$dir"/..?*
  for entry do
    if [ -e "$entry" ] || [ -L "$entry" ]; then
      return 1
    fi
  done
  return 0
}

has_valid_marker() {
  marker="$1/$marker_name"
  [ ! -L "$marker" ] && [ -f "$marker" ] && [ "$(cat "$marker")" = "$marker_value" ]
}

write_marker() {
  printf '%s\n' "$marker_value" > "$1/$marker_name"
}

resolve_managed_target() {
  target="$1"

  case "$target" in
    ""|"/"|".")
      die "refusing unsafe managed output directory: $target"
      ;;
  esac

  while :; do
    case "$target" in
      ./*) target="${target#./}" ;;
      *) break ;;
    esac
  done

  case "$target" in
    ""|".")
      die "refusing unsafe managed output directory: $1"
      ;;
    /*) abs_target="$target" ;;
    *) abs_target="$repo_root/$target" ;;
  esac

  case "$abs_target/" in
    "$repo_root/"*)
      ;;
    *)
      die "refusing managed output directory outside repository: $1"
      ;;
  esac

  rel_target="${abs_target#"$repo_root"/}"
  case "$rel_target" in
    "$abs_target"|"")
      die "refusing unsafe managed output directory: $1"
      ;;
  esac

  case "/$rel_target/" in
    *"/../"*|*"/./"*|*"//"*)
      die "refusing unsafe managed output directory: $1"
      ;;
  esac

  current="$repo_root"
  remaining="$rel_target"
  while :; do
    component="${remaining%%/*}"
    case "$component" in
      ""|"."|"..")
        die "refusing unsafe managed output directory: $1"
        ;;
    esac

    if [ "$component" = "$remaining" ]; then
      managed_target="$current/$component"
      return 0
    fi

    next="$current/$component"
    if [ -L "$next" ]; then
      die "refusing managed output directory through symlink: $1"
    fi
    if [ -e "$next" ] && [ ! -d "$next" ]; then
      die "refusing managed output directory with non-directory parent: $1"
    fi
    if [ ! -d "$next" ]; then
      mkdir "$next"
    fi

    current="$next"
    remaining="${remaining#*/}"
  done
}

ensure_owned_dir() {
  target="$1"
  resolve_managed_target "$target"
  dir="$managed_target"
  marker="$dir/$marker_name"

  if [ -L "$dir" ]; then
    die "refusing symlink managed output directory: $target"
  fi
  if [ -e "$dir" ] && [ ! -d "$dir" ]; then
    die "refusing non-directory managed output path: $target"
  fi
  if [ ! -d "$dir" ]; then
    mkdir "$dir"
  fi

  if [ -e "$marker" ] || [ -L "$marker" ]; then
    if has_valid_marker "$dir"; then
      return 0
    fi
    die "refusing output directory with invalid ownership marker: $target"
  fi
  if is_empty_dir "$dir"; then
    write_marker "$dir"
    return 0
  fi

  die "refusing to manage nonempty unmarked output directory: $target"
}

reset_dir() {
  target="$1"
  ensure_owned_dir "$target"
  dir="$managed_target"

  find "$dir" -mindepth 1 -exec rm -rf {} +
  write_marker "$dir"
}

remove_dir() {
  target="$1"
  resolve_managed_target "$target"
  dir="$managed_target"

  if [ ! -e "$dir" ] && [ ! -L "$dir" ]; then
    return 0
  fi
  if [ -L "$dir" ]; then
    die "refusing symlink managed output directory: $target"
  fi
  if [ ! -d "$dir" ]; then
    die "refusing non-directory managed output path: $target"
  fi
  if ! has_valid_marker "$dir" && ! is_empty_dir "$dir"; then
    die "refusing to remove nonempty unmarked output directory: $target"
  fi

  if has_valid_marker "$dir"; then
    find "$dir" -mindepth 1 -exec rm -rf {} +
  fi
  rmdir "$dir"
}

usage() {
  die "usage: $0 ensure|reset|remove DIR"
}

case "${1:-}" in
  ensure)
    [ "$#" -eq 2 ] || usage
    ensure_owned_dir "$2"
    ;;
  reset)
    [ "$#" -eq 2 ] || usage
    reset_dir "$2"
    ;;
  remove)
    [ "$#" -eq 2 ] || usage
    remove_dir "$2"
    ;;
  *)
    usage
    ;;
esac
