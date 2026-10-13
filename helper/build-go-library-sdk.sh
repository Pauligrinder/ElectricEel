#!/usr/bin/env bash
# Cross-build cgo against Sailfish's libc, not the host's aarch64 sysroot.
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
target="${SAILFISH_TARGET:-SailfishOS-5.2.0.15-aarch64}"
image="${SAILFISH_SDK_IMAGE:-coderus/sailfishos-platform-sdk-aarch64}"
container="electric-eel-go-build-$$"
go_root="$(go env GOROOT)"
module_cache="$(go env GOMODCACHE)"
(cd "$repo_root/helper/session" && go mod download)
mkdir -p "$repo_root/app/lib"
trap 'docker rm -f "$container" >/dev/null 2>&1 || true' EXIT
docker create --name "$container" \
  --volume "$repo_root:/home/mersdk/ee-src:ro" \
  --volume "$go_root:/home/mersdk/ee-go:ro" \
  --volume "$module_cache:/home/mersdk/ee-mod:ro" \
  "$image" sleep infinity >/dev/null
docker start "$container" >/dev/null
docker exec "$container" mkdir -p /home/mersdk/ee-build
docker exec --workdir /home/mersdk/ee-src/helper/session \
  --env GOROOT=/home/mersdk/ee-go --env GOMODCACHE=/home/mersdk/ee-mod \
  --env GOPATH=/home/mersdk/ee-build/gopath --env GOCACHE=/home/mersdk/ee-build/cache \
  --env TMPDIR=/home/mersdk/ee-build --env GOOS=linux --env GOARCH=arm64 \
  --env CGO_ENABLED=1 --env "CC=sb2 -t $target gcc" \
  "$container" /home/mersdk/ee-go/bin/go build -buildvcs=false -trimpath \
  -buildmode=c-shared -o /home/mersdk/ee-build/libteslable.so ./cmd/teslable
docker exec "$container" sb2 -t "$target" strip --strip-unneeded /home/mersdk/ee-build/libteslable.so
docker exec "$container" chmod 0644 /home/mersdk/ee-build/libteslable.so
docker cp "$container:/home/mersdk/ee-build/libteslable.so" "$repo_root/app/lib/libteslable.so"
