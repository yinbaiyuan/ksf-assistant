'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const renderer = fs.readFileSync(path.join(__dirname, '../src/renderer/app.js'), 'utf8');
const main = fs.readFileSync(path.join(__dirname, '../src/main.cjs'), 'utf8');
const preload = fs.readFileSync(path.join(__dirname, '../src/preload.cjs'), 'utf8');

function toolchainHarness(confirm = true) {
  const calls = [];
  const context = vm.createContext({
    state: { toolchain: null, toolchainBusy: false, toolchainError: '' },
    window: { confirm: () => confirm },
    render: () => {},
    escapeHTML: (value) => String(value).replaceAll('<', '&lt;'),
    api: {
      toolchainStatus: async () => { calls.push('status'); return { schemaVersion: 1, healthy: true, version: '1.0.93', skills: [] }; },
      installToolchain: async (confirmed) => { calls.push(['install', confirmed]); return { schemaVersion: 1, healthy: true, version: '1.0.93', skills: [] }; },
    },
  });
  vm.runInContext(renderer.slice(renderer.indexOf('function renderToolchainSettings()'), renderer.indexOf('function renderFeishuActivation(')), context);
  return { context, calls };
}

test('toolchain status is read-only; install requires explicit confirmation', async () => {
  const { context, calls } = toolchainHarness();
  await context.updateToolchain();
  assert.deepEqual(calls, ['status']);
  await context.updateToolchain(true);
  assert.deepEqual(calls, ['status', ['install', true]]);
  const canceled = toolchainHarness(false);
  await canceled.context.updateToolchain(true);
  assert.deepEqual(canceled.calls, []);
});

test('toolchain handles pending, unavailable and incompatible states without raw errors', async () => {
  const { context, calls } = toolchainHarness();
  context.state.toolchainBusy = true;
  await context.updateToolchain(true);
  assert.deepEqual(calls, []);
  assert.match(context.renderToolchainSettings(), /disabled/);
  context.state.toolchainBusy = false;
  context.api.toolchainStatus = async () => { throw new Error('/secret/config bearer-private'); };
  await context.updateToolchain();
  assert.equal(context.state.toolchainBusy, false);
  assert.match(context.renderToolchainSettings(), /无法检查/);
  assert.doesNotMatch(context.renderToolchainSettings(), /secret|bearer/);
  context.api.toolchainStatus = async () => ({ schemaVersion: 2, healthy: true });
  await context.updateToolchain();
  assert.equal(context.state.toolchain, null);
});

test('host toolchain IPC admits only confirmed pinned installation', async () => {
  const handlers = new Map();
  const calls = [];
  const context = vm.createContext({
    ipcMain: { handle: (name, handler) => handlers.set(name, handler), on: () => {} },
    readDashboard: () => {},
    core: { request: async (...args) => { calls.push(args); return {}; } },
  });
  vm.runInContext(main.slice(main.indexOf('function registerIPC()'), main.indexOf('function allowedFeishuURL(')), context);
  context.registerIPC();
  await assert.rejects(handlers.get('toolchain:install')({}, false), /确认/);
  assert.equal(calls.length, 0);
  await handlers.get('toolchain:status')({});
  await handlers.get('toolchain:install')({}, true);
  assert.equal(calls[0][0], 'toolchain/status');
  assert.equal(calls[1][0], 'toolchain/install');
  assert.equal(calls[1][1].confirm, true);
});

test('preload exposes narrow toolchain calls, not process or generic RPC access', async () => {
  let api;
  const calls = [];
  vm.runInNewContext(preload, { require: () => ({
    contextBridge: { exposeInMainWorld: (_name, value) => { api = value; } },
    ipcRenderer: { invoke: (...args) => { calls.push(args); }, send: () => {} },
  }) });
  api.toolchainStatus();
  api.installToolchain(true);
  assert.deepEqual(calls, [['toolchain:status'], ['toolchain:install', true]]);
  assert.equal(api.spawn, undefined);
  assert.equal(api.request, undefined);
});

function authHarness(confirm = true) {
  const calls = [];
  const pending = { schemaVersion: 1, status: 'pending', identity: 'user', profile: 'default', profileValid: true, userCode: 'ABC-123', verificationUrl: 'https://accounts.feishu.cn/authorize' };
  const context = vm.createContext({
    state: { feishuAuth: null, feishuAuthBusy: false, feishuAuthError: '' },
    window: { confirm: () => confirm }, render: () => {},
    escapeHTML: (value) => String(value).replaceAll('<', '&lt;'),
    api: {
      feishuAuthStatus: async () => { calls.push('status'); return { ...pending, status: 'unauthorized' }; },
      startFeishuAuth: async () => { calls.push('start'); return pending; },
      finishFeishuAuth: async () => { calls.push('finish'); return pending; },
      logoutFeishuAuth: async (confirmed) => { calls.push(['logout', confirmed]); return { ...pending, status: 'unauthorized' }; },
      feishuOverview: async () => ({}), dashboard: async () => ({}),
    },
  });
  vm.runInContext(renderer.slice(renderer.indexOf('function renderFeishuAuthorization()'), renderer.indexOf('function selectHomeProjects(')), context);
  return { context, calls };
}

test('authorization exposes pending instructions and never claims a pending finish succeeded', async () => {
  const { context, calls } = authHarness();
  await context.updateFeishuAuthorization('start');
  assert.match(context.renderFeishuAuthorization(), /ABC-123/);
  assert.match(context.renderFeishuAuthorization(), /我已授权，检查/);
  await context.updateFeishuAuthorization('finish');
  assert.equal(context.state.feishuAuth.status, 'pending');
  assert.deepEqual(calls, ['start', 'finish']);
  context.state.feishuAuth = { schemaVersion: 1, status: 'authorized', identity: 'user', profile: 'default', identityValid: true, profileValid: true, missingCapabilities: ['应用功能权限'] };
  assert.match(context.renderFeishuAuthorization(), /补充授权/);
  assert.match(context.renderFeishuAuthorization(), /应用功能权限/);
  assert.match(context.renderFeishuAuthorization(), /不代表所有功能权限齐备/);
});

test('logout is explicit; auth errors clear busy and allow reauthorization without raw errors', async () => {
  const canceled = authHarness(false);
  await canceled.context.updateFeishuAuthorization('logout');
  assert.deepEqual(canceled.calls, []);
  const { context, calls } = authHarness();
  await context.updateFeishuAuthorization('logout');
  assert.deepEqual(calls, [['logout', true]]);
  context.api.finishFeishuAuth = async () => { throw new Error('access_token=private /config'); };
  await context.updateFeishuAuthorization('finish');
  assert.equal(context.state.feishuAuthBusy, false);
  assert.equal(context.state.feishuAuth, null);
  assert.match(context.renderFeishuAuthorization(), /重新授权/);
  assert.doesNotMatch(context.renderFeishuAuthorization(), /private|access_token|\/config/);
});

test('host auth RPC never forwards raw scopes, profiles or device codes from renderer', async () => {
  const handlers = new Map();
  const calls = [];
  const context = vm.createContext({
    ipcMain: { handle: (name, handler) => handlers.set(name, handler), on: () => {} },
    readDashboard: () => {}, core: { request: async (...args) => { calls.push(args); return {}; } },
  });
  vm.runInContext(main.slice(main.indexOf('function registerIPC()'), main.indexOf('function allowedFeishuURL(')), context);
  context.registerIPC();
  await handlers.get('feishu:auth-start')({}, { scope: 'all', profile: 'other' });
  await handlers.get('feishu:auth-finish')({}, { deviceCode: 'private' });
  await assert.rejects(handlers.get('feishu:auth-logout')({}, false), /确认/);
  assert.equal(calls[0][0], 'feishu/auth/start');
  assert.equal(calls[0][1].scope, 'required');
  assert.equal(calls[0][1].profile, undefined);
  assert.equal(calls[1][0], 'feishu/auth/finish');
  assert.equal(calls[1].length, 1);
});

test('task runtime labels keep Agent reports, freshness and verified source separate', () => {
  const context = vm.createContext({ escapeHTML: (value) => String(value).replaceAll('<', '&lt;') });
  vm.runInContext(renderer.slice(renderer.indexOf('function taskReportLabel('), renderer.indexOf('function renderProjectsPage(')), context);
  assert.equal(context.taskReportLabel(null), '暂无可用的 Agent 上报');
  const runtime = { reportedStatus: 'completed', reportFreshness: 'stale', routeFreshness: 'current', observedStatus: 'running', progress: { summary: '<reported>', percent: 100 } };
  assert.match(context.renderTaskRuntimeDetails(runtime), /Agent 上报已过期/);
  assert.match(context.renderTaskRuntimeDetails(runtime), /不替代实际任务状态/);
  assert.match(context.renderTaskRuntimeDetails(runtime), /&lt;reported>/);
  assert.equal(runtime.observedStatus, 'running');
  assert.match(context.taskRouteSourceLabel(runtime), /来源已验证/);
  assert.match(context.taskRouteSourceLabel({ ...runtime, routeFreshness: 'stale' }), /不作为当前路由/);
  assert.match(context.taskRouteSourceLabel(null, true), /时效未知/);
  assert.doesNotMatch(renderer.slice(renderer.indexOf('function taskStateIcon('), renderer.indexOf('async function refreshStaticData(')), /reportedStatus/);
});

test('only setup activation, verification and continuation receive extended host budget', async () => {
  const source = fs.readFileSync(path.join(__dirname, '../src/core-client.cjs'), 'utf8');
  const timeouts = [];
  const module = { exports: {} };
  vm.runInNewContext(source, {
    require, module, process, Buffer,
    setTimeout: (_callback, milliseconds) => { timeouts.push(milliseconds); return 1; }, clearTimeout: () => {},
  });
  const client = new module.exports.CoreClient({ executablePath: '/fake/core' });
  client.process = { stdin: { writable: true, write: (_data, callback) => callback(new Error('fake write rejected')) } };
  for (const method of ['feishu/setup/activate', 'feishu/setup/verify', 'feishu/setup/continue', 'dashboard/read', 'feishu/auth/status', 'toolchain/install']) {
    await assert.rejects(client.request(method, {}, { skipStart: true }), /fake write rejected/);
  }
  assert.deepEqual(timeouts, [125_000, 125_000, 125_000, 45_000, 45_000, 45_000]);
});
