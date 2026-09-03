#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
source_app="$repo_root/dist/CodexAssistant.app"
install_root="${INSTALL_ROOT:-$HOME/Applications}"
target_name="${INSTALL_APP_NAME:-CodexAssistant.app}"
default_target="${install_root%/}/$target_name"
if [[ -d "$default_target" ]]; then
    existing_identifier="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$default_target/Contents/Info.plist" 2>/dev/null || true)"
    if [[ "$existing_identifier" != "com.codexassistant.desktop" && -z "${INSTALL_APP_NAME:-}" ]]; then
        target_name="CodexAssistant Team.app"
    fi
fi
target_app="${install_root%/}/$target_name"

"$repo_root/scripts/build-app.sh"
mkdir -p "$install_root"
if [[ -z "$install_root" || "$install_root" == "/" || "$target_name" == */* || "$target_app" != "${install_root%/}/$target_name" ]]; then
    echo "Refusing unsafe install target: $target_app" >&2
    exit 1
fi
/usr/bin/pkill -TERM -x CodexAssistant >/dev/null 2>&1 || true
/usr/bin/pkill -TERM -x CodexUsageBar >/dev/null 2>&1 || true
rm -rf "$target_app"
/usr/bin/ditto "$source_app" "$target_app"
/usr/bin/codesign --verify --deep --strict --verbose=2 "$target_app"
/usr/bin/lipo -info "$target_app/Contents/MacOS/CodexAssistant"
echo "Installed $target_app"
