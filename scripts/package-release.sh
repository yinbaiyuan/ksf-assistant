#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
version="${RELEASE_VERSION:-0.10.0-preview.1}"
release_root="$repo_root/dist/release-$version"
app_path="$repo_root/dist/CodexAssistant.app"
zip_path="$release_root/CodexAssistant-$version-macOS-universal.zip"
source_path="$release_root/CodexAssistant-$version-source.tar.gz"

if [[ -n "$(git -C "$repo_root" status --porcelain)" ]]; then
    echo "Release packaging requires a clean Git worktree." >&2
    exit 1
fi
SIGNING_MODE="${SIGNING_MODE:-adhoc}" "$repo_root/scripts/build-app.sh"
rm -rf "$release_root"
mkdir -p "$release_root"
/usr/bin/ditto -c -k --sequesterRsrc --keepParent "$app_path" "$zip_path"
git -C "$repo_root" archive --format=tar.gz --prefix="CodexAssistant-$version/" HEAD > "$source_path"
cp "$repo_root/dist/sbom/CodexAssistant.spdx.json" "$release_root/CodexAssistant-$version.spdx.json"
git -C "$repo_root" ls-tree -r --name-only HEAD | LC_ALL=C sort > "$release_root/SOURCES.txt"
(
    cd "$release_root"
    /usr/bin/shasum -a 256 "$(basename "$zip_path")" "$(basename "$source_path")" "CodexAssistant-$version.spdx.json" > SHA256SUMS
)
cat > "$release_root/RELEASE.txt" <<EOF
CodexAssistant $version
Bundle ID: com.codexassistant.desktop
Architectures: arm64, x86_64
Signing: ${SIGNING_MODE:-adhoc}
Notarized: no
Distribution: public cross-platform preview

This build is not notarized. Verify SHA256SUMS before installation and follow README.md for Gatekeeper instructions.
EOF
/usr/bin/unzip -t "$zip_path" >/dev/null
/usr/bin/lipo -info "$app_path/Contents/MacOS/CodexAssistant"
echo "Packaged release artifacts in $release_root"
