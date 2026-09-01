#!/bin/bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
build_dir="$repo_root/.build/live-probe"
mkdir -p "$build_dir"

/usr/bin/swiftc \
    -emit-library \
    -static \
    -emit-module \
    -module-name CodexUsageCore \
    -target arm64-apple-macos13.0 \
    "$repo_root"/Sources/CodexUsageCore/*.swift \
    -o "$build_dir/libCodexUsageCore.a" \
    -emit-module-path "$build_dir/CodexUsageCore.swiftmodule"

/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    -I "$build_dir" \
    -L "$build_dir" \
    -lCodexUsageCore \
    "$repo_root/Sources/CodexUsageBar/AppConfiguration.swift" \
    "$repo_root/Sources/CodexUsageBar/CodexLocator.swift" \
    "$repo_root/Sources/CodexUsageBar/ProcessAppServerTransport.swift" \
    "$repo_root/Tests/LiveStandalone/main.swift" \
    -o "$build_dir/live-probe"

"$build_dir/live-probe"

/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    -I "$build_dir" \
    -L "$build_dir" \
    -lCodexUsageCore \
    "$repo_root/Sources/CodexUsageBar/AppConfiguration.swift" \
    "$repo_root/Sources/CodexUsageBar/UnixSocketDesktopIPCTransport.swift" \
    "$repo_root/Tests/TaskActivityLiveStandalone/main.swift" \
    -framework AppKit \
    -o "$build_dir/task-activity-live-probe"

"$build_dir/task-activity-live-probe"
