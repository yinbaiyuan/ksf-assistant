const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');

const {
  configureExisting,
  extractUrls,
  firstVerificationUrl,
  findValue,
  startConfig,
} = require('../lib/auth-flow');
const { parseArgs } = require('../scripts/bridge-client');

test('auth flow extracts verification URLs from lark-cli text and JSON output', () => {
  assert.deepEqual(extractUrls('open https://passport.feishu.cn/device and continue'), [
    'https://passport.feishu.cn/device',
  ]);
  assert.equal(firstVerificationUrl({
    ok: true,
    data: {
      verification_uri_complete: 'https://open.feishu.cn/authen/qr?code=abc',
      device_code: 'device-private',
    },
  }), 'https://open.feishu.cn/authen/qr?code=abc');
});

test('auth flow can recover device and user codes from nested envelopes', () => {
  const payload = {
    ok: true,
    data: {
      device_code: 'device-private',
      user_code: 'ABCD-EFGH',
    },
  };
  assert.equal(findValue(payload, ['device_code', 'deviceCode']), 'device-private');
  assert.equal(findValue(payload, ['user_code', 'userCode']), 'ABCD-EFGH');
});

test('bridge client exposes non-interactive auth commands', () => {
  assert.deepEqual(parseArgs(['auth', 'start-user', '--scope', 'recommend']).positional, [
    'auth',
    'start-user',
  ]);
  assert.equal(parseArgs(['auth', 'finish-user', '--device-code', 'abc']).flags.deviceCode, 'abc');
});

test('config QR creation is fail-closed unless a new app is explicitly allowed', () => {
  assert.throws(() => startConfig({
    dataRoot: process.cwd(),
    platform: process.platform,
    larkCliBin: process.execPath,
    projectRoot: process.cwd(),
    env: {},
  }), /existing_app_credentials_required/);
  assert.equal(parseArgs(['auth', 'start-config', '--create-new']).flags.createNew, true);
});

test('existing app configuration reads the secret from stdin without --new', (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-auth-flow-test-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const capturePath = path.join(dir, 'capture.json');
  const fakeCliPath = path.join(dir, 'fake-lark-cli.js');
  fs.writeFileSync(fakeCliPath, `
const fs = require('node:fs');
const input = fs.readFileSync(0, 'utf8');
fs.writeFileSync(process.env.CAPTURE_PATH, JSON.stringify({
  argv: process.argv.slice(2),
  input,
}));
`, { mode: 0o700 });

  const result = configureExisting({
    dataRoot: dir,
    platform: 'linux',
    larkCliBin: fakeCliPath,
    projectRoot: dir,
    env: { CAPTURE_PATH: capturePath },
  }, {
    appId: 'cli_existing_app',
    appSecret: 'private-secret',
    profile: 'default',
  });
  const capture = JSON.parse(fs.readFileSync(capturePath, 'utf8'));
  assert.equal(result.status, 'configured');
  assert.equal(JSON.stringify(result).includes('private-secret'), false);
  assert.equal(capture.input, 'private-secret\n');
  assert.equal(capture.argv.includes('--new'), false);
  assert.equal(capture.argv.includes('--app-secret-stdin'), true);
  assert.equal(capture.argv.includes('private-secret'), false);
});

test('default config check does not expose lark-cli create-new hints', (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-auth-hint-test-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const fakeCliPath = path.join(dir, 'fake-lark-cli.js');
  fs.writeFileSync(fakeCliPath, `
process.stderr.write(JSON.stringify({
  ok: false,
  error: {
    type: 'config',
    subtype: 'not_configured',
    message: 'not configured',
    hint: 'run lark-cli config init --new',
  },
}));
process.exit(1);
`, { mode: 0o700 });
  assert.throws(() => startConfig({
    dataRoot: dir,
    platform: 'linux',
    larkCliBin: fakeCliPath,
    projectRoot: dir,
    env: {},
  }), (error) => {
    assert.match(error.message, /existing_app_credentials_required/);
    assert.doesNotMatch(error.message, /config init --new/);
    return true;
  });
});

test('current OAuth user becomes the private authorized 我 alias without exposing the open_id', (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-current-user-test-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const fakeCliPath = path.join(dir, 'fake-lark-cli.js');
  fs.writeFileSync(fakeCliPath, `#!/usr/bin/env node
process.stdout.write(JSON.stringify({ ok: true, data: { users: [{ open_id: 'ou_private_current' }] } }));
`, { mode: 0o700 });

  const clientPath = path.join(__dirname, '..', 'scripts', 'bridge-client.js');
  const result = spawnSync(process.execPath, [clientPath, 'auth', 'ensure-current-user'], {
    cwd: path.join(__dirname, '..'),
    env: {
      ...process.env,
      KSF_ASSISTANT_MANAGED: '1',
      FEISHU_BRIDGE_DATA_DIR: dir,
      LARK_CLI_BIN: fakeCliPath,
    },
    encoding: 'utf8',
  });
  assert.equal(result.status, 0, result.stderr);
  assert.doesNotMatch(result.stdout, /ou_private_current/);
  const config = JSON.parse(fs.readFileSync(path.join(dir, 'client.json'), 'utf8'));
  assert.deepEqual(config.directAllowedAliases, ['我']);
  assert.deepEqual(config.messageTargets['我'], { type: 'open_id', id: 'ou_private_current' });
});
