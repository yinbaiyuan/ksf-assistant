'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { UserApprovalController, parsePoll, detailPages } = require('../src/user-approval.cjs');

const settle = () => new Promise((resolve) => setImmediate(resolve));
const request = (extra = {}) => ({
  id: 'request-1', title: '发送测试消息', user: '测试用户', application: '测试应用', action: '发送', target: '专用测试群',
  content: '测试正文', attachments: [{ name: 'fixture.txt', size: 12, sha256: 'a'.repeat(64) }], source: '来源未验证',
  expiresAt: new Date(Date.now() + 120_000).toISOString(), ...extra,
});

function fixture(t) {
  const state = { interactive: true, payload: { schemaVersion: 1, request: request() }, calls: [], dialogs: [], failure: false };
  const core = { request: async (method, params, options) => {
    state.calls.push({ method, params, options });
    if (state.failure) throw new Error('fixture disconnected');
    return method === 'userApproval/poll' ? state.payload : { schemaVersion: 1, accepted: true };
  } };
  const dialog = { showMessageBox: (options) => new Promise((resolve) => {
    const entry = { options, resolve: (response) => resolve({ response }) };
    state.dialogs.push(entry);
    options.signal.addEventListener('abort', () => resolve({ response: 0 }), { once: true });
  }) };
  const controller = new UserApprovalController({ core, dialog, isInteractive: () => state.interactive });
  controller.running = true;
  t.after(() => controller.stop());
  return { state, controller, decisions: () => state.calls.filter((call) => call.method === 'userApproval/decide') };
}

test('strict version, shape, identity, expiry and attachments fail closed', () => {
  assert.equal(parsePoll({ schemaVersion: 1, request: null }), null);
  for (const payload of [{ schemaVersion: 2, request: null }, { schemaVersion: 1 }, { schemaVersion: 1, request: null, approved: true }]) assert.throws(() => parsePoll(payload));
  for (const fields of [{ user: '' }, { content: 'bad\0content' }, { expiresAt: '2020-01-01T00:00:00Z' }, { approve: true }, { attachments: [{ name: 'test', size: -1, sha256: 'a'.repeat(64) }] }]) {
    assert.throws(() => parsePoll({ schemaVersion: 1, request: request(fields) }));
  }
});

test('complete long contents are paginated without dropping characters', () => {
  const value = request({ content: '一行 🧪 & text\n'.repeat(2500) });
  const pages = detailPages(value);
  assert.ok(pages.length > 10);
  assert.ok(pages.join('').includes(value.content));
  for (const field of [value.title, value.user, value.application, value.action, value.target, value.source, value.attachments[0].sha256]) assert.ok(pages.join('').includes(field));
});

test('polling continues with a single native dialog and default reject', async (t) => {
  const setup = fixture(t);
  await setup.controller.tick();
  await setup.controller.tick();
  assert.equal(setup.state.dialogs.length, 1);
  const options = setup.state.dialogs[0].options;
  assert.equal(options.defaultId, 0);
  assert.equal(options.cancelId, 0);
  assert.equal(options.buttons[0], '拒绝');
  assert.match(options.message, /本机受管工具请求以你的身份操作飞书/);
  assert.doesNotMatch(options.message, /Codex/);
  assert.equal(setup.decisions().length, 0);
  assert.equal(setup.state.calls.filter((call) => call.method === 'userApproval/poll').length, 2);
  setup.state.dialogs[0].resolve(0);
  await settle();
  assert.deepEqual(setup.decisions().map((call) => call.params), [{ id: 'request-1', approve: false }]);
  await setup.controller.tick();
  assert.equal(setup.state.dialogs.length, 1);
});

test('only final explicit native approve sends a single decision', async (t) => {
  const setup = fixture(t);
  setup.state.payload.request.content = '完整内容\n'.repeat(50);
  await setup.controller.tick();
  let page = 0;
  while (setup.state.dialogs[page].options.buttons[1] === '下一页') {
    assert.equal(setup.decisions().length, 0);
    setup.state.dialogs[page].resolve(1);
    await settle();
    page += 1;
  }
  assert.equal(setup.state.dialogs[page].options.buttons[1], '批准本次操作');
  setup.state.dialogs[page].resolve(1);
  await settle();
  assert.deepEqual(setup.decisions().map((call) => call.params), [{ id: 'request-1', approve: true }]);
  await setup.controller.tick();
  assert.equal(setup.state.dialogs.length, page + 1);
});

for (const cause of ['locked', 'disconnected', 'cancelled', 'changed', 'stopped', 'expired']) {
  test(`invalidating an active dialog (${cause}) never submits even a synthetic cancel`, async (t) => {
    const setup = fixture(t);
    await setup.controller.tick();
    if (cause === 'locked') { setup.state.interactive = false; await setup.controller.unavailable(); }
    if (cause === 'disconnected') setup.state.failure = true;
    if (cause === 'cancelled') setup.state.payload = { schemaVersion: 1, request: null };
    if (cause === 'changed') setup.state.payload.request.content = 'different content';
    if (cause === 'expired') setup.state.payload.request.expiresAt = '2020-01-01T00:00:00Z';
    if (cause === 'stopped') await setup.controller.stop(); else await setup.controller.tick();
    await settle();
    assert.equal(setup.state.dialogs[0].options.signal.aborted, true);
    assert.equal(setup.decisions().length, 0);
  });
}

test('locked host sends false heartbeat and does not present', async (t) => {
  const setup = fixture(t);
  setup.state.interactive = false;
  await setup.controller.tick();
  assert.equal(setup.state.dialogs.length, 0);
  assert.equal(setup.state.calls[0].params.interactive, false);
  assert.equal(setup.state.calls[0].options.skipStart, true);
});

test('unavailable system interactivity probe fails closed and keeps polling', async (t) => {
  const setup = fixture(t);
  setup.controller.isInteractive = () => { throw new Error('probe unavailable'); };
  await setup.controller.tick();
  await setup.controller.tick();
  assert.equal(setup.state.dialogs.length, 0);
  assert.deepEqual(setup.state.calls.map((call) => call.params.interactive), [false, false]);
  assert.equal(setup.controller.polling, false);
});

test('Go nanosecond expiry parses without losing the whole timestamp', () => {
  const value = request({ expiresAt: '2026-09-06T12:00:00.123456789+08:00', attachments: [] });
  assert.equal(parsePoll({ schemaVersion: 1, request: value }, Date.parse('2026-09-06T03:59:00Z')).expiresAt, value.expiresAt);
});

test('late poll response after loss of interactivity cannot resurrect a dialog', async (t) => {
  const setup = fixture(t);
  let resolvePoll;
  setup.controller.core = { request: (method, params) => params.interactive ? new Promise((resolve) => { resolvePoll = resolve; }) : Promise.resolve({ schemaVersion: 1, request: null }) };
  const polling = setup.controller.tick();
  await setup.controller.unavailable();
  resolvePoll(setup.state.payload);
  await polling;
  assert.equal(setup.state.dialogs.length, 0);
});

test('renderer and preload expose no approval decision API', () => {
  const main = fs.readFileSync(path.join(__dirname, '../src/main.cjs'), 'utf8');
  const preload = fs.readFileSync(path.join(__dirname, '../src/preload.cjs'), 'utf8');
  const handlers = new Map();
  const context = vm.createContext({ ipcMain: { handle: (name, handler) => handlers.set(name, handler), on: () => {} }, readDashboard: () => {}, core: {} });
  vm.runInContext(main.slice(main.indexOf('function registerIPC()'), main.indexOf('function allowedFeishuURL(')), context);
  context.registerIPC();
  assert.equal([...handlers.keys()].some((key) => /approval/i.test(key)), false);
  assert.doesNotMatch(preload, /userApproval|approval:decide|core\.request/);
  assert.match(main, /userApproval\.start\(\)/);
  assert.match(main, /userApproval\?\.stop\(\)/);
});


test('optional compact preview validates without changing complete native review', () => {
  const preview = { content: '修改正文', confirmLabel: '更新', destructive: true };
  const value = parsePoll({ schemaVersion: 1, request: request({ preview }) });
  assert.deepEqual(value.preview, preview);
  assert.ok(detailPages(value).join('').includes(value.content));
  for (const bad of [null, { ...preview, destructive: 1 }, { ...preview, confirmLabel: '更\n新' }, { ...preview, approved: true }]) {
    assert.throws(() => parsePoll({ schemaVersion: 1, request: request({ preview: bad }) }));
  }
});
