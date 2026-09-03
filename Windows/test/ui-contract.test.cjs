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

test('formal product identity is CodexAssistant on both desktop hosts', () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
  const macInfo = fs.readFileSync(path.join(root, '..', 'Resources', 'Info.plist'), 'utf8');
  assert.equal(manifest.build.productName, 'CodexAssistant');
  assert.equal(manifest.build.appId, 'com.codexassistant.desktop');
  assert.match(macInfo, /<string>CodexAssistant<\/string>/);
  assert.match(macInfo, /<string>com\.codexassistant\.desktop<\/string>/);
  assert.match(macInfo, /<string>0\.10\.0-preview\.1<\/string>/);
});

test('Windows host keeps business reads behind the shared core contract', () => {
  assert.match(main, /dashboard\/read/);
  assert.doesNotMatch(app, /spawn\(|readFileSync|\.codex/);
  assert.match(preload, /contextBridge\.exposeInMainWorld/);
  assert.match(main, /contextIsolation: true/);
  assert.match(main, /nodeIntegration: false/);
  assert.match(main, /sandbox: true/);
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
  assert.match(app, /共享核心暂不可用/);
  assert.match(app, /KSF 路由/);
  assert.match(app, /data-action="task-detail"/);
});

test('explicit app exit waits for the shared core to stop the full server tree', () => {
  assert.match(main, /event\.preventDefault\(\)/);
  assert.match(main, /Promise\.resolve\(core\?\.close\(\)\)/);
  assert.match(main, /finally\(\(\) => app\.exit\(0\)\)/);
  assert.match(fs.readFileSync(path.join(root, 'src', 'core-client.cjs'), 'utf8'), /taskkill\.exe/);
  assert.match(app, /退出 CodexAssistant 将停止 Shared Core、飞书桥及其子进程/);
  assert.doesNotMatch(main, /installWindowsService/);
});

test('Feishu settings use the packaged service and keep secrets out of persisted settings', () => {
  assert.doesNotMatch(app, /choose-feishu|feishuBridgeRoot/);
  assert.match(app, /function renderFeishuPage/);
  assert.match(app, /创建专用飞书应用/);
  assert.match(app, /接入已有应用/);
  assert.match(app, /data-field="feishu-profile"/);
  assert.match(app, /运行组件/);
  assert.match(app, /feishuComponentStatusText/);
  assert.match(app, /value\.processRunning/);
  assert.match(preload, /feishu:setup-read/);
  assert.match(preload, /feishu:setup-begin/);
  assert.match(preload, /feishu:setup-continue/);
  assert.match(preload, /feishu:setup-verify/);
  assert.match(preload, /feishu:setup-activate/);
  assert.match(app, /确认启用并发送测试消息/);
  assert.doesNotMatch(app, /data-action="feishu-start"/);
  assert.doesNotMatch(main, /settings\.update\([^)]*appSecret/s);
});

test('KSF is an optional integration and preview version is explicit', () => {
  assert.match(app, /KSF 是可选增强能力/);
  assert.match(app, /0\.10\.0-preview\.1/);
  assert.doesNotMatch(app, /请先选择 KSF/);
});
