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
const macPopover = fs.readFileSync(path.join(root, '..', 'Sources', 'KSFAssistant', 'UsagePopoverView.swift'), 'utf8');

test('connected completed tasks retain unpinned home containers until disconnected', () => {
  const vm = require('node:vm');
  const selectors = app.slice(app.indexOf('function selectHomeProjects('), app.indexOf('function headlineRemaining('));
  const context = {};
  vm.runInNewContext(selectors, context);
  const item = { isPinned: false, runningCount: 0, waitingCount: 0, tasks: [{ taskKey: 'linked', classification: 'completed' }] };
  for (const select of [context.selectHomeProjects, context.selectHomeWorkspaces]) {
    assert.equal(select([item], [{ taskKey: 'linked', linkState: 'active' }]).length, 1);
    for (const linkState of ['released', 'expired', 'pending']) {
      assert.equal(select([item], [{ taskKey: 'linked', linkState }]).length, 0);
    }
    assert.equal(select([item], []).length, 0);
    assert.equal(select([{ ...item, isPinned: true }], []).length, 1);
  }
});

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
  assert.match(periodicRefresh, /api\.dashboard\(forceAccountRefresh\)/);
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
  const resizeHandler = main.slice(main.indexOf("ipcMain.on('window:resize'"), main.indexOf("ipcMain.on('window:hide'"));
  assert.match(app, /item\.isPinned \|\| item\.tasks\.some/);
  assert.match(app, /api\.resize\(Math\.max\(320, root\.scrollHeight\)\)/);
  assert.doesNotMatch(app, /root\.scrollHeight\s*\+\s*1/);
  assert.match(resizeHandler, /const contentBounds = window\.getContentBounds\(\)/);
  assert.match(main, /const HEIGHT_ROUNDING_TOLERANCE = 2;/);
  assert.match(resizeHandler, /Math\.abs\(contentBounds\.height - height\) <= HEIGHT_ROUNDING_TOLERANCE/);
  assert.match(resizeHandler, /window\.setContentBounds\(\{ \.\.\.contentBounds, height \}, false\)/);
  assert.match(resizeHandler, /if \(wasVisible\) positionWindow\(\)/);
  assert.doesNotMatch(resizeHandler, /setContentSize|showWindow\(/);
  assert.doesNotMatch(css, /\.project-stack[^}]*overflow-y\s*:\s*(auto|scroll)/s);
  assert.match(main, /isLoadingMainFrame\(\)/);
});

test('Windows tray supplies native small-icon representations for every supported DPI', () => {
  assert.match(main, /\{ size: 16, scaleFactor: 1 \}/);
  assert.match(main, /\{ size: 20, scaleFactor: 1\.25 \}/);
  assert.match(main, /\{ size: 24, scaleFactor: 1\.5 \}/);
  assert.match(main, /\{ size: 32, scaleFactor: 2 \}/);
  assert.match(main, /nativeImage\.createEmpty\(\)/);
  assert.match(main, /icon\.addRepresentation\(/);
  assert.doesNotMatch(main, /TRAY_ICON_SIZE|createFromDataURL\(trayIconDataURL\(status\)\)\.resize/);
});

test('Windows panel mirrors the macOS popover frame and compact visual rhythm', () => {
  assert.match(macPopover, /\.padding\(12\)\s*\n\s*\.frame\(width: 336\)/);
  assert.match(main, /const APP_WIDTH = 336;/);
  assert.match(main, /useContentSize: true/);
  assert.match(css, /#app\s*\{[^}]*padding:\s*12px/s);
  assert.match(css, /--radius:\s*10px/);
  assert.match(css, /\.icon-button\s*\{[^}]*width:\s*22px;[^}]*height:\s*22px/s);
  assert.match(css, /\.quota-card\s*\{[^}]*padding:\s*9px/s);
  assert.match(css, /\.project-stack\s*\{[^}]*gap:\s*5px/s);
  assert.match(css, /\.project-card\s*\{[^}]*padding:\s*8px/s);
});

test('Windows home hierarchy follows the macOS quota, token and section structure', () => {
  const home = app.slice(app.indexOf('function renderHome()'), app.indexOf('function renderHistoryPage()'));
  const headerSource = app.slice(app.indexOf('function header('), app.indexOf('function render()'));
  assert.match(home, /通用额度剩余/);
  assert.doesNotMatch(home, /plan-badge|quota-updated/);
  assert.doesNotMatch(home, /账号数据可能延迟/);
  assert.match(home, /class="section-divider"/);
  assert.match(app, /header\('飞书配置', buttonIcon\('feishu-configuration-check', 'refresh'/);
  assert.doesNotMatch(headerSource, /\$\{buttonIcon\('refresh'/);
});

test('Windows home settings uses the native Fluent glyph at compact size', () => {
  assert.match(app, /if \(name === 'settings'\) return `<span class="fluent-icon"/);
  assert.match(app, /&#xE713;/);
  assert.match(css, /\.fluent-icon\s*\{[^}]*font-family:\s*"Segoe Fluent Icons", "Segoe MDL2 Assets"/s);
  assert.match(css, /\.fluent-icon\s*\{[^}]*font-size:\s*14px/s);
});

test('Windows action icons preserve the macOS symbol semantics', () => {
  assert.match(macPopover, /systemName: "plus\.bubble"/);
  assert.match(app, /buttonIcon\(`create-task:\$\{item\.id\}`, 'addBubble', '新建任务'\)/);
  assert.match(app, /play: '<path fill="currentColor"/);
  assert.match(app, /sendOff:/);
  assert.match(app, /icon\(linkReleasable \? 'sendOff' : 'send'\)/);
  assert.doesNotMatch(app, /^\s+(?:add|open|stop|close):/m);
});

test('Windows task cards retain the macOS compact status and action alignment', () => {
  const task = app.slice(app.indexOf('function renderTask('), app.indexOf('function renderWorkspaceCard('));
  assert.match(task, /status-chip \$\{escapeHTML\(task\.classification\)\}/);
  assert.match(css, /\.status-chip\s*\{[^}]*font-size:\s*9px/s);
  assert.match(css, /\.task-actions \.icon-button\s*\{[^}]*width:\s*20px;[^}]*height:\s*20px/s);
  assert.match(css, /\.project-footer\s*\{[^}]*padding-top:\s*6px/s);
});

test('Windows workspace rows and Feishu surfaces mirror the macOS hierarchy', () => {
  const task = app.slice(app.indexOf('function renderTask('), app.indexOf('function renderWorkspaceCard('));
  assert.match(task, /const showsKSFRoute = !workspace/);
  assert.match(task, /showsKSFRoute \? `<span class="task-detail">/);
  assert.match(task, /renderTaskRouteSummary\(task\)/);
  assert.match(app, /route\?\.jobs\?\.find\(\(job\) => job\.role === 'main'\)/);
  assert.match(app, /route\?\.abilities\?\.map\(\(ability\) => ability\.name\)/);
  assert.match(app, /route\?\.dispatchableSkills\?\.length/);
  assert.match(app, /class="card feishu-overview"/);
  assert.match(app, /class="card feishu-diagnostics"/);
  assert.match(css, /\.feishu-page\s*\{[^}]*gap:\s*12px/s);
  assert.match(css, /\.feishu-overview\s*\{[^}]*padding:\s*12px/s);
  assert.match(css, /\.feishu-diagnostics\s*\{[^}]*padding:\s*12px/s);
});

test('Windows settings reproduce the macOS grouped card and native control rhythm', () => {
  const settings = app.slice(app.indexOf('function renderSettingsPage()'), app.indexOf('function feishuConfigurationPresentation'));
  assert.match(settings, /KSF 知识库（可选）/);
  assert.match(settings, /class="setting setting-link"[^>]*data-action="pricing"/);
  assert.match(settings, /class="setting setting-link"[^>]*data-action="feishu-settings"/);
  assert.match(settings, /class="settings-exit"/);
  assert.match(settings, /title="退出 KSFAssistant 将停止核心服务、飞书服务及其子进程。"/);
  assert.doesNotMatch(settings, /class="setting-title">运行组件/);
  assert.doesNotMatch(settings, /class="setting-title">版本/);
  assert.match(css, /\.settings-list\s*\{[^}]*padding:\s*0 10px/s);
  assert.match(css, /\.setting\s*\{[^}]*padding:\s*7px 0/s);
  assert.match(css, /\.switch\s*\{[^}]*width:\s*32px;[^}]*height:\s*18px/s);
});

test('ordinary Codex workspaces are independent, conditional and KSF-free', () => {
  assert.match(app, /function selectHomeWorkspaces/);
  assert.match(app, /workspaceLibrary\.length \? `<div class="section-divider"><\/div><div class="section-header"><h2 class="section-title">Codex 工作区/);
  assert.match(app, /workspaces\.map\(\(item\) => renderWorkspaceCard\(item\)\)/);
  assert.match(app, /return items\.filter\(\(item\) => item\.isPinned \|\| item\.runningCount > 0 \|\| item\.waitingCount > 0 \|\| item\.tasks\.some/);
  assert.match(app, /pinButton\('workspace-pin', item\.id, item\.isPinned, '工作区'\)/);
  assert.match(preload, /setWorkspacePinned/);
  assert.match(main, /workspace:set-pinned/);
  assert.match(app, /item\.usage\?\.cumulativeTokens/);
  assert.match(app, /workspace-create-task:/);
  assert.match(app, /workspace-open-folder:/);
  assert.match(preload, /createWorkspaceTask/);
  assert.match(preload, /openWorkspacePath/);
  assert.match(main, /workspace:task-create/);
  assert.match(main, /workspace:path-open/);
  assert.match(main, /authorizedWorkspacePaths/);
  assert.match(app, /showsKSFRoute \? `<span class="task-detail">\$\{renderTaskRouteSummary\(task\)\}/);
  assert.match(app, /showsKSFRoute \? `<div class="section-header"><h2 class="section-title">KSF 路由/);
  assert.match(app, /state\.dashboard\?\.workspaces\?\.workspaces/);
});

test('project and workspace pin controls visibly distinguish their pressed state', () => {
  assert.match(macPopover, /item\.isPinned \? "pin\.fill" : "pin"/);
  assert.match(app, /function pinButton\(action, id, isPinned, targetLabel\)/);
  assert.match(app, /class="icon-button pin-button \$\{pinned \? 'pinned' : ''\}"/);
  assert.match(app, /aria-pressed="\$\{pinned\}"/);
  assert.match(app, /pinButton\('workspace-pin', item\.id, item\.isPinned, '工作区'\)/);
  assert.ok((app.match(/pinButton\('pin',/g) || []).length >= 2);
  assert.match(css, /\.pin-button\.pinned svg\s*\{[^}]*fill:\s*currentColor/s);
  assert.match(css, /\.pin-button:not\(\.pinned\) svg\s*\{[^}]*fill:\s*none/s);
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

test('pending Feishu task links expose a safe release path', () => {
  assert.match(app, /function isTaskLinkReleasable\(link\)/);
  assert.match(app, /link\?\.linkState === 'pending'/);
  assert.match(app, /为避免重复发送，系统不会自动重试/);
});

test('explicit app exit waits for the KSFAssistant Core to stop the full server tree', () => {
  assert.match(main, /event\.preventDefault\(\)/);
  assert.doesNotMatch(main, /userApproval/);
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
  assert.match(app, /create_app/);
  assert.doesNotMatch(app, /接入已有应用|feishu-app-secret|connect_app/);
  assert.doesNotMatch(app, /本机事件|主设备|仅手动能力|feishuProfileText|data-field="feishu-profile"/);
  assert.doesNotMatch(preload, /setFeishuProfile|feishu:profile-set/);
  assert.doesNotMatch(main, /feishu:profile-set|feishu\/profile\/set/);
  assert.match(app, /renderFeishuFacts\(\['robot', 'authorizedUser', 'taskConnection'\]\)/);
  assert.match(app, /renderFeishuVersions/);


  assert.match(preload, /feishu:configuration-read/);
  assert.match(preload, /feishu:configuration-action/);
  assert.doesNotMatch(preload, /feishu:setup-|feishu:auth-/);
  assert.doesNotMatch(app, /确认启用并发送测试消息/);
  assert.doesNotMatch(app, /data-action="feishu-start"/);
  assert.doesNotMatch(main, /settings\.update\([^)]*appSecret/s);
});

test('Feishu configuration exposes diagnostics without duplicate product features', () => {
 assert.match(app,/missingApplicationScopes/);assert.match(app,/missingUserScopes/);assert.match(app,/诊断详情/);assert.doesNotMatch(app,/ · lark-cli /);assert.doesNotMatch(app,/data-field="feishu-feature"|高级功能|测试目标/);
 assert.match(preload,/readFeishuConfiguration/);assert.match(preload,/actFeishuConfiguration/);assert.match(main,/feishu\/configuration\/result/);
});

test('KSF is an optional integration and preview version is explicit', () => {
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
  assert.match(app, /KSF 是可选增强能力/);
  assert.match(manifest.version, /-preview\./);
  assert.match(app, /KSFAssistant /);
  assert.doesNotMatch(app, /请先选择 KSF/);
});

test('history bars apply factual heights without CSP-blocked inline style attributes', () => {
  const historyPage = app.slice(
    app.indexOf('function renderHistoryPage()'),
    app.indexOf('function renderPricingPage()')
  );
  assert.doesNotMatch(historyPage, /style="height:/);
  assert.match(historyPage, /data-height="\$\{serverHeight\}"/);
  assert.match(historyPage, /data-height="\$\{localHeight\}"/);
  assert.match(app, /function applyHistoryBarHeights\(\)/);
  assert.match(app, /bar\.style\.height = `\$\{height\}%`/);
  assert.match(app, /if \(state\.page === 'history'\) applyHistoryBarHeights\(\)/);
});

test('connected KSF library can be cancelled and selected again', () => {
  assert.match(app, /settings\.ksfRoot \? 'clear-ksf' : 'choose-ksf'/);
  assert.match(app, /settings\.ksfRoot \? '取消' : '选择目录'/);
  assert.doesNotMatch(app, /settings\.ksfRoot \? '更换' : '选择目录'/);
  assert.match(app, /api\.clearDirectory\('ksfRoot'\)/);
  assert.match(preload, /clearDirectory/);
  assert.match(main, /directory:clear/);
  assert.match(main, /persistKSFRoot\(''\)/);
});
