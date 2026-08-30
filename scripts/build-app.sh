#!/bin/bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
direct_dir="$repo_root/.build/direct-release"
app_path="$repo_root/dist/Codex Usage Bar.app"
binary_path="$app_path/Contents/MacOS/CodexUsageBar"
icon_path="$repo_root/Resources/CodexStatusIcon.svg"
local_signing_identity='Codex Usage Bar Local Signing'
local_identity_check='identifier "com.lawis.codexusagebar" and anchor trusted'

if [[ "$app_path" != "$repo_root/dist/Codex Usage Bar.app" ]]; then
    echo "Refusing to remove an unexpected app path: $app_path" >&2
    exit 1
fi

rm -rf "$app_path"
mkdir -p "$direct_dir" "$app_path/Contents/MacOS" "$app_path/Contents/Resources"

if /usr/bin/xcrun --sdk macosx --show-sdk-platform-path >/dev/null 2>&1; then
    /usr/bin/swift build --package-path "$repo_root" -c release --arch arm64
    cp "$repo_root/.build/release/CodexUsageBar" "$binary_path"
else
    echo "Command Line Tools cannot provide SDK PlatformPath; using direct arm64 compilation."
    /usr/bin/swiftc \
        -emit-library \
        -static \
        -emit-module \
        -module-name CodexUsageCore \
        -target arm64-apple-macos13.0 \
        "$repo_root"/Sources/CodexUsageCore/*.swift \
        -o "$direct_dir/libCodexUsageCore.a" \
        -emit-module-path "$direct_dir/CodexUsageCore.swiftmodule"

    /usr/bin/swiftc \
        -O \
        -target arm64-apple-macos13.0 \
        -I "$direct_dir" \
        -L "$direct_dir" \
        -lCodexUsageCore \
        "$repo_root"/Sources/CodexUsageBar/*.swift \
        -framework SwiftUI \
        -framework AppKit \
        -framework CoreImage \
        -framework Security \
        -framework ServiceManagement \
        -framework UserNotifications \
        -o "$binary_path"
fi

cp "$repo_root/Resources/Info.plist" "$app_path/Contents/Info.plist"
cp "$icon_path" "$app_path/Contents/Resources/CodexStatusIcon.svg"
test -s "$app_path/Contents/Resources/CodexStatusIcon.svg"
/usr/bin/plutil -lint "$app_path/Contents/Info.plist"
if ! /usr/bin/security find-identity -v -p codesigning \
    /Users/lawis/Library/Keychains/login.keychain-db \
    | /usr/bin/grep -Fq '"Codex Usage Bar Local Signing"'; then
    echo "Missing trusted local signing identity: $local_signing_identity" >&2
    exit 1
fi
/usr/bin/codesign \
    --force \
    --sign "$local_signing_identity" \
    --timestamp=none \
    "$app_path"
/usr/bin/codesign --verify --deep --strict --verbose=2 "$app_path"
/usr/bin/codesign --verify --deep --strict --verbose=2 -R="$local_identity_check" "$app_path"
/usr/bin/codesign -d -r- "$app_path"
/usr/bin/file "$binary_path"
echo "Built $app_path"
