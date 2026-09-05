const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const {
  firstMessageTaskTitle,
  readHostContext,
  resolveRootMessageWorkspace,
} = require('../lib/host-context');
const contract = require('./fixtures/host-task-routing-contract-v1.json');

function fixture(value) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ksfassistant-host-context-'));
  const filePath = path.join(root, 'ksfassistant-host-context-v1.json');
  fs.writeFileSync(filePath, JSON.stringify(value), { mode: 0o600 });
  return { root, filePath };
}

test('managed root messages use only the ready host KSF root', () => {
  const ksfRoot = fs.mkdtempSync(path.join(os.tmpdir(), 'ksfassistant-ksf-'));
  const item = fixture({
    protocol: 'ksfassistant-host-context-v1',
    schemaVersion: 1,
    updatedAt: new Date().toISOString(),
    ksf: { state: 'ready', root: ksfRoot },
  });
  assert.equal(readHostContext(item.filePath).ksf.root, fs.realpathSync(ksfRoot));
  assert.deepEqual(resolveRootMessageWorkspace({
    managed: true,
    hostContextPath: item.filePath,
    fallbackWorkspace: '/fallback',
    dataRoot: item.root,
  }), { cwd: fs.realpathSync(ksfRoot), ksfState: 'ready', ksfRoot: fs.realpathSync(ksfRoot) });
});

test('language-neutral host task routing contract remains reusable by the Go bridge', () => {
  assert.equal(contract.schemaVersion, 1);
  assert.equal(contract.rootMessageRouting.ready, 'ksf_root_unassigned_task');
  assert.equal(contract.transport.managedBridgeOwned, 'app-server');
  assert.equal(contract.transport.proxyAttempts, 0);
  assert.deepEqual(contract.timing.cardLabels, ['总耗时', 'Codex']);
});

test('not configured uses managed generic workspace and invalid fails closed', () => {
  const unconfigured = fixture({
    protocol: 'ksfassistant-host-context-v1', schemaVersion: 1,
    updatedAt: new Date().toISOString(), ksf: { state: 'not_configured' },
  });
  assert.deepEqual(resolveRootMessageWorkspace({
    managed: true, hostContextPath: unconfigured.filePath, fallbackWorkspace: '/managed', dataRoot: unconfigured.root,
  }), { cwd: '/managed', ksfState: 'not_configured', ksfRoot: '' });

  const invalid = fixture({
    protocol: 'ksfassistant-host-context-v1', schemaVersion: 1,
    updatedAt: new Date().toISOString(), ksf: { state: 'invalid' },
  });
  assert.throws(() => resolveRootMessageWorkspace({
    managed: true, hostContextPath: invalid.filePath, fallbackWorkspace: '/managed', dataRoot: invalid.root,
  }), /KSFAssistant → 设置 → KSF 知识库/);
});

test('Feishu task title normalizes and safely truncates user text', () => {
  assert.equal(firstMessageTaskTitle('  ＡＢＣ  \nsecond'), '飞书 · ABC');
  assert.equal(firstMessageTaskTitle('', 'image'), '飞书 · 图片');
  assert.equal(firstMessageTaskTitle('', ''), '飞书 · 飞书任务');
  const title = firstMessageTaskTitle(`👨‍👩‍👧‍👦${'长'.repeat(100)}`);
  const segmenter = new Intl.Segmenter('zh-CN', { granularity: 'grapheme' });
  assert.ok([...segmenter.segment(title)].length <= 64);
  assert.ok(title.startsWith('飞书 · 👨‍👩‍👧‍👦'));
});
