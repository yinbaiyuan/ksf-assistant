#!/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
build_dir="$repo_root/.build/feishu-layout-preview"
mkdir -p "$build_dir/images"
preview_home="$(mktemp -d /private/tmp/ksfas-layout-preview.XXXXXX)"
trap 'rm -rf "$preview_home"' EXIT
architecture="$(uname -m)"
/usr/bin/swiftc -emit-library -static -emit-module -module-name KSFAssistantCore \
    -target "$architecture-apple-macos13.0" "$repo_root"/Sources/KSFAssistantCore/*.swift \
    -o "$build_dir/libKSFAssistantCore.a" -emit-module-path "$build_dir/KSFAssistantCore.swiftmodule"
sources=()
for source in "$repo_root"/Sources/KSFAssistant/*.swift; do
    [[ "$source" == */KSFAssistantApp.swift ]] || sources+=("$source")
done
/usr/bin/swiftc -parse-as-library -D FEISHU_LAYOUT_PREVIEW -target "$architecture-apple-macos13.0" \
    -I "$build_dir" -L "$build_dir" -lKSFAssistantCore "${sources[@]}" \
    "$repo_root/Tests/FeishuLayoutSnapshots/main.swift" \
    -framework SwiftUI -framework AppKit -framework CoreImage -framework Security \
    -framework ServiceManagement -framework UserNotifications -o "$build_dir/render"
HOME="$preview_home" CFFIXED_USER_HOME="$preview_home" KSF_LAYOUT_OUTPUT="$build_dir/images" "$build_dir/render"
