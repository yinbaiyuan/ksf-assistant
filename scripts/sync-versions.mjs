import assert from 'node:assert/strict';
import { readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (relative) => readFileSync(path.join(root, relative), 'utf8');
const write = (relative, value) => writeFileSync(path.join(root, relative), value);
const product = JSON.parse(read('version.json'));
const lark = JSON.parse(read('runtime/lark-cli-runtime.json'));

assert.equal(product.schemaVersion, 1, 'unsupported product version schema');
assert.match(product.productVersion, /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/, 'invalid product version');
assert.ok(Number.isSafeInteger(product.macOSBuildNumber) && product.macOSBuildNumber > 0, 'invalid macOS build number');
assert.match(lark.version, /^\d+\.\d+\.\d+-ksfassistant\.\d+$/, 'invalid managed lark-cli version');
assert.match(lark.upstreamVersion, /^\d+\.\d+\.\d+$/, 'invalid upstream lark-cli version');
assert.ok(lark.version.startsWith(`${lark.upstreamVersion}-`), 'managed lark-cli version must identify its upstream version');
assert.ok(!Object.hasOwn(lark.patch, 'version'), 'the patch must not have an independent version');
const shortVersion = product.productVersion.split('-', 1)[0];

function replace(relative, pattern, replacement) {
  const before = read(relative);
  assert.match(before, pattern, `${relative}: version pattern not found`);
  const after = before.replace(pattern, replacement);
  write(relative, after);
}

write('Core/internal/productversion/version.go', `// Code generated from /version.json by scripts/sync-versions.mjs. DO NOT EDIT.\n\npackage productversion\n\n// Version is the single release version shared by every KSFAssistant-owned\n// runtime and host. Managed third-party runtimes keep their own release line.\nconst Version = ${JSON.stringify(product.productVersion)}\n`);
write('Core/internal/larkversion/version.go', `// Code generated from /runtime/lark-cli-runtime.json by scripts/sync-versions.mjs. DO NOT EDIT.\n\npackage larkversion\n\n// Version identifies the complete KSFAssistant-managed lark-cli distribution.\n// UpstreamVersion is retained only for source, API metadata and Skills provenance.\nconst Version = ${JSON.stringify(lark.version)}\nconst UpstreamVersion = ${JSON.stringify(lark.upstreamVersion)}\n`);

for (const relative of ['Windows/package.json', 'Windows/package-lock.json']) {
  const document = JSON.parse(read(relative));
  document.version = product.productVersion;
  if (relative.endsWith('package-lock.json')) document.packages[''].version = product.productVersion;
  write(relative, `${JSON.stringify(document, null, 2)}\n`);
}

replace('Resources/Info.plist', /(<key>CFBundleShortVersionString<\/key>\s*<string>)[^<]+(<\/string>)/, `$1${shortVersion}$2`);
replace('Resources/Info.plist', /(<key>CFBundleVersion<\/key>\s*<string>)[^<]+(<\/string>)/, `$1${product.macOSBuildNumber}$2`);
replace('Resources/Info.plist', /(<key>KSFAssistantReleaseVersion<\/key>\s*<string>)[^<]+(<\/string>)/, `$1${product.productVersion}$2`);

console.log(`Synchronized KSFAssistant ${product.productVersion} (macOS build ${product.macOSBuildNumber})`);
console.log(`Synchronized managed lark-cli ${lark.version} (upstream ${lark.upstreamVersion})`);
