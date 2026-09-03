const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const test = require('node:test');
const { loadClientConfig, readCompleteJsonl } = require('../lib/bridge-client-core');
const { assertPrivateMode, bridgeTestEnv } = require('./helpers/platform-private');

test('production actionbox saves only registered test-asset identifiers into private client config', async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-test-asset-capture-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const fakeLarkPath = path.join(dir, 'fake-lark.js');
  fs.writeFileSync(fakeLarkPath, `#!/usr/bin/env node
const args = process.argv.slice(2);
if (args.includes('--version')) process.stdout.write('lark-cli version 1.0.92\\n');
else process.stdout.write(JSON.stringify({ok:true,data:{spreadsheet_token:'shtcn_private_asset'}}) + '\\n');
`);
  fs.chmodSync(fakeLarkPath, 0o700);
  const queuePath = path.join(dir, 'actionbox.jsonl');
  const resultsPath = path.join(dir, 'actionbox-results.jsonl');
  const configPath = path.join(dir, 'private', 'client.json');
  fs.writeFileSync(queuePath, `${JSON.stringify({
    id: 'CAP-20260902050000-ABCDEF12',
    type: 'feishu_capability',
    domain: 'capability',
    action: 'execute',
    capabilityId: 'sheets.workbook.create',
    identity: 'user',
    input: { title: 'Codex桥测试-Sheets-1.0.0' },
    explicitAuthorization: true,
    saveAs: 'Codex桥测试-Sheets-1.0.0',
    source: 'codex',
  })}\n`);
  const child = spawn(process.execPath, [path.join(__dirname, '..', 'bot-bridge.js')], {
    cwd: path.join(__dirname, '..'),
    env: bridgeTestEnv(dir, {
      ...process.env,
      FEISHU_BRIDGE_LOG_DIR: dir,
      FEISHU_AUDIT_DIR: path.join(dir, 'audit'),
      FEISHU_CLIENT_CONFIG_PATH: configPath,
      FEISHU_OUTBOUND_ENABLED: 'false',
      FEISHU_DOCBOX_ENABLED: 'false',
      FEISHU_ACTIONBOX_ENABLED: 'true',
      FEISHU_ACTIONBOX_DRY_RUN: 'false',
      FEISHU_ACTIONBOX_PATH: queuePath,
      FEISHU_ACTIONBOX_POLL_MS: '500',
      FEISHU_EVENT_CONSUMER_ENABLED: 'false',
      LARK_CLI_BIN: fakeLarkPath,
    }),
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let diagnostics = '';
  child.stdout.on('data', (chunk) => { diagnostics += chunk; });
  child.stderr.on('data', (chunk) => { diagnostics += chunk; });
  t.after(() => { if (!child.killed) child.kill('SIGTERM'); });

  const deadline = Date.now() + 5000;
  let result;
  while (Date.now() < deadline) {
    result = readCompleteJsonl(resultsPath).find((item) => item.id === 'CAP-20260902050000-ABCDEF12');
    if (result) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  child.kill('SIGTERM');
  await new Promise((resolve) => child.once('exit', resolve));
  assert.ok(result, diagnostics || 'bridge did not produce an actionbox result');
  assert.equal(result.status, 'completed');
  assert.ok(result.savedAsset, JSON.stringify(result));
  assert.equal(result.savedAsset.alias, 'Codex桥测试-Sheets-1.0.0');
  assert.match(result.savedAsset.valueFingerprint, /^sha256:/);
  assert.equal(fs.readFileSync(resultsPath, 'utf8').includes('shtcn_private_asset'), false);
  const config = loadClientConfig(configPath);
  assert.equal(config.testAssets['Codex桥测试-Sheets-1.0.0'].value, 'shtcn_private_asset');
  assertPrivateMode(configPath, 0o600);
});
