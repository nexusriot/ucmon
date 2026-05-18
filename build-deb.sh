#!/bin/env bash

version=0.1.9
arch="${1:-amd64}"

echo "building deb for ucmon $version ($arch)"

if ! type "dpkg-deb" > /dev/null; then
  echo "please install required build tools first"
fi

case "$arch" in
  amd64)  goarch="amd64" ;;
  arm64)  goarch="arm64" ;;
  *)      echo "unsupported architecture: $arch"; exit 1 ;;
esac

project="ucmon_${version}_${arch}"
folder_name="build/$project"
echo "creating $folder_name"
mkdir -p $folder_name
cp -r DEBIAN/ $folder_name
bin_dir="$folder_name/usr/bin"
mkdir -p $bin_dir
if [ "$arch" = "$(go env GOARCH)" ]; then
  go build -o ucmon cmd/ucmon/main.go
else
  CGO_ENABLED=0 GOARCH=$goarch go build -ldflags "-s -w" -o ucmon cmd/ucmon/main.go
fi

mv ucmon $bin_dir
sed -i "s/_version_/$version/g" $folder_name/DEBIAN/control
sed -i "s/Architecture: .*/Architecture: $arch/" $folder_name/DEBIAN/control

cd build/ && dpkg-deb --build -Z gzip --root-owner-group $project
