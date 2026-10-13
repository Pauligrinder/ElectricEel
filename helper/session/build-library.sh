#!/usr/bin/env bash
# Build the new in-process backend separately until the app migration is ready.
# Cross-build callers must set GOARCH and a target-compatible CC (cgo is needed).
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
    CGO_ENABLED=1 go build -trimpath -buildmode=c-shared -o "$output" ./cmd/teslable
)
