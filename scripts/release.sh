#!/bin/sh
set -eu

version="${1:?version required}"
root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
dist="$root/dist"
go_cmd="${GO:-go}"

cd "$root"
mkdir -p "$dist"

targets="linux/amd64 linux/arm64 darwin/amd64 darwin/arm64"
module="$("$go_cmd" list -m)"
commit="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
date="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
ldflags="-s -w -X ${module}/internal/buildinfo.Version=${version} -X ${module}/internal/buildinfo.Commit=${commit} -X ${module}/internal/buildinfo.Date=${date}"

rm -f "$dist"/gpttop-"$version"-*.tar.gz "$dist"/checksums.txt

for target in $targets; do
	os="${target%/*}"
	arch="${target#*/}"
	name="gpttop-${version}-${os}-${arch}"
	work="$dist/$name"
	rm -rf "$work"
	mkdir -p "$work"
	echo "building $name"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" "$go_cmd" build -trimpath -ldflags="$ldflags" -o "$work/gpttop" ./cmd/gpttop
	cp README.md "$work/README.md"
	tar -C "$dist" -czf "$dist/$name.tar.gz" "$name"
	rm -rf "$work"
done

(
	cd "$dist"
	sha256sum gpttop-"$version"-*.tar.gz > checksums.txt
)
