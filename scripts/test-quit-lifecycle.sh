#!/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
build_dir="$repo_root/.build/quit-lifecycle-tests"
mkdir -p "$build_dir"
sources=()
for source in "$repo_root"/Sources/CodexUsageBar/*.swift; do
    case "$source" in
        */CodexUsageBarApp.swift|*/UsagePopoverView.swift|*/StatusItemImageRenderer.swift) ;;
        *) sources+=("$source") ;;
    esac
done
/usr/bin/swiftc -emit-library -static -emit-module -module-name CodexUsageCore \
    "$repo_root"/Sources/CodexUsageCore/*.swift \
    -o "$build_dir/libCodexUsageCore.a" \
    -emit-module-path "$build_dir/CodexUsageCore.swiftmodule"
/usr/bin/swiftc -parse-as-library -I "$build_dir" -L "$build_dir" -lCodexUsageCore \
    "${sources[@]}" "$repo_root/Tests/QuitLifecycleStandalone/main.swift" \
    -framework AppKit -framework SwiftUI -framework CoreImage -framework Security \
    -framework ServiceManagement -framework UserNotifications \
    -o "$build_dir/quit-lifecycle-tests"
"$build_dir/quit-lifecycle-tests" --system
"$build_dir/quit-lifecycle-tests" --button
"$build_dir/quit-lifecycle-tests" --button --repeat
