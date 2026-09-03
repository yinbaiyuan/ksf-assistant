const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { createLarkCliRunner } = require('../lib/lark-cli-runner');
const { assertPrivateMode } = require('./helpers/platform-private');
const {
  bindDirectoryName,
  collectPages,
  createDirectoryService,
  fetchVisibleDirectory,
  loadDirectoryCache,
  normalizeDirectoryName,
  resolveDirectoryName,
  searchDirectory,
} = require('../lib/contact-directory');
const { fingerprintIdentifier, writeSecureJson } = require('../lib/bridge-client-core');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-directory-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function response(items, hasMore = false, pageToken = '') {
  return { data: { items, has_more: hasMore, page_token: pageToken } };
}

test('name normalization is exact, Unicode-aware, and whitespace-stable', () => {
  assert.equal(normalizeDirectoryName('  Ａlice   Zhang  '), 'alice zhang');
  assert.equal(normalizeDirectoryName(' 张 三 '), '张 三');
  assert.equal(normalizeDirectoryName(''), '');
});

test('collectPages walks page tokens and rejects loops', async () => {
  const tokens = [];
  const items = await collectPages(async (token) => {
    tokens.push(token);
    if (!token) return response([{ id: 1 }], true, 'next');
    return response([{ id: 2 }]);
  }, ['items']);
  assert.deepEqual(items, [{ id: 1 }, { id: 2 }]);
  assert.deepEqual(tokens, ['', 'next']);
  await assert.rejects(
    collectPages(async () => response([], true, 'same'), ['items']),
    /pagination_token_invalid/,
  );
});

test('full directory fetch paginates departments and users, deduplicates memberships, and skips inactive users', async () => {
  const userCalls = [];
  const lark = {
    async larkContactListDepartments({ pageToken }) {
      if (!pageToken) {
        return response([{
          open_department_id: 'od-a',
          parent_department_id: '0',
          name: '产品',
        }], true, 'departments-2');
      }
      return response([{
        open_department_id: 'od-b',
        parent_department_id: 'od-a',
        name: '研发',
      }]);
    },
    async larkContactListUsers({ departmentId, pageToken }) {
      userCalls.push([departmentId, pageToken]);
      if (departmentId === '0') return response([]);
      if (departmentId === 'od-a' && !pageToken) {
        return response([{
          open_id: 'ou-one',
          name: '张三',
          en_name: 'San Zhang',
          department_ids: ['od-a'],
          status: { is_activated: true },
        }], true, 'users-2');
      }
      if (departmentId === 'od-a') {
        return response([{
          open_id: 'ou-left',
          name: '离职用户',
          department_ids: ['od-a'],
          status: { is_resigned: true },
        }]);
      }
      return response([{
        open_id: 'ou-one',
        name: '张三',
        department_ids: ['od-a', 'od-b'],
        status: { is_activated: true },
      }, {
        open_id: 'ou-two',
        name: '李四',
        department_ids: ['od-b'],
        status: { is_activated: true },
      }]);
    },
  };

  const result = await fetchVisibleDirectory(lark);
  assert.equal(result.departmentCount, 2);
  assert.equal(result.sourceRows, 4);
  assert.equal(result.skippedInactiveCount, 1);
  assert.deepEqual(result.users.map((user) => user.openId).sort(), ['ou-one', 'ou-two']);
  const zhang = result.users.find((user) => user.openId === 'ou-one');
  assert.deepEqual(zhang.names.sort(), ['San Zhang', '张三'].sort());
  assert.deepEqual(zhang.departmentPaths.sort(), ['产品', '产品 / 研发'].sort());
  assert.deepEqual(userCalls, [
    ['0', ''],
    ['od-a', ''],
    ['od-a', 'users-2'],
    ['od-b', ''],
  ]);
});

test('secure sync writes only the reduced cache and preserves the last cache on failure', async (t) => {
  const dir = temporaryDirectory(t);
  const cachePath = path.join(dir, 'private', 'directory.json');
  const statePath = path.join(dir, 'private', 'directory-state.json');
  let fail = false;
  const lark = {
    async larkContactListDepartments() {
      if (fail) throw new Error('permission denied for ou_secret');
      return response([]);
    },
    async larkContactListUsers() {
      return response([{
        open_id: 'ou-secret',
        name: '唯一用户',
        email: 'must-not-cache@example.com',
        mobile: '18800000000',
        status: { is_activated: true },
      }]);
    },
  };
  const service = createDirectoryService({ lark, cachePath, statePath, enabled: true });
  const synced = await service.sync('test');
  assert.equal(synced.userCount, 1);
  assertPrivateMode(path.dirname(cachePath), 0o700);
  assertPrivateMode(cachePath, 0o600);
  const raw = fs.readFileSync(cachePath, 'utf8');
  assert.equal(raw.includes('must-not-cache@example.com'), false);
  assert.equal(raw.includes('18800000000'), false);
  const before = loadDirectoryCache(cachePath);

  fail = true;
  await assert.rejects(service.sync('test-failure'), /permission denied/);
  assert.deepEqual(loadDirectoryCache(cachePath), before);
  const status = service.status();
  assert.equal(status.hasLastError, true);
  assert.equal(status.lastError.includes('ou_secret'), false);
});

test('unique exact names resolve, partial names never auto-resolve, and ambiguous names require binding', (t) => {
  const dir = temporaryDirectory(t);
  const cachePath = path.join(dir, 'directory.json');
  const cache = {
    schemaVersion: 1,
    source: 'test',
    complete: true,
    generatedAt: new Date().toISOString(),
    departmentCount: 2,
    users: [
      {
        openId: 'ou-one',
        displayName: '张三',
        names: ['张三', 'San Zhang'],
        departmentPaths: ['产品'],
        active: true,
      },
      {
        openId: 'ou-two',
        displayName: '张三',
        names: ['张三'],
        departmentPaths: ['研发'],
        active: true,
      },
      {
        openId: 'ou-three',
        displayName: '李四',
        names: ['李四'],
        departmentPaths: ['销售'],
        active: true,
      },
    ],
  };
  writeSecureJson(cachePath, cache);

  const unique = resolveDirectoryName(cache, '李四', {});
  assert.equal(unique.status, 'resolved');
  assert.equal(unique.source, 'unique_exact_name');
  assert.equal(unique.target.id, 'ou-three');
  assert.equal(resolveDirectoryName(cache, '张', {}).status, 'not_found');
  assert.equal(searchDirectory(cache, '张').length, 2);

  const ambiguous = resolveDirectoryName(cache, '张三', {});
  assert.equal(ambiguous.status, 'ambiguous');
  assert.equal(ambiguous.candidates.length, 2);
  assert.equal(JSON.stringify(ambiguous).includes('ou-one'), false);

  const bound = bindDirectoryName(cache, {}, '张三', fingerprintIdentifier('ou-two'));
  const resolved = resolveDirectoryName(cache, ' 张三 ', bound.config);
  assert.equal(resolved.status, 'resolved');
  assert.equal(resolved.source, 'binding');
  assert.equal(resolved.target.id, 'ou-two');
});

test('stale bindings fail closed instead of silently moving a name to another user', () => {
  const cache = {
    schemaVersion: 1,
    generatedAt: new Date().toISOString(),
    users: [{
      openId: 'ou-new',
      displayName: '张三',
      names: ['张三'],
      departmentPaths: [],
      active: true,
    }],
  };
  const result = resolveDirectoryName(cache, '张三', {
    nameBindings: {
      张三: { type: 'open_id', id: 'ou-old', name: '张三' },
    },
  });
  assert.equal(result.status, 'binding_stale');
  assert.equal(result.candidates.length, 1);
  assert.equal(JSON.stringify(result).includes('ou-old'), false);
});

test('directory service marks old caches stale without exposing identifiers', (t) => {
  const dir = temporaryDirectory(t);
  const cachePath = path.join(dir, 'directory.json');
  const generatedAt = new Date('2026-08-01T00:00:00Z');
  writeSecureJson(cachePath, {
    schemaVersion: 1,
    source: 'test',
    complete: true,
    generatedAt: generatedAt.toISOString(),
    departmentCount: 1,
    users: [{
      openId: 'ou-private',
      displayName: '测试用户',
      names: ['测试用户'],
      departmentPaths: ['研发'],
      active: true,
    }],
  });
  const service = createDirectoryService({
    cachePath,
    enabled: true,
    maxAgeMs: 1000,
    now: () => new Date(generatedAt.getTime() + 2000),
  });
  const status = service.status();
  assert.equal(status.stale, true);
  assert.equal(status.userCount, 1);
  assert.equal(JSON.stringify(status).includes('ou-private'), false);
});

test('lark runner restricts directory reads to bot contact GET endpoints', async (t) => {
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
  await lark.larkContactListDepartments({ departmentId: '0', pageSize: 50 });
  await lark.larkContactListUsers({ departmentId: 'od-test', pageSize: 50, pageToken: 'next' });
  const calls = fs.readFileSync(callsPath, 'utf8').trim().split('\n').map(JSON.parse);
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[0].slice(0, 5), [
    'api',
    'GET',
    '/open-apis/contact/v3/departments/0/children',
    '--as',
    'bot',
  ]);
  assert.deepEqual(calls[1].slice(0, 5), [
    'api',
    'GET',
    '/open-apis/contact/v3/users',
    '--as',
    'bot',
  ]);
  assert.match(calls[1].join(' '), /open_department_id/);
  assert.match(calls[1].join(' '), /page_token/);
  assert.doesNotMatch(calls.flat().join(' '), /POST|PATCH|PUT|DELETE/);
});
