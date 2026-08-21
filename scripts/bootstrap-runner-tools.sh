#!/usr/bin/env sh
set -eu

if [ "$#" -eq 0 ]; then
  echo "usage: $0 TOOL..." >&2
  exit 2
fi

packages=""
for tool in "$@"; do
  case "$tool" in
    gcc|gh|make|ruby|tar|zip)
      ;;
    *)
      echo "unsupported runner tool: $tool" >&2
      exit 2
      ;;
  esac
  if ! command -v "$tool" >/dev/null 2>&1; then
    packages="$packages $tool"
  fi
done

if [ -z "$packages" ]; then
  exit 0
fi

if ! command -v apt-get >/dev/null 2>&1; then
  echo "missing runner tools:$packages; install them in the wtgc-arc image" >&2
  exit 1
fi

run_apt() {
  if [ "$(id -u)" -eq 0 ]; then
    apt-get "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo apt-get "$@"
  else
    echo "missing runner tools:$packages; apt-get requires root or sudo" >&2
    exit 1
  fi
}

run_apt update
# Package names match their commands on the Debian-based ARC runner image.
run_apt install -y --no-install-recommends $packages

for tool in "$@"; do
  if ! command -v "$tool" >/dev/null 2>&1; then
    echo "runner tool installation did not provide: $tool" >&2
    exit 1
  fi
done
