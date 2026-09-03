const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const test = require('node:test');
const { createLarkCliRunner } = require('../lib/lark-cli-runner');
const { bridgeTestEnv } = require('./helpers/platform-private');
const {
  fingerprintIdentifier,
  sanitizeReadForOutput,
  validateBoundedTimeRange,
  validateReadLimit,
} = require('../lib/bridge-client-core');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-read-capabilities-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function fakeRunner(t, as = 'bot') {
  const dir = temporaryDirectory(t);
  const bin = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocations.jsonl');
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require('node:fs');
fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(process.argv.slice(2)) + '\\n');
process.stdout.write(JSON.stringify({data:{items:[]}}) + '\\n');
`);
  fs.chmodSync(bin, 0o700);
  return {
    invocationPath,
    runner: createLarkCliRunner({
      bin,
      cwd: dir,
      env: { ...process.env, INVOCATIONS: invocationPath },
      as,
    }),
  };
}

function readCalls(filePath) {
  return fs.readFileSync(filePath, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse);
}

test('bounded message reads require timezone-aware dates and a maximum 31-day window', () => {
  assert.doesNotThrow(() => validateBoundedTimeRange(
    '2026-08-01T00:00:00+08:00',
    '2026-08-31T23:59:59+08:00',
  ));
  assert.throws(() => validateBoundedTimeRange('2026-08-01', '2026-08-02'), /timezone-aware/);
  assert.throws(() => validateBoundedTimeRange(
    '2026-07-01T00:00:00+08:00',
    '2026-08-31T23:59:59+08:00',
  ), /31 days/);
  assert.throws(() => validateBoundedTimeRange(
    '2026-08-02T00:00:00+08:00',
    '2026-08-01T00:00:00+08:00',
  ), /before/);
});

test('bounded read limits reject invalid and oversized requests', () => {
  assert.equal(validateReadLimit('20', 50), 20);
  assert.throws(() => validateReadLimit('0', 50), /between 1 and 50/);
  assert.throws(() => validateReadLimit('51', 50), /between 1 and 50/);
  assert.throws(() => validateReadLimit('abc', 50), /integer/);
});

test('message list, search, and thread runner methods never auto-paginate', async (t) => {
  const { runner, invocationPath } = fakeRunner(t, 'bot');
  await runner.larkImListMessages({
    chatId: 'oc_private',
    start: '2026-08-01T00:00:00+08:00',
    end: '2026-08-02T00:00:00+08:00',
    pageSize: 25,
    order: 'desc',
  });
  await runner.larkImSearchMessages({
    chatId: 'oc_private',
    query: '进度',
    start: '2026-08-01T00:00:00+08:00',
    end: '2026-08-02T00:00:00+08:00',
    pageSize: 12,
  });
  await runner.larkImListThread({ threadId: 'omt_private', pageSize: 30, order: 'asc' });
  const calls = readCalls(invocationPath);
  assert.equal(calls.length, 3);
  assert.equal(calls.every((call) => !call.includes('--page-all')), true);
  assert.deepEqual(calls[0].slice(0, 2), ['im', '+chat-messages-list']);
  assert.equal(calls[0].includes('--chat-id'), true);
  assert.equal(calls[0].includes('--start'), true);
  assert.equal(calls[0].includes('--end'), true);
  assert.equal(calls[0].includes('25'), true);
  assert.deepEqual(calls[1].slice(0, 2), ['im', '+messages-search']);
  assert.equal(calls[1].includes('进度'), true);
  assert.deepEqual(calls[2].slice(0, 2), ['im', '+threads-messages-list']);
  assert.equal(calls[2].includes('omt_private'), true);
});

test('knowledge runner uses user identity and bounded official shortcuts', async (t) => {
  const { runner, invocationPath } = fakeRunner(t, 'user');
  await runner.larkKnowledgeSearch({ query: '项目', docTypes: 'docx,wiki', pageSize: 10 });
  await runner.larkDriveInspect({ url: 'https://example.feishu.cn/wiki/private_token' });
  await runner.larkDocFetch('https://example.feishu.cn/docx/private_token', {
    scope: 'outline',
    docFormat: 'markdown',
  });
  await runner.larkDriveListComments({
    url: 'https://example.feishu.cn/docx/private_token',
    pageSize: 50,
    solvedStatus: 'false',
    commentScope: 'all',
  });
  const calls = readCalls(invocationPath);
  assert.equal(calls.every((call) => call.includes('--as') && call.includes('user')), true);
  assert.deepEqual(calls[0].slice(0, 2), ['drive', '+search']);
  assert.equal(calls[0].includes('10'), true);
  assert.deepEqual(calls[1].slice(0, 2), ['drive', '+inspect']);
  assert.deepEqual(calls[2].slice(0, 2), ['docs', '+fetch']);
  assert.equal(calls[2].includes('outline'), true);
  assert.deepEqual(calls[3].slice(0, 2), ['drive', '+list-comments']);
  assert.equal(calls.every((call) => !call.includes('--page-all')), true);
});

test('comment body is passed on stdin and never exposed in the process arguments', async (t) => {
  const dir = temporaryDirectory(t);
  const bin = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocations.jsonl');
  const inputPath = path.join(dir, 'stdin.txt');
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require('node:fs');
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => { input += chunk; });
process.stdin.on('end', () => {
  fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(process.argv.slice(2)) + '\\n');
  fs.writeFileSync(process.env.INPUT, input);
  process.stdout.write('{}\\n');
});
`);
  fs.chmodSync(bin, 0o700);
  const runner = createLarkCliRunner({
    bin,
    cwd: dir,
    env: { ...process.env, INVOCATIONS: invocationPath, INPUT: inputPath },
    as: 'user',
  });
  await runner.larkDriveAddComment({
    doc: 'https://example.feishu.cn/docx/private_token',
    comment: '不得进入命令行的评论正文',
  });
  const [call] = readCalls(invocationPath);
  assert.equal(call.includes('不得进入命令行的评论正文'), false);
  assert.equal(call.includes('-'), true);
  assert.deepEqual(JSON.parse(fs.readFileSync(inputPath, 'utf8')), [
    { type: 'text', text: '不得进入命令行的评论正文' },
  ]);
});

test('read output fingerprints identifiers but preserves a bounded content preview', () => {
  const result = sanitizeReadForOutput({
    chat_id: 'oc_private',
    message_id: 'om_private',
    thread_id: 'omt_private',
    sender: { open_id: 'ou_private' },
    document_id: 'document_private',
    wiki_node: {
      node_token: 'node_private',
      obj_token: 'object_private',
      space_id: 7337552898078162948,
    },
    icon_info: '{"token":"embedded_private","version":22}',
    body: '甲'.repeat(260),
    tenant_access_token: 'secret-value',
  }, {}, { previewLength: 80 });
  assert.equal(result.chat_id, fingerprintIdentifier('oc_private'));
  assert.equal(result.message_id, fingerprintIdentifier('om_private'));
  assert.equal(result.thread_id, fingerprintIdentifier('omt_private'));
  assert.equal(result.sender.open_id, fingerprintIdentifier('ou_private'));
  assert.equal(result.document_id, fingerprintIdentifier('document_private'));
  assert.equal(result.wiki_node.node_token, fingerprintIdentifier('node_private'));
  assert.equal(result.wiki_node.obj_token, fingerprintIdentifier('object_private'));
  assert.equal(result.wiki_node.space_id, fingerprintIdentifier('7337552898078163000'));
  assert.equal(JSON.stringify(result.icon_info).includes('embedded_private'), false);
  assert.equal(result.body.preview.length, 80);
  assert.equal(result.body.length, 260);
  assert.equal(result.body.truncated, true);
  assert.equal(JSON.stringify(result).includes('secret-value'), false);
});

test('bridge client message read enforces bounds and returns sanitized JSON end to end', (t) => {
  const dir = temporaryDirectory(t);
  const bin = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocations.jsonl');
  const configPath = path.join(dir, 'client.json');
  fs.writeFileSync(configPath, `${JSON.stringify({
    schemaVersion: 3,
    launchdLabel: 'test.bridge',
    defaultSource: 'test',
    messageTargets: {},
    nameBindings: {},
    groupNameBindings: {},
    documentTargets: {},
  })}\n`, { mode: 0o600 });
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(args) + '\\n');
if (args.includes('--version')) process.stdout.write('lark-cli version 1.0.92\\n');
else if (args[0] === 'auth') process.stdout.write('{}\\n');
else process.stdout.write(JSON.stringify({data:{items:[{message_id:'om_cli_private',body:'端到端读取正文'}]}}) + '\\n');
`);
  fs.chmodSync(bin, 0o700);
  const result = spawnSync(process.execPath, [
    path.join(__dirname, '..', 'scripts', 'bridge-client.js'),
    'message',
    'list',
    '--target',
    'chat_id:oc_cli_private',
    '--start',
    '2026-09-01T00:00:00+08:00',
    '--end',
    '2026-09-02T00:00:00+08:00',
    '--limit',
    '10',
    '--config',
    configPath,
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
  const output = JSON.parse(result.stdout);
  const serialized = JSON.stringify(output);
  assert.equal(serialized.includes('oc_cli_private'), false);
  assert.equal(serialized.includes('om_cli_private'), false);
  assert.equal(output.result.data.items[0].body.preview, '端到端读取正文');
  const calls = readCalls(invocationPath);
  const readCall = calls.find((call) => call.includes('+chat-messages-list'));
  assert.ok(readCall);
  assert.equal(readCall.includes('--page-all'), false);
  assert.equal(readCall[readCall.indexOf('--page-size') + 1], '10');
});
