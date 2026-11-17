#!/usr/bin/env bash
# Build the standalone Go provider tool, then install or inspect official native CLIs.
set -euo pipefail

if [[ ${1:-} == --help || ${1:-} == -h ]]; then
  echo 'Usage: providers.sh [install|update|check] [--root PATH] [--api-only|--only opencode|cline]'
  echo 'Source checkout: Go 1.26+ is required. Node and npm are not used.'
  exit 0
fi
repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)"
root="${FREEWAY_PROVIDER_HOME:-$HOME/.freeway/providers}"
action=install
selection=()
if [[ ${1:-} == install || ${1:-} == update || ${1:-} == check ]]; then action=$1; shift; fi
while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) [[ $# -gt 1 ]] || exit 2; root=$2; shift 2 ;;
    --api-only) [[ ${#selection[@]} -eq 0 ]] || exit 2; selection=(--api-only); shift ;;
    --only) [[ $# -gt 1 && ( $2 == opencode || $2 == cline ) && ${#selection[@]} -eq 0 ]] || exit 2; selection=(--only "$2"); shift 2 ;;
    *) echo 'Unknown argument; use --help.' >&2; exit 2 ;;
  esac
done
if [[ -d "$root" ]]; then root="$(cd -- "$root" && pwd -P)"; fi
[[ "$root" == /* && "$root" != / && "$root" != "$HOME" && "$root" != "$repo" && "$root/" != *'/../'* && "$root/" != *'/./'* ]] || { echo 'Choose a dedicated absolute install directory.' >&2; exit 2; }
export FREEWAY_PROVIDER_HOME="$root"
binary="$root/bin/freeway-providers"
# Checking an existing installation does not require a compiler or change any files.
if [[ "$action" == check ]]; then
  [[ -x "$binary" ]] || { echo '{"ok":false,"code":"provider_tool_not_installed"}'; exit 1; }
  exec "$binary" check --root "$root" "${selection[@]}"
fi
command -v go >/dev/null || { echo 'Install Go 1.26+ from https://go.dev/doc/install/, then rerun this command.' >&2; exit 1; }
mkdir -p -- "$root/bin"
stage="$(mktemp -d "$root/bin/.provider-build.XXXXXX")"
trap 'rm -rf -- "$stage"' EXIT
go -C "$repo/backend" build -o "$stage/freeway-providers" ./cmd/providerctl
mv -- "$stage/freeway-providers" "$binary"
cat > "$root/bin/freeway-provider-setup" <<'LAUNCHER'
#!/bin/sh
exec "$(dirname -- "$0")/freeway-providers" "$@"
LAUNCHER
chmod 755 "$root/bin/freeway-provider-setup"
rm -rf -- "$stage"
trap - EXIT
exec "$binary" "$action" --root "$root" "${selection[@]}"
