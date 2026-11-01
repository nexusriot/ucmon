#!/usr/bin/env bash
# Legacy single-arch .deb builder; `make deb-<platform>` is the canonical path.
# Usage: ./build-deb.sh [amd64|arm64|armhf|riscv64]
set -euo pipefail
cd "$(dirname "$0")"

# Keep the .deb version in lockstep with the embedded UI version string.
version="$(sed -nE 's/.*const version = "([^"]+)".*/\1/p' internal/ui/model.go)"
if [ -z "$version" ]; then
  echo "could not read the version from internal/ui/model.go" >&2
  exit 1
fi
arch="${1:-amd64}"

if ! command -v dpkg-deb >/dev/null 2>&1; then
  echo "dpkg-deb not found: install dpkg first" >&2
  exit 1
fi

goarm=""
case "$arch" in
  amd64)    goarch="amd64" ;;
  arm64)    goarch="arm64" ;;
  armhf)    goarch="arm"; goarm="7" ;;
  riscv64)  goarch="riscv64" ;;
  *)        echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac

echo "building deb for ucmon $version ($arch)"

project="ucmon_${version}_${arch}"
folder_name="build/$project"
rm -rf "$folder_name"
mkdir -p "$folder_name/usr/bin"
cp -r DEBIAN "$folder_name/"

# One build for every architecture, the same as the Makefile's: static
# (CGO_ENABLED=0 — net/http would otherwise link the host's glibc), stripped,
# and reproducible.
CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM="$goarm" \
  go build -trimpath -ldflags "-s -w" -o "$folder_name/usr/bin/ucmon" ./cmd/ucmon

sed -i "s/_version_/$version/g" "$folder_name/DEBIAN/control"
sed -i "s/^Architecture: .*/Architecture: $arch/" "$folder_name/DEBIAN/control"
size_kb="$(du -sk "$folder_name/usr" | cut -f1)"
sed -i "s/^Installed-Size: .*/Installed-Size: $size_kb/" "$folder_name/DEBIAN/control"

dpkg-deb --build -Z gzip --root-owner-group "$folder_name" "build/$project.deb"
