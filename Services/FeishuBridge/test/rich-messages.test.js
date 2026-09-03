const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { createLarkCliRunner } = require('../lib/lark-cli-runner');
const { assertPrivateMode } = require('./helpers/platform-private');
const {
  buildRuntime,
  cleanupStagedMedia,
  readCompleteJsonl,
  stageOutboundMedia,
  validateMessageRequest,
} = require('../lib/bridge-client-core');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-rich-message-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('outbox validator accepts supported rich formats and rejects unsafe shapes', () => {
  const base = {
    id: 'OUT-RICH',
    target: { type: 'chat_id', id: 'oc_target' },
    explicitAuthorization: true,
    source: 'codex',
  };
  assert.equal(validateMessageRequest({ ...base, type: 'text', text: 'hello' }), '');
  assert.equal(validateMessageRequest({ ...base, type: 'markdown', text: '**hello**' }), '');
  assert.equal(validateMessageRequest({ ...base, type: 'card', text: '{"elements":[]}' }), '');
  assert.equal(validateMessageRequest({ ...base, type: 'image', filePath: '/safe/staged/image.png' }), '');
  assert.equal(validateMessageRequest({ ...base, type: 'file', filePath: '/safe/staged/report.pdf' }), '');
  assert.equal(validateMessageRequest({ ...base, type: 'card', text: 'not-json' }), 'invalid_card_json');
  assert.equal(validateMessageRequest({ ...base, type: 'video', filePath: '/tmp/video.mp4' }), 'unsupported_type');
});

test('lark runner maps rich message formats to explicit shortcut flags', async (t) => {
  const dir = temporaryDirectory(t);
  const fakeLarkPath = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocations.jsonl');
  fs.writeFileSync(fakeLarkPath, `#!/usr/bin/env node
const fs = require('node:fs');
fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(process.argv.slice(2)) + '\\n');
process.stdout.write(JSON.stringify({data:{message_id:'om_result'}}) + '\\n');
`);
  fs.chmodSync(fakeLarkPath, 0o700);
  const mediaDir = path.join(dir, 'media');
  fs.mkdirSync(mediaDir);
  const imagePath = path.join(mediaDir, 'image.png');
  fs.writeFileSync(imagePath, 'image-bytes');
  const runner = createLarkCliRunner({
    bin: fakeLarkPath,
    cwd: dir,
    env: { ...process.env, INVOCATIONS: invocationPath },
    as: 'bot',
  });
  const target = { type: 'chat_id', id: 'oc_target' };
  await runner.larkImSend({ target, format: 'markdown', content: '**hello**', idempotencyKey: 'one' });
  await runner.larkImSend({ target, format: 'card', content: '{"elements":[]}', idempotencyKey: 'two' });
  await runner.larkImSend({ target, format: 'image', filePath: imagePath, idempotencyKey: 'three' });
  const calls = readCompleteJsonl(invocationPath);
  assert.equal(calls[0].includes('--markdown'), true);
  assert.equal(calls[1].includes('--content'), true);
  assert.equal(calls[1].includes('interactive'), true);
  assert.equal(calls[2].includes('--image'), true);
  assert.equal(calls[2].includes('media/image.png'), true);
  assert.equal(calls.flat().includes(imagePath), false);
});

test('media staging is secure, project-local, and cleanup never deletes the source', (t) => {
  const dir = temporaryDirectory(t);
  const root = path.join(dir, 'project');
  fs.mkdirSync(root);
  const source = path.join(dir, 'report.pdf');
  fs.writeFileSync(source, 'report bytes');
  const runtime = buildRuntime(root, {}, { platform: 'darwin', homeDir: dir });
  const staged = stageOutboundMedia(runtime, 'OUT-STAGE', source);
  assert.equal(staged.startsWith(path.join(root, 'node_modules', '.cache', 'feishu-bridge')), true);
  assertPrivateMode(path.dirname(staged), 0o700);
  assertPrivateMode(staged, 0o600);
  cleanupStagedMedia(runtime, staged);
  assert.equal(fs.existsSync(staged), false);
  assert.equal(fs.existsSync(source), true);
});

test('Windows media staging uses the ACL private root and remains sendable', async (t) => {
  const dir = temporaryDirectory(t);
  const projectRoot = path.join(dir, 'project');
  const dataRoot = path.join(dir, '.config', 'feishu-bridge');
  fs.mkdirSync(projectRoot, { recursive: true });
  const source = path.join(dir, 'image.png');
  fs.writeFileSync(source, 'image bytes');
  const runtime = buildRuntime(projectRoot, {}, {
    platform: 'win32', homeDir: dir,
  });
  const staged = stageOutboundMedia(runtime, 'OUT-WINDOWS-MEDIA', source);
  assert.equal(staged.startsWith(path.join(dataRoot, 'private-cache', 'outbox-assets')), true);

  const fakeLarkPath = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'windows-media-invocations.jsonl');
  fs.writeFileSync(fakeLarkPath, `#!/usr/bin/env node
const fs = require('node:fs');
fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(process.argv.slice(2)) + '\\n');
process.stdout.write('{"data":{"message_id":"om_windows"}}\\n');
`);
  fs.chmodSync(fakeLarkPath, 0o700);
  const runner = createLarkCliRunner({
    bin: fakeLarkPath,
    cwd: projectRoot,
    env: { ...process.env, INVOCATIONS: invocationPath },
    as: 'bot',
    mediaRoot: runtime.mediaStagingDir,
  });
  await runner.larkImSend({
    target: { type: 'chat_id', id: 'oc_target' },
    format: 'image',
    filePath: staged,
    idempotencyKey: 'windows-media',
  });
  assert.equal(readCompleteJsonl(invocationPath)[0].includes(staged.split(path.sep).join('/')), true);
});

test('card updates keep delayed tokens and card bodies out of process arguments', async (t) => {
  const dir = temporaryDirectory(t);
  const fakeLarkPath = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocations.jsonl');
  fs.writeFileSync(fakeLarkPath, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
fs.appendFileSync(process.env.INVOCATIONS, JSON.stringify(args) + '\\n');
const dataIndex = args.indexOf('--data');
if (dataIndex >= 0 && args[dataIndex + 1].startsWith('@')) {
  const body = fs.readFileSync(args[dataIndex + 1].slice(1), 'utf8');
  fs.appendFileSync(process.env.PAYLOADS, body + '\\n');
}
process.stdout.write('{}\\n');
`);
  fs.chmodSync(fakeLarkPath, 0o700);
  const payloadPath = path.join(dir, 'payloads.jsonl');
  const runner = createLarkCliRunner({
    bin: fakeLarkPath,
    cwd: dir,
    env: { ...process.env, INVOCATIONS: invocationPath, PAYLOADS: payloadPath },
    as: 'bot',
  });
  await runner.larkCardUpdateByToken({
    token: 'private_delayed_token',
    card: { elements: [{ tag: 'div', text: { content: 'private card body' } }] },
  });
  const args = JSON.stringify(readCompleteJsonl(invocationPath));
  assert.doesNotMatch(args, /private_delayed_token|private card body/);
  const payload = fs.readFileSync(payloadPath, 'utf8');
  assert.match(payload, /private_delayed_token/);
  assert.match(payload, /private card body/);
  const payloadDir = path.join(dir, 'node_modules', '.cache', 'feishu-bridge', 'action-payloads');
  assert.deepEqual(fs.readdirSync(payloadDir), []);
});
