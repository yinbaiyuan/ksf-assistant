'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const repoRoot = path.resolve(__dirname, '..', '..');
const read = relative => fs.readFileSync(path.join(repoRoot, relative), 'utf8');

test('production bridge owns the official SDK and only required callbacks', () => {
  const goMod = read('Core/go.mod');
  const inbound = read('Core/internal/feishu/native_inbound.go');
  const events = read('Core/internal/feishu/inbound.go');
  assert.match(goMod, /github\.com\/larksuite\/oapi-sdk-go\/v3 v3\.12\.0/);
  assert.match(inbound, /larkws\.NewClient/);
  assert.match(inbound, /im\.message\.receive_v1/);
  assert.match(inbound, /OnP2CardActionTrigger/);
  const fixedCatalog = events.match(/var FixedEventKeys = \[\]string\{([\s\S]*?)\n\}/)?.[1] || '';
  assert.doesNotMatch(fixedCatalog, /approval\.|mail\.user_mailbox/);
});

test('Windows and macOS package only the internal bridge', () => {
  const config = JSON.parse(read('Windows/package.json'));
  const resources = config.build.extraResources;
  assert.ok(resources.some(item => item.to === 'runtime/feishu-bridge/windows-${arch}'));
  assert.ok(!resources.some(item => /lark-cli|lark-skills/.test(`${item.from} ${item.to}`)));
  assert.equal(config.build.afterSign, undefined);
  assert.doesNotMatch(config.scripts['pack:win'], /prepare:feishu/);
  assert.doesNotMatch(config.scripts['dist:win'], /prepare:feishu/);

  const mac = read('scripts/build-app.sh');
  assert.match(mac, /runtime\/feishu-bridge\/darwin-arm64/);
  assert.doesNotMatch(mac, /prepare-feishu-runtime|lark-cli|lark-skills/);
});

test('hosts inject no managed CLI or Node Feishu runtime', () => {
  const windowsMain = read('Windows/src/main.cjs');
  const macClient = read('Sources/KSFAssistant/CoreServiceProcessClient.swift');
  assert.match(windowsMain, /KSF_ASSISTANT_FEISHU_BRIDGE:\s*runtime\.bridge/);
  assert.match(macClient, /environment\["KSF_ASSISTANT_FEISHU_BRIDGE"\] = runtime\.path/);
  for (const source of [windowsMain, macClient]) {
    assert.doesNotMatch(source, /KSF_ASSISTANT_LARK_CLI|KSF_ASSISTANT_NODE|KSF_ASSISTANT_FEISHU_SERVICE_ROOT/);
  }
});

test('internal credentials use platform protection and one-scan registration', () => {
  assert.match(read('Core/internal/feishu/credentials_windows.go'), /CryptProtectData/);
  assert.match(read('Core/internal/feishu/credentials_darwin.go'), /com\.ksfassistant\.feishu/);
  const registration = read('Core/internal/feishu/app_registration_native.go');
  assert.match(registration, /oauth\/v1\/app\/registration/);
  assert.match(registration, /request_user_info/);
  assert.match(registration, /BaseConnectionPermissionScopes\(\)/);
  assert.match(registration, /im\.message\.receive_v1/);
  assert.match(registration, /card\.action\.trigger/);
  assert.match(registration, /bindRegistrationOperator/);
  assert.doesNotMatch(registration, /RunAuthJSON|lark-cli/);
});

test('the old skipped distribution check is now an always-on retirement check', () => {
  const forbidden = [
    ['Windows/package.json', /lark-cli-runtime|runtime\/lark-cli|lark-skills/],
    ['scripts/build-app.sh', /lark-cli-runtime|runtime\/lark-cli|lark-skills/],
    ['scripts/generate-sbom.mjs', /SPDXRef-Lark-CLI|SPDXRef-Lark-Skills/],
  ];
  for (const [file, pattern] of forbidden) assert.doesNotMatch(read(file), pattern, file);
});

test('both platform builds retain only the product-owned bridge and task helper', () => {
  const build = read('scripts/build-core.sh');
  assert.match(build, /darwin-arm64 darwin-x64 windows-x64 windows-arm64/);
  assert.match(build, /for component in task/);
  assert.doesNotMatch(build, /ksf-assistant-toolchain|component in toolchain/);
  assert.equal((build.match(/go build -buildvcs=false/g) || []).length, 3);
  const windowsBuild = read('Windows/scripts/build-core.mjs');
  assert.doesNotMatch(windowsBuild, /ksf-assistant-toolchain|'toolchain'/);
  assert.equal((windowsBuild.match(/'build', '-buildvcs=false'/g) || []).length, 3);
});
