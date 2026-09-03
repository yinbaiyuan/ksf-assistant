const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { execFileSync } = require('node:child_process');
const test = require('node:test');
const { collectValues, scheduleAnalysis } = require('../scripts/bridge-client');
const { bridgeTestEnv } = require('./helpers/platform-private');

test('standup schedule analysis sorts, deduplicates, detects overlap, and reports free windows', () => {
  const result = scheduleAnalysis({ data: { items: [
    { summary: 'B', start_time: '2026-09-02T10:30:00+08:00', end_time: '2026-09-02T11:30:00+08:00' },
    { summary: 'A', start_time: '2026-09-02T09:00:00+08:00', end_time: '2026-09-02T11:00:00+08:00' },
    { summary: 'A', start_time: '2026-09-02T09:00:00+08:00', end_time: '2026-09-02T11:00:00+08:00' },
  ] } }, '2026-09-02T08:00:00+08:00', '2026-09-02T18:00:00+08:00');
  assert.deepEqual(result.slots.map((item) => item.title), ['A', 'B']);
  assert.equal(result.conflicts.length, 1);
  assert.equal(result.free.length, 2);
  assert.equal(result.free[0].end, '2026-09-02T01:00:00.000Z');
  assert.equal(result.free[1].start, '2026-09-02T03:30:00.000Z');
});

test('meeting material identifiers are unique and workflow commands never publish automatically', () => {
  assert.deepEqual(collectValues({ a: [{ note_id: 'n1' }, { noteId: 'n1' }], b: { note_id: 'n2' } }, ['note_id', 'noteId']), ['n1', 'n2']);
  const source = fs.readFileSync(path.join(__dirname, '..', 'scripts', 'bridge-client.js'), 'utf8');
  const section = source.slice(source.indexOf('async function handleWorkflow'), source.indexOf('function handleResult'));
  assert.match(section, /publish:\s*false/g);
  assert.doesNotMatch(section, /submitQueueRequest|docbox|actionbox|outbox/);
});

test('standup-report composes bounded calendar and task reads through the unified client', (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-workflow-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const fake = path.join(dir, 'fake-lark.js');
  fs.writeFileSync(fake, `#!/usr/bin/env node
const args = process.argv.slice(2);
if (args.includes('--version')) process.stdout.write('lark-cli version 1.0.92\\n');
else if (args[0] === 'auth') process.stdout.write('{}\\n');
else if (args[0] === 'calendar') process.stdout.write(JSON.stringify({data:{items:[{summary:'站会',start_time:'2026-09-02T09:00:00+08:00',end_time:'2026-09-02T09:30:00+08:00'}]}})+'\\n');
else if (args[0] === 'task') process.stdout.write(JSON.stringify({data:{tasks:[{summary:'未完成任务',completed:false}]}})+'\\n');
else process.stdout.write('{}\\n');
`);
  fs.chmodSync(fake, 0o700);
  const output = execFileSync(process.execPath, [
    path.join(__dirname, '..', 'scripts', 'bridge-client.js'),
    'workflow', 'standup-report',
    '--start', '2026-09-02T08:00:00+08:00',
    '--end', '2026-09-02T18:00:00+08:00',
    '--config', path.join(dir, 'client.json'),
  ], {
    cwd: path.join(__dirname, '..'),
    env: bridgeTestEnv(dir, { ...process.env, LARK_CLI_BIN: fake, FEISHU_BRIDGE_LOG_DIR: dir }),
    encoding: 'utf8',
  });
  const result = JSON.parse(output);
  assert.equal(result.workflow, 'standup-report');
  assert.equal(result.publish, false);
  assert.equal(result.schedule.slots[0].title, '站会');
  assert.equal(result.schedule.conflicts.length, 0);
});

test('meeting-summary accepts a Minutes URL, resolves its Note, and remains read-only', (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-meeting-summary-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const fake = path.join(dir, 'fake-lark.js');
  const invocations = path.join(dir, 'invocations.jsonl');
  fs.writeFileSync(fake, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(args) + '\\n');
if (args.includes('--version')) process.stdout.write('lark-cli version 1.0.92\\n');
else if (args[0] === 'auth') process.stdout.write('{}\\n');
else if (args[0] === 'minutes' && args[1] === 'minutes') process.stdout.write(JSON.stringify({data:{minute:{token:'obcn_private',note_id:'note_private',title:'验收会议'}}})+'\\n');
else if (args[0] === 'note') process.stdout.write(JSON.stringify({data:{note_id:'note_private',summary:'关联 Note'}})+'\\n');
else if (args[0] === 'minutes' && args[1] === '+detail') process.stdout.write(JSON.stringify({data:{minutes:[{minute_token:'obcn_private',artifacts:{summary:'结构化总结'}}]}})+'\\n');
else process.stdout.write('{}\\n');
`);
  fs.chmodSync(fake, 0o700);
  const output = execFileSync(process.execPath, [
    path.join(__dirname, '..', 'scripts', 'bridge-client.js'),
    'workflow', 'meeting-summary',
    '--minutes-url', 'https://tenant.feishu.cn/minutes/obcn_private',
    '--config', path.join(dir, 'client.json'),
  ], {
    cwd: path.join(__dirname, '..'),
    env: bridgeTestEnv(dir, {
      ...process.env,
      LARK_CLI_BIN: fake,
      INVOCATIONS: invocations,
      FEISHU_BRIDGE_LOG_DIR: dir,
    }),
    encoding: 'utf8',
  });
  const result = JSON.parse(output);
  assert.equal(result.workflow, 'meeting-summary');
  assert.equal(result.source, 'minutes');
  assert.equal(result.publish, false);
  assert.equal(output.includes('obcn_private'), false);
  assert.equal(output.includes('note_private'), false);
  const calls = fs.readFileSync(invocations, 'utf8').trim().split('\n').map(JSON.parse);
  assert.deepEqual(calls.filter((call) => !call.includes('--version') && call[0] !== 'auth').map((call) => call.slice(0, 2)), [
    ['minutes', 'minutes'],
    ['note', '+detail'],
    ['minutes', '+detail'],
  ]);
  assert.equal(calls.some((call) => call.includes('https://tenant.feishu.cn/minutes/obcn_private')), false);
});
