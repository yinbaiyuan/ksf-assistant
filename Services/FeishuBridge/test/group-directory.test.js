const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { createLarkCliRunner } = require('../lib/lark-cli-runner');
const { assertPrivateMode } = require('./helpers/platform-private');
const {
  DEFAULT_GROUP_MAX_AGE_MS,
  bindGroupName,
  createGroupDirectoryService,
  fetchVisibleGroups,
  groupDirectoryEventRequiresRefresh,
  groupDirectoryStatus,
  loadGroupDirectoryCache,
  normalizeGroupName,
  resolveGroupName,
  searchGroups,
} = require('../lib/group-directory');
const { fingerprintIdentifier, writeSecureJson } = require('../lib/bridge-client-core');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-group-directory-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function response(chats, hasMore = false, pageToken = '') {
  return { data: { items: chats, has_more: hasMore, page_token: pageToken } };
}

function cache(groups) {
  return {
    schemaVersion: 1,
    source: 'test',
    complete: true,
    generatedAt: new Date().toISOString(),
    groups,
  };
}

function readStateReason(statePath) {
  return JSON.parse(fs.readFileSync(statePath, 'utf8')).reason;
}

test('group name normalization is exact, Unicode-aware, and whitespace-stable', () => {
  assert.equal(normalizeGroupName('  Ｃodex   测试群  '), 'codex 测试群');
  assert.equal(normalizeGroupName('研发 群'), '研发 群');
  assert.equal(normalizeGroupName(''), '');
});

test('group directory freshness uses a two-hour safety window and only reviewed chat events invalidate it', (t) => {
  const dir = temporaryDirectory(t);
  const cachePath = path.join(dir, 'private', 'group-directory.json');
  const now = Date.now();
  fs.mkdirSync(path.dirname(cachePath), { recursive: true });
  writeSecureJson(cachePath, {
    ...cache([]),
    generatedAt: new Date(now - (90 * 60 * 1000)).toISOString(),
  });
  assert.equal(DEFAULT_GROUP_MAX_AGE_MS, 2 * 60 * 60 * 1000);
  assert.equal(groupDirectoryStatus(cachePath, { enabled: true, now }).stale, false);
  assert.equal(groupDirectoryStatus(cachePath, {
    enabled: true,
    now: now + (31 * 60 * 1000),
  }).stale, true);

  for (const eventKey of [
    'im.chat.disbanded_v1',
    'im.chat.member.bot.added_v1',
    'im.chat.member.bot.deleted_v1',
    'im.chat.updated_v1',
  ]) assert.equal(groupDirectoryEventRequiresRefresh(eventKey), true);
  assert.equal(groupDirectoryEventRequiresRefresh('im.chat.member.user.added_v1'), false);
  assert.equal(groupDirectoryEventRequiresRefresh('im.message.receive_v1'), false);
});

test('group directory lifecycle syncs on startup, debounces event refresh, and cancels pending work on stop', async (t) => {
  const dir = temporaryDirectory(t);
  const cachePath = path.join(dir, 'private', 'group-directory.json');
  const statePath = path.join(dir, 'private', 'group-directory-state.json');
  let calls = 0;
  const lark = {
    async larkImListChats() {
      calls += 1;
      return response([{
        chat_id: 'oc-lifecycle',
        name: '生命周期群',
        chat_status: 'normal',
      }]);
    },
  };
  const service = createGroupDirectoryService({
    lark,
    cachePath,
    statePath,
    enabled: true,
    refreshMs: 60 * 1000,
  });
  t.after(() => service.stop());

  let resolveSecond;
  const secondRefresh = new Promise((resolve) => { resolveSecond = resolve; });
  const refreshes = [];
  const firstRefresh = new Promise((resolve, reject) => {
    assert.equal(service.start((error, result) => {
      if (error) {
        reject(error);
        return;
      }
      refreshes.push(result);
      if (refreshes.length === 1) resolve();
      if (refreshes.length === 2) resolveSecond();
    }), true);
  });
  assert.equal(service.start(), false);
  await firstRefresh;
  assert.equal(calls, 1);
  assert.equal(readStateReason(statePath), 'startup');

  assert.equal(service.scheduleRefresh('event:im.chat.updated_v1', 5), true);
  assert.equal(service.scheduleRefresh('event:im.chat.updated_v1', 5), false);
  await Promise.race([
    secondRefresh,
    new Promise((_, reject) => setTimeout(() => reject(new Error('event refresh timed out')), 500)),
  ]);
  assert.equal(calls, 2);
  assert.equal(readStateReason(statePath), 'event:im.chat.updated_v1');

  assert.equal(service.scheduleRefresh('event:im.chat.disbanded_v1', 40), true);
  service.stop();
  await new Promise((resolve) => setTimeout(resolve, 60));
  assert.equal(calls, 2);
});

test('production bridge owns the group directory lifecycle and routes reviewed chat events to refresh', () => {
  const source = fs.readFileSync(path.join(__dirname, '..', 'bot-bridge.js'), 'utf8');
  assert.match(source, /groupDirectoryService\.start\(/);
  assert.match(source, /groupDirectoryService\.stop\(\)/);
  assert.match(source, /groupDirectoryEventRequiresRefresh\(eventKey\)/);
  assert.match(source, /groupDirectoryService\.scheduleRefresh\(`event:\$\{eventKey\}`\)/);
});

test('visible group fetch paginates, deduplicates chat IDs, and keeps only reduced send metadata', async () => {
  const calls = [];
  const lark = {
    async larkImListChats({ pageToken, pageSize }) {
      calls.push({ pageToken, pageSize });
      if (!pageToken) {
        return response([{
          chat_id: 'oc-one',
          name: 'codex测试群',
          description: '测试用途',
          chat_mode: 'group',
          chat_status: 'normal',
          external: false,
          avatar: 'must-not-cache',
          owner_id: 'ou-must-not-cache',
          tenant_key: 'tenant-must-not-cache',
        }], true, 'next');
      }
      return response([{
        chat_id: 'oc-one',
        name: 'codex测试群',
        description: '测试用途',
        chat_mode: 'group',
        chat_status: 'normal',
        external: false,
      }, {
        chat_id: 'oc-two',
        name: '项目群',
        description: '',
        chat_mode: 'group',
        chat_status: 'normal',
        external: true,
      }, {
        chat_id: '',
        name: '无效群',
      }]);
    },
  };

  const result = await fetchVisibleGroups(lark, { pageSize: 100 });
  assert.equal(result.sourceRows, 4);
  assert.equal(result.groups.length, 2);
  assert.deepEqual(result.groups.map((group) => group.chatId).sort(), ['oc-one', 'oc-two']);
  assert.deepEqual(calls, [
    { pageToken: '', pageSize: 100 },
    { pageToken: 'next', pageSize: 100 },
  ]);
  const serialized = JSON.stringify(result);
  assert.equal(serialized.includes('must-not-cache'), false);
});

test('secure group sync preserves the last cache on permission failure and redacts identifiers', async (t) => {
  const dir = temporaryDirectory(t);
  const cachePath = path.join(dir, 'private', 'group-directory.json');
  const statePath = path.join(dir, 'private', 'group-directory-state.json');
  let fail = false;
  const lark = {
    async larkImListChats() {
      if (fail) throw new Error('permission denied for oc_secret');
      return response([{
        chat_id: 'oc-private',
        name: '唯一群',
        description: '安全缓存',
        chat_mode: 'group',
        chat_status: 'normal',
        external: false,
        avatar: 'must-not-cache',
        owner_id: 'ou-must-not-cache',
      }]);
    },
  };
  const service = createGroupDirectoryService({ lark, cachePath, statePath, enabled: true });
  const synced = await service.sync('test');
  assert.equal(synced.groupCount, 1);
  assertPrivateMode(path.dirname(cachePath), 0o700);
  assertPrivateMode(cachePath, 0o600);
  const raw = fs.readFileSync(cachePath, 'utf8');
  assert.equal(raw.includes('must-not-cache'), false);
  const before = loadGroupDirectoryCache(cachePath);

  fail = true;
  await assert.rejects(service.sync('test-failure'), /permission denied/);
  assert.deepEqual(loadGroupDirectoryCache(cachePath), before);
  const status = service.status();
  assert.equal(status.hasLastError, true);
  assert.equal(status.lastError.includes('oc_secret'), false);
});

test('unique exact group names resolve, partial names never auto-resolve, and duplicate names require binding', () => {
  const groups = [
    { chatId: 'oc-one', displayName: '项目群', description: '产品', external: false, active: true },
    { chatId: 'oc-two', displayName: '项目群', description: '研发', external: false, active: true },
    { chatId: 'oc-three', displayName: 'codex测试群', description: '', external: false, active: true },
  ];
  const directory = cache(groups);

  const unique = resolveGroupName(directory, 'codex测试群', {});
  assert.equal(unique.status, 'resolved');
  assert.equal(unique.source, 'unique_exact_group_name');
  assert.deepEqual(unique.target, { type: 'chat_id', id: 'oc-three' });
  assert.equal(resolveGroupName(directory, 'codex', {}).status, 'not_found');
  assert.equal(searchGroups(directory, '项目').length, 2);

  const ambiguous = resolveGroupName(directory, '项目群', {});
  assert.equal(ambiguous.status, 'ambiguous');
  assert.equal(ambiguous.candidates.length, 2);
  assert.equal(JSON.stringify(ambiguous).includes('oc-one'), false);

  const bound = bindGroupName(directory, {}, '项目群', fingerprintIdentifier('oc-two'));
  const resolved = resolveGroupName(directory, ' 项目群 ', bound.config);
  assert.equal(resolved.status, 'resolved');
  assert.equal(resolved.source, 'binding');
  assert.equal(resolved.target.id, 'oc-two');
});

test('stale group bindings fail closed instead of migrating to another same-name group', () => {
  const result = resolveGroupName(cache([{
    chatId: 'oc-new',
    displayName: '项目群',
    description: '新群',
    external: false,
    active: true,
  }]), '项目群', {
    groupNameBindings: {
      项目群: { type: 'chat_id', id: 'oc-old', name: '项目群' },
    },
  });
  assert.equal(result.status, 'binding_stale');
  assert.equal(result.candidates.length, 1);
  assert.equal(JSON.stringify(result).includes('oc-old'), false);
});

test('lark runner restricts group directory reads to bot GET chat-list endpoint', async (t) => {
  const dir = temporaryDirectory(t);
  const bin = path.join(dir, 'fake-lark.js');
  const callsPath = path.join(dir, 'calls.jsonl');
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
fs.appendFileSync(process.env.FAKE_LARK_CALLS, JSON.stringify(args) + '\\n');
process.stdout.write(JSON.stringify({ data: { items: [], has_more: false } }));
`);
  fs.chmodSync(bin, 0o700);
  const lark = createLarkCliRunner({
    bin,
    cwd: dir,
    env: { ...process.env, FAKE_LARK_CALLS: callsPath },
    as: 'bot',
  });
  await lark.larkImListChats({ pageSize: 100, pageToken: 'next' });
  const calls = fs.readFileSync(callsPath, 'utf8').trim().split('\n').map(JSON.parse);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0].slice(0, 5), [
    'api',
    'GET',
    '/open-apis/im/v1/chats',
    '--as',
    'bot',
  ]);
  assert.match(calls[0].join(' '), /page_size/);
  assert.match(calls[0].join(' '), /page_token/);
  assert.doesNotMatch(calls[0].join(' '), /POST|PATCH|PUT|DELETE/);
});
