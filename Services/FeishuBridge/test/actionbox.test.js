const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const test = require('node:test');
const {
  ACTIONBOX_TERMINAL_STATUSES,
  validateActionRequest,
} = require('../lib/action-registry');
const { readCompleteJsonl } = require('../lib/bridge-client-core');
const { bridgeTestEnv } = require('./helpers/platform-private');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-actionbox-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function request(overrides = {}) {
  return {
    id: 'ACT-TEST',
    type: 'feishu_action',
    domain: 'drive',
    action: 'add_comment',
    identity: 'user',
    target: { kind: 'url', value: 'https://example.feishu.cn/docx/private_token' },
    input: { comment: '请核对这一段。' },
    explicitAuthorization: true,
    source: 'codex',
    ...overrides,
  };
}

test('actionbox accepts the reviewed registry and rejects arbitrary actions', () => {
  assert.equal(validateActionRequest(request()), '');
  assert.equal(validateActionRequest(request({ action: 'delete_file' })), 'unsupported_domain_or_action');
  assert.equal(validateActionRequest(request({ domain: 'openapi' })), 'unsupported_domain_or_action');
  assert.equal(validateActionRequest(request({ identity: 'bot' })), 'unsupported_identity');
  assert.equal(validateActionRequest(request({ explicitAuthorization: false })), 'explicit_authorization_required');
  assert.equal(validateActionRequest(request({ shortcut: 'api DELETE /open-apis/drive' })), 'unsupported_request_field:shortcut');
  assert.equal(validateActionRequest(request({ input: { comment: '' } })), 'missing_comment');
  assert.equal(validateActionRequest(request({ target: { kind: 'token', value: 'docx_private' } })), 'unsupported_target_kind');
});

test('actionbox exposes an explicit terminal status set for synchronous result waiting', () => {
  for (const status of ['completed', 'dry_run', 'denied', 'duplicate', 'invalid', 'failed']) {
    assert.equal(ACTIONBOX_TERMINAL_STATUSES.has(status), true);
  }
});

test('request-level actionbox dry-run records a terminal result without invoking the write shortcut', async (t) => {
  const dir = temporaryDirectory(t);
  const fakeLarkPath = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocations.jsonl');
  fs.writeFileSync(fakeLarkPath, `#!/usr/bin/env node
const fs = require('node:fs');
fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(process.argv.slice(2)) + '\\n');
if (process.argv.includes('--version')) process.stdout.write('fake-lark 1.0.0\\n');
else process.stdout.write('{}\\n');
`);
  fs.chmodSync(fakeLarkPath, 0o700);
  const queuePath = path.join(dir, 'actionbox.jsonl');
  const resultsPath = path.join(dir, 'actionbox-results.jsonl');
  fs.writeFileSync(queuePath, `${JSON.stringify(request({ dryRun: true }))}\n`);
  const child = spawn(process.execPath, [path.join(__dirname, '..', 'bot-bridge.js')], {
    cwd: path.join(__dirname, '..'),
    env: bridgeTestEnv(dir, {
      ...process.env,
      FEISHU_BRIDGE_LOG_DIR: dir,
      FEISHU_AUDIT_DIR: path.join(dir, 'audit'),
      FEISHU_OUTBOUND_ENABLED: 'false',
      FEISHU_DOCBOX_ENABLED: 'false',
      FEISHU_ACTIONBOX_ENABLED: 'true',
      FEISHU_ACTIONBOX_DRY_RUN: 'false',
      FEISHU_ACTIONBOX_PATH: queuePath,
      FEISHU_ACTIONBOX_POLL_MS: '500',
      FEISHU_EVENT_CONSUMER_ENABLED: 'false',
      LARK_CLI_BIN: fakeLarkPath,
      INVOCATIONS: invocationPath,
    }),
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let diagnostics = '';
  child.stdout.on('data', (chunk) => { diagnostics += chunk; });
  child.stderr.on('data', (chunk) => { diagnostics += chunk; });
  t.after(() => {
    if (!child.killed) child.kill('SIGTERM');
  });
  const deadline = Date.now() + 5000;
  let result;
  while (Date.now() < deadline) {
    result = readCompleteJsonl(resultsPath).find((item) => item.id === 'ACT-TEST');
    if (result) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  child.kill('SIGTERM');
  await new Promise((resolve) => child.once('exit', resolve));
  assert.ok(result, diagnostics || 'bridge did not produce an actionbox result');
  assert.equal(result.status, 'dry_run');
  const calls = fs.existsSync(invocationPath) ? readCompleteJsonl(invocationPath) : [];
  assert.equal(calls.some((args) => args.includes('+add-comment')), false);
  const audit = fs.readdirSync(path.join(dir, 'audit'), { recursive: true })
    .filter((entry) => String(entry).endsWith('.md'))
    .map((entry) => fs.readFileSync(path.join(dir, 'audit', entry), 'utf8'))
    .join('\n');
  assert.equal(audit.includes('请核对这一段。'), false);
  assert.equal(audit.includes('private_token'), false);
});
