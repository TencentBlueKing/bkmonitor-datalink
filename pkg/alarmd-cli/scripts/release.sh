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
command -v zip >/dev/null 2>&1 || { echo 'zip is required for the Windows archive' >&2; exit 1; }

for target in darwin/arm64 linux/amd64 windows/amd64; do
  target_os=${target%/*}
  target_arch=${target#*/}
  package_dir="$build_dir/${target_os}_${target_arch}"
  mkdir -p "$package_dir"
  executable=alarmd-cli
  if [ "$target_os" = windows ]; then executable=alarmd-cli.exe; fi
  # BUILD-INFO records this repository explicitly. Version control metadata
  # in a parent directory must not make Go's automatic VCS detection prevent
  # a reproducible build.
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -buildvcs=false -trimpath -ldflags "-s -w -X main.version=$version" -o "$package_dir/$executable" .
  cp README.md "$package_dir/README.md"
  mkdir -p "$package_dir/docs"
  cp docs/windows-acceptance.md "$package_dir/docs/windows-acceptance.md"
  printf 'version=%s\ncommit=%s\ndirty=%s\ntarget=%s\ntoolchain=%s\nCGO_ENABLED=0\nflags=-buildvcs=false -trimpath -ldflags "-s -w -X main.version=%s"\n' "$version" "$commit" "$dirty" "$target" "$toolchain" "$version" > "$package_dir/BUILD-INFO.txt"
  if [ "$target_os" = windows ]; then
    (cd "$package_dir" && zip -q "$output_dir/alarmd-cli_${version}_${target_os}_${target_arch}.zip" "$executable" README.md BUILD-INFO.txt docs/windows-acceptance.md)
  else
    tar -czf "$output_dir/alarmd-cli_${version}_${target_os}_${target_arch}.tar.gz" -C "$package_dir" "$executable" README.md BUILD-INFO.txt docs/windows-acceptance.md
  fi
done

cd "$output_dir"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum ./*.tar.gz ./*.zip > SHA256SUMS
else
  shasum -a 256 ./*.tar.gz ./*.zip > SHA256SUMS
fi
printf 'Artifacts: %s\n' "$output_dir"
