#!/bin/bash

set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
source_app="$repo_root/dist/Codex Usage Bar.app"
install_root="/Users/lawis/Applications"
target_app="$install_root/Codex Usage Bar.app"

"$repo_root/scripts/build-app.sh"
mkdir -p "$install_root"

if [[ "$target_app" != "/Users/lawis/Applications/Codex Usage Bar.app" ]]; then
    echo "Refusing to replace an unexpected app path: $target_app" >&2
    exit 1
fi

/usr/bin/pkill -TERM -x CodexUsageBar >/dev/null 2>&1 || true
rm -rf "$target_app"
/usr/bin/ditto "$source_app" "$target_app"
/usr/bin/codesign --verify --deep --strict --verbose=2 "$target_app"
/usr/bin/codesign -d -r- "$target_app"
echo "Installed $target_app"
