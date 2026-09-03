const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const test = require('node:test');
const {
  auditContentDescriptor,
  auditTargetDescriptor,
  redactAuditText,
} = require('../lib/audit-policy');
const { readCompleteJsonl } = require('../lib/bridge-client-core');
const { bridgeTestEnv } = require('./helpers/platform-private');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-audit-policy-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('audit descriptors contain fingerprints and lengths instead of targets or content', () => {
  const target = auditTargetDescriptor({ type: 'open_id', id: 'ou_private_target' });
  const content = auditContentDescriptor('private message body');
  assert.match(target, /^open_id:sha256:[a-f0-9]{12}$/);
  assert.equal(target.includes('ou_private_target'), false);
  assert.match(content, /^length=20, sha256:[a-f0-9]{12}$/);
  assert.equal(content.includes('private message body'), false);
  assert.equal(redactAuditText('failed for oc_private_chat'), 'failed for [sha256:37a4ec158fea]');
});

test('request-level outbound dry-run never sends and audit omits raw target and body', async (t) => {
  const dir = temporaryDirectory(t);
  const fakeLarkPath = path.join(dir, 'fake-lark.js');
  fs.writeFileSync(fakeLarkPath, `#!/usr/bin/env node
if (process.argv.includes('--version')) process.stdout.write('fake-lark 1.0.0\\n');
else process.stdout.write('{}\\n');
`);
  fs.chmodSync(fakeLarkPath, 0o700);
  const outboxPath = path.join(dir, 'outbox.jsonl');
  const resultPath = path.join(dir, 'outbox-results.jsonl');
  const rawTarget = 'oc_private_audit_target';
  const rawBody = 'PRIVATE-AUDIT-BODY-DO-NOT-STORE';
  fs.writeFileSync(outboxPath, `${JSON.stringify({
    id: 'OUT-AUDIT-REDACTION',
    type: 'markdown',
    target: { type: 'chat_id', id: rawTarget },
    text: rawBody,
    explicitAuthorization: true,
    dryRun: true,
    source: 'test',
  })}\n`);
  const child = spawn(process.execPath, [path.join(__dirname, '..', 'bot-bridge.js')], {
    cwd: path.join(__dirname, '..'),
    env: bridgeTestEnv(dir, {
      ...process.env,
      FEISHU_BRIDGE_LOG_DIR: dir,
      FEISHU_AUDIT_DIR: path.join(dir, 'audit'),
      FEISHU_OUTBOX_PATH: outboxPath,
      FEISHU_OUTBOX_POLL_MS: '500',
      FEISHU_OUTBOUND_ENABLED: 'true',
      FEISHU_OUTBOUND_DRY_RUN: 'false',
      FEISHU_OUTBOUND_WAKE_ENABLED: 'false',
      FEISHU_DOCBOX_ENABLED: 'false',
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
    result = readCompleteJsonl(resultPath).find((item) => item.id === 'OUT-AUDIT-REDACTION');
    if (result) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  child.kill('SIGTERM');
  await new Promise((resolve) => child.once('exit', resolve));
  assert.ok(result, diagnostics || 'bridge did not produce a dry-run result');
  assert.equal(result.status, 'dry_run');
  const auditFiles = fs.readdirSync(path.join(dir, 'audit'));
  const auditText = auditFiles.map((name) => fs.readFileSync(path.join(dir, 'audit', name), 'utf8')).join('\n');
  assert.equal(auditText.includes(rawTarget), false);
  assert.equal(auditText.includes(rawBody), false);
  assert.match(auditText, /target：chat_id:sha256:/);
  assert.match(auditText, /content：length=31, sha256:/);
});
