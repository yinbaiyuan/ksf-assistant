#!/bin/bash
set -euo pipefail
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/ksfassistant-identity-tests.XXXXXX")"
trap 'rm -rf "$build_dir"' EXIT
export CLANG_MODULE_CACHE_PATH="$build_dir/module-cache"
"${SWIFTC:-/usr/bin/swiftc}" -emit-library -static -emit-module -module-name KSFAssistantCore \
    -target "$(uname -m)-apple-macos13.0" \
    "$repo_root"/Sources/KSFAssistantCore/*.swift \
    -o "$build_dir/libKSFAssistantCore.a" \
    -emit-module-path "$build_dir/KSFAssistantCore.swiftmodule"
"${SWIFTC:-/usr/bin/swiftc}" -parse-as-library \
    -target "$(uname -m)-apple-macos13.0" \
    -I "$build_dir" -L "$build_dir" -lKSFAssistantCore \
    "$repo_root/Sources/KSFAssistant/AppConfiguration.swift" \
    "$repo_root/Tests/IdentityMigrationStandalone/main.swift" \
    -o "$build_dir/identity-migration-tests"
"$build_dir/identity-migration-tests"
if [[ "${1:-}" == "--with-view-model" ]]; then
    "${SWIFTC:-/usr/bin/swiftc}" -typecheck \
        -target "$(uname -m)-apple-macos13.0" \
        -I "$build_dir" "$repo_root"/Sources/KSFAssistant/*.swift
    echo "PASS: complete macOS host typechecked (not executed)"
fi
