#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
direct_root="$repo_root/.build/direct-release"
app_path="$repo_root/dist/KSFAssistant.app"
binary_path="$app_path/Contents/MacOS/KSFAssistant"
icon_path="$repo_root/Resources/CodexStatusIcon.svg"
architectures="${ARCHS:-arm64 x86_64}"
signing_mode="${SIGNING_MODE:-adhoc}"

CORE_TARGETS="darwin-arm64 darwin-x64" "$repo_root/scripts/build-core.sh"
(cd "$repo_root/Core" && go run ./cmd/ksf-assistant-build-assets \
    --repo-root "$repo_root" \
    --stage-lark darwin-arm64,darwin-x64 \
    --sbom)

if [[ "$app_path" != "$repo_root/dist/KSFAssistant.app" ]]; then
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
    /usr/bin/swiftc -emit-library -static -emit-module -module-name KSFAssistantCore \
        -target "$architecture-apple-macos13.0" \
        "$repo_root"/Sources/KSFAssistantCore/*.swift \
        -o "$architecture_dir/libKSFAssistantCore.a" \
        -emit-module-path "$architecture_dir/KSFAssistantCore.swiftmodule"
    slice="$architecture_dir/KSFAssistant"
    /usr/bin/swiftc -O -target "$architecture-apple-macos13.0" \
        -I "$architecture_dir" -L "$architecture_dir" -lKSFAssistantCore \
        "$repo_root"/Sources/KSFAssistant/*.swift \
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
cp "$repo_root/dist/core/darwin-arm64/ksf-assistant-core" "$app_path/Contents/Resources/core/darwin-arm64/ksf-assistant-core"
cp "$repo_root/dist/core/darwin-x64/ksf-assistant-core" "$app_path/Contents/Resources/core/darwin-x64/ksf-assistant-core"
mkdir -p "$app_path/Contents/Resources/compliance"
mkdir -p "$app_path/Contents/Resources/runtime/feishu-bridge/darwin-arm64" "$app_path/Contents/Resources/runtime/feishu-bridge/darwin-x64"
mkdir -p "$app_path/Contents/Resources/runtime/lark-cli/darwin-arm64" "$app_path/Contents/Resources/runtime/lark-cli/darwin-x64"
cp "$repo_root/dist/runtime/feishu-bridge/darwin-arm64/ksf-assistant-feishu-bridge" "$app_path/Contents/Resources/runtime/feishu-bridge/darwin-arm64/ksf-assistant-feishu-bridge"
cp "$repo_root/dist/runtime/feishu-bridge/darwin-x64/ksf-assistant-feishu-bridge" "$app_path/Contents/Resources/runtime/feishu-bridge/darwin-x64/ksf-assistant-feishu-bridge"
cp "$repo_root/dist/runtime/lark-cli/darwin-arm64/lark-cli" "$app_path/Contents/Resources/runtime/lark-cli/darwin-arm64/lark-cli"
cp "$repo_root/dist/runtime/lark-cli/darwin-x64/lark-cli" "$app_path/Contents/Resources/runtime/lark-cli/darwin-x64/lark-cli"
cp "$repo_root/LICENSE" "$app_path/Contents/Resources/compliance/LICENSE.txt"
cp "$repo_root/THIRD_PARTY_NOTICES.md" "$app_path/Contents/Resources/compliance/THIRD_PARTY_NOTICES.md"
cp "$repo_root/dist/sbom/KSFAssistant.spdx.json" "$app_path/Contents/Resources/compliance/KSFAssistant.spdx.json"
test -s "$app_path/Contents/Resources/CodexStatusIcon.svg"
test -x "$app_path/Contents/Resources/runtime/feishu-bridge/darwin-arm64/ksf-assistant-feishu-bridge"
test -x "$app_path/Contents/Resources/runtime/feishu-bridge/darwin-x64/ksf-assistant-feishu-bridge"
test -x "$app_path/Contents/Resources/runtime/lark-cli/darwin-arm64/lark-cli"
test -x "$app_path/Contents/Resources/runtime/lark-cli/darwin-x64/lark-cli"
test -s "$app_path/Contents/Resources/compliance/KSFAssistant.spdx.json"
test ! -e "$app_path/Contents/Resources/runtime/node"
test ! -e "$app_path/Contents/Resources/services/feishu-bridge"
/usr/bin/plutil -lint "$app_path/Contents/Info.plist"

case "$signing_mode" in
    adhoc)
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path/Contents/Resources/core/darwin-arm64/ksf-assistant-core"
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path/Contents/Resources/core/darwin-x64/ksf-assistant-core"
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path/Contents/Resources/runtime/feishu-bridge/darwin-arm64/ksf-assistant-feishu-bridge"
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path/Contents/Resources/runtime/feishu-bridge/darwin-x64/ksf-assistant-feishu-bridge"
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path/Contents/Resources/runtime/lark-cli/darwin-arm64/lark-cli"
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path/Contents/Resources/runtime/lark-cli/darwin-x64/lark-cli"
        /usr/bin/codesign --force --sign - --timestamp=none "$app_path"
        ;;
    identity)
        signing_identity="${SIGNING_IDENTITY:-}"
        [[ -n "$signing_identity" ]] || { echo "SIGNING_IDENTITY is required when SIGNING_MODE=identity." >&2; exit 1; }
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path/Contents/Resources/core/darwin-arm64/ksf-assistant-core"
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path/Contents/Resources/core/darwin-x64/ksf-assistant-core"
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path/Contents/Resources/runtime/feishu-bridge/darwin-arm64/ksf-assistant-feishu-bridge"
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path/Contents/Resources/runtime/feishu-bridge/darwin-x64/ksf-assistant-feishu-bridge"
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path/Contents/Resources/runtime/lark-cli/darwin-arm64/lark-cli"
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path/Contents/Resources/runtime/lark-cli/darwin-x64/lark-cli"
        /usr/bin/codesign --force --sign "$signing_identity" --timestamp=none "$app_path"
        ;;
    *) echo "SIGNING_MODE must be 'adhoc' or 'identity'." >&2; exit 1 ;;
esac

/usr/bin/codesign --verify --deep --strict --verbose=2 "$app_path"
/usr/bin/lipo -info "$binary_path"
/usr/bin/file "$binary_path"
echo "Built $app_path with signing mode $signing_mode"
