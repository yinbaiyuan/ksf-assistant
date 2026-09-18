#!/bin/bash
set -euo pipefail

repo_root="$(cd "$(dirname "$0")/.." && pwd)"
mode="${1:---mac}"
case "$mode" in --freeze-only|--mac|--windows|--all) ;; *) echo "Usage: package-preview.sh [--freeze-only|--mac|--windows|--all]" >&2; exit 1 ;; esac
mkdir -p "$repo_root/dist/previews"
preview_root="$(mktemp -d "$repo_root/dist/previews/worktree-XXXXXXXX")"
node --input-type=module - "$repo_root" "$preview_root" <<'NODE'
import { createHash } from 'node:crypto';
import { lstatSync, readdirSync, readFileSync, mkdirSync, copyFileSync, chmodSync, writeFileSync } from 'node:fs';
import path from 'node:path';
const [root, output] = process.argv.slice(2);
const allowlist = ['Core', 'Sources', 'Resources', 'Tests', 'runtime', 'patches', 'scripts', 'docs', 'Windows/src', 'Windows/assets', 'Windows/scripts', 'Windows/test', 'Windows/package.json', 'Windows/package-lock.json', 'Windows/README.md', 'version.json', 'Package.swift', 'AGENTS.md', 'README.md', 'DESIGN.md', 'PRODUCT.md', 'SUPPORT.md', 'SECURITY.md', 'PRIVACY.md', 'CONTRIBUTING.md', 'CODE_OF_CONDUCT.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md'];
const excluded = new Set(['.git', '.auth', '.build', 'dist', 'node_modules', '.DS_Store', '.cache', 'cache', 'output', 'coverage', 'logs', '.env']);
function inventory() {
  const files = {};
  function visit(relative) {
    const full = path.join(root, relative);
    const info = lstatSync(full);
    if (info.isSymbolicLink()) throw new Error(`Symlink source refused: ${relative}`);
    if (info.isDirectory()) {
      for (const name of readdirSync(full).sort()) if (!excluded.has(name) && (!name.startsWith('.env.') || name === '.env.example')) visit(`${relative}/${name}`);
    } else if (info.isFile()) {
      files[relative] = { sha256: createHash('sha256').update(readFileSync(full)).digest('hex'), mode: info.mode & 0o777 };
    } else throw new Error('Non-regular source refused');
  }
  for (const name of allowlist) visit(name);
  return files;
}
const before = inventory();
for (const [name, record] of Object.entries(before)) {
  const destination = path.join(output, 'source', name);
  mkdirSync(path.dirname(destination), { recursive: true });
  copyFileSync(path.join(root, name), destination);
  chmodSync(destination, record.mode);
  const actual = createHash('sha256').update(readFileSync(destination)).digest('hex');
  if (actual !== record.sha256) throw new Error('Shared worktree changed during freeze; retry at a checkpoint');
}
if (JSON.stringify(before) !== JSON.stringify(inventory())) throw new Error('Shared worktree changed during freeze; retry at a checkpoint');
const sourceSha256 = createHash('sha256').update(JSON.stringify(before)).digest('hex');
const manifest = { schemaVersion: 1, basis: 'frozen-current-worktree-allowlist', sourceSha256, sourceDateEpoch: Math.floor(Date.now()/1000), allowlist, files: before };
writeFileSync(path.join(output, 'source-manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`);
writeFileSync(path.join(output, 'source.sha256'), sourceSha256+'\n');
console.log(`Frozen ${Object.keys(before).length} current worktree files: ${sourceSha256}`);
NODE
export PREVIEW_SOURCE_SHA256="$(cat "$preview_root/source.sha256")"
export SOURCE_DATE_EPOCH="$(node -p "require(process.argv[1]).sourceDateEpoch" "$preview_root/source-manifest.json")"
source_root="$preview_root/source"
tar -czf "$preview_root/KSFAssistant-preview-source.tar.gz" -C "$preview_root" source source-manifest.json source.sha256
if [[ "$mode" == "--mac" || "$mode" == "--all" ]]; then
    (cd "$source_root" && bash scripts/build-app.sh)
    /usr/bin/ditto -c -k --sequesterRsrc --keepParent "$source_root/dist/KSFAssistant.app" "$preview_root/KSFAssistant-preview-mac-universal.zip"
fi
if [[ "$mode" == "--windows" || "$mode" == "--all" ]]; then
    (cd "$source_root/Windows" && npm ci && npm run dist:win)
    for installer in "$source_root"/Windows/dist/KSFAssistant-*-Windows-*.exe; do
        [[ -f "$installer" ]] || { echo "Missing Windows installer" >&2; exit 1; }
        cp "$installer" "$preview_root/"
    done
fi
node --input-type=module - "$preview_root" <<'NODE'
import { readFileSync, readdirSync, writeFileSync, lstatSync } from 'node:fs';
import { createHash } from 'node:crypto';
import path from 'node:path';
const root = process.argv[2];
const source = JSON.parse(readFileSync(path.join(root, 'source-manifest.json')));
for (const [name, entry] of Object.entries(source.files)) {
  const file = path.join(root, 'source', name);
  if (!lstatSync(file).isFile() || createHash('sha256').update(readFileSync(file)).digest('hex') !== entry.sha256) throw new Error('Frozen source changed during build');
}
const artifacts = {};
for (const name of readdirSync(root).sort()) {
  const file = path.join(root, name);
  if (lstatSync(file).isFile()) artifacts[name] = createHash('sha256').update(readFileSync(file)).digest('hex');
}
writeFileSync(path.join(root, 'artifacts.sha256'), Object.entries(artifacts).map(([name, hash])=>`${hash}  ${name}\n`).join(''));
console.log(`Preview ready (not installed): ${root}`);
NODE
