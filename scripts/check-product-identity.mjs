import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (relative) => readFileSync(path.join(root, relative), 'utf8');
const manifest = JSON.parse(read('Windows/package.json'));
const lock = JSON.parse(read('Windows/package-lock.json'));
assert.equal(manifest.name, 'ksfassistant-windows');
assert.equal(lock.name, manifest.name);
assert.equal(lock.packages[''].name, manifest.name);
assert.equal(lock.version, manifest.version);
assert.equal(lock.packages[''].version, manifest.version);
const plist = read('Resources/Info.plist');
const plistVersion = (key) => plist.match(new RegExp(`<key>${key}</key>\\s*<string>([^<]+)</string>`))?.[1];
assert.equal(plistVersion('CFBundleShortVersionString'), manifest.version.split('-')[0]);
assert.equal(plistVersion('KSFAssistantReleaseVersion'), manifest.version);
assert.ok(read('Core/internal/service/service.go').includes(`const Version = "${manifest.version}"`));
assert.equal(manifest.build.productName, 'KSFAssistant');
assert.equal(manifest.build.appId, 'com.ksfassistant.desktop');
assert.match(read('Resources/Info.plist'), /<string>com\.ksfassistant\.desktop<\/string>/);
assert.match(read('Core/go.mod'), /^module ksfassistant\/core\n/);
assert.match(read('Package.swift'), /name: "KSFAssistant"/);
assert.match(read('Package.swift'), /name: "KSFAssistantCore"/);
assert.match(read('Windows/src/preload.cjs'), /exposeInMainWorld\('ksfAssistant'/);
assert.match(read('Windows/src/renderer/app.js'), /window\.ksfAssistant/);
for (const relative of [
  'Sources/KSFAssistant/KSFAssistantApp.swift', 'Sources/KSFAssistantCore',
  'Tests/KSFAssistantCoreTests', 'Core/cmd/ksf-assistant-core',
  'Core/cmd/ksf-assistant-feishu-bridge', 'Core/cmd/ksf-assistant-build-assets',
]) assert.ok(existsSync(path.join(root, relative)), relative);

const exceptions = new Set([
  'Sources/KSFAssistant/AppConfiguration.swift',
  'Sources/KSFAssistant/KSFAssistantApp.swift',
  'Sources/KSFAssistant/UsageViewModel.swift',
  'Sources/KSFAssistantCore/LocalTokenHistoryStore.swift',
  'Core/internal/bridge/process.go',
  'Core/internal/bridge/identity_test.go',
  'Windows/src/main.cjs',
  'Windows/src/identity-migration.cjs',
  'Windows/test/identity-migration.test.cjs',
  'Windows/test/config-store.test.cjs',
  'Tests/IdentityMigrationStandalone/main.swift',
  'scripts/check-product-identity.mjs',
  'scripts/install-local.sh',
  'scripts/check-release-hygiene.sh',
  'Services/FeishuBridge/bot-bridge.js',
  'Services/FeishuBridge/lib/ksf-project-promotion.js',
  'Services/FeishuBridge/test/ksf-project-promotion.test.js',
  'Services/FeishuBridge/test/codex-app-server-lifecycle.test.js',
  'docs/COMPATIBILITY.md',
  'docs/architecture/product-identity-migration.md',
  'docs/architecture/feishu-service-v2-validation.md',
]);
const obsolete = /codex[-_ ]?assistant|codex[-_ ]?usage[-_ ]?(?:bar|core)|codex-feishu-bridge|codex-build-assets|usageBar/gi;
const files = new Set(execFileSync('git', ['ls-files', '-co', '--exclude-standard', '-z'], { cwd: root, encoding: 'utf8' }).split('\0').filter(Boolean));
let compatibilityFiles = 0;
const unexpected = [];
for (const relative of files) {
  if (!existsSync(path.join(root, relative))) continue;
  const data = readFileSync(path.join(root, relative));
  if (data.includes(0)) continue;
  const matches = data.toString('utf8').match(obsolete);
  if (!matches) continue;
  if (exceptions.has(relative)) compatibilityFiles += 1;
  else unexpected.push(`${relative}: ${[...new Set(matches)].join(', ')}`);
}
assert.deepEqual(unexpected, [], 'Unexpected old product identity; do not conceal names through string concatenation');
console.log(`PASS product identity; ${compatibilityFiles} explicitly reviewed compatibility files`);
