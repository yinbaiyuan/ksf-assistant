const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { readCompleteJsonl, writeSecureJson } = require('../lib/bridge-client-core');
const { handleSend, handleTargets } = require('../scripts/bridge-client');
const { assertPrivateMode } = require('./helpers/platform-private');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-client-directory-'));
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

function writeCache(cachePath, users) {
  writeSecureJson(cachePath, {
    schemaVersion: 1,
    source: 'test',
    complete: true,
    generatedAt: new Date().toISOString(),
    departmentCount: 2,
    users,
  });
}

function baseConfig() {
  return {
    schemaVersion: 2,
    launchdLabel: 'com.example.test',
    defaultSource: 'test',
    messageTargets: {},
    nameBindings: {},
    documentTargets: {},
  };
}

test('unique name resolution submits the existing outbox schema and hides the cached open_id', async (t) => {
  const dir = temporaryDirectory(t);
  const logDir = path.join(dir, 'logs');
  const configPath = path.join(dir, 'config', 'client.json');
  const cachePath = path.join(dir, 'config', 'directory.json');
  const statePath = path.join(dir, 'config', 'directory-state.json');
  const textPath = path.join(dir, 'message.txt');
  fs.mkdirSync(logDir, { recursive: true });
  fs.writeFileSync(textPath, '仅用于测试的消息');
  fs.writeFileSync(path.join(logDir, 'bridge.pid'), `${JSON.stringify({ pid: process.pid })}\n`);
  writeSecureJson(configPath, baseConfig());
  writeCache(cachePath, [{
    openId: 'ou-private-target',
    displayName: '唯一用户',
    names: ['唯一用户'],
    departmentPaths: ['研发'],
    active: true,
  }]);
  setEnvironment(t, {
    FEISHU_BRIDGE_LOG_DIR: logDir,
    FEISHU_OUTBOX_PATH: path.join(logDir, 'outbox.jsonl'),
    FEISHU_OUTBOUND_ENABLED: 'true',
    FEISHU_DIRECTORY_ENABLED: 'true',
    FEISHU_DIRECTORY_CACHE_PATH: cachePath,
    FEISHU_DIRECTORY_STATE_PATH: statePath,
    FEISHU_DIRECTORY_MAX_AGE_MS: '60000',
  });

  setTimeout(() => {
    fs.writeFileSync(path.join(logDir, 'outbox-results.jsonl'), `${JSON.stringify({
      id: 'OUT-NAME-UNIQUE',
      status: 'sent',
      target: { type: 'open_id', id: 'ou-private-target' },
    })}\n`);
  }, 50);
  const result = await handleSend({
    config: configPath,
    targetName: '唯一用户',
    textFile: textPath,
    id: 'OUT-NAME-UNIQUE',
    timeoutMs: '2000',
    pollMs: '20',
  });
  assert.equal(result.status, 'sent');
  assert.equal(result.directoryResolution.source, 'unique_exact_name');
  assert.equal(JSON.stringify(result).includes('ou-private-target'), false);
  const queued = readCompleteJsonl(path.join(logDir, 'outbox.jsonl'));
  assert.equal(queued.length, 1);
  assert.deepEqual(queued[0].target, { type: 'open_id', id: 'ou-private-target' });
  assert.equal(queued[0].targetName, undefined);
});

test('ambiguous names stop before outbox submission and expose only fingerprints', async (t) => {
  const dir = temporaryDirectory(t);
  const logDir = path.join(dir, 'logs');
  const configPath = path.join(dir, 'config', 'client.json');
  const cachePath = path.join(dir, 'config', 'directory.json');
  const textPath = path.join(dir, 'message.txt');
  fs.mkdirSync(logDir, { recursive: true });
  fs.writeFileSync(textPath, '不会入队');
  writeSecureJson(configPath, baseConfig());
  writeCache(cachePath, [
    { openId: 'ou-one', displayName: '张三', names: ['张三'], departmentPaths: ['产品'], active: true },
    { openId: 'ou-two', displayName: '张三', names: ['张三'], departmentPaths: ['研发'], active: true },
  ]);
  setEnvironment(t, {
    FEISHU_BRIDGE_LOG_DIR: logDir,
    FEISHU_OUTBOX_PATH: path.join(logDir, 'outbox.jsonl'),
    FEISHU_OUTBOUND_ENABLED: 'true',
    FEISHU_DIRECTORY_ENABLED: 'true',
    FEISHU_DIRECTORY_CACHE_PATH: cachePath,
    FEISHU_DIRECTORY_STATE_PATH: path.join(dir, 'config', 'state.json'),
    FEISHU_DIRECTORY_MAX_AGE_MS: '60000',
  });

  const result = await handleSend({ config: configPath, targetName: '张三', textFile: textPath });
  assert.equal(result.status, 'ambiguous');
  assert.equal(result.submitted, false);
  assert.equal(result.candidates.length, 2);
  assert.equal(JSON.stringify(result).includes('ou-one'), false);
  assert.equal(fs.existsSync(path.join(logDir, 'outbox.jsonl')), false);
});

test('directory bind remembers a selected same-name fingerprint in secure client config', async (t) => {
  const dir = temporaryDirectory(t);
  const configPath = path.join(dir, 'config', 'client.json');
  const cachePath = path.join(dir, 'config', 'directory.json');
  writeSecureJson(configPath, baseConfig());
  writeCache(cachePath, [
    { openId: 'ou-one', displayName: '张三', names: ['张三'], departmentPaths: ['产品'], active: true },
    { openId: 'ou-two', displayName: '张三', names: ['张三'], departmentPaths: ['研发'], active: true },
  ]);
  setEnvironment(t, {
    FEISHU_DIRECTORY_ENABLED: 'true',
    FEISHU_DIRECTORY_CACHE_PATH: cachePath,
    FEISHU_DIRECTORY_STATE_PATH: path.join(dir, 'config', 'state.json'),
    FEISHU_DIRECTORY_MAX_AGE_MS: '60000',
  });

  const search = await handleTargets(['directory', 'search'], { config: configPath, query: '张三' });
  const selected = search.matches.find((candidate) => candidate.departmentPaths.includes('研发'));
  const bound = await handleTargets(['directory', 'bind'], {
    config: configPath,
    name: '张三',
    candidate: selected.targetFingerprint,
  });
  assert.equal(bound.status, 'bound');
  const saved = JSON.parse(fs.readFileSync(configPath, 'utf8'));
  assert.equal(saved.nameBindings['张三'].id, 'ou-two');
  assertPrivateMode(configPath, 0o600);
});
