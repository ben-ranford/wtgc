#!/usr/bin/env sh
set -eu

if [ "$#" -eq 0 ]; then
  echo "usage: $0 TOOL..." >&2
  exit 2
fi

packages=""

tool_available() {
  case "$1" in
    gcc)
      command -v gcc >/dev/null 2>&1 &&
        printf '#include <stdlib.h>\n#include <pthread.h>\n' |
          gcc -x c -E - >/dev/null 2>&1
      ;;
    *)
      command -v "$1" >/dev/null 2>&1
      ;;
  esac
}

for tool in "$@"; do
  case "$tool" in
    gcc)
      package="build-essential"
      ;;
    gh|make|ruby|tar|zip)
      package="$tool"
      ;;
    *)
      echo "unsupported runner tool: $tool" >&2
      exit 2
      ;;
  esac
  if ! tool_available "$tool"; then
    packages="$packages $package"
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
run_apt install -y --no-install-recommends $packages

for tool in "$@"; do
  if ! tool_available "$tool"; then
    echo "runner tool installation did not provide: $tool" >&2
    exit 1
  fi
done
