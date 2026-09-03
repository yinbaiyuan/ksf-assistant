#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
direct_root="$repo_root/.build/direct-release"
app_path="$repo_root/dist/Codex Usage Bar.app"
binary_path="$app_path/Contents/MacOS/CodexUsageBar"
icon_path="$repo_root/Resources/CodexStatusIcon.svg"
architectures="${ARCHS:-arm64 x86_64}"
signing_mode="${SIGNING_MODE:-adhoc}"

CORE_TARGETS="darwin-arm64 darwin-x64" "$repo_root/scripts/build-core.sh"
node "$repo_root/scripts/prepare-feishu-runtime.mjs" --platform darwin --arch arm64 --arch x64

if [[ "$app_path" != "$repo_root/dist/Codex Usage Bar.app" ]]; then
    echo "Refusing to remove an unexpected app path: $app_path" >&2
    exit 1
fi
rm -rf "$app_path" "$direct_root"
mkdir -p "$direct_root" "$app_path/Contents/MacOS" "$app_path/Contents/Resources"

binaries=()
for architecture in $architectures; do
    case "$architecture" in arm64|x86_64) ;; *) echo "Unsupported architecture: $architecture" >&2; exit 1 ;; esac
    architecture_dir="$direct_root/$architecture"
    mkdir -p "$architecture_dir"
    echo "Building $architecture release slice."
    /usr/bin/swiftc -emit-library -static -emit-module -module-name CodexUsageCore \
        -target "$architecture-apple-macos13.0" \
        "$repo_root"/Sources/CodexUsageCore/*.swift \
        -o "$architecture_dir/libCodexUsageCore.a" \
        -emit-module-path "$architecture_dir/CodexUsageCore.swiftmodule"
    slice="$architecture_dir/CodexUsageBar"
    /usr/bin/swiftc -O -target "$architecture-apple-macos13.0" \
        -I "$architecture_dir" -L "$architecture_dir" -lCodexUsageCore \
        "$repo_root"/Sources/CodexUsageBar/*.swift \
        -framework SwiftUI -framework AppKit -framework CoreImage -framework Security \
        -framework ServiceManagement -framework UserNotifications -o "$slice"
    binaries+=("$slice")
done

if [[ ${#binaries[@]} -eq 1 ]]; then
    cp "${binaries[0]}" "$binary_path"
else
    /usr/bin/lipo -create "${binaries[@]}" -output "$binary_path"
fi
cp "$repo_root/Resources/Info.plist" "$app_path/Contents/Info.plist"
cp "$icon_path" "$app_path/Contents/Resources/CodexStatusIcon.svg"
mkdir -p "$app_path/Contents/Resources/core/darwin-arm64" "$app_path/Contents/Resources/core/darwin-x64"
cp "$repo_root/dist/core/darwin-arm64/codex-usage-core" "$app_path/Contents/Resources/core/darwin-arm64/codex-usage-core"
cp "$repo_root/dist/core/darwin-x64/codex-usage-core" "$app_path/Contents/Resources/core/darwin-x64/codex-usage-core"
mkdir -p "$app_path/Contents/Resources/services" "$app_path/Contents/Resources/runtime/node/darwin-arm64" "$app_path/Contents/Resources/runtime/node/darwin-x64"
cp -R "$repo_root/dist/services/feishu-bridge" "$app_path/Contents/Resources/services/feishu-bridge"
cp "$repo_root/dist/runtime/node/darwin-arm64/node" "$app_path/Contents/Resources/runtime/node/darwin-arm64/node"
cp "$repo_root/dist/runtime/node/darwin-x64/node" "$app_path/Contents/Resources/runtime/node/darwin-x64/node"
test -s "$app_path/Contents/Resources/CodexStatusIcon.svg"
test -x "$app_path/Contents/Resources/runtime/node/darwin-arm64/node"
test -x "$app_path/Contents/Resources/runtime/node/darwin-x64/node"
test -f "$app_path/Contents/Resources/services/feishu-bridge/scripts/bridge-client.js"
/usr/bin/plutil -lint "$app_path/Contents/Info.plist"

case "$signing_mode" in
    adhoc)
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path/Contents/Resources/core/darwin-arm64/codex-usage-core"
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path/Contents/Resources/core/darwin-x64/codex-usage-core"
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path/Contents/Resources/runtime/node/darwin-arm64/node"
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path/Contents/Resources/runtime/node/darwin-x64/node"
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path"
        ;;
    identity)
        signing_identity="${SIGNING_IDENTITY:-}"
        [[ -n "$signing_identity" ]] || { echo "SIGNING_IDENTITY is required when SIGNING_MODE=identity." >&2; exit 1; }
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path/Contents/Resources/core/darwin-arm64/codex-usage-core"
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path/Contents/Resources/core/darwin-x64/codex-usage-core"
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path/Contents/Resources/runtime/node/darwin-arm64/node"
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path/Contents/Resources/runtime/node/darwin-x64/node"
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path"
        ;;
    *) echo "SIGNING_MODE must be 'adhoc' or 'identity'." >&2; exit 1 ;;
esac

/usr/bin/codesign --verify --deep --strict --verbose=2 "$app_path"
/usr/bin/lipo -info "$binary_path"
/usr/bin/file "$binary_path"
echo "Built $app_path with signing mode $signing_mode"
