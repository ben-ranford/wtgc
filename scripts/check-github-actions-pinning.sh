#!/usr/bin/env sh
set -eu

failed=0

for workflow in .github/workflows/*.yml .github/workflows/*.yaml; do
  [ -e "$workflow" ] || continue
  while IFS= read -r line; do
    case "$line" in
      *"uses: docker://"*|*"uses: ./"*) continue ;;
      *"uses:"*"@"*)
        ref="$(printf '%s\n' "$line" | sed -n 's/.*uses:[[:space:]]*[^@[:space:]]*@\([0-9a-f][0-9a-f]*\).*/\1/p')"
        if [ "${#ref}" -ne 40 ]; then
          echo "$workflow: action is not pinned to a full 40-character SHA: $line" >&2
          failed=1
        fi
        case "$line" in
          *"# v"*) ;;
          *)
            echo "$workflow: pinned action is missing a version comment: $line" >&2
            failed=1
            ;;
        esac
        ;;
    esac
  done < "$workflow"
done

exit "$failed"
