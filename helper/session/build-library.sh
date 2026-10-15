#!/usr/bin/env bash
# Build libteslable.so for the host (CI FFI integration tests, C smoke test,
# docs/tesla-ble-abi.md). The shipped aarch64 library is built against the
# Sailfish SDK by helper/build-go-library-sdk.sh. Cross-build callers must set
# GOARCH and a target-compatible CC (cgo is needed).
set -euo pipefail

if [[ $# -ne 1 ]]; then
    printf 'Usage: %s /absolute/output/path/libteslable.so\n' "$0" >&2
    exit 2
fi
output="$1"
if [[ "$output" != /* ]]; then
    printf 'Output path must be absolute\n' >&2
    exit 2
fi
mkdir -p "$(dirname "$output")"
session_dir="$(cd "$(dirname "$0")" && pwd)"
(
    cd "$session_dir"
    CGO_ENABLED=1 go build -buildvcs=false -trimpath -buildmode=c-shared -o "$output" ./cmd/teslable
)
