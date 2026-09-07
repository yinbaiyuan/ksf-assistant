'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const root = path.resolve(__dirname, '..');
const app = fs.readFileSync(path.join(root, 'src', 'renderer', 'app.js'), 'utf8');
const css = fs.readFileSync(path.join(root, 'src', 'renderer', 'styles.css'), 'utf8');
const preload = fs.readFileSync(path.join(root, 'src', 'preload.cjs'), 'utf8');
const main = fs.readFileSync(path.join(root, 'src', 'main.cjs'), 'utf8');
const coreClient = fs.readFileSync(path.join(root, 'src', 'core-client.cjs'), 'utf8');

test('formal product identity is KSFAssistant on both desktop hosts', () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
  const macInfo = fs.readFileSync(path.join(root, '..', 'Resources', 'Info.plist'), 'utf8');
  assert.equal(manifest.build.productName, 'KSFAssistant');
  assert.equal(manifest.build.appId, 'com.ksfassistant.desktop');
  assert.match(macInfo, /<string>KSFAssistant<\/string>/);
  assert.match(macInfo, /<string>com\.ksfassistant\.desktop<\/string>/);
  const releaseVersion = macInfo.match(/<key>KSFAssistantReleaseVersion<\/key>\s*<string>([^<]+)<\/string>/);
  assert.equal(releaseVersion?.[1], manifest.version);
});

test('Windows host keeps business reads behind the KSFAssistant Core contract', () => {
  assert.match(main, /dashboard\/read/);
  assert.doesNotMatch(app, /spawn\(|readFileSync|\.codex/);
  assert.match(preload, /contextBridge\.exposeInMainWorld/);
  assert.match(main, /contextIsolation: true/);
  assert.match(main, /nodeIntegration: false/);
  assert.match(main, /sandbox: true/);
});

test('periodic refresh only reads the dashboard while static settings load on demand', () => {
  const periodicRefresh = app.slice(
    app.indexOf('async function refreshDashboard('),
    app.indexOf('function scheduleRefresh()')
  );
  assert.match(periodicRefresh, /api\.dashboard\(\)/);
  assert.doesNotMatch(periodicRefresh, /api\.settings\(\)|api\.pricingCatalog\(\)|api\.readFeishuSetup\(\)|api\.feishuOverview\(\)/);
  assert.match(app, /async function refreshStaticData\(/);
  assert.match(app, /await refreshStaticData\(\{ page: state\.page \}\)/);
});

test('Windows host publishes KSF context at startup and after in-app directory changes', () => {
  assert.match(coreClient, /integrations: this\.integrations/);
  assert.match(main, /integrations: \{ ksfRoot: store\.get\(\)\.ksfRoot \}/);
  assert.ok((main.match(/integration\/context\/update/g) || []).length >= 2);
});

test('project workset and dynamic panel height remain explicit', () => {
  assert.match(app, /item\.isPinned \|\| item\.tasks\.some/);
  assert.match(app, /root\.scrollHeight/);
  assert.doesNotMatch(css, /\.project-stack[^}]*overflow-y\s*:\s*(auto|scroll)/s);
  assert.match(main, /isLoadingMainFrame\(\)/);
});

test('Token hierarchy matches the six-cell shared product contract', () => {
  const historyPage = app.slice(
    app.indexOf('function renderHistoryPage()'),
    app.indexOf('function renderPricingPage()')
  );
  for (const label of ['模型普通输入', '模型缓存输入', '模型输出', '账号最新', '本机昨日', '本机今日']) {
    assert.match(app, new RegExp(label));
  }
  assert.match(css, /grid-template-columns:\s*repeat\(3/);
  assert.match(main, /token\/history\/compare/);
  assert.match(app, /最近 30 个自然日/);
  assert.match(app, /data-action="history-day"/);
  assert.match(app, /服务器当日/);
  assert.match(app, /本机占比/);
  assert.match(app, /history-server/);
  assert.match(app, /history-local/);
  assert.match(app, /30 日 API 估算/);
  assert.match(app, /管理价格方案/);
  assert.doesNotMatch(historyPage, /管理价格方案/);
  assert.match(app, /不代表实际账单/);
  assert.match(main, /pricing\/catalog\/read/);
  assert.match(main, /pricingSelection/);
});

test('interactive states and reduced motion are present', () => {
  assert.match(css, /:focus-visible/);
  assert.match(css, /prefers-reduced-motion/);
  assert.match(css, /button:disabled/);
  assert.match(app, /核心服务暂不可用/);
  assert.match(app, /KSF 路由/);
  assert.match(app, /data-action="task-detail"/);
});

test('explicit app exit waits for the KSFAssistant Core to stop the full server tree', () => {
  assert.match(main, /event\.preventDefault\(\)/);
  assert.match(main, /Promise\.resolve\(userApproval\?\.stop\(\)\)/);
  assert.match(main, /then\(\(\) => core\?\.close\(\)\)/);
  assert.match(main, /finally\(\(\) => app\.exit\(0\)\)/);
  assert.match(fs.readFileSync(path.join(root, 'src', 'core-client.cjs'), 'utf8'), /taskkill\.exe/);
  assert.match(app, /退出 KSFAssistant 将停止核心服务、飞书服务及其子进程/);
  assert.doesNotMatch(main, /installWindowsService/);
});

test('Feishu settings use the packaged service and keep secrets out of persisted settings', () => {
  assert.doesNotMatch(app, /choose-feishu|feishuBridgeRoot/);
  assert.match(app, /function renderFeishuPage/);
  assert.match(main, /action\.confirmation/);
  assert.match(app, /接入已有应用/);
  assert.doesNotMatch(app, /本机事件|主设备|仅手动能力|feishuProfileText|data-field="feishu-profile"/);
  assert.doesNotMatch(preload, /setFeishuProfile|feishu:profile-set/);
  assert.doesNotMatch(main, /feishu:profile-set|feishu\/profile\/set/);
  assert.match(app, /renderFeishuFacts\(\['robot', 'authorizedUser', 'taskConnection'\]\)/);
  assert.match(app, /运行组件/);


  assert.match(preload, /feishu:configuration-read/);
  assert.match(preload, /feishu:configuration-action/);
  assert.doesNotMatch(preload, /feishu:setup-|feishu:auth-/);
  assert.doesNotMatch(app, /确认启用并发送测试消息/);
  assert.doesNotMatch(app, /data-action="feishu-start"/);
  assert.doesNotMatch(main, /settings\.update\([^)]*appSecret/s);
});

test('Feishu configuration exposes diagnostics without duplicate product features', () => {
 assert.match(app,/missingApplicationScopes/);assert.match(app,/missingUserScopes/);assert.match(app,/诊断详情/);assert.match(app,/lark-cli/);assert.doesNotMatch(app,/data-field="feishu-feature"|高级功能|测试目标/);
 assert.match(preload,/readFeishuConfiguration/);assert.match(preload,/actFeishuConfiguration/);assert.match(main,/feishu\/configuration\/result/);
});

test('KSF is an optional integration and preview version is explicit', () => {
  assert.match(app, /KSF 是可选增强能力/);
  assert.match(app, /0\.11\.0-preview\.1/);
  assert.doesNotMatch(app, /请先选择 KSF/);
});
