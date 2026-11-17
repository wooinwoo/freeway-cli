#!/usr/bin/env bash
set -euo pipefail

show_help() {
  cat <<'USAGE'
Usage: providers.sh [install|update|check] [options]

  install    Install the private Node runtime and selected official CLIs
  update     Apply the tested versions bundled with this Freeway checkout
  check      Inspect installed versions without installing or authenticating

  --api-only           Install Node only for HTTP API providers
  --only opencode|cline
  --root /absolute/path
  -h, --help
USAGE
}

if [[ ${1:-} == --help || ${1:-} == -h ]]; then show_help; exit 0; fi
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
root="${FREEWAY_PROVIDER_HOME:-$HOME/.freeway/providers}"
action=install
selection=()
if [[ ${1:-} == install || ${1:-} == update || ${1:-} == check ]]; then action=$1; shift; fi
while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) [[ $# -gt 1 ]] || exit 2; root=$2; shift 2 ;;
    --api-only) [[ ${#selection[@]} -eq 0 ]] || exit 2; selection=(--api-only); shift ;;
    --only) [[ $# -gt 1 && ( $2 == opencode || $2 == cline ) && ${#selection[@]} -eq 0 ]] || exit 2; selection=(--only "$2"); shift 2 ;;
    --help|-h) show_help; exit 0 ;;
    *) show_help >&2; exit 2 ;;
  esac
done
[[ -n "$root" && "$root" == /* ]] || { echo 'Use an absolute install directory.' >&2; exit 2; }
if [[ -d "$root" ]]; then root="$(cd -- "$root" && pwd -P)"; fi
repo_root="$(cd -- "$script_dir/../.." && pwd -P)"
[[ "$root" != / && "$root" != "$HOME" && "$root" != "$repo_root" && "$root/" != *'/../'* && "$root/" != *'/./'* ]] || { echo 'Choose a dedicated install directory.' >&2; exit 2; }
version=$(sed -n 's/.*"node": *"\([0-9.]*\)".*/\1/p' "$script_dir/versions.json")
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || exit 2
case "$(uname -s)" in Linux) platform=linux ;; Darwin) platform=darwin ;; *) echo 'Use providers.ps1 on Windows.' >&2; exit 2 ;; esac
case "$(uname -m)" in x86_64|amd64) arch=x64 ;; arm64|aarch64) arch=arm64 ;; *) echo 'Only x64 and arm64 are supported.' >&2; exit 2 ;; esac
distribution="node-v$version-$platform-$arch"
target="$root/runtime/$distribution"
node="$target/bin/node"

if [[ ! -x "$node" ]]; then
  if [[ "$action" == check ]]; then
    printf '%s\n' '{"ok":false,"command":"check","code":"runtime_not_installed_or_update_required"}'
    exit 1
  fi
  command -v curl >/dev/null || { echo 'curl is required.' >&2; exit 1; }
  command -v tar >/dev/null || { echo 'tar is required.' >&2; exit 1; }
  mkdir -p -- "$root/runtime"
  runtime_lock="$root/.runtime-install-lock"
  mkdir -- "$runtime_lock" 2>/dev/null || { echo 'Runtime installation already in progress.' >&2; exit 1; }
  stage=''
  cleanup() { [[ -z "$stage" ]] || rm -rf -- "$stage"; rmdir -- "$runtime_lock"; }
  trap cleanup EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  [[ ! -e "$target" ]] || { echo 'Existing runtime directory is incomplete; choose a clean install root.' >&2; exit 1; }
  stage=$(mktemp -d "${TMPDIR:-/tmp}/freeway-node.XXXXXX")
  archive="$distribution.tar.gz"
  echo "Installing private Node.js $version..." >&2
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "https://nodejs.org/dist/v$version/$archive" --output "$stage/$archive"
  curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 "https://nodejs.org/dist/v$version/SHASUMS256.txt" --output "$stage/SHASUMS256.txt"
  expected=$(awk -v name="$archive" '$2 == name {print $1}' "$stage/SHASUMS256.txt")
  [[ "$expected" =~ ^[a-f0-9]{64}$ ]] || { echo 'Missing official checksum.' >&2; exit 1; }
  if command -v sha256sum >/dev/null; then actual=$(sha256sum "$stage/$archive" | awk '{print $1}');
  else actual=$(shasum -a 256 "$stage/$archive" | awk '{print $1}'); fi
  [[ "$expected" == "$actual" ]] || { echo 'Node checksum mismatch.' >&2; exit 1; }
  tar -xzf "$stage/$archive" -C "$stage"
  "$stage/$distribution/bin/node" --version >/dev/null
  mv -- "$stage/$distribution" "$target"
  cleanup
  trap - EXIT
  trap - INT TERM
fi
exec "$node" "$script_dir/providers.mjs" "$action" --root "$root" "${selection[@]}"
