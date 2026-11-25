#!/usr/bin/env bash
# Bootstrap the native client; activation and recovery remain owned by Go.
set -euo pipefail
umask 077

fail() { printf 'Freeway: %s\n' "$*" >&2; exit 1; }
usage() {
  cat <<'USAGE'
Usage: install.sh [--source | --archive PATH --sha256 SHA256] [--root PATH]
                  [--release-url HTTPS_BASE_URL] [--no-modify-path]

No arguments: download the native client from the official GitHub release.
--source: build this checkout with Go 1.26+; Node.js is not used.
--archive: install a local package using its trusted SHA256.
Default program directory: ~/.freeway/app (or FREEWAY_INSTALL_ROOT).
The bin directory is added to PATH in your shell profile unless --no-modify-path is given.
USAGE
}

source_build=0
modify_path=1
archive=''
expected=''
root="${FREEWAY_INSTALL_ROOT:-$HOME/.freeway/app}"
release_url='https://github.com/wooinwoo/freeway-cli/releases/latest/download'
while [[ $# -gt 0 ]]; do
  case "$1" in
    --source) source_build=1; shift ;;
    --no-modify-path) modify_path=0; shift ;;
    --archive|--sha256|--root|--release-url)
      [[ $# -gt 1 && -n $2 ]] || fail "$1 needs a value."
      case "$1" in
        --archive) archive=$2 ;;
        --sha256) expected=$2 ;;
        --root) root=$2 ;;
        --release-url) release_url=$2 ;;
      esac
      shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) fail 'Unknown argument; use --help.' ;;
  esac
done
[[ $source_build -eq 0 || ( -z $archive && -z $expected ) ]] || fail 'Choose --source or --archive.'
[[ ( -z $archive && -z $expected ) || ( -n $archive && -n $expected ) ]] || fail '--archive and --sha256 must be used together.'
[[ $root == /* ]] || root="$PWD/$root"

case "$(uname -s)" in Linux) platform=linux ;; Darwin) platform=darwin ;; *) fail 'Use install.ps1 on Windows.' ;; esac
case "$(uname -m)" in x86_64|amd64) architecture=amd64 ;; arm64|aarch64) architecture=arm64 ;; *) fail 'Unsupported CPU architecture.' ;; esac
target="${platform}_${architecture}"
stage="$(mktemp -d "${TMPDIR:-/tmp}/freeway-install.XXXXXX")"
trap 'rm -rf -- "$stage"' EXIT

download() {
  command -v curl >/dev/null || fail 'curl is required for release downloads.'
  local address=$1 output=$2 limit=$3
  local protocols='=https'
  [[ $address != http://* ]] || protocols='=http,https'
  curl --fail --silent --show-error --location --max-redirs 10 \
    --proto "$protocols" --proto-redir '=https' --connect-timeout 15 \
    --max-time 180 --max-filesize "$limit" --output "$output" "$address" || return 1
  [[ $(wc -c < "$output") -le $limit ]] || fail 'Download exceeds size limit.'
}

if [[ $source_build -eq 1 ]]; then
  [[ -n ${BASH_SOURCE[0]:-} && -f ${BASH_SOURCE[0]} ]] || fail '--source requires a repository checkout.'
  repo="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
  [[ -f "$repo/cli/go.mod" && -f "$repo/backend/go.mod" && -f "$repo/VERSION" ]] || fail '--source requires a repository checkout.'
  go_binary="$(command -v go)" || fail 'Source installation requires Go 1.26+ from https://go.dev/doc/install/.'
  "$go_binary" -C "$repo/cli" run ./cmd/package --root "$repo" --out "$stage/release" \
    --targets "$platform/$architecture" --go "$go_binary"
  release_url=''
elif [[ -n $archive ]]; then
  [[ -f $archive ]] || fail 'Archive file does not exist.'
fi

if [[ -z $archive ]]; then
  sums="$stage/SHA256SUMS"
  if [[ $source_build -eq 1 ]]; then
    sums="$stage/release/SHA256SUMS"
  else
    release_url="${release_url%/}"
    [[ $release_url != *'@'* && $release_url != *'?'* && $release_url != *'#'* && $release_url != *[[:space:]]* ]] || fail 'Invalid release URL.'
    loopback='^http://(127\.0\.0\.1|localhost|\[::1\])(:[0-9]+)?(/[^?#[:space:]@]*)?$'
    [[ $release_url == https://* || $release_url =~ $loopback ]] || fail 'Release downloads require HTTPS; HTTP is allowed only for loopback fixtures.'
    if ! download "$release_url/SHA256SUMS" "$sums" 1048576; then
      fail 'Native release checksums are unavailable. From a checkout, run: bash install.sh --source. No installed version was changed.'
    fi
  fi
  matches=0
  pattern="^([[:xdigit:]]{64})[[:space:]]+[*]?(freeway_[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z][0-9A-Za-z.-]*)?_${target}\.tar\.gz)$"
  while IFS= read -r line || [[ -n $line ]]; do
    line="${line%$'\r'}"
    if [[ $line =~ $pattern ]]; then
      expected="${BASH_REMATCH[1]}"
      filename="${BASH_REMATCH[2]}"
      matches=$((matches + 1))
    fi
  done < "$sums"
  [[ $matches -eq 1 ]] || fail 'Expected exactly one checksum for this OS and CPU.'
  if [[ $source_build -eq 1 ]]; then
    archive="$stage/release/$filename"
  else
    archive="$stage/$filename"
    download "$release_url/$filename" "$archive" 268435456 || fail 'Native archive download failed. No installed version was changed.'
  fi
fi

[[ $expected =~ ^[[:xdigit:]]{64}$ ]] || fail 'Expected SHA256 must contain 64 hexadecimal characters.'
[[ $(wc -c < "$archive") -le 268435456 ]] || fail 'Archive exceeds size limit.'
if command -v sha256sum >/dev/null; then
  actual="$(sha256sum < "$archive")"
elif command -v shasum >/dev/null; then
  actual="$(shasum -a 256 < "$archive")"
else
  fail 'sha256sum or shasum is required.'
fi
actual="${actual%% *}"
expected="$(printf '%s' "$expected" | tr 'A-F' 'a-f')"
[[ $actual == "$expected" ]] || fail 'Archive SHA256 mismatch. No package executable was run.'
command -v tar >/dev/null || fail 'tar is required.'
# Extract only the bootstrap executable to stdout; never unpack archive paths.
tar -xOzf "$archive" -- freeway > "$stage/freeway" || fail 'Archive does not contain the native freeway executable.'
[[ -s "$stage/freeway" && $(wc -c < "$stage/freeway") -le 67108864 ]] || fail 'Invalid bootstrap executable size.'
chmod 700 "$stage/freeway"
"$stage/freeway" install --archive "$archive" --sha256 "$expected" --root "$root"
bin="$root/bin"

# Pick the file this user's login or interactive shell reads.
shell_profile() {
  case "${SHELL:-}" in
    */zsh|zsh) printf '%s' "${ZDOTDIR:-$HOME}/.zshrc" ;;
    */bash|bash) if [[ $platform == darwin ]]; then printf '%s' "$HOME/.bash_profile"; else printf '%s' "$HOME/.bashrc"; fi ;;
    */fish|fish) printf '%s' "${XDG_CONFIG_HOME:-$HOME/.config}/fish/conf.d/freeway.fish" ;;
    *) printf '%s' "$HOME/.profile" ;;
  esac
}

profile=''
if [[ $modify_path -eq 1 && ":$PATH:" != *":$bin:"* && $bin != *[\"\$\`\\]* ]]; then
  profile="$(shell_profile)"
  entry="$bin"
  [[ $bin != "$HOME"/* ]] || entry="\$HOME/${bin#"$HOME"/}"
  if [[ $profile == *.fish ]]; then line="fish_add_path --path \"$entry\""; else line="export PATH=\"$entry:\$PATH\""; fi
  existing=''
  [[ ! -f $profile ]] || existing="$(<"$profile")"
  if [[ $existing != *"$line"* ]]; then
    { [[ -d ${profile%/*} ]] || mkdir -p -- "${profile%/*}"; } &&
      printf '\n# Freeway\n%s\n' "$line" >> "$profile" || profile=''
  fi
fi

printf '\nFreeway installed.\n'
if [[ ":$PATH:" == *":$bin:"* ]]; then
  printf 'Run: freeway\n'
elif [[ -n $profile ]]; then
  printf 'Freeway is on PATH in %s. Open a new terminal, or run this now:\n' "$profile"
  printf '  export PATH=%q:$PATH\n' "$bin"
  printf 'Then run: freeway\n'
else
  printf 'Add its bin directory to PATH:\n'
  printf '  export PATH=%q:$PATH\n' "$bin"
  printf 'Then run: freeway\n'
fi
