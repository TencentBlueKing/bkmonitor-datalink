#!/bin/sh
# Installs the alarmd-cli an alarmd deployment carries:
#
#   curl -fsSL <server>/api/cli/install.sh | sh -s -- --server <server>
#
# <server> is the alarmd root the CLI will talk to: alarmd's own address, or
# the prefix a console forwards to alarmd under (for example <console>/alarmd).
# The script carries no address or credential of its own. It installs the
# server's version -- the CLI is meant to match the alarmd it
# talks to, so a version older than the installed one is installed too --
# verifies the archive's SHA-256 and the executable's own version, and
# replaces the old binary only once all of that has passed.
set -eu

fail() {
  printf 'alarmd-cli install: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: sh install.sh --server <alarmd root URL> [--install-dir <dir>]

  --server       the alarmd the CLI is for, e.g. https://ops.example.com/admin/alarmd
  --install-dir  where alarmd-cli goes (default: $HOME/.local/bin)
EOF
}

server=
install_dir=
while [ $# -gt 0 ]; do
  case $1 in
    --server) [ $# -ge 2 ] || fail '--server needs a URL'; server=$2; shift 2 ;;
    --server=*) server=${1#--server=}; shift ;;
    --install-dir) [ $# -ge 2 ] || fail '--install-dir needs a directory'; install_dir=$2; shift 2 ;;
    --install-dir=*) install_dir=${1#--install-dir=}; shift ;;
    -h|--help) usage; exit 0 ;;
    *) usage >&2; fail "unknown argument: $1" ;;
  esac
done

[ -n "$server" ] || { usage >&2; fail '--server is required'; }
case $server in
  http://*|https://*) ;;
  *) fail "--server must be an http:// or https:// URL: $server" ;;
esac
case $server in
  *@*|*'?'*|*'#'*|*[[:space:]]*) fail '--server must not carry credentials, a query or a fragment' ;;
esac
while :; do
  case $server in
    */) server=${server%/} ;;
    *) break ;;
  esac
done
if [ -z "$install_dir" ]; then
  [ -n "${HOME:-}" ] || fail 'HOME is not set; pass --install-dir'
  install_dir=$HOME/.local/bin
fi

for tool in curl tar uname mktemp; do
  command -v "$tool" >/dev/null 2>&1 || fail "$tool is required"
done
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
  fail 'sha256sum or shasum is required'
fi

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) fail "no alarmd-cli build for $(uname -s); builds exist for Linux and macOS" ;;
esac
case $(uname -m) in
  x86_64|amd64) arch=x64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail "no alarmd-cli build for $(uname -m); builds exist for x86_64 and arm64" ;;
esac

work=$(mktemp -d)
staged=
# The staged copy in the install directory goes too, on any way out: a cp cut
# short or an interrupt before the rename must not leave it behind.
trap 'rm -rf "$work"; if [ -n "$staged" ]; then rm -f "$staged"; fi' EXIT
trap 'exit 1' HUP INT TERM

# fetch writes a URL's body to a file and stops on anything but 200, showing
# what the server said: a 503 names why the deployment has no release.
fetch() {
  status=$(curl -sSL --proto '=http,https' -o "$2" -w '%{http_code}' "$1") || fail "cannot reach $1"
  [ "$status" = 200 ] || fail "$1 answered HTTP $status: $(head -c 400 "$2" 2>/dev/null)"
}

fetch "$server/api/cli/version" "$work/version"
version=$(tr -d '\r\n' < "$work/version")
case $version in
  ''|*[!0-9A-Za-z.-]*) fail "the server reports an unusable version: $version" ;;
esac

file=alarmd-cli-$version-$os-$arch.tar.gz
fetch "$server/api/cli/releases/$version/$file" "$work/$file"
fetch "$server/api/cli/releases/$version/$file.sha256" "$work/$file.sha256"
read -r expected named < "$work/$file.sha256" || fail "the checksum file for $file is empty"
[ "$named" = "$file" ] || fail "the checksum file names $named, not $file"
actual=$(sha256 "$work/$file")
[ "$actual" = "$expected" ] || fail "SHA-256 of $file is $actual, the server says $expected"

mkdir "$work/unpacked"
tar -xzf "$work/$file" -C "$work/unpacked" alarmd-cli || fail "cannot unpack $file"

# The new binary is staged beside the target and checked from there -- the
# temporary directory may be mounted noexec, the install directory cannot
# be -- then renamed over the target: the old binary stays whole until the
# new one is complete, and a rename in one directory is atomic.
mkdir -p "$install_dir" || fail "cannot create $install_dir"
target=$install_dir/alarmd-cli
staged=$install_dir/.alarmd-cli.$$
cp "$work/unpacked/alarmd-cli" "$staged" || fail "cannot write to $install_dir"
chmod 755 "$staged"
reported=$("$staged" --version 2>/dev/null) || fail "the downloaded alarmd-cli does not run from $install_dir: check that it is not mounted noexec and that $os-$arch is this machine"
case $reported in
  *"\"version\":\"$version\""*) ;;
  *) fail "the downloaded alarmd-cli reports $reported, not version $version" ;;
esac
previous=
if [ -x "$target" ]; then
  previous=$("$target" --version 2>/dev/null | sed -n 's/.*"version":"\([^"]*\)".*/\1/p') || previous=
fi
mv -f "$staged" "$target" || fail "cannot replace $target"
staged=

if [ -n "$previous" ] && [ "$previous" != "$version" ]; then
  printf 'alarmd-cli %s -> %s installed at %s\n' "$previous" "$version" "$target"
else
  printf 'alarmd-cli %s installed at %s\n' "$version" "$target"
fi
case ":${PATH:-}:" in
  *":$install_dir:"*) ;;
  *) printf '%s is not on PATH; add it with:\n  export PATH="%s:$PATH"\n' "$install_dir" "$install_dir" ;;
esac
printf 'Next: alarmd-cli auth login\n'
