#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
core_root="$repo_root/Core"

build_core() {
    local goos="$1"
    local goarch="$2"
    local output_dir="$3"
    local filename="$4"
    mkdir -p "$repo_root/dist/core/$output_dir"
    (
        cd "$core_root"
        GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" \
            -o "$repo_root/dist/core/$output_dir/$filename" ./cmd/codex-usage-core
    )
}

targets="${CORE_TARGETS:-darwin-arm64 darwin-x64 windows-x64 windows-arm64}"
for target in $targets; do
    case "$target" in
        darwin-arm64) build_core darwin arm64 darwin-arm64 codex-usage-core ;;
        darwin-x64) build_core darwin amd64 darwin-x64 codex-usage-core ;;
        windows-x64) build_core windows amd64 windows-x64 codex-usage-core.exe ;;
        windows-arm64) build_core windows arm64 windows-arm64 codex-usage-core.exe ;;
        *) echo "Unsupported core target: $target" >&2; exit 1 ;;
    esac
done
