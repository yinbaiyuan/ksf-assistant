const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const test = require('node:test');
const { validateActionRequest } = require('../lib/action-registry');
const { createLarkCliRunner } = require('../lib/lark-cli-runner');
const { executeActionRequest } = require('../lib/work-actions');
const { capabilityManifest, REQUIRED_SCOPES } = require('../lib/capability-policy');
const { sanitizeReadForOutput } = require('../lib/bridge-client-core');
const { bridgeTestEnv } = require('./helpers/platform-private');
const {
  validateMeetingReadRange,
  validateRemoteIdList,
} = require('../scripts/bridge-client');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-meeting-minutes-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function actionRequest(action, target, input, overrides = {}) {
  return {
    id: 'ACT-MINUTES-TEST',
    type: 'feishu_action',
    domain: 'minutes',
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
  const payloadPath = path.join(dir, 'payloads.jsonl');
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
  for (const value of args) {
    if (value.startsWith('@')) {
      const file = path.resolve(process.cwd(), value.slice(1));
      if (fs.existsSync(file)) fs.appendFileSync(process.env.PAYLOAD_LOG, fs.readFileSync(file, 'utf8') + '\\n');
    }
  }
  if (args[0] === 'minutes' && args[1] === '+detail') {
    process.stdout.write(JSON.stringify({ok:true,data:{minutes:[{minute_token:'obcn_private',artifacts:{summary:'旧总结',todos:[]}}]}}) + '\\n');
  } else if (args[0] === 'api' && args[1] === 'GET') {
    process.stdout.write(JSON.stringify({ok:true,data:{speakers:[{speaker_id:'speaker_private'}]}}) + '\\n');
  } else if (args.includes('--version')) {
    process.stdout.write('fake 1.0.0\\n');
  } else {
    process.stdout.write(JSON.stringify({ok:true,data:{updated:true,minute_token:'obcn_private'}}) + '\\n');
  }
});
`);
  fs.chmodSync(bin, 0o700);
  return {
    cwd: dir,
    invocationPath,
    stdinPath,
    payloadPath,
    runner: createLarkCliRunner({
      bin,
      cwd: dir,
      env: {
        ...process.env,
        INVOCATIONS: invocationPath,
        STDIN_LOG: stdinPath,
        PAYLOAD_LOG: payloadPath,
      },
      as: 'user',
    }),
  };
}

function readJsonl(filePath) {
  if (!fs.existsSync(filePath)) return [];
  return fs.readFileSync(filePath, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse);
}

test('meeting and minutes read bounds reject unbounded or oversized requests', () => {
  assert.doesNotThrow(() => validateMeetingReadRange('2026-08-01', '2026-08-31'));
  assert.throws(() => validateMeetingReadRange('2026-08-01', undefined), /both/);
  assert.throws(() => validateMeetingReadRange('2026-01-01', '2026-08-31'), /90 days/);
  assert.deepEqual(validateRemoteIdList('one,two', { label: 'meeting IDs', maximum: 10 }), ['one', 'two']);
  assert.throws(() => validateRemoteIdList('a,b,c', { label: 'meeting IDs', maximum: 2 }), /between 1 and 2/);
  assert.throws(() => validateRemoteIdList('bad value', { label: 'meeting IDs', maximum: 2 }), /invalid/);
});

test('minutes registry accepts reviewed writes and rejects deletes or unconfirmed replacements', () => {
  assert.equal(validateActionRequest(actionRequest('upload',
    { kind: 'file_token', value: 'boxcn_private' }, {})), '');
  assert.equal(validateActionRequest(actionRequest('update_title',
    { kind: 'minute_token', value: 'obcn_private' }, { topic: '新标题' })), '');
  assert.equal(validateActionRequest(actionRequest('replace_summary',
    { kind: 'minute_token', value: 'obcn_private' }, { summary: '新总结' })), 'high_impact_confirmation_required');
  assert.equal(validateActionRequest(actionRequest('replace_summary',
    { kind: 'minute_token', value: 'obcn_private' }, { summary: '新总结' }, { confirmHighImpact: true })), '');
  assert.equal(validateActionRequest(actionRequest('mutate_todos',
    { kind: 'minute_token', value: 'obcn_private' }, {
      todos: [{ operation: 'add', content: '跟进事项', is_done: false }],
    })), '');
  assert.equal(validateActionRequest(actionRequest('mutate_todos',
    { kind: 'minute_token', value: 'obcn_private' }, {
      todos: [{ operation: 'delete', todo_id: 'todo_private' }],
    })), 'minutes_todo_delete_not_supported');
  assert.equal(validateActionRequest(actionRequest('replace_words',
    { kind: 'minute_token', value: 'obcn_private' }, {
      replacements: [{ source_word: '旧词', target_word: '新词' }],
    })), 'high_impact_confirmation_required');
  assert.equal(validateActionRequest(actionRequest('replace_speaker',
    { kind: 'minute_token', value: 'obcn_private' }, {
      fromSpeakerId: 'speaker_private', toUserId: 'ou_private',
    }, { confirmHighImpact: true })), '');
  assert.equal(validateActionRequest(actionRequest('delete',
    { kind: 'minute_token', value: 'obcn_private' }, {})), 'unsupported_domain_or_action');
});

test('minutes summary replacement uses stdin and performs preflight plus reread', async (t) => {
  const { runner, invocationPath, stdinPath } = fakeRunner(t);
  const request = actionRequest('replace_summary',
    { kind: 'minute_token', value: 'obcn_private' },
    { summary: '不得进入 argv 的新总结' },
    { confirmHighImpact: true });
  const result = await executeActionRequest(runner, request);
  const calls = readJsonl(invocationPath);
  assert.deepEqual(calls.map((call) => call.slice(0, 2)), [
    ['minutes', '+detail'],
    ['minutes', '+summary'],
    ['minutes', '+detail'],
  ]);
  assert.equal(calls.some((call) => JSON.stringify(call).includes('不得进入 argv')), false);
  assert.equal(calls[1][calls[1].indexOf('--summary') + 1], '-');
  assert.deepEqual(readJsonl(stdinPath), ['不得进入 argv 的新总结']);
  assert.equal(result.verified, true);
});

test('minutes todo payload stays in a private file and delete never reaches lark-cli', async (t) => {
  const { runner, invocationPath, payloadPath } = fakeRunner(t);
  const request = actionRequest('mutate_todos',
    { kind: 'minute_token', value: 'obcn_private' }, {
      todos: [{ operation: 'add', content: '私密待办', is_done: false }],
    });
  const result = await executeActionRequest(runner, request);
  const calls = readJsonl(invocationPath);
  assert.deepEqual(calls.map((call) => call.slice(0, 2)), [
    ['minutes', '+detail'],
    ['minutes', '+todo'],
    ['minutes', '+detail'],
  ]);
  assert.equal(calls.some((call) => JSON.stringify(call).includes('私密待办')), false);
  assert.equal(fs.readFileSync(payloadPath, 'utf8').includes('私密待办'), true);
  assert.equal(result.verified, true);
});

test('remaining minutes actions use the reviewed shortcuts and exact verification strategy', async (t) => {
  const uploadFixture = fakeRunner(t);
  await executeActionRequest(uploadFixture.runner, actionRequest('upload',
    { kind: 'file_token', value: 'boxcn_private' }, {}));
  assert.deepEqual(readJsonl(uploadFixture.invocationPath)[0].slice(0, 2), ['minutes', '+upload']);

  const titleFixture = fakeRunner(t);
  const title = await executeActionRequest(titleFixture.runner, actionRequest('update_title',
    { kind: 'minute_token', value: 'obcn_private' }, { topic: '新标题' }));
  assert.deepEqual(readJsonl(titleFixture.invocationPath).map((call) => call.slice(0, 2)), [
    ['minutes', '+detail'], ['minutes', '+update'], ['minutes', '+detail'],
  ]);
  assert.equal(title.verified, true);

  const wordsFixture = fakeRunner(t);
  const words = await executeActionRequest(wordsFixture.runner, actionRequest('replace_words',
    { kind: 'minute_token', value: 'obcn_private' }, {
      replacements: [{ source_word: '私密旧词', target_word: '私密新词' }],
    }, { confirmHighImpact: true }));
  const wordCalls = readJsonl(wordsFixture.invocationPath);
  assert.deepEqual(wordCalls[0].slice(0, 2), ['minutes', '+word-replace']);
  assert.equal(JSON.stringify(wordCalls).includes('私密旧词'), false);
  assert.equal(fs.readFileSync(wordsFixture.payloadPath, 'utf8').includes('私密旧词'), true);
  assert.equal(words.verified, true);

  const speakerFixture = fakeRunner(t);
  const speaker = await executeActionRequest(speakerFixture.runner, actionRequest('replace_speaker',
    { kind: 'minute_token', value: 'obcn_private' }, {
      fromSpeakerId: 'speaker_private', toUserId: 'ou_private',
    }, { confirmHighImpact: true }));
  assert.deepEqual(readJsonl(speakerFixture.invocationPath).map((call) => call.slice(0, 2)), [
    ['api', 'GET'], ['minutes', '+speaker-replace'], ['api', 'GET'],
  ]);
  assert.equal(speaker.verified, true);
});

test('meeting sanitization hides remote IDs, links, and deleted transcript paths', () => {
  const result = sanitizeReadForOutput({
    meeting_id: 'meeting_private',
    minute_token: 'obcn_private_token',
    note_id: 'note_private',
    meeting_no: '123456789',
    url: 'https://vc.feishu.cn/j/123456789',
    transcript_file: '/tmp/private-title-obcn_private_token/transcript.txt',
  }, {}, { preserveUrls: false });
  const serialized = JSON.stringify(result);
  assert.equal(serialized.includes('meeting_private'), false);
  assert.equal(serialized.includes('obcn_private_token'), false);
  assert.equal(serialized.includes('note_private'), false);
  assert.equal(serialized.includes('123456789'), false);
  assert.equal(serialized.includes('https://vc.feishu.cn'), false);
  assert.deepEqual(result.transcript_file, { redacted: true });
});

test('meeting search is user-scoped, single-page, bounded, and sanitizes meeting identifiers and URLs', (t) => {
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
else process.stdout.write(JSON.stringify({ok:true,data:{items:[{meeting_id:'meeting_private',meeting_no:'123456789',note_id:'note_private',minute_token:'obcn_private',url:'https://vc.feishu.cn/j/123456789',topic:'周会'}]}}) + '\\n');
`);
  fs.chmodSync(bin, 0o700);
  const result = spawnSync(process.execPath, [
    path.join(__dirname, '..', 'scripts', 'bridge-client.js'),
    'meeting', 'search', '--query', '周会',
    '--start', '2026-08-01', '--end', '2026-08-31', '--limit', '10', '--config', configPath,
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
  const serialized = result.stdout;
  assert.equal(serialized.includes('meeting_private'), false);
  assert.equal(serialized.includes('note_private'), false);
  assert.equal(serialized.includes('obcn_private'), false);
  assert.equal(serialized.includes('https://vc.feishu.cn/j/123456789'), false);
  const command = readJsonl(invocationPath).find((call) => call.includes('+search'));
  assert.ok(command);
  assert.equal(command.includes('--as') && command.includes('user'), true);
  assert.equal(command.includes('--page-all'), false);
  assert.equal(command[command.indexOf('--page-size') + 1], '10');
});

test('minutes transcript is read through a private temporary file and cleaned after output', (t) => {
  const dir = temporaryDirectory(t);
  const bin = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocations.jsonl');
  const configPath = path.join(dir, 'client.json');
  fs.writeFileSync(configPath, `${JSON.stringify({ schemaVersion: 3, launchdLabel: 'test.bridge' })}\n`, { mode: 0o600 });
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require('node:fs');
const path = require('node:path');
const args = process.argv.slice(2);
fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(args) + '\\n');
if (args.includes('--version')) process.stdout.write('fake 1.0.0\\n');
else if (args[0] === 'auth') process.stdout.write('{}\\n');
else {
  const out = args[args.indexOf('--output-dir') + 1];
  const artifact = path.join(out, 'artifact-private-obcn_private');
  fs.mkdirSync(artifact, { recursive: true });
  fs.writeFileSync(path.join(artifact, 'transcript.txt'), '这是需要被读取并清理的妙记逐字稿');
  process.stdout.write(JSON.stringify({ok:true,data:{minutes:[{minute_token:'obcn_private',artifacts:{transcript_file:path.join(artifact,'transcript.txt')}}]}}) + '\\n');
}
`);
  fs.chmodSync(bin, 0o700);
  const result = spawnSync(process.execPath, [
    path.join(__dirname, '..', 'scripts', 'bridge-client.js'),
    'minutes', 'transcript', '--minute-token', 'obcn_private', '--max-chars', '20000', '--config', configPath,
  ], {
    cwd: path.join(__dirname, '..'),
    env: bridgeTestEnv(dir, {
      ...process.env,
      TMPDIR: dir,
      LARK_CLI_BIN: bin,
      INVOCATIONS: invocationPath,
      FEISHU_BRIDGE_LOG_DIR: path.join(dir, 'logs'),
      FEISHU_AUDIT_DIR: path.join(dir, 'audit'),
    }),
    encoding: 'utf8',
  });
  assert.equal(result.status, 0, result.stderr);
  const output = JSON.parse(result.stdout);
  assert.equal(output.result.transcript.preview, '这是需要被读取并清理的妙记逐字稿');
  assert.equal(result.stdout.includes('obcn_private'), false);
  const transcriptCall = readJsonl(invocationPath).find((call) => call.includes('--output-dir'));
  const outputDir = transcriptCall[transcriptCall.indexOf('--output-dir') + 1];
  assert.equal(path.isAbsolute(outputDir), false);
  assert.match(outputDir, /^node_modules\/\.cache\/feishu-bridge\/transcripts\/minutes-/);
  assert.equal(fs.existsSync(path.join(__dirname, '..', outputDir)), false);
});

test('capability and permission manifests include meeting and minutes without live meeting controls', () => {
  const capabilities = capabilityManifest();
  assert.equal(capabilities.readCapabilities.includes('meeting.search'), true);
  assert.equal(capabilities.readCapabilities.includes('minutes.transcript'), true);
  assert.equal(capabilities.queuedWriteCapabilities.includes('minutes.replace_summary'), true);
  assert.equal(capabilities.queuedWriteCapabilities.includes('vc.meeting_end'), false);
  assert.equal(REQUIRED_SCOPES.user.includes('vc:meeting.search:read'), true);
  assert.equal(REQUIRED_SCOPES.user.includes('vc:meeting'), true);
  assert.equal(REQUIRED_SCOPES.user.includes('vc:meeting:readonly'), false);
  assert.equal(REQUIRED_SCOPES.user.includes('minutes:minutes:update'), true);
});
