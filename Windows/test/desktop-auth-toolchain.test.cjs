'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const renderer = fs.readFileSync(path.join(__dirname, '../src/renderer/app.js'), 'utf8');
const main = fs.readFileSync(path.join(__dirname, '../src/main.cjs'), 'utf8');
const preload = fs.readFileSync(path.join(__dirname, '../src/preload.cjs'), 'utf8');

test('Agent middleware is absent from host and renderer', () => {
  assert.doesNotMatch(renderer, /renderToolchainSettings|updateToolchain|Codex 飞书技能/);
  assert.doesNotMatch(main, /toolchain:install|toolchain:status|UserApprovalController/);
  assert.doesNotMatch(preload, /installToolchain|toolchainStatus/);
});

test('legacy auth APIs are replaced by the authoritative lifecycle snapshot and desktop confirmation channel', () => {
  assert.doesNotMatch(renderer, /updateFeishuAuthorization|api\.feishuAuthStatus|api\.startFeishuAuth|api\.logoutFeishuAuth/);
  assert.match(preload, /readFeishuConfiguration/);
  assert.match(preload, /actFeishuConfiguration/);
  assert.doesNotMatch(preload, /feishu:auth-start|feishu:auth-finish|feishu:auth-logout/);
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
