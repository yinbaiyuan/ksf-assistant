#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
direct_root="$repo_root/.build/direct-release"
app_path="$repo_root/dist/KSFAssistant.app"
binary_path="$app_path/Contents/MacOS/KSFAssistant"
icon_path="$repo_root/Resources/CodexStatusIcon.svg"
architectures="${ARCHS:-arm64 x86_64}"
signing_mode="${SIGNING_MODE:-adhoc}"
release_version="$(node -p "require(process.argv[1]).productVersion" "$repo_root/version.json")"
bundle_release_version="$(/usr/libexec/PlistBuddy -c 'Print :KSFAssistantReleaseVersion' "$repo_root/Resources/Info.plist")"
bundle_short_version="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$repo_root/Resources/Info.plist")"
if [[ "$bundle_release_version" != "$release_version" || "$bundle_short_version" != "${release_version%%-*}" ]]; then
    echo "Application release versions disagree; refusing package build." >&2
    exit 1
fi
node "$repo_root/scripts/check-product-identity.mjs"

CORE_TARGETS="darwin-arm64 darwin-x64" "$repo_root/scripts/build-core.sh"
node "$repo_root/scripts/prepare-feishu-runtime.mjs" --platform darwin --arch arm64 --arch x64
bash "$repo_root/scripts/check-managed-feishu-contract.sh"
node "$repo_root/scripts/generate-sbom.mjs"

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
cp "$repo_root/dist/runtime/lark-cli-runtime.json" "$app_path/Contents/Resources/runtime/lark-cli-runtime.json"
cp -R "$repo_root/dist/runtime/lark-skills" "$app_path/Contents/Resources/runtime/lark-skills"
for component in toolchain task; do
    for target in darwin-arm64 darwin-x64; do
        mkdir -p "$app_path/Contents/Resources/runtime/$component/$target"
        cp "$repo_root/dist/runtime/$component/$target/ksf-assistant-$component" "$app_path/Contents/Resources/runtime/$component/$target/ksf-assistant-$component"
    done
    /usr/bin/lipo -create "$repo_root/dist/runtime/$component/darwin-arm64/ksf-assistant-$component" \
        "$repo_root/dist/runtime/$component/darwin-x64/ksf-assistant-$component" \
        -output "$app_path/Contents/MacOS/ksf-assistant-$component"
done
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
        ;;
    *) echo "SIGNING_MODE must be 'adhoc' or 'identity'." >&2; exit 1 ;;
esac

identity="${signing_identity:--}"
for component in toolchain task; do
    for target in darwin-arm64 darwin-x64; do
        /usr/bin/codesign --force --sign "$identity" --timestamp=none "$app_path/Contents/Resources/runtime/$component/$target/ksf-assistant-$component"
    done
    /usr/bin/codesign --force --sign "$identity" --timestamp=none "$app_path/Contents/MacOS/ksf-assistant-$component"
done
node --input-type=module - "$app_path/Contents/Resources" <<'NODE'
import { readFileSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import path from 'node:path';
const root = process.argv[2];
const file = path.join(root, 'runtime/lark-cli-runtime.json');
const manifest = JSON.parse(readFileSync(file));
for (const target of ['darwin-arm64', 'darwin-x64']) {
    const item = manifest.artifacts[target];
    item.controlledExecutableSha256 = item.executableSha256;
    item.executableSha256 = createHash('sha256').update(readFileSync(path.join(root, 'runtime/lark-cli', target, item.executable))).digest('hex');
    item.taskExecutableSha256 = createHash('sha256').update(readFileSync(path.join(root, 'runtime/task', target, 'ksf-assistant-task'))).digest('hex');
}
writeFileSync(file, `${JSON.stringify(manifest, null, 2)}\n`);
NODE
/usr/bin/codesign --force --sign "$identity" --timestamp=none "$app_path"

/usr/bin/codesign --verify --deep --strict --verbose=2 "$app_path"
/usr/bin/lipo -info "$binary_path"
/usr/bin/file "$binary_path"
echo "Built $app_path with signing mode $signing_mode"
