#!/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
build_dir="$repo_root/.build/user-approval-preview"
mkdir -p "$build_dir/images"
architecture="$(uname -m)"
# Standalone fixture process: no Core client, credentials, network or live requests.
/usr/bin/swiftc -emit-library -static -emit-module -module-name KSFAssistantCore \
    -target "$architecture-apple-macos13.0" "$repo_root/Sources/KSFAssistantCore/UserApprovalModels.swift" \
    "$repo_root/Sources/KSFAssistantCore/JSONRPCPipeConnection.swift" \
    -o "$build_dir/libKSFAssistantCore.a" -emit-module-path "$build_dir/KSFAssistantCore.swiftmodule"
/usr/bin/swiftc -parse-as-library -target "$architecture-apple-macos13.0" \
    -I "$build_dir" -L "$build_dir" -lKSFAssistantCore \
    "$repo_root/Sources/KSFAssistant/UserApprovalContentView.swift" \
    "$repo_root/Tests/UserApprovalSnapshots/main.swift" -framework AppKit -o "$build_dir/render"
"$build_dir/render" "$build_dir/images"
