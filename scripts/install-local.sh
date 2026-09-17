#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
source_app="${INSTALL_SOURCE_APP:-$repo_root/dist/KSFAssistant.app}"
install_root="${INSTALL_ROOT:-$HOME/Applications}"
target_name="${INSTALL_APP_NAME:-KSFAssistant.app}"
if [[ "$install_root" != /* || "$install_root" == "/" || "$target_name" != *.app || "$target_name" == */* || "$target_name" == .* ]]; then
    echo "Refusing unsafe install target." >&2
    exit 1
fi
if [[ "${INSTALL_BUILD:-1}" == "1" && -z "${INSTALL_SOURCE_APP:-}" ]]; then
    bash "$repo_root/scripts/build-app.sh"
fi
mkdir -p "$install_root"
install_root="$(cd "$install_root" && pwd -P)"
target_app="$install_root/$target_name"
if [[ -L "$target_app" || -L "$source_app" ]]; then
    echo "Refusing symbolic-link app target or source." >&2
    exit 1
fi
source_app="$(cd "$source_app" && pwd -P)"
if [[ "$source_app" == "$target_app" ]]; then
    echo "Source app must differ from the installed app." >&2
    exit 1
fi
lock="$install_root/.ksfassistant-install.lock"
mkdir "$lock" || { echo "Another install is running or an interrupted install requires inspection." >&2; exit 1; }
stage_root="$(mktemp -d "$install_root/.ksfassistant-stage-XXXXXXXX")"
stage_app="$stage_root/KSFAssistant.app"
previous_app="$install_root/KSFAssistant.previous.$(date +%Y%m%d-%H%M%S).$$.app"
swapped=0
cleanup() {
    if [[ "$swapped" == "0" ]]; then rm -rf "$stage_root"; fi
    rmdir "$lock" 2>/dev/null || true
}
trap cleanup EXIT

validate_artifact() {
    local app="$1"
    local identifier
    identifier="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$app/Contents/Info.plist")" || return 1
    [[ "$identifier" == "com.ksfassistant.desktop" ]] || { echo "Unexpected app identifier." >&2; return 1; }
    /usr/bin/codesign --verify --deep --strict "$app" || return 1
    /usr/bin/lipo "$app/Contents/MacOS/KSFAssistant" -verify_arch arm64 x86_64 || return 1
    for component in toolchain task; do
        /usr/bin/lipo "$app/Contents/MacOS/ksf-assistant-$component" -verify_arch arm64 x86_64 || return 1
        for target in darwin-arm64 darwin-x64; do
            test -x "$app/Contents/Resources/runtime/$component/$target/ksf-assistant-$component" || return 1
        done
    done
    test -x "$app/Contents/Resources/runtime/feishu-bridge/darwin-arm64/ksf-assistant-feishu-bridge" || return 1
    test -x "$app/Contents/Resources/runtime/feishu-bridge/darwin-x64/ksf-assistant-feishu-bridge" || return 1
}

/usr/bin/ditto "$source_app" "$stage_app"
validate_artifact "$stage_app"
if [[ -e "$target_app" ]]; then
    existing_identifier="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$target_app/Contents/Info.plist" 2>/dev/null || true)"
    [[ "$existing_identifier" == "com.ksfassistant.desktop" ]] || { echo "Existing app belongs to another product; refusing overwrite." >&2; exit 1; }
fi
identified_apps=()
[[ ! -e "$target_app" ]] || identified_apps+=("$target_app")
for legacy_name in "CodexAssistant.app" "Codex Usage Bar.app" "CodexUsageBar.app"; do
    candidate="$install_root/$legacy_name"
    [[ -d "$candidate" && ! -L "$candidate" ]] || continue
    identifier="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$candidate/Contents/Info.plist" 2>/dev/null || true)"
    case "$legacy_name:$identifier" in
        "CodexAssistant.app:com.codexassistant.desktop"|"Codex Usage Bar.app:com.ksf.codexusagebar"|"CodexUsageBar.app:com.ksf.codexusagebar") identified_apps+=("$candidate") ;;
    esac
done
/usr/bin/swiftc -o "$stage_root/atomic-swap" - <<'SWIFT'
import Darwin
import Foundation
let arguments = CommandLine.arguments
guard arguments.count == 3 else { exit(2) }
let result = arguments[1].withCString { source in
    arguments[2].withCString { target in
        renameatx_np(AT_FDCWD, source, AT_FDCWD, target, UInt32(RENAME_SWAP))
    }
}
if result != 0 { exit(1) }
SWIFT

is_identified_executable() {
    local executable="$1"
    local app
    [[ ${#identified_apps[@]} -gt 0 ]] || return 1
    for app in "${identified_apps[@]}"; do
        case "$executable" in "$app/Contents/"*) return 0 ;; esac
    done
    return 1
}
running_target_pids() {
    /bin/ps -axo pid=,comm= | while read -r pid executable; do
        if is_identified_executable "$executable"; then printf '%s\n' "$pid"; fi
    done
}
pids="$(running_target_pids)"
for pid in $pids; do
    executable="$(/bin/ps -p "$pid" -o comm= 2>/dev/null || true)"
    if is_identified_executable "$executable"; then /bin/kill -TERM "$pid" 2>/dev/null || true; fi
done
for attempt in {1..50}; do
    [[ -z "$(running_target_pids)" ]] && break
    sleep 0.1
done
[[ -z "$(running_target_pids)" ]] || { echo "Old app processes have not exited; original app preserved." >&2; exit 1; }
if [[ -e "$target_app" ]]; then
    swapped=1
    if ! "$stage_root/atomic-swap" "$stage_app" "$target_app"; then
        swapped=0
        echo "Atomic app swap failed; original preserved." >&2
        exit 1
    fi
    if ! validate_artifact "$target_app"; then
        "$stage_root/atomic-swap" "$stage_app" "$target_app" || { echo "Rollback failed; previous app preserved at $stage_app" >&2; exit 1; }
        swapped=0
        echo "Validation failed; original app restored." >&2
        exit 1
    fi
    mv "$stage_app" "$previous_app"
    swapped=0
else
    mv "$stage_app" "$target_app"
    if ! validate_artifact "$target_app"; then
        mv "$target_app" "$stage_app"
        echo "Validation failed; installation rolled back." >&2
        exit 1
    fi
fi
echo "Installed $target_app"
[[ ! -d "$previous_app" ]] || echo "Previous app preserved: $previous_app"
echo "Independent CLI credentials and PATH are unchanged. Legacy Agent middleware retirement is a separate ownership-checked action."
echo "Artifact health only: signature, architecture and required resources verified. Runtime/business health is not tested; launch and verify it separately."
