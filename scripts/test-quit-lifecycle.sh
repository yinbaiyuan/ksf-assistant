#!/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
build_dir="$repo_root/.build/quit-lifecycle-tests"
mkdir -p "$build_dir"
sources=()
for source in "$repo_root"/Sources/KSFAssistant/*.swift; do
    case "$source" in
        */KSFAssistantApp.swift|*/UsagePopoverView.swift|*/StatusItemImageRenderer.swift) ;;
        *) sources+=("$source") ;;
    esac
done
/usr/bin/swiftc -emit-library -static -emit-module -module-name KSFAssistantCore \
    "$repo_root"/Sources/KSFAssistantCore/*.swift \
    -o "$build_dir/libKSFAssistantCore.a" \
    -emit-module-path "$build_dir/KSFAssistantCore.swiftmodule"
/usr/bin/swiftc -parse-as-library -I "$build_dir" -L "$build_dir" -lKSFAssistantCore \
    "${sources[@]}" "$repo_root/Tests/QuitLifecycleStandalone/main.swift" \
    -framework AppKit -framework SwiftUI -framework CoreImage -framework Security \
    -framework ServiceManagement -framework UserNotifications \
    -o "$build_dir/quit-lifecycle-tests"
"$build_dir/quit-lifecycle-tests" --system
"$build_dir/quit-lifecycle-tests" --button
"$build_dir/quit-lifecycle-tests" --button --repeat
