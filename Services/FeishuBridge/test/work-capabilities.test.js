const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const test = require('node:test');
const { validateActionRequest } = require('../lib/action-registry');
const { createLarkCliRunner } = require('../lib/lark-cli-runner');
const { executeActionRequest } = require('../lib/work-actions');
const { validateBoundedA1Range, validateCalendarReadRange } = require('../scripts/bridge-client');
const { bridgeTestEnv } = require('./helpers/platform-private');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-work-capabilities-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function actionRequest(domain, action, target, input, overrides = {}) {
  return {
    id: 'ACT-WORK-TEST',
    type: 'feishu_action',
    domain,
    action,
    identity: 'user',
    target,
    input,
    explicitAuthorization: true,
    source: 'codex',
    ...overrides,
  };
}

function fakeRunner(t) {
  const dir = temporaryDirectory(t);
  const bin = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocations.jsonl');
  const stdinPath = path.join(dir, 'stdin.jsonl');
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require('node:fs');
const path = require('node:path');
const args = process.argv.slice(2);
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => { input += chunk; });
process.stdin.on('end', () => {
  fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(args) + '\\n');
  if (input) fs.appendFileSync(process.env.STDIN_LOG, JSON.stringify(input) + '\\n');
  if (args[0] === 'base' && args[1] === '+url-resolve') {
    process.stdout.write(JSON.stringify({ok:true,data:{base_token:'bascn_private'}}) + '\\n');
  } else if (args[0] === 'base' && args[1] === '+field-list') {
    process.stdout.write(JSON.stringify({ok:true,data:{fields:[{id:'fld_name',name:'Name',type:'text'},{id:'fld_status',name:'Status',type:'select'}]}}) + '\\n');
  } else if (args[0] === 'sheets' && args[1] === '+cells-get') {
    process.stdout.write(JSON.stringify({ok:true,data:{valueRange:{values:[[null]]}}}) + '\\n');
  } else if (args[0] === 'sheets' && args[1] === '+revision-get') {
    process.stdout.write(JSON.stringify({ok:true,data:{revision:42}}) + '\\n');
  } else if (args.includes('--version')) {
    process.stdout.write('fake 1.0.0\\n');
  } else {
    process.stdout.write(JSON.stringify({ok:true,data:{id:'result_private'}}) + '\\n');
  }
});
`);
  fs.chmodSync(bin, 0o700);
  return {
    cwd: dir,
    invocationPath,
    stdinPath,
    runner: createLarkCliRunner({
      bin,
      cwd: dir,
      env: { ...process.env, INVOCATIONS: invocationPath, STDIN_LOG: stdinPath },
      as: 'user',
    }),
  };
}

function calls(filePath) {
  return fs.readFileSync(filePath, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse);
}

test('reviewed P2 and P3 actions validate while destructive and ambiguous writes fail closed', () => {
  assert.equal(validateActionRequest(actionRequest('calendar', 'create_event',
    { kind: 'calendar_id', value: 'primary' },
    { summary: '评审', start: '2026-09-02T10:00:00+08:00', end: '2026-09-02T11:00:00+08:00' })), '');
  assert.equal(validateActionRequest(actionRequest('calendar', 'create_event',
    { kind: 'calendar_id', value: 'primary' },
    { summary: '评审', start: '2026-09-02T10:00:00', end: '2026-09-02T11:00:00' })), 'timezone_required');
  assert.equal(validateActionRequest(actionRequest('task', 'delete',
    { kind: 'task_id', value: 'task-private' }, {})), 'unsupported_domain_or_action');
  assert.equal(validateActionRequest(actionRequest('sheets', 'set_cells',
    { kind: 'url', value: 'https://example.feishu.cn/sheets/private' },
    { sheetName: 'Sheet1', range: 'A1:B1', cells: [[{ value: 1 }, { value: 2 }]], allowOverwrite: true })), 'overwrite_confirmation_required');
  assert.equal(validateActionRequest(actionRequest('sheets', 'set_cells',
    { kind: 'url', value: 'https://example.feishu.cn/sheets/private' },
    { sheetName: 'Sheet1', range: 'A1:B1', cells: [[{ value: 1 }, { value: 2 }]], allowOverwrite: true },
    { confirmHighImpact: true })), '');
  assert.equal(validateActionRequest(actionRequest('base', 'create_records',
    { kind: 'url', value: 'https://example.feishu.cn/base/private' },
    { tableId: 'Tasks', records: [{ Name: 'A', Status: ['Todo'] }] })), '');
  assert.equal(validateActionRequest(actionRequest('base', 'delete_records',
    { kind: 'url', value: 'https://example.feishu.cn/base/private' }, {})), 'unsupported_domain_or_action');
});

test('calendar create reads description from stdin rather than process arguments', async (t) => {
  const { runner, invocationPath, stdinPath } = fakeRunner(t);
  const request = actionRequest('calendar', 'create_event', { kind: 'calendar_id', value: 'primary' }, {
    summary: '日程标题',
    start: '2026-09-02T10:00:00+08:00',
    end: '2026-09-02T11:00:00+08:00',
    description: '不能进入命令参数的日程正文',
  });
  await executeActionRequest(runner, request);
  const [call] = calls(invocationPath);
  assert.deepEqual(call.slice(0, 2), ['calendar', '+create']);
  assert.equal(call.includes('不能进入命令参数的日程正文'), false);
  assert.equal(call[call.indexOf('--description') + 1], '-');
  assert.equal(JSON.parse(fs.readFileSync(stdinPath, 'utf8').trim()), '不能进入命令参数的日程正文');
});

test('sheets write performs preflight and reread while payload stays in a private temporary file', async (t) => {
  const { runner, invocationPath, cwd } = fakeRunner(t);
  const request = actionRequest('sheets', 'set_cells',
    { kind: 'url', value: 'https://example.feishu.cn/sheets/private' },
    { sheetName: 'Sheet1', range: 'A1:B1', cells: [[{ value: '秘密甲' }, { value: '秘密乙' }]], allowOverwrite: false });
  const result = await executeActionRequest(runner, request);
  assert.equal(result.verified, true);
  const invocations = calls(invocationPath);
  assert.deepEqual(invocations.map((call) => call.slice(0, 2)), [
    ['sheets', '+revision-get'],
    ['sheets', '+cells-get'],
    ['sheets', '+cells-set'],
    ['sheets', '+cells-get'],
    ['sheets', '+revision-get'],
  ]);
  const writeCall = invocations[2];
  assert.equal(JSON.stringify(writeCall).includes('秘密甲'), false);
  const payloadArg = writeCall[writeCall.indexOf('--cells') + 1];
  assert.match(payloadArg, /^@node_modules\/\.cache\/feishu-bridge\/action-payloads\//);
  assert.equal(fs.existsSync(path.join(cwd, payloadArg.slice(1))), false);
});

test('base update accepts lark-cli 1.0.92 fields, rereads records, and keeps row payload out of argv', async (t) => {
  const { runner, invocationPath } = fakeRunner(t);
  const request = actionRequest('base', 'update_records',
    { kind: 'url', value: 'https://example.feishu.cn/base/private' },
    { tableId: 'Tasks', updates: { rec_private: { Status: ['Done'] } } });
  const result = await executeActionRequest(runner, request);
  assert.equal(result.verified, true);
  const invocations = calls(invocationPath);
  assert.deepEqual(invocations.map((call) => call.slice(0, 2)), [
    ['base', '+url-resolve'],
    ['base', '+field-list'],
    ['base', '+record-get'],
    ['base', '+record-batch-update'],
    ['base', '+record-get'],
  ]);
  assert.equal(invocations.some((call) => JSON.stringify(call).includes('Done')), false);
});

test('bounded calendar and sheet reads reject oversized or incomplete ranges', () => {
  assert.doesNotThrow(() => validateCalendarReadRange('2026-09-01', '2026-09-10'));
  assert.throws(() => validateCalendarReadRange('2026-09-01', undefined), /both/);
  assert.throws(() => validateCalendarReadRange('2026-01-01', '2026-09-01'), /40 days/);
  assert.equal(validateBoundedA1Range('A1:J100'), 1000);
  assert.throws(() => validateBoundedA1Range('A1:Z1000'), /10000 cells/);
  assert.throws(() => validateBoundedA1Range('A:A'), /bounded A1/);
});

test('calendar search runs as user, one page only, and sanitizes event identifiers', (t) => {
  const dir = temporaryDirectory(t);
  const bin = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocations.jsonl');
  const configPath = path.join(dir, 'client.json');
  fs.writeFileSync(configPath, `${JSON.stringify({ schemaVersion: 3, launchdLabel: 'test.bridge' })}\n`, { mode: 0o600 });
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(args) + '\\n');
if (args.includes('--version')) process.stdout.write('fake 1.0.0\\n');
else if (args[0] === 'auth') process.stdout.write('{}\\n');
else process.stdout.write(JSON.stringify({ok:true,data:{items:[{event_id:'event_private',summary:'评审'}]}}) + '\\n');
`);
  fs.chmodSync(bin, 0o700);
  const result = spawnSync(process.execPath, [
    path.join(__dirname, '..', 'scripts', 'bridge-client.js'),
    'calendar', 'search', '--query', '评审',
    '--start', '2026-09-01', '--end', '2026-09-10', '--limit', '10', '--config', configPath,
  ], {
    cwd: path.join(__dirname, '..'),
    env: bridgeTestEnv(dir, {
      ...process.env,
      LARK_CLI_BIN: bin,
      INVOCATIONS: invocationPath,
      FEISHU_BRIDGE_LOG_DIR: path.join(dir, 'logs'),
      FEISHU_AUDIT_DIR: path.join(dir, 'audit'),
    }),
    encoding: 'utf8',
  });
  assert.equal(result.status, 0, result.stderr);
  assert.equal(result.stdout.includes('event_private'), false);
  const command = calls(invocationPath).find((call) => call.includes('+search-event'));
  assert.ok(command);
  assert.equal(command.includes('--as') && command.includes('user'), true);
  assert.equal(command.includes('--page-all'), false);
  assert.equal(command[command.indexOf('--page-size') + 1], '10');
});
