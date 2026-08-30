#!/bin/bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
build_dir="$repo_root/.build/direct-tests"
mkdir -p "$build_dir"

if /usr/bin/xcrun --sdk macosx --show-sdk-platform-path >/dev/null 2>&1; then
    exec /usr/bin/swift test --package-path "$repo_root"
fi

echo "Command Line Tools cannot provide SDK PlatformPath; using the Swift 5.8 direct-compile test fallback."

/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    "$repo_root"/Sources/CodexUsageCore/*.swift \
    "$repo_root/Tests/Standalone/main.swift" \
    -o "$build_dir/model-tests"
"$build_dir/model-tests"

/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    "$repo_root"/Sources/CodexUsageCore/*.swift \
    "$repo_root/Tests/ProtocolStandalone/main.swift" \
    -o "$build_dir/protocol-tests"
"$build_dir/protocol-tests"

/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    "$repo_root"/Sources/CodexUsageCore/*.swift \
    "$repo_root/Tests/TaskActivityStandalone/main.swift" \
    -o "$build_dir/task-activity-tests"
"$build_dir/task-activity-tests"

/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    "$repo_root"/Sources/CodexUsageCore/*.swift \
    "$repo_root/Tests/TaskSubmissionStandalone/main.swift" \
    -o "$build_dir/task-submission-tests"
"$build_dir/task-submission-tests"

/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    "$repo_root"/Sources/CodexUsageCore/*.swift \
    "$repo_root/Tests/ProjectStandalone/main.swift" \
    -o "$build_dir/project-workbench-tests"
"$build_dir/project-workbench-tests"

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
    "$repo_root/Tests/WeChatStandalone/main.swift" \
    -o "$build_dir/wechat-tests"
"$build_dir/wechat-tests"

/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    -I "$build_dir" \
    -L "$build_dir" \
    -lCodexUsageCore \
    "$repo_root/Sources/CodexUsageBar/CodexTaskOpener.swift" \
    "$repo_root/Tests/TaskOpeningStandalone/main.swift" \
    -framework AppKit \
    -o "$build_dir/task-opening-tests"
"$build_dir/task-opening-tests"

/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    -I "$build_dir" \
    -L "$build_dir" \
    -lCodexUsageCore \
    "$repo_root/Sources/CodexUsageBar/KSFBridgeClient.swift" \
    "$repo_root/Tests/KSFBridgeStandalone/main.swift" \
    -o "$build_dir/ksf-bridge-tests"
"$build_dir/ksf-bridge-tests"

view_model_test_app="$build_dir/UsageViewModelTests.app"
view_model_test_binary="$view_model_test_app/Contents/MacOS/CodexUsageBar"
mkdir -p "$view_model_test_app/Contents/MacOS"
cp "$repo_root/Resources/Info.plist" "$view_model_test_app/Contents/Info.plist"
/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    -I "$build_dir" \
    -L "$build_dir" \
    -lCodexUsageCore \
    "$repo_root/Sources/CodexUsageBar/CodexLocator.swift" \
    "$repo_root/Sources/CodexUsageBar/CodexTaskOpener.swift" \
    "$repo_root/Sources/CodexUsageBar/KSFBridgeClient.swift" \
    "$repo_root/Sources/CodexUsageBar/ProcessAppServerTransport.swift" \
    "$repo_root/Sources/CodexUsageBar/ProjectUsageStore.swift" \
    "$repo_root/Sources/CodexUsageBar/SnapshotStore.swift" \
    "$repo_root/Sources/CodexUsageBar/SystemServices.swift" \
    "$repo_root/Sources/CodexUsageBar/TerminalActionLauncher.swift" \
    "$repo_root/Sources/CodexUsageBar/UnixSocketDesktopIPCTransport.swift" \
    "$repo_root/Sources/CodexUsageBar/UsageViewModel.swift" \
    "$repo_root/Sources/CodexUsageBar/WeChatConnector.swift" \
    "$repo_root/Tests/UsageViewModelStandalone/main.swift" \
    -framework AppKit \
    -framework Security \
    -framework ServiceManagement \
    -framework UserNotifications \
    -o "$view_model_test_binary"
"$view_model_test_binary"

/usr/bin/swiftc \
    -parse-as-library \
    -target arm64-apple-macos13.0 \
    "$repo_root/Sources/CodexUsageBar/StatusItemImageRenderer.swift" \
    "$repo_root/Tests/StatusItemImageStandalone/main.swift" \
    -framework AppKit \
    -o "$build_dir/status-item-image-tests"
"$build_dir/status-item-image-tests"

"$repo_root/scripts/build-app.sh"
