#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
host="$(node -p "process.platform + '-' + (process.arch === 'x64' ? 'x64' : process.arch)")"
suffix=""
[[ "$host" != win32-* ]] || { host="windows-${host#win32-}"; suffix=".exe"; }
binary="$repo_root/dist/runtime/lark-cli/$host/lark-cli$suffix"
[[ -f "$binary" ]] || { echo "Stage the hash-pinned host CLI before running the mandatory offline contract check." >&2; exit 1; }
temporary="$(mktemp -d "${TMPDIR:-/tmp}/ksfas-contract-XXXXXXXX")"
trap 'rm -rf "$temporary"' EXIT
python3 - "$repo_root" "$temporary" <<'PY'
import hashlib
import json
import pathlib
import sys
import tarfile

root, destination = map(pathlib.Path, sys.argv[1:])
source = json.loads((root / 'runtime/lark-skills.json').read_bytes())['source']
archive = root / 'dist/cache/lark-cli' / source['archive']
if hashlib.sha256(archive.read_bytes()).hexdigest() != source['sha256']:
    raise SystemExit('Pinned source archive checksum mismatch')
with tarfile.open(archive) as bundle:
    for member in bundle.getmembers():
        relative = pathlib.PurePosixPath(member.name)
        if relative.is_absolute() or '..' in relative.parts or member.issym() or member.islnk():
            raise SystemExit('Unsafe pinned source archive entry')
        target = destination.joinpath(*relative.parts)
        if member.isdir():
            target.mkdir(parents=True, exist_ok=True)
        elif member.isfile():
            target.parent.mkdir(parents=True, exist_ok=True)
            with target.open('xb') as output:
                output.write(bundle.extractfile(member).read())
        else:
            raise SystemExit('Non-regular pinned source entry')
print('Pinned source archive verified and isolated')
PY
source_root="$(node -p "require(process.argv[1]).source.root" "$repo_root/runtime/lark-skills.json")"
KSF_USERCOMMAND_PINNED_SOURCE="$temporary/$source_root" \
    node --test "$repo_root/scripts/test-lark-skills-adapter.mjs"
KSF_SKILLS_SOURCE="$temporary/$source_root/skills" \
    python3 -B "$repo_root/scripts/test-lark-skill-python.py"
node --test "$repo_root/scripts/test-lark-skills-upgrade.mjs"
KSF_USERCOMMAND_PINNED_SOURCE="$temporary/$source_root" \
    bash "$repo_root/scripts/verify-execution-contract.sh" "$binary"
echo "PASS fixed official CLI execution contract"
