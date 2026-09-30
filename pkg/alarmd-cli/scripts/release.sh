#!/bin/sh
set -eu

version=${1:-}
case "$version" in
  ''|.*|*[!A-Za-z0-9._+-]*)
    echo 'Usage: sh scripts/release.sh <version> (letters, digits, dot, dash, underscore, plus)' >&2
    exit 2
    ;;
esac

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
output_dir="$repo_dir/dist/$version"
mkdir -p "$output_dir"
build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT HUP INT TERM
commit=$(git rev-parse HEAD)
dirty=false
if test -n "$(git status --porcelain --untracked-files=normal)"; then dirty=true; fi
toolchain=$(go version)

for target in darwin/arm64 linux/amd64; do
  target_os=${target%/*}
  target_arch=${target#*/}
  package_dir="$build_dir/${target_os}_${target_arch}"
  mkdir -p "$package_dir"
  # BUILD-INFO records this repository explicitly. Version control metadata
  # in a parent directory must not make Go's automatic VCS detection prevent
  # a reproducible build.
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -buildvcs=false -trimpath -ldflags "-s -w -X main.version=$version" -o "$package_dir/alarmd-cli" .
  cp README.md "$package_dir/README.md"
  printf 'version=%s\ncommit=%s\ndirty=%s\ntarget=%s\ntoolchain=%s\n' "$version" "$commit" "$dirty" "$target" "$toolchain" > "$package_dir/BUILD-INFO.txt"
  tar -czf "$output_dir/alarmd-cli_${version}_${target_os}_${target_arch}.tar.gz" -C "$package_dir" alarmd-cli README.md BUILD-INFO.txt
done

cd "$output_dir"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum ./*.tar.gz > SHA256SUMS
else
  shasum -a 256 ./*.tar.gz > SHA256SUMS
fi
printf 'Artifacts: %s\n' "$output_dir"
