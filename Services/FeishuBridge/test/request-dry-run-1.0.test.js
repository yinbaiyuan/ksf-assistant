const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const test = require('node:test');
const { readCompleteJsonl } = require('../lib/bridge-client-core');
const { bridgeTestEnv } = require('./helpers/platform-private');

test('request-level docbox dry-run bypasses document execution while global dry-run is off', async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-docbox-request-dry-run-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const fakeLarkPath = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocations.jsonl');
  fs.writeFileSync(fakeLarkPath, `#!/usr/bin/env node
const fs = require('node:fs');
fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(process.argv.slice(2)) + '\\n');
if (process.argv.includes('--version')) process.stdout.write('fake-lark 1.0.92\\n');
else process.stdout.write('{}\\n');
`);
  fs.chmodSync(fakeLarkPath, 0o700);
  const queuePath = path.join(dir, 'docbox.jsonl');
  const resultsPath = path.join(dir, 'docbox-results.jsonl');
  const rawBody = 'REQUEST-LEVEL-DOCBOX-DRY-RUN';
  fs.writeFileSync(queuePath, `${JSON.stringify({
    id: 'DOC-REQUEST-DRY-RUN',
    type: 'document_task',
    action: 'create_document',
    identity: 'user',
    content: { format: 'markdown', text: rawBody },
    instruction: 'Create the dedicated test document.',
    explicitAuthorization: true,
    dryRun: true,
    source: 'codex',
  })}\n`);
  const child = spawn(process.execPath, [path.join(__dirname, '..', 'bot-bridge.js')], {
    cwd: path.join(__dirname, '..'),
    env: bridgeTestEnv(dir, {
      ...process.env,
      FEISHU_BRIDGE_LOG_DIR: dir,
      FEISHU_AUDIT_DIR: path.join(dir, 'audit'),
      FEISHU_OUTBOUND_ENABLED: 'false',
      FEISHU_DOCBOX_ENABLED: 'true',
      FEISHU_DOCBOX_DRY_RUN: 'false',
      FEISHU_DOCBOX_PATH: queuePath,
      FEISHU_DOCBOX_POLL_MS: '500',
      FEISHU_ACTIONBOX_ENABLED: 'false',
      FEISHU_EVENT_CONSUMER_ENABLED: 'false',
      LARK_CLI_BIN: fakeLarkPath,
      INVOCATIONS: invocationPath,
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
    result = readCompleteJsonl(resultsPath).find((item) => item.id === 'DOC-REQUEST-DRY-RUN');
    if (result) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  child.kill('SIGTERM');
  await new Promise((resolve) => child.once('exit', resolve));
  assert.ok(result, diagnostics || 'bridge did not produce a docbox result');
  assert.equal(result.status, 'dry_run');
  assert.equal(result.dryRun, true);
  assert.match(result.summary, /请求级 dry-run/);
  const invocations = fs.existsSync(invocationPath) ? readCompleteJsonl(invocationPath) : [];
  assert.equal(invocations.some((args) => args.includes('docx') || args.includes('wiki')), false);
  const audit = fs.readdirSync(path.join(dir, 'audit'))
    .map((name) => fs.readFileSync(path.join(dir, 'audit', name), 'utf8'))
    .join('\n');
  assert.equal(audit.includes(rawBody), false);
});
