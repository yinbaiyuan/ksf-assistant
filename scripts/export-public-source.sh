#!/bin/bash
set -euo pipefail
export LC_ALL=C
export LANG=C

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
target="${1:-}"
if [[ -z "$target" || "$target" != /* ]]; then
    echo "Usage: $0 /absolute/empty/output-directory" >&2
    exit 2
fi
target="${target%/}"
case "$target" in ""|/|"$repo_root"|"$repo_root"/*|"$(dirname "$repo_root")")
    echo "Refusing unsafe export target: $target" >&2
    exit 2
esac
if [[ -e "$target" && -n "$(find "$target" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]]; then
    echo "Export target must not exist or must be empty: $target" >&2
    exit 2
fi

mkdir -p "$target"
while IFS= read -r -d '' relative; do
    source_path="$repo_root/$relative"
    destination="$target/$relative"
    mkdir -p "$(dirname "$destination")"
    cp -pPR "$source_path" "$destination"
done < <(git -C "$repo_root" ls-files -z)

git -C "$repo_root" ls-files | LC_ALL=C sort > "$target/SOURCES.txt"
: > "$target/SHA256SUMS"
while IFS= read -r relative; do
    [[ -f "$target/$relative" ]] || continue
    digest="$(/usr/bin/shasum -a 256 "$target/$relative" | awk '{print $1}')"
    printf '%s  %s\n' "$digest" "$relative" >> "$target/SHA256SUMS"
done < "$target/SOURCES.txt"

if [[ -e "$target/.git" ]]; then
    echo "Export unexpectedly contains Git metadata." >&2
    exit 1
fi
"$target/scripts/check-release-hygiene.sh"
echo "Public source snapshot exported to $target"
