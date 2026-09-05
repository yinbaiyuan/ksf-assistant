#!/bin/bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
if ! /usr/bin/xcrun --sdk macosx --show-sdk-platform-path >/dev/null 2>&1; then
    echo "Swift tests require a macOS SDK with Swift Package Manager test support. Configure the developer toolchain; legacy standalone fallback tests are no longer supported." >&2
    exit 1
fi

build_dir="$repo_root/.build/direct-tests"
mkdir -p "$build_dir"

node "$repo_root/scripts/check-product-identity.mjs"
bash "$repo_root/scripts/test-identity-migration.sh"

(cd "$repo_root/Core" && go test ./...)
(cd "$repo_root/Services/FeishuBridge" && npm test)
(cd "$repo_root/Windows" && npm test)

host_arch="$(uname -m)"
case "$host_arch" in
    arm64|x86_64) ;;
    *) echo "Unsupported macOS test architecture: $host_arch" >&2; exit 1 ;;
esac
/usr/bin/swiftc \
    -emit-library \
    -static \
    -emit-module \
    -module-name KSFAssistantCore \
    -target "$host_arch-apple-macos13.0" \
    "$repo_root"/Sources/KSFAssistantCore/*.swift \
    -o "$build_dir/libKSFAssistantCore.a" \
    -emit-module-path "$build_dir/KSFAssistantCore.swiftmodule"
/usr/bin/swiftc \
    -parse-as-library \
    -target "$host_arch-apple-macos13.0" \
    -I "$build_dir" \
    -L "$build_dir" \
    -lKSFAssistantCore \
    "$repo_root/Sources/KSFAssistant/FeishuModels.swift" \
    "$repo_root/Sources/KSFAssistant/CoreServiceProcessClient.swift" \
    "$repo_root/Tests/CoreServiceProcessStandalone/main.swift" \
    -o "$build_dir/core-service-process-tests"
"$build_dir/core-service-process-tests"
bash "$repo_root/scripts/test-quit-lifecycle.sh"

/usr/bin/swift test --package-path "$repo_root"
"$repo_root/scripts/build-app.sh"
