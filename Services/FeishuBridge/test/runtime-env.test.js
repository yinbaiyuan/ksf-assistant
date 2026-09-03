const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { loadRuntimeEnvironment } = require('../lib/runtime-env');

test('private runtime settings survive packaged deployments and process values win', (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-runtime-env-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const project = path.join(root, 'project');
  const data = path.join(root, 'private');
  fs.mkdirSync(project, { recursive: true });
  fs.mkdirSync(data, { recursive: true });
  fs.writeFileSync(path.join(project, '.env.local'), 'FEISHU_OUTBOUND_ENABLED=false\n');
  fs.writeFileSync(path.join(data, 'runtime.env'), 'FEISHU_OUTBOUND_ENABLED=true\nFEISHU_GROUP_ENABLED=true\n');
  const loaded = loadRuntimeEnvironment(project, {
    env: { CODEX_USAGE_BAR_MANAGED: '1', FEISHU_BRIDGE_DATA_DIR: data, FEISHU_GROUP_ENABLED: 'false' },
    homeDir: root,
  });
  assert.equal(loaded.FEISHU_OUTBOUND_ENABLED, 'true');
  assert.equal(loaded.FEISHU_GROUP_ENABLED, 'false');
});

test('managed packaged runtime uses canonical non-secret settings over legacy flags', (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-managed-settings-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const project = path.join(root, 'project');
  const data = path.join(root, 'private');
  fs.mkdirSync(project, { recursive: true });
  fs.mkdirSync(data, { recursive: true, mode: 0o700 });
  fs.writeFileSync(path.join(data, 'runtime.env'), 'FEISHU_OUTBOUND_ENABLED=false\n');
  fs.writeFileSync(path.join(data, 'feishu-settings-v1.json'), JSON.stringify({
    version: 1,
    profile: 'primary',
    group: { enabled: true },
    outbound: { enabled: true, dryRun: true },
    directory: { enabled: true },
    groupDirectory: { enabled: false },
    docbox: { enabled: true, dryRun: true },
    actionbox: { enabled: false, dryRun: true },
    codex: { defaultThreadTitle: '飞书默认对话' },
  }), { mode: 0o600 });

  const loaded = loadRuntimeEnvironment(project, {
    env: {
      CODEX_USAGE_BAR_MANAGED: '1',
      FEISHU_BRIDGE_DATA_DIR: data,
      FEISHU_OUTBOUND_ENABLED: 'false',
    },
    homeDir: root,
  });

  assert.equal(loaded.FEISHU_OUTBOUND_ENABLED, 'true');
  assert.equal(loaded.FEISHU_OUTBOUND_DRY_RUN, 'true');
  assert.equal(loaded.FEISHU_GROUP_ENABLED, 'true');
  assert.equal(loaded.FEISHU_DIRECTORY_ENABLED, 'true');
  assert.equal(loaded.FEISHU_DOCBOX_ENABLED, 'true');
  assert.equal(loaded.CODEX_FEISHU_DEFAULT_THREAD_TITLE, '飞书默认对话');
});
