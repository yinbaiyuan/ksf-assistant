'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');
const crypto = require('node:crypto');

const source = fs.readFileSync(path.join(__dirname, '../src/renderer/app.js'), 'utf8');
const action = (id, enabled = true) => ({ id, title: id, enabled });
const flow = (extra = {}) => ({ id: 'flow-1', kind: 'auth', state: 'pending', expiresAt: new Date(Date.now() + 60_000).toISOString(), verificationURL: 'https://accounts.feishu.cn/authorize', qrDataURL: 'data:image/png;base64,QUJD', userCode: 'ABC', ...extra });
const snapshot = (extra = {}) => ({
  schemaVersion: 2, epoch: 'core-1', revision: 1, contextRevision: 'context-1', observedAt: '2026-09-06T10:00:00Z', refreshing: false,
  summary: { state: 'partial', title: '部分可用', detail: '用户身份未知；机器人已验证', tone: 'warning' },
  facts: [{ id: 'application', title: '应用', state: 'present', value: '已有应用' }, { id: 'user', title: '用户', state: 'unknown' }, { id: 'bot', title: '机器人', state: 'present', value: '机器人可用' }],
  diagnostics: {selfTarget:'fixture',serviceVersion:'0.11.0-preview.16',cliVersion:'1.0.93-ksfassistant.1'}, actions: [], setup: { stage: 'not_started' }, connection: { targetAliases: ['fixture'], processState: 'ready' }, issues: [], ...extra,
});

function harness(value = snapshot()) {
  const calls = [];
  const timers = new Map();
  const listeners = new Map();
  let timerId = 0;
  const fields = { app: { value: ' fixture-app ' }, secret: { value: 'fixture-secret' } };
  const context = vm.createContext({
    state: { page: 'feishu', dashboard: {}, settings: { selectedFeishuTargetAlias: 'fixture' }, feishuConfiguration: value, feishuConfigurationGeneration: 0, feishuConfigurationLoading: false, feishuRetiredEpochs: new Set(), feishuSetupMode: 'new', feishuSetupBusy: false, feishuSetupError: '', feishuSections: {}, staticDataLoaded: true },
    document: { hidden: false, addEventListener: (name, handler) => listeners.set(name, handler) },
    root: { querySelector: (selector) => selector.includes('secret') ? fields.secret : fields.app, addEventListener: (name, handler) => listeners.set(name, handler) },
    render: () => {}, header: (_title, actions = '') => actions, buttonIcon: (action, _name, label, _extra, disabled) => `<button data-action="${action}" aria-label="${label}" ${disabled ? 'disabled' : ''}></button>`, selectedPricingPlan: () => null, feishuComponentStatusText: () => '',
    crypto, window: { confirm: () => { throw new Error('Renderer must not confirm configuration'); } },
    setTimeout: (callback, delay) => { timers.set(++timerId, { callback, delay }); return timerId; },
    clearTimeout: (timer) => timers.delete(timer),
    escapeHTML: (value) => String(value ?? '').replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('"', '&quot;'),
    showToast: (message) => calls.push(['toast', message]),
    api: {
      readFeishuConfiguration: async (options) => { calls.push(['read', { ...options }]); return value; },
      actFeishuConfiguration: async (payload) => { calls.push(['action', JSON.parse(JSON.stringify(payload))]); return { outcome: 'completed', snapshot: value }; },
      settings: async () => ({}), pricingCatalog: async () => ({}),
      toolchainStatus: async () => ({ schemaVersion: 1, healthy: true, skills: [] }),
      openFeishuFlow: async (payload) => calls.push(['open', { ...payload }]),
    },
  });
  vm.runInContext(source.slice(source.indexOf('function renderSettingsPage()'), source.indexOf('function selectHomeProjects(')), context);
  vm.runInContext(source.slice(source.indexOf('async function refreshStaticData('), source.indexOf('async function refreshDashboard(')), context);
  vm.runInContext(source.slice(source.indexOf('async function handleAction('), source.indexOf('function findProject(')), context);
  const visibilityStart = source.indexOf("document.addEventListener('visibilitychange'");
  vm.runInContext(source.slice(visibilityStart, source.indexOf("window.addEventListener('keydown'", visibilityStart)), context);
  return { context, calls, timers, fields, listeners };
}

test('Core summary is authoritative with absent, incomplete, cancelled and future legacy stages', () => {
  for (const stage of [undefined, 'not_started', 'platform_pending', 'cancelled', 'future', 'ready']) {
    const value = snapshot({ summary: { state: 'ready', title: '现有配置可用', detail: 'Core 确认', tone: 'success' }, setup: stage ? { stage } : undefined });
    const { context } = harness(value);
    for (const html of [context.renderFeishuPage(), context.renderSettingsPage()]) {
      assert.match(html, /现有配置可用/);
      assert.doesNotMatch(html, /data-action="feishu-config-create_app"|feishu-app-secret|不支持的配置状态/);
    }
    assert.equal(context.feishuConfigurationPresentation(), value.summary);
  }
});

test('empty initial contextRevision is valid and every Core summary state remains display-only', async () => {
  for (const state of ['checking', 'unknown', 'not_configured', 'connected', 'configured', 'unavailable']) {
    const value = snapshot({
      contextRevision: '', refreshing: true,
      summary: { state, title: 'Core 状态：' + state, detail: '仅显示当前观测', tone: 'neutral' },
      setup: { stage: 'ready' },
      actions: [action('restart', false)],
    });
    const { context, calls } = harness(value);
    context.state.feishuConfiguration = null;
    await context.readFeishuConfiguration(false);
    assert.equal(context.state.feishuConfiguration, value);
    assert.equal(context.state.feishuSetupError, '');
    assert.equal(context.feishuConfigurationPresentation(), value.summary);
    const html = context.renderFeishuOverview();
    assert.ok(html.includes('Core 状态：' + state));
    assert.doesNotMatch(html, /配置已就绪|未配置|不支持的配置/);
    await context.performFeishuConfigurationAction('restart');
    await context.performFeishuConfigurationAction('start_auth');
    assert.deepEqual(calls, [['read', { refresh: false }]]);
    value.actions = [action('restart')];
    await context.performFeishuConfigurationAction('restart');
    assert.equal(calls[1][0], 'action');
    assert.equal(calls[1][1].contextRevision, '');
    assert.equal(calls[1][1].confirm, false);
  }
});

test('unknown is not missing; user and bot evidence and stale facts remain independent', () => {
 const {context}=harness(snapshot({facts:[{id:'authorizedUser',title:'授权用户',state:'unknown',value:'待核验'},{id:'robot',title:'飞书机器人',state:'present',value:'此前名称',stale:true}]}));
 const html=context.renderFeishuOverview(); assert.match(html,/待核验/); assert.match(html,/此前观测，待复核/); assert.doesNotMatch(html,/扫码创建/);
});

test('normal configuration has one diagnostics section and bottom logout', () => {
 const {context}=harness(snapshot({auth:{identityValid:true},actions:[action('logout'),action('start_auth',false)]}));
 const html=context.renderFeishuPage(); assert.doesNotMatch(html,/feishu-step"|feishu-config-start_auth|feishu-primary|authorization|feishu-feature/);
 assert.ok(html.indexOf('feishu-logout')>html.indexOf('data-feishu-section="diagnostics"'));
 assert.match(html,/KSFAssistant v0.11.0-preview.16 · lark-cli v1.0.93-ksfassistant.1/);
});

test('optional user authorization does not appear as a required setup action', () => {
  const { context } = harness(snapshot({
    facts: [
      { id: 'application', title: '接入应用', state: 'present', value: '已验证' },
      { id: 'robot', title: '飞书机器人', state: 'present', value: '示例 Codex' },
      { id: 'authorizedUser', title: '用户能力授权', state: 'missing', value: '按需授权（不影响消息和卡片）' },
      { id: 'taskConnection', title: '任务连接', state: 'present', value: '正常' },
    ],
    actions: [action('logout'), action('start_auth', false)],
  }));
  assert.equal(context.feishuPrimaryAction(), null);
  const html = context.renderFeishuPage();
  assert.match(html, /按需授权（不影响消息和卡片）/);
  assert.doesNotMatch(html, /feishu-config-start_auth|补充本人授权/);
});

test('one enabled next action is prioritized using facts without deriving lifecycle readiness', () => {
  const { context } = harness(snapshot({
    summary: { state: 'unknown', title: '保持 Core 未知状态', tone: 'warning' },
    facts: [{ id: 'user', title: '用户', state: 'missing' }, { id: 'operator', title: '操作人', state: 'missing' }],
    actions: [action('start_auth'), action('finish_app', false)],
  }));
  assert.equal(context.feishuPrimaryAction(), 'start_auth');
  let html = context.renderFeishuPage();
  assert.equal((html.match(/class="button primary /g) || []).length, 1);
  assert.match(html, /class="button primary feishu-primary"[^>]*feishu-config-start_auth/);
  assert.match(html, /保持 Core 未知状态/);
  context.state.feishuConfiguration.facts[0].state = 'present';
  context.state.feishuConfiguration.actions.find(action=>action.id==='start_auth').enabled=false;
  assert.equal(context.feishuPrimaryAction(), null);
  context.state.feishuConfiguration.facts[0].state = 'unknown';
  assert.equal(context.feishuPrimaryAction(), null);
  html = context.renderFeishuPage();
  assert.doesNotMatch(html, /class="button primary /);
});

test('current matching flow finish is the only primary and never leaks wrong or disabled finish', () => {
  const { context } = harness(snapshot({ flow: flow({ kind: 'user' }), actions: [action('finish_auth'), action('finish_app'), action('start_auth'), action('cancel_flow')] }));
  let html = context.renderFeishuPage();
  assert.equal(context.feishuPrimaryAction(), null);
  assert.equal((html.match(/class="button primary /g) || []).length, 0);
  assert.doesNotMatch(html, /feishu-config-finish_auth/);
  assert.doesNotMatch(html, /feishu-config-finish_app/);
  context.state.feishuConfiguration.flow.kind = 'app';
  context.state.feishuConfiguration.flow.state = 'completed';
  html = context.renderFeishuPage();
  assert.equal(context.feishuPrimaryAction(), 'finish_app');
  assert.doesNotMatch(html, /feishu-config-finish_auth|feishu-config-cancel_flow|<img/);
  context.state.feishuConfiguration.actions.find((action) => action.id === 'finish_app').enabled = false;
  assert.equal(context.feishuPrimaryAction(), null);
  assert.doesNotMatch(context.renderFeishuPage(), /feishu-config-finish_app/);
});

test('one scan entry is the only application setup action', () => {
  const { context } = harness(snapshot({ facts: [{ id: 'application', title: '应用', state: 'missing' }], actions: [action('create_app')] }));
  const html = context.renderFeishuPage();
  assert.match(html, /feishu-config-create_app/);
  assert.match(html, /连接飞书|正常流程只需扫码一次/);
  assert.doesNotMatch(html, /connect_app|接入已有应用|type="password"/);
  assert.equal((html.match(/class="button primary /g) || []).length, 1);
});

test('blocked reconnect keeps the scan guidance visible and offers controlled cleanup', () => {
  const create = { id: 'create_app', title: '扫码连接飞书', enabled: false, reason: '仍有活动飞书连接，请先完成注销清理。' };
  const cleanup = { id: 'logout', title: '清理旧连接数据', enabled: true };
  const { context } = harness(snapshot({ facts: [{ id: 'application', title: '应用', state: 'missing' }], actions: [create, cleanup] }));
  const html = context.renderFeishuPage();
  assert.match(html, /连接飞书|正常流程只需扫码一次/);
  assert.match(html, /feishu-config-create_app[^>]*disabled/);
  assert.match(html, /仍有活动飞书连接/);
  assert.equal((html.match(/feishu-config-logout/g) || []).length, 1);
  assert.match(html, /清理旧连接数据/);
});

test('fact help exposes escaped source and check time without repeating the visible value', () => {
  const { context } = harness(snapshot({ facts: [{ id: 'connection', title: '连接', state: 'present', value: '已连接', source: 'Core "观测"', checkedAt: '2026-09-06T10:00:00Z' }] }));
  const html = context.renderFeishuFacts(['connection']);
  assert.match(html, /title="来源：Core &quot;观测&quot;；状态：已确认"/);
  assert.match(html, />已连接<\/dd>/);
  assert.doesNotMatch(html, /已连接 · 已确认/);
  assert.match(html, /aria-label=/);
});

test('overview contains exactly three Core conclusions; components stay in diagnostics', () => {
 const {context}=harness(snapshot({facts:[{id:'robot',title:'飞书机器人',state:'present'},{id:'authorizedUser',title:'授权用户',state:'present'},{id:'taskConnection',title:'任务连接',state:'present'},{id:'application',title:'App ID',state:'present'},{id:'desktop',title:'桌面通道',state:'present'}]}));
 const html=context.renderFeishuOverview();assert.equal((html.match(/<dt>/g)||[]).length,3);assert.doesNotMatch(html,/App ID|桌面通道/);assert.match(context.renderFeishuDiagnostics(),/App ID/);assert.doesNotMatch(context.renderFeishuDiagnostics(),/桌面通道/);
});

test('initial and failed reads never manufacture an empty configuration or expose raw errors', async () => {
  const { context } = harness(null);
  assert.doesNotMatch(context.renderFeishuPage(), /正在更新检查结果/);
  context.api.readFeishuConfiguration = async () => { throw new Error('bearer-private /secret/config'); };
  await context.readFeishuConfiguration();
  assert.equal(context.state.feishuConfiguration, null);
  assert.match(context.renderFeishuPage(), /状态更新失败/);
  assert.doesNotMatch(context.renderFeishuPage(), /bearer-private|\/secret|feishu-app-secret/);
  context.state.feishuConfiguration = snapshot();
  await context.readFeishuConfiguration(true);
  assert.equal(context.state.feishuConfiguration.summary.title, '部分可用');
  assert.match(context.renderFeishuPage(), /状态更新失败/);
});

test('startup, entering page and recheck call only configuration read; existing setup never forces OAuth', async () => {
  const { context, calls } = harness();
  context.state.staticDataLoaded = false;
  await context.refreshStaticData({ page: 'home' });
  await context.handleAction('feishu-settings', {});
  await context.handleAction('feishu-configuration-check', {});
  assert.deepEqual(calls, [['read', { refresh: false }], ['read', { refresh: false }], ['read', { refresh: true }]]);
  assert.doesNotMatch(source, /api\.(verifyFeishuSetup|beginFeishuSetup|feishuAuthStatus|feishuOverview|sendFeishuTest)\(/);
});

test('settled background reads stay quiet and legacy setup stages are not displayed', () => {
  const { context } = harness(snapshot({ setup: { stage: 'platform_pending' } }));
  context.state.feishuConfigurationLoading = true;
  const settled = context.renderFeishuPage();
  assert.doesNotMatch(settled, /正在读取配置|platform_pending|旧配置记录|aria-busy="true"/);
  context.state.feishuConfiguration.refreshing = true;
  assert.doesNotMatch(context.renderFeishuPage(), /正在更新检查结果/);
  context.state.feishuConfiguration = null;
  assert.doesNotMatch(context.renderFeishuPage(), /正在更新检查结果/);
});

test('application polls cached snapshots while hidden and after leaving settings', async () => {
  const { context, calls, timers, listeners } = harness(snapshot({ refreshing: true }));
  await context.readFeishuConfiguration(false);
  context.document.hidden = true;
  listeners.get('visibilitychange')();
  let timer = timers.get(context.state.feishuPollTimer);
  assert.equal(timer.delay, 2500);
  await timer.callback();
  await Promise.resolve(); await Promise.resolve();
  assert.equal(calls.length, 2);
  await context.handleAction('back', {});
  assert.equal(context.state.page, 'settings');
  timer = timers.get(context.state.feishuPollTimer);
  assert.ok(timer);
  await timer.callback();
  await Promise.resolve();
  assert.equal(calls.length, 3);
  assert.ok(calls.every(([method]) => method === 'read'));
});

test('late reads and retired epochs are rejected while current hidden responses are accepted', async () => {
  const { context, listeners } = harness();
  let oldRead;
  context.api.readFeishuConfiguration = () => new Promise((resolve) => { oldRead = resolve; });
  const first = context.readFeishuConfiguration();
  context.api.readFeishuConfiguration = async () => snapshot({ epoch: 'core-2', revision: 4 });
  await context.readFeishuConfiguration();
  oldRead(snapshot({ revision: 99 }));
  await first;
  assert.equal(context.state.feishuConfiguration.epoch, 'core-2');
  assert.equal(context.applyFeishuConfiguration(snapshot({ revision: 100 }), context.state.feishuConfigurationGeneration), false);
  assert.equal(context.applyFeishuConfiguration(snapshot({ epoch: 'core-2', revision: 3 }), context.state.feishuConfigurationGeneration), false);
  let hiddenRead;
  context.api.readFeishuConfiguration = () => new Promise((resolve) => { hiddenRead = resolve; });
  const pending = context.readFeishuConfiguration();
  context.document.hidden = true;
  listeners.get('visibilitychange')();
  hiddenRead(snapshot({ epoch: 'core-3' }));
  await pending;
  assert.equal(context.state.feishuConfiguration.epoch, 'core-3');
});

test('actions carry an unconfirmed unique gesture and exact snapshot context, applying one whole result', async () => {
  const initial = snapshot({ actions: [action('start_auth'), action('test_message'), action('logout')] });
  const { context, calls } = harness(initial);
  let revision = 1;
  context.api.actFeishuConfiguration = async (payload) => {
    calls.push(['action', { ...payload }]);
    return { outcome: 'completed', snapshot: snapshot({ revision: ++revision, actions: initial.actions, summary: { state: 'ready', title: 'Core 新状态', tone: 'success' } }) };
  };
  for (const id of ['start_auth', 'test_message', 'logout']) await context.performFeishuConfigurationAction(id);
  assert.equal(calls.length, 3);
  assert.equal(new Set(calls.map((entry) => entry[1].requestId)).size, 3);
  calls.forEach((entry, index) => {
    assert.equal(entry[1].confirm, false);
    assert.equal(entry[1].epoch, 'core-1');
    assert.equal(entry[1].contextRevision, 'context-1');
    assert.equal(entry[1].revision, index + 1);
  });
  assert.equal(calls[1][1].targetAlias, 'fixture');
  assert.equal(context.state.feishuConfiguration.revision, 4);
  assert.equal(context.state.feishuConfiguration.summary.title, 'Core 新状态');
});

test('unknown action results are not retried and errors retain the last snapshot', async () => {
  const value = snapshot({ actions: [action('start_auth')] });
  const { context, calls } = harness(value);
  context.api.actFeishuConfiguration = async () => { calls.push(['attempt']); throw new Error('private credentials'); };
  await context.performFeishuConfigurationAction('start_auth');
  await context.performFeishuConfigurationAction('start_auth');
  assert.equal(calls.length, 1);
  assert.equal(context.state.feishuConfiguration, value);
  assert.match(context.renderFeishuPage(), /操作结果未知/);
  assert.doesNotMatch(context.renderFeishuPage(), /private credentials/);
  assert.equal(context.state.feishuSetupBusy, false);
});

test('duplicate actions are blocked and a newer read invalidates late mutation results', async () => {
  const { context, calls } = harness(snapshot({ actions: [action('start_auth')] }));
  let finish;
  context.api.actFeishuConfiguration = () => new Promise((resolve) => { finish = resolve; calls.push(['action']); });
  const pending = context.performFeishuConfigurationAction('start_auth');
  await context.performFeishuConfigurationAction('start_auth');
  context.api.readFeishuConfiguration = async () => snapshot({ epoch: 'core-2', revision: 3 });
  await context.readFeishuConfiguration();
  finish({ outcome: 'pending', snapshot: snapshot({ revision: 99, flow: flow() }) });
  await pending;
  assert.equal(context.state.feishuConfiguration.epoch, 'core-2');
  assert.equal(context.state.feishuSetupBusy, false);
  assert.equal(calls.length, 1);
});

test('QR, link, finish and cancel are bound to the current flow; terminals and expired QR disappear', async () => {
  const { context, calls } = harness(snapshot({ flow: flow(), actions: [action('finish_auth'), action('cancel_flow')] }));
  assert.match(context.renderFeishuPage(), /当前飞书会话二维码/);
  await context.handleAction('feishu-flow-open', { dataset: { flowId: 'old-flow' } });
  assert.equal(calls.length, 0);
  await context.handleAction('feishu-flow-open', { dataset: { flowId: 'flow-1' } });
  assert.equal(calls[0][1].flowId, 'flow-1');
  await context.performFeishuConfigurationAction('finish_auth');
  await context.performFeishuConfigurationAction('cancel_flow');
  assert.equal(calls[1][1].flowId, 'flow-1');
  assert.equal(calls[2][1].flowId, 'flow-1');
  for (const state of ['completed', 'failed', 'expired', 'cancelled']) {
    context.state.feishuConfiguration = snapshot({ flow: flow({ state }) });
    assert.doesNotMatch(context.renderFeishuPage(), /<img|feishu-flow-open|验证码 ABC/);
  }
  context.state.feishuConfiguration = snapshot({ flow: flow({ expiresAt: '2020-01-01T00:00:00Z' }) });
  assert.doesNotMatch(context.renderFeishuPage(), /<img/);
  context.state.feishuConfiguration = snapshot({ flow: flow({ id: 'flow-2', qrDataURL: undefined }) });
  assert.doesNotMatch(context.renderFeishuPage(), /QUJD/);
  context.state.feishuConfiguration = snapshot({ flow: flow({ qrDataURL: 'https://evil.example/qr.png' }) });
  assert.doesNotMatch(context.renderFeishuPage(), /<img/);
});

test('new application entry comes only from the one-scan Core affordance', async () => {
  const { context, calls } = harness(snapshot({ actions: [action('create_app')] }));
  assert.match(context.renderFeishuPage(), /feishu-config-create_app/);
  assert.doesNotMatch(context.renderFeishuPage(), /接入已有应用|type="password"|feishu-app-secret/);
  await context.performFeishuConfigurationAction('create_app');
  assert.equal(calls[0][1].action, 'create_app');
  assert.equal(calls[0][1].appId, undefined);
  assert.equal(calls[0][1].appSecret, undefined);
  context.state.feishuConfiguration = snapshot();
  assert.doesNotMatch(context.renderFeishuPage(), /feishu-app-secret/);
  context.state.feishuConfiguration = snapshot({ facts: [{ id: 'application', title: '应用', state: 'missing' }], actions: [action('create_app', false)] });
  assert.match(context.renderFeishuPage(), /feishu-config-create_app[^>]*disabled/);
  assert.doesNotMatch(context.renderFeishuPage(), /feishu-app-secret|接入已有应用/);
});

test('cached background reads do not replace focused controls while the response is pending', async () => {
  const { context } = harness();
  let renders = 0;
  let finish;
  context.render = () => { renders += 1; };
  context.api.readFeishuConfiguration = () => new Promise((resolve) => { finish = resolve; });
  const pending = context.readFeishuConfiguration(false, true);
  assert.equal(renders, 0);
  finish(snapshot());
  await pending;
  assert.equal(renders, 1);
});

test('retired feature payloads never produce renderer writes or mode controls', async () => {
 const {context,calls}=harness(snapshot({actions:[action('set_feature'),action('enable_outbound')],overview:{features:[{id:'writer',writable:true,state:'dry_run'}]}}));
 const html=context.renderFeishuPage();assert.doesNotMatch(html,/feishu-feature|高级功能|dry_run/);
 await context.performFeishuConfigurationAction('set_feature',{feature:'writer',mode:'live'});await context.performFeishuConfigurationAction('enable_outbound');assert.equal(calls.length,0);
});

test('restart follows Core action and stale target aliases cannot send', async () => {
  const { context, calls } = harness(snapshot({ actions: [action('restart'), action('test_message')] }));
  assert.match(context.renderFeishuDiagnostics(), /data-action="feishu-config-restart"/);
  await context.performFeishuConfigurationAction('restart');
  assert.equal(calls.length, 1);
  context.state.feishuConfiguration.diagnostics.selfTarget = 'removed';
  await context.performFeishuConfigurationAction('test_message');
  assert.equal(calls.length, 1);
  context.state.feishuConfiguration.connection.processState = 'degraded';
  context.state.feishuConfiguration.actions.find((action) => action.id === 'restart').enabled = false;
  await context.performFeishuConfigurationAction('restart');
  assert.equal(calls.length, 1);
});

test('native confirmation cancellation is local failure, not operation success or unknown result', async () => {
  const value = snapshot({ actions: [action('logout')] });
  const { context, calls } = harness(value);
  context.api.actFeishuConfiguration = async () => ({ outcome: 'failed', cancelled: true, snapshot: value, message: '已取消，未执行配置操作。' });
  await context.performFeishuConfigurationAction('logout');
  assert.equal(context.state.feishuSetupError, '');
  assert.equal(context.state.feishuConfiguration, value);
  assert.deepEqual(calls, [['toast', '已取消，未执行配置操作。']]);
  assert.doesNotMatch(context.renderFeishuPage(), /操作结果未知|操作未完成/);
});

test('malformed snapshot is rejected before any assignment', () => {
  const value = snapshot();
  const { context } = harness(value);
  for (const changed of [{ schemaVersion: 1 }, { facts: [null] }, { actions: [null] }, { revision: Number.MAX_SAFE_INTEGER + 1 }, { overview: { features: {} } }]) {
    assert.throws(() => context.applyFeishuConfiguration(snapshot(changed), 0));
    assert.equal(context.state.feishuConfiguration, value);
  }
});

test('native disclosures keep one expanded section and scroll/focus fallbacks remain', () => {
  const { context, listeners } = harness();
  const authorization = { dataset: { feishuSection: 'authorization' }, open: true };
  const diagnostics = { dataset: { feishuSection: 'diagnostics' }, open: true };
  let reports = 0;
  context.root.querySelectorAll = () => [authorization, diagnostics];
  context.reportHeight = () => { reports += 1; };
  const start = source.indexOf("root.addEventListener('toggle'");
  vm.runInContext(source.slice(start, source.indexOf('}, true);', start) + '}, true);'.length), context);
  listeners.get('toggle')({ target: diagnostics });
  assert.equal(authorization.open, false);
  assert.equal(context.state.feishuSections.diagnostics, true);
  assert.equal(reports, 1);
  const css = fs.readFileSync(path.join(__dirname, '../src/renderer/styles.css'), 'utf8');
  assert.match(css, /body:has\(\.feishu-page\) \{ overflow-y: auto/);
  assert.match(css, /:focus-visible/);
  assert.match(source, /event.key === 'Escape'/);
});

test('background rendering has no credential-entry state to preserve', () => {
  const { context } = harness(snapshot({ actions: [action('create_app')] }));
  context.root.dataset = {};
  context.root.contains = () => false;
  context.root.querySelectorAll = () => [];
  context.renderFeishuSurface();
  assert.doesNotMatch(context.root.innerHTML || '', /feishu-app-secret|type="password"|connect_app/);
});

test('pending OAuth stays visible through background polling and has no manual finish', async () => {
  const value = snapshot({flow: flow({kind:'user'}), actions:[action('finish_auth'), action('cancel_flow')]});
  const {context, calls} = harness(value);
  const before = context.renderFeishuPage();
  let release;
  context.api.readFeishuConfiguration = () => new Promise(resolve => { release = resolve; });
  const pending = context.readFeishuConfiguration(false, true);
  assert.equal(context.renderFeishuPage(), before);
  assert.match(before, /请用飞书补充本人授权/);
  assert.doesNotMatch(before, /feishu-config-finish_auth/);
  release(value); await pending;
  assert.equal(context.renderFeishuPage(), before);
  assert.equal(calls.length, 0);
});

test('identical page observations do not replace DOM or lose focus and scroll', () => {
  const {context} = harness(snapshot());
  context.root.dataset = {};
  context.root.contains = () => false;
  context.root.querySelectorAll = () => [];
  context.document.scrollingElement = {scrollTop:81};
  let paints = 0;
  Object.defineProperty(context.root, 'innerHTML', {set: () => { paints += 1; }});
  context.renderFeishuSurface();
  context.state.feishuConfiguration.observedAt = '2099-01-01T00:00:00Z';
  context.state.feishuConfiguration.refreshing = true;
  context.renderFeishuSurface();
  assert.equal(paints, 1);
  assert.equal(context.document.scrollingElement.scrollTop, 81);
});
