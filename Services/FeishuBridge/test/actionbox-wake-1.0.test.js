const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const test = require('node:test');
const { readCompleteJsonl } = require('../lib/bridge-client-core');

function waitFor(predicate, timeoutMs = 5000) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve, reject) => {
    const tick = () => {
      try {
        const value = predicate();
        if (value) return resolve(value);
      } catch {
        // State files may be between atomic rename operations; retry.
      }
      if (Date.now() >= deadline) return reject(new Error('timed out waiting for condition'));
      setTimeout(tick, 25);
    };
    tick();
  });
}

function post(port, route) {
  return new Promise((resolve, reject) => {
    const request = http.request({ hostname: '127.0.0.1', port, path: route, method: 'POST' }, (response) => {
      response.resume();
      response.on('end', () => resolve(response.statusCode));
    });
    request.on('error', reject);
    request.end();
  });
}

test('production actionbox wake accepts only the local fixed route and processes without waiting for the poll interval', async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-action-wake-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const fakeLark = path.join(dir, 'fake-lark.js');
  fs.writeFileSync(fakeLark, `#!/usr/bin/env node
if (process.argv.includes('--version')) process.stdout.write('fake 1.0.92\\n');
else process.stdout.write('{}\\n');
`);
  fs.chmodSync(fakeLark, 0o700);
  const queuePath = path.join(dir, 'actionbox.jsonl');
  const statePath = path.join(dir, 'actionbox-state.json');
  const resultPath = path.join(dir, 'actionbox-results.jsonl');
  const child = spawn(process.execPath, [path.join(__dirname, '..', 'bot-bridge.js')], {
    cwd: path.join(__dirname, '..'),
    env: {
      ...process.env,
      FEISHU_BRIDGE_DATA_DIR: dir,
      FEISHU_BRIDGE_LOG_DIR: dir,
      FEISHU_AUDIT_DIR: path.join(dir, 'audit'),
      FEISHU_OUTBOUND_ENABLED: 'false',
      FEISHU_DOCBOX_ENABLED: 'false',
      FEISHU_ACTIONBOX_ENABLED: 'true',
      FEISHU_ACTIONBOX_DRY_RUN: 'true',
      FEISHU_ACTIONBOX_PATH: queuePath,
      FEISHU_ACTIONBOX_POLL_MS: '60000',
      FEISHU_ACTIONBOX_WAKE_ENABLED: 'true',
      FEISHU_ACTIONBOX_WAKE_HOST: '127.0.0.1',
      FEISHU_ACTIONBOX_WAKE_PORT: '0',
      FEISHU_EVENT_CONSUMER_ENABLED: 'false',
      FEISHU_EVENT_INBOX_ENABLED: 'false',
      LARK_CLI_BIN: fakeLark,
    },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let diagnostics = '';
  child.stdout.on('data', (chunk) => { diagnostics += chunk; });
  child.stderr.on('data', (chunk) => { diagnostics += chunk; });
  t.after(() => { if (!child.killed) child.kill('SIGTERM'); });
  const state = await waitFor(() => {
    if (!fs.existsSync(statePath)) return null;
    const value = JSON.parse(fs.readFileSync(statePath, 'utf8'));
    return value.wake?.actualPort ? value : null;
  });
  const request = {
    id: 'CAP-WAKE-TEST',
    type: 'feishu_capability',
    domain: 'capability',
    action: 'execute',
    capabilityId: 'im.chat.create',
    identity: 'user',
    input: { name: 'Codex桥测试群' },
    explicitAuthorization: true,
    source: 'codex',
  };
  fs.appendFileSync(queuePath, `${JSON.stringify(request)}\n`);
  assert.equal(await post(state.wake.actualPort, '/internal/actionbox/wake'), 202);
  const result = await waitFor(() => readCompleteJsonl(resultPath).find((item) => item.id === request.id));
  assert.equal(result.status, 'dry_run', diagnostics);
  assert.equal(await post(state.wake.actualPort, '/internal/not-actionbox'), 404);
  child.kill('SIGTERM');
  await new Promise((resolve) => child.once('exit', resolve));
});
