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
