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
            -o "$repo_root/dist/core/$output_dir/$filename" ./cmd/ksf-assistant-core
        mkdir -p "$repo_root/dist/runtime/feishu-bridge/$output_dir"
        local bridge_filename="ksf-assistant-feishu-bridge"
        [[ "$goos" == "windows" ]] && bridge_filename="ksf-assistant-feishu-bridge.exe"
        GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" \
            -o "$repo_root/dist/runtime/feishu-bridge/$output_dir/$bridge_filename" ./cmd/ksf-assistant-feishu-bridge
        for component in toolchain task; do
            local component_filename="ksf-assistant-$component"
            [[ "$goos" == "windows" ]] && component_filename="$component_filename.exe"
            mkdir -p "$repo_root/dist/runtime/$component/$output_dir"
            CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -ldflags="-s -w" \
                -o "$repo_root/dist/runtime/$component/$output_dir/$component_filename" "./cmd/ksf-assistant-$component"
        done
    )
}

targets="${CORE_TARGETS:-darwin-arm64 darwin-x64 windows-x64 windows-arm64}"
for target in $targets; do
    case "$target" in
        darwin-arm64) build_core darwin arm64 darwin-arm64 ksf-assistant-core ;;
        darwin-x64) build_core darwin amd64 darwin-x64 ksf-assistant-core ;;
        windows-x64) build_core windows amd64 windows-x64 ksf-assistant-core.exe ;;
        windows-arm64) build_core windows arm64 windows-arm64 ksf-assistant-core.exe ;;
        *) echo "Unsupported core target: $target" >&2; exit 1 ;;
    esac
done
