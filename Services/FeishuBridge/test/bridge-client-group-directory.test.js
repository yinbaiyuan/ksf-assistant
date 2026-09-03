const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { readCompleteJsonl, writeSecureJson } = require('../lib/bridge-client-core');
const { handleSend, handleTargets } = require('../scripts/bridge-client');
const { assertPrivateMode } = require('./helpers/platform-private');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-client-group-directory-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function setEnvironment(t, values) {
  const previous = {};
  for (const [key, value] of Object.entries(values)) {
    previous[key] = process.env[key];
    process.env[key] = String(value);
  }
  t.after(() => {
    for (const [key, value] of Object.entries(previous)) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
  });
}

function writeCache(cachePath, groups) {
  writeSecureJson(cachePath, {
    schemaVersion: 1,
    source: 'test',
    complete: true,
    generatedAt: new Date().toISOString(),
    groups,
  });
}

function baseConfig() {
  return {
    schemaVersion: 3,
    launchdLabel: 'com.example.test',
    defaultSource: 'test',
    messageTargets: {},
    nameBindings: {},
    groupNameBindings: {},
    documentTargets: {},
  };
}

function baseEnvironment(dir, logDir, cachePath) {
  return {
    FEISHU_BRIDGE_LOG_DIR: logDir,
    FEISHU_OUTBOX_PATH: path.join(logDir, 'outbox.jsonl'),
    FEISHU_OUTBOUND_ENABLED: 'true',
    FEISHU_GROUP_DIRECTORY_ENABLED: 'true',
    FEISHU_GROUP_DIRECTORY_CACHE_PATH: cachePath,
    FEISHU_GROUP_DIRECTORY_STATE_PATH: path.join(dir, 'config', 'group-directory-state.json'),
    FEISHU_GROUP_DIRECTORY_MAX_AGE_MS: '60000',
  };
}

test('unique exact group name submits the existing outbox chat_id schema without exposing the ID', async (t) => {
  const dir = temporaryDirectory(t);
  const logDir = path.join(dir, 'logs');
  const configPath = path.join(dir, 'config', 'client.json');
  const cachePath = path.join(dir, 'config', 'group-directory.json');
  const textPath = path.join(dir, 'message.txt');
  fs.mkdirSync(logDir, { recursive: true });
  fs.writeFileSync(textPath, '仅用于测试的群消息');
  fs.writeFileSync(path.join(logDir, 'bridge.pid'), `${JSON.stringify({ pid: process.pid })}\n`);
  writeSecureJson(configPath, baseConfig());
  writeCache(cachePath, [{
    chatId: 'oc-private-target',
    displayName: 'codex测试群',
    description: '测试',
    external: false,
    active: true,
  }]);
  setEnvironment(t, baseEnvironment(dir, logDir, cachePath));

  setTimeout(() => {
    if (!fs.existsSync(logDir)) return;
    fs.writeFileSync(path.join(logDir, 'outbox-results.jsonl'), `${JSON.stringify({
      id: 'OUT-GROUP-UNIQUE',
      status: 'sent',
      target: { type: 'chat_id', id: 'oc-private-target' },
    })}\n`);
  }, 50);
  const result = await handleSend({
    config: configPath,
    targetGroupName: 'codex测试群',
    textFile: textPath,
    id: 'OUT-GROUP-UNIQUE',
    timeoutMs: '2000',
    pollMs: '20',
  });
  assert.equal(result.status, 'sent');
  assert.equal(result.groupDirectoryResolution.source, 'unique_exact_group_name');
  assert.equal(JSON.stringify(result).includes('oc-private-target'), false);
  const queued = readCompleteJsonl(path.join(logDir, 'outbox.jsonl'));
  assert.equal(queued.length, 1);
  assert.deepEqual(queued[0].target, { type: 'chat_id', id: 'oc-private-target' });
  assert.equal(queued[0].targetGroupName, undefined);
});

test('duplicate exact group names stop before outbox and expose only safe candidate fields', async (t) => {
  const dir = temporaryDirectory(t);
  const logDir = path.join(dir, 'logs');
  const configPath = path.join(dir, 'config', 'client.json');
  const cachePath = path.join(dir, 'config', 'group-directory.json');
  const textPath = path.join(dir, 'message.txt');
  fs.mkdirSync(logDir, { recursive: true });
  fs.writeFileSync(textPath, '不会入队');
  writeSecureJson(configPath, baseConfig());
  writeCache(cachePath, [
    { chatId: 'oc-one', displayName: '项目群', description: '产品', external: false, active: true },
    { chatId: 'oc-two', displayName: '项目群', description: '研发', external: false, active: true },
  ]);
  setEnvironment(t, baseEnvironment(dir, logDir, cachePath));

  const result = await handleSend({ config: configPath, targetGroupName: '项目群', textFile: textPath });
  assert.equal(result.status, 'ambiguous');
  assert.equal(result.submitted, false);
  assert.equal(result.candidates.length, 2);
  assert.equal(JSON.stringify(result).includes('oc-one'), false);
  assert.equal(fs.existsSync(path.join(logDir, 'outbox.jsonl')), false);
});

test('group directory search and bind store the selected target only in secure client config', async (t) => {
  const dir = temporaryDirectory(t);
  const configPath = path.join(dir, 'config', 'client.json');
  const cachePath = path.join(dir, 'config', 'group-directory.json');
  writeSecureJson(configPath, baseConfig());
  writeCache(cachePath, [
    { chatId: 'oc-one', displayName: '项目群', description: '产品', external: false, active: true },
    { chatId: 'oc-two', displayName: '项目群', description: '研发', external: false, active: true },
  ]);
  setEnvironment(t, {
    FEISHU_GROUP_DIRECTORY_ENABLED: 'true',
    FEISHU_GROUP_DIRECTORY_CACHE_PATH: cachePath,
    FEISHU_GROUP_DIRECTORY_STATE_PATH: path.join(dir, 'config', 'state.json'),
    FEISHU_GROUP_DIRECTORY_MAX_AGE_MS: '60000',
  });

  const search = await handleTargets(['group-directory', 'search'], { config: configPath, query: '项目群' });
  const selected = search.matches.find((candidate) => candidate.description === '研发');
  const bound = await handleTargets(['group-directory', 'bind'], {
    config: configPath,
    name: '项目群',
    candidate: selected.targetFingerprint,
  });
  assert.equal(bound.status, 'bound');
  const saved = JSON.parse(fs.readFileSync(configPath, 'utf8'));
  assert.equal(saved.groupNameBindings['项目群'].id, 'oc-two');
  assertPrivateMode(configPath, 0o600);
});

test('explicit, person-name, and group-name target flags are mutually exclusive', async () => {
  await assert.rejects(
    handleSend({ target: '我', targetGroupName: 'codex测试群', textFile: 'unused' }),
    /mutually exclusive/,
  );
  await assert.rejects(
    handleSend({ targetName: '测试用户', targetGroupName: 'codex测试群', textFile: 'unused' }),
    /mutually exclusive/,
  );
});
