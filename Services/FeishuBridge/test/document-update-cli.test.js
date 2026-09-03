const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { createLarkCliRunner } = require('../lib/lark-cli-runner');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-doc-update-cli-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function fakeLark(t, output = '{}') {
  const dir = temporaryDirectory(t);
  const bin = path.join(dir, 'fake-lark.js');
  const invocationPath = path.join(dir, 'invocation.json');
  const inputPath = path.join(dir, 'stdin.txt');
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require('node:fs');
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => { input += chunk; });
process.stdin.on('end', () => {
  fs.writeFileSync(process.env.INVOCATION, JSON.stringify(process.argv.slice(2)));
  fs.writeFileSync(process.env.INPUT, input);
  process.stdout.write(process.env.OUTPUT + '\\n');
});
`);
  fs.chmodSync(bin, 0o700);
  return {
    invocationPath,
    inputPath,
    runner: createLarkCliRunner({
      bin,
      cwd: dir,
      env: { ...process.env, INVOCATION: invocationPath, INPUT: inputPath, OUTPUT: output },
      as: 'bot',
    }),
  };
}

test('document update follows lark-cli 1.0.92 stdin and pattern contract', async (t) => {
  const { runner, invocationPath, inputPath } = fakeLark(t);
  await runner.larkDocUpdate('docx_private', {
    content: '替换后的正文',
    docFormat: 'markdown',
    command: 'str_replace',
    selectionWithEllipsis: '唯一旧文本',
  });
  const args = JSON.parse(fs.readFileSync(invocationPath, 'utf8'));
  assert.equal(args.includes('--content'), true);
  assert.equal(args[args.indexOf('--content') + 1], '-');
  assert.equal(args.includes('替换后的正文'), false);
  assert.equal(fs.readFileSync(inputPath, 'utf8'), '替换后的正文');
  assert.equal(args.includes('--pattern'), true);
  assert.equal(args[args.indexOf('--pattern') + 1], '唯一旧文本');
  assert.equal(args.includes('--selection-with-ellipsis'), false);
  assert.equal(args.includes('--selection-by-title'), false);
});

test('document create uses the fixed docs shortcut and keeps body on stdin', async (t) => {
  const { runner, invocationPath, inputPath } = fakeLark(
    t,
    '{"data":{"document":{"document_id":"docx_result"}}}',
  );
  await runner.larkDocCreate({
    content: '固定创建正文',
    docFormat: 'markdown',
    title: '固定创建标题',
  });
  const args = JSON.parse(fs.readFileSync(invocationPath, 'utf8'));
  assert.deepEqual(args.slice(0, 2), ['docs', '+create']);
  assert.equal(args[args.indexOf('--content') + 1], '-');
  assert.equal(args.includes('固定创建正文'), false);
  assert.equal(fs.readFileSync(inputPath, 'utf8'), '固定创建正文');
});
