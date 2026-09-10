'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const { pathToFileURL } = require('node:url');

const main = fs.readFileSync(path.join(__dirname, '../src/main.cjs'), 'utf8');
const preload = fs.readFileSync(path.join(__dirname, '../src/preload.cjs'), 'utf8');
const request = (action = 'logout', extra = {}) => ({ action, requestId: 'gesture-1', epoch: 'core-1', revision: 8, contextRevision: 'context-1', confirm: false, ...extra });

function harness() {
  const handlers = new Map();
  const calls = [];
  const dialogs = [];
  const values = {
    response: 1, visible: true,
    snapshot: { schemaVersion: 2, epoch: 'core-1', revision: 8, contextRevision: 'context-1',
      actions: ['create_app', 'start_auth', 'finish_auth', 'finish_app', 'cancel_flow', 'logout', 'test_message', 'restart']
        .map((id) => ({ id, title: 'Core ' + id, enabled: true, confirmation: 'Core 原生确认文案' })),
      diagnostics: {selfTarget:'fixture'}, flow: { id: 'flow-1', state: 'pending', verificationURL: 'https://accounts.feishu.cn/authorize', expiresAt: new Date(Date.now() + 60_000).toISOString() },
      connection: { targetAliases: ['fixture'], processState: 'degraded' },
      overview: { features: [{ id: 'reader', title: '只读', writable: false }, { id: 'writer', title: '写入', writable: true }] },
    },
  };
  const webContents = { mainFrame: { url: pathToFileURL(path.join(__dirname, '../src/renderer/index.html')).href } };
  const event = { sender: webContents, senderFrame: webContents.mainFrame };
  const context = vm.createContext({
    ipcMain: { handle: (name, handler) => handlers.set(name, handler), on: () => {} },
    readDashboard: () => {}, window: { webContents, isDestroyed: () => false, isVisible: () => values.visible },
    approvalUnavailableReasons: new Set(), userApproval: null, quitting: false, URL, path, pathToFileURL, __dirname: path.join(__dirname, '../src'),
    core: { request: async (method, params, options) => {
      calls.push({ method, params: JSON.parse(JSON.stringify(params)), options });
      return method === 'feishu/configuration/read' ? values.snapshot : { outcome: 'completed', snapshot: values.snapshot };
    } },
    shell: { openExternal: async (url) => calls.push({ method: 'open', url }) },
    dialog: { showMessageBox: async (_window, options) => { dialogs.push(options); return { response: values.response }; } },
  });
  vm.runInContext(main.slice(main.indexOf('function registerIPC()'), main.indexOf('function allowedLocalPath(')), context);
  context.registerIPC();
  return { context, handlers, calls, dialogs, values, event, act: (payload = request()) => handlers.get('feishu:configuration-action')(event, payload) };
}

test('configuration writes are main-confirmed desktop actions with Core confirmation copy', async () => {
  const setup = harness();
  for (const action of ['start_auth', 'test_message', 'logout']) {
    await setup.act(request(action, action === 'test_message' ? { targetAlias: 'fixture' } : {}));
  }
  assert.equal(setup.dialogs.length, 3);
  const mutations = setup.calls.filter((call) => call.method === 'feishu/configuration/action');
  assert.equal(mutations.length, 3);
  assert.deepEqual(mutations.map((call) => call.params.action), ['start_auth', 'test_message', 'logout']);
  for (const options of setup.dialogs) {
    assert.equal(options.defaultId, 0);
    assert.equal(options.cancelId, 0);
    assert.match(options.message, /^Core /);
    assert.match(options.detail, /Core 原生确认文案/);
  }
  for (const call of mutations) {
    assert.equal(call.params.confirm, true);
    assert.equal(call.params.revision, 8);
    assert.equal(call.params.contextRevision, 'context-1');
    assert.equal(call.options.timeoutMs, 125_000);
  }
  assert.equal(setup.calls.some((call) => /userApproval\/decide/.test(call.method)), false);
});

test('renderer confirm true is not authorization; native cancel makes zero mutations', async () => {
  const setup = harness();
  setup.values.response = 0;
  const result = await setup.act(request('logout', { confirm: true }));
  assert.equal(setup.dialogs.length, 1);
  assert.equal(result.outcome, 'failed');
  assert.equal(result.cancelled, true);
  assert.equal(result.snapshot, setup.values.snapshot);
  assert.match(result.message, /未执行/);
  assert.deepEqual(setup.calls.map((call) => call.method), ['feishu/configuration/read']);
});

test('foreign renderer, subframes, hidden and unavailable desktop fail closed', async () => {
  const setup = harness();
  const handler = setup.handlers.get('feishu:configuration-action');
  await assert.rejects(handler({ ...setup.event, sender: {} }, request()));
  await assert.rejects(handler({ ...setup.event, senderFrame: {} }, request()));
  const originalURL = setup.event.senderFrame.url;
  setup.event.senderFrame.url = 'https://untrusted.example/';
  await assert.rejects(setup.act());
  setup.event.senderFrame.url = originalURL;
  setup.values.visible = false;
  await assert.rejects(setup.act());
  setup.values.visible = true;
  setup.context.approvalUnavailableReasons.add('lock-screen');
  await assert.rejects(setup.act());
  setup.context.approvalUnavailableReasons.clear();
  setup.context.userApproval = { active: {} };
  await assert.rejects(setup.act());
  assert.equal(setup.calls.length, 0);
  assert.equal(setup.dialogs.length, 0);
});

test('renderer cannot forward scopes, arbitrary methods or generic approval capabilities', async () => {
  const setup = harness();
  for (const extra of [{ action: 'userApproval/decide' }, { scope: 'all' }, { profile: 'other' }, { deviceCode: 'private' }, { approve: true }, { confirmation: 'renderer copy' }, { revision: -1 }, { targetAlias: 'unexpected' }, { appSecret: 'unexpected' }]) {
    await assert.rejects(setup.act(request('start_auth', extra)));
  }
  assert.equal(setup.calls.length, 0);
  assert.equal([...setup.handlers.keys()].some((name) => /approval|setup-|auth-|feature-update|supervisor-restart|^feishu:test$/.test(name)), false);
  assert.doesNotMatch(preload, /userApproval|approval:decide|core\.request|startFeishuAuth|activateFeishuSetup/);
});

test('epoch, context, future revision and current action affordance remain strict', async () => {
  for (const changed of [{ epoch: 'new-core' }, { revision: 7 }, { revision: undefined }, { revision: -1 }, { contextRevision: 'new-context' }, { schemaVersion: 1 }, { actions: [] }]) {
    const setup = harness();
    Object.assign(setup.values.snapshot, changed);
    await assert.rejects(setup.act(), /配置已变化/);
    assert.equal(setup.dialogs.length, 0);
    assert.deepEqual(setup.calls.map((call) => call.method), ['feishu/configuration/read']);
  }
});

test('display-only revision advances do not reject actions or silently rebase the request', async () => {
  const setup = harness();
  setup.values.snapshot.revision = 9;
  setup.values.snapshot.connection.processState = 'running';
  setup.values.snapshot.connection.inboundConnection = false;
  await setup.act(request('restart'));
  assert.equal(setup.dialogs.length, 1);
  assert.equal(setup.calls.at(-1).params.revision, 8);
  assert.equal(setup.calls.at(-1).params.epoch, 'core-1');
  assert.equal(setup.calls.at(-1).params.contextRevision, 'context-1');
  assert.equal(setup.calls.at(-1).params.confirm, true);
});

test('older display revision never bypasses changed flow or a newly disabled action', async () => {
  const setup = harness();
  setup.values.snapshot.revision = 10;
  setup.values.snapshot.flow.id = 'flow-2';
  await assert.rejects(setup.act(request('finish_auth', { flowId: 'flow-1' })), /会话已变化/);
  setup.values.snapshot.actions.find((action) => action.id === 'restart').enabled = false;
  await assert.rejects(setup.act(request('restart')), /操作不可用/);
  assert.equal(setup.dialogs.length, 0);
  assert.equal(setup.calls.some((call) => call.method === 'feishu/configuration/action'), false);
});

test('unknown identity context does not block Core-authorized recovery or flow cancellation', async () => {
  const setup = harness();
  setup.values.snapshot.contextRevision = '';
  await setup.act(request('restart', { contextRevision: '' }));
  await setup.act(request('cancel_flow', { contextRevision: '', flowId: 'flow-1' }));
  assert.equal(setup.calls.filter((call) => call.method === 'feishu/configuration/action').length, 2);
});

test('only one native configuration dialog is active and post-confirm lock prevents execution', async () => {
  const setup = harness();
  let finish;
  setup.context.dialog.showMessageBox = () => new Promise((resolve) => { finish = resolve; });
  const first = setup.act();
  await Promise.resolve();
  await assert.rejects(setup.act(), /当前对话框/);
  setup.context.approvalUnavailableReasons.add('lock-screen');
  finish({ response: 1 });
  await assert.rejects(first, /不可交互/);
  assert.equal(setup.calls.some((call) => call.method === 'feishu/configuration/action'), false);
  assert.equal(setup.context.approvalUnavailableReasons.has('feishu-configuration'), false);
});

test('flow actions, feature modes and target aliases stay scoped', async () => {
  const setup = harness();
  for (const payload of [
    request('cancel_flow', { flowId: 'old-flow' }),
    request('finish_auth'),
    request('test_message', { targetAlias: 'removed' }),
    request('set_feature', { feature: 'reader', mode: 'live' }),
    request('set_feature', { feature: 'unknown', mode: 'off' }),
  ]) await assert.rejects(setup.act(payload));
  assert.equal(setup.dialogs.length, 0);
  await setup.act(request('cancel_flow', { flowId: 'flow-1' }));
  await assert.rejects(setup.act(request('set_feature', { feature: 'writer', mode: 'live' })));
  assert.equal(setup.dialogs[0].detail, 'Core 原生确认文案');
  assert.equal(setup.dialogs.length,1);
});

test('retired credential entry is refused before secrets reach a dialog or Core', async () => {
  const setup = harness();
  await assert.rejects(setup.act(request('connect_app', { appId: 'cli_fixture_app', appSecret: 'fixture-private-secret' })));
  assert.doesNotMatch(JSON.stringify(setup.dialogs), /fixture-private-secret/);
  assert.equal(setup.dialogs.length, 0);
  assert.equal(setup.calls.length, 0);
});

test('retired feature operations are explicitly refused without a dialog or write', async () => {
 const setup=harness();
 for (const mode of ['off','dry_run','live','enabled']) await assert.rejects(setup.act(request('set_feature',{feature:'writer',mode})));
 await assert.rejects(setup.act(request('enable_outbound')));
 assert.equal(setup.dialogs.length,0); assert.equal(setup.calls.length,0);
});

test('restart follows Core enabled even when process is running and only connection is lost', async () => {
  const setup = harness();
  setup.values.snapshot.connection = { processState: 'running', inboundConnection: false };
  await setup.act(request('restart'));
  assert.equal(setup.dialogs.length, 1);
  assert.equal(setup.calls.at(-1).params.action, 'restart');
  setup.values.snapshot.connection.processState = 'degraded';
  setup.values.snapshot.actions.find((action) => action.id === 'restart').enabled = false;
  await assert.rejects(setup.act(request('restart')), /操作不可用/);
  assert.equal(setup.dialogs.length, 1);
});

test('finish checks without Core confirmation bypass the dialog without manufacturing confirm true', async () => {
  for (const id of ['finish_auth', 'finish_app']) {
    for (const confirmation of [undefined, '', ' ']) {
      const setup = harness();
      setup.values.snapshot.actions.find((action) => action.id === id).confirmation = confirmation;
      await setup.act(request(id, { flowId: 'flow-1', confirm: true }));
      assert.equal(setup.dialogs.length, 0);
      assert.equal(setup.calls.at(-1).params.confirm, false);
      assert.equal(setup.calls.at(-1).params.flowId, 'flow-1');
    }
    const setup = harness();
    await setup.act(request(id, { flowId: 'flow-1' }));
    assert.equal(setup.dialogs.length, 1);
    assert.equal(setup.calls.at(-1).params.confirm, true);
  }
});

test('mutations with missing or invalid Core confirmation fail closed instead of inventing copy', async () => {
  for (const confirmation of ['', undefined, true]) {
    const setup = harness();
    setup.values.snapshot.actions.find((action) => action.id === 'logout').confirmation = confirmation;
    await assert.rejects(setup.act(), /确认文案/);
    assert.equal(setup.dialogs.length, 0);
    assert.equal(setup.calls.some((call) => call.method === 'feishu/configuration/action'), false);
  }
});

test('test send confirmation displays exact fixed body, bot identity, target and limited evidence', async () => {
  const setup = harness();
  await setup.act(request('test_message', { targetAlias: 'fixture' }));
  const detail = setup.dialogs[0].detail;
  assert.match(detail, /Core 原生确认文案/);
  assert.match(detail, /目标别名：fixture/);
  assert.match(detail, /发送身份：机器人（bot）/);
  assert.match(detail, /消息正文：【KSFAssistant 接入验收】[^\n]+\n/);
  assert.match(detail, /只证明消息发送，不证明用户授权、消息接收或全部功能可用/);
});

test('unknown result or RPC failure is never replayed and does not leave the dialog locked', async () => {
  const setup = harness();
  const original = setup.context.core.request;
  setup.context.core.request = async (method, params, options) => {
    if (method === 'feishu/configuration/action') { setup.calls.push({ method }); throw new Error('unknown outcome'); }
    return original(method, params, options);
  };
  const queried = await setup.act();
  assert.equal(queried.outcome,'completed');
  assert.equal(setup.calls.filter(call => call.method === 'feishu/configuration/result').length,1);
  assert.equal(setup.calls.filter((call) => call.method === 'feishu/configuration/action').length, 1);
  assert.equal(setup.context.approvalUnavailableReasons.size, 0);
});

test('flow URL is resolved by main from matching active snapshot, never from renderer input', async () => {
  const setup = harness();
  const open = (extra = {}) => setup.handlers.get('feishu:flow-open')(setup.event, { flowId: 'flow-1', epoch: 'core-1', contextRevision: 'context-1', url: 'https://evil.example', ...extra });
  await open();
  assert.equal(setup.calls.at(-1).url, 'https://accounts.feishu.cn/authorize');
  for (const extra of [{ flowId: 'old' }, { epoch: 'old' }, { contextRevision: 'old' }]) await assert.rejects(open(extra));
  for (const state of ['completed', 'cancelled', 'expired', 'failed']) {
    setup.values.snapshot.flow.state = state;
    await assert.rejects(open());
  }
  setup.values.snapshot.flow.state = 'pending';
  setup.values.snapshot.flow.expiresAt = '2020-01-01T00:00:00Z';
  await assert.rejects(open());
  assert.equal(setup.calls.filter((call) => call.method === 'open').length, 1);
});

test('configuration read never invokes legacy verification or any mutating endpoint', async () => {
  const setup = harness();
  await setup.handlers.get('feishu:configuration-read')(setup.event, { refresh: false });
  await setup.handlers.get('feishu:configuration-read')(setup.event, { refresh: true });
  assert.deepEqual(setup.calls.map((call) => [call.method, call.params.refresh]), [['feishu/configuration/read', false], ['feishu/configuration/read', true]]);
  assert.equal(setup.dialogs.length, 0);
});

test('preload exposes only narrow lifecycle entry points', () => {
  let api;
  const calls = [];
  vm.runInNewContext(preload, { require: () => ({
    contextBridge: { exposeInMainWorld: (_name, value) => { api = value; } },
    ipcRenderer: { invoke: (...args) => calls.push(args), send: () => {} },
  }) });
  api.readFeishuConfiguration();
  api.actFeishuConfiguration(request());
  api.openFeishuFlow({ flowId: 'flow-1' });
  assert.deepEqual(calls.map((call) => call[0]), ['feishu:configuration-read', 'feishu:configuration-action', 'feishu:flow-open']);
  assert.equal(calls[1][1].confirm, false);
  assert.equal(api.request, undefined);
  assert.equal(api.verifyFeishuSetup, undefined);
});


test('configuration observation continues on other pages and while hidden, without writes', () => {
  const source = fs.readFileSync(path.join(__dirname, '../src/renderer/app.js'), 'utf8');
  const calls = []; let scheduled;
  const state = { page: 'settings', feishuSetupBusy: false, feishuConfigurationLoading: false };
  const context = vm.createContext({ state, document: { hidden: true }, clearTimeout: () => {},
    setTimeout: (callback) => { scheduled = callback; return 1; },
    readFeishuConfiguration: (...args) => calls.push(args) });
  vm.runInContext(source.slice(source.indexOf('function scheduleFeishuConfigurationPoll()'), source.indexOf('function selectHomeProjects(')), context);
  context.scheduleFeishuConfigurationPoll();
  assert.equal(typeof scheduled, 'function'); scheduled();
  assert.deepEqual(calls, [[false, true]]);
  scheduled = null; state.feishuSetupBusy = true;
  context.scheduleFeishuConfigurationPoll();
  assert.equal(scheduled, null);
});
