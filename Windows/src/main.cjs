'use strict';

const { startActivityMonitor } = require('./activity-monitor.cjs');

const { app, BrowserWindow, Tray, Menu, dialog, ipcMain, nativeImage, screen, shell, powerMonitor, powerSaveBlocker } = require('electron');
const { SleepInhibitor } = require('./sleep-inhibitor.cjs');
const sleepInhibitor = new SleepInhibitor(powerSaveBlocker);
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { pathToFileURL } = require('node:url');
const { CoreClient } = require('./core-client.cjs');
const { ConfigStore } = require('./config-store.cjs');
const { migrateLegacySettings: migrateSettings } = require('./identity-migration.cjs');
const { taskURL, clamp, isPathInside } = require('./security.cjs');
const { buildTrayStatus, trayIconDataURL } = require('./tray-status.cjs');

const WINDOWS_TRAY_REPRESENTATIONS = [
  { size: 16, scaleFactor: 1 },
  { size: 20, scaleFactor: 1.25 },
  { size: 24, scaleFactor: 1.5 },
  { size: 32, scaleFactor: 2 },
];

const APP_WIDTH = 336;
const HEIGHT_ROUNDING_TOLERANCE = 2;
let window = null;
let tray = null;
let core = null;
let store = null;
let quitting = false;
let shutdownStarted = false;
let dashboardPromise = null;
let authorizedWorkspacePaths = new Map();
const approvalUnavailableReasons = new Set();

app.setAppUserModelId('com.ksfassistant.desktop');
app.setName('KSFAssistant');
app.setPath('userData', path.join(app.getPath('appData'), 'KSFAssistant'));
const hasSingleInstanceLock = app.requestSingleInstanceLock();
if (!hasSingleInstanceLock) app.exit(0);

function coreExecutablePath() {
  if (app.isPackaged) {
    const arch = process.arch === 'arm64' ? 'windows-arm64' : 'windows-x64';
    return path.join(process.resourcesPath, 'core', arch, 'ksf-assistant-core.exe');
  }
  const repoRoot = path.resolve(__dirname, '..', '..');
  if (process.platform === 'win32') {
    const arch = process.arch === 'arm64' ? 'windows-arm64' : 'windows-x64';
    return path.join(repoRoot, 'dist', 'core', arch, 'ksf-assistant-core.exe');
  }
  return path.join(repoRoot, 'dist', 'core', `darwin-${process.arch}`, 'ksf-assistant-core');
}

function createWindow() {
  window = new BrowserWindow({
    width: APP_WIDTH,
    height: 760,
    useContentSize: true,
    minWidth: APP_WIDTH,
    maxWidth: APP_WIDTH,
    minHeight: 320,
    show: false,
    frame: false,
    resizable: false,
    skipTaskbar: true,
    maximizable: false,
    minimizable: false,
    fullscreenable: false,
    backgroundColor: '#eef1f4',
    webPreferences: {
      preload: path.join(__dirname, 'preload.cjs'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      devTools: !app.isPackaged,
    },
  });
  window.loadFile(path.join(__dirname, 'renderer', 'index.html'));
  window.on('blur', () => {
    if (!window?.webContents.isDevToolsOpened() && !feishuConfigurationActionBusy) window?.hide();
  });
  window.on('close', (event) => {
    if (!quitting) {
      event.preventDefault();
      window.hide();
    }
  });
}

function createTray() {
  const icon = createTrayStatusIcon(buildTrayStatus(null));
  tray = new Tray(icon);
  updateTrayStatus(null);
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: '打开 KSFAssistant', click: () => showWindow() },
    { type: 'separator' },
    { label: '退出', click: () => { quitting = true; app.quit(); } },
  ]));
  tray.on('click', () => window?.isVisible() ? window.hide() : showWindow());
}

function createTrayStatusIcon(status) {
  const icon = nativeImage.createEmpty();
  for (const representation of WINDOWS_TRAY_REPRESENTATIONS) {
    icon.addRepresentation({
      scaleFactor: representation.scaleFactor,
      dataURL: trayIconDataURL(status, representation.size),
    });
  }
  return icon;
}

function feishuRuntime() {
  const arch = process.arch === 'arm64' ? 'windows-arm64' : 'windows-x64';
  const repoRoot = path.resolve(__dirname, '..', '..');
  const bundledBridge = app.isPackaged
    ? path.join(process.resourcesPath, 'runtime', 'feishu-bridge', arch, 'ksf-assistant-feishu-bridge.exe')
    : path.join(repoRoot, 'dist', 'runtime', 'feishu-bridge', arch, 'ksf-assistant-feishu-bridge.exe');
	return {
		bridge: fs.existsSync(bundledBridge) ? bundledBridge : '',
	};
}

function removeLegacyFeishuScheduledTask() {
	if (process.platform !== 'win32') return;
	spawnSync('schtasks.exe', ['/End', '/TN', 'FeishuBotBridge'], { windowsHide: true, stdio: 'ignore', timeout: 10_000 });
	spawnSync('schtasks.exe', ['/Delete', '/F', '/TN', 'FeishuBotBridge'], { windowsHide: true, stdio: 'ignore', timeout: 10_000 });
}

function updateTrayStatus(snapshot) {
  if (!tray) return;
  const status = buildTrayStatus(snapshot);
  const icon = createTrayStatusIcon(status);
  if (!icon.isEmpty()) tray.setImage(icon);
  tray.setToolTip(status.tooltip);
}

function positionWindow() {
  if (!window || !tray) return;
  const trayBounds = tray.getBounds();
  const display = screen.getDisplayNearestPoint({ x: trayBounds.x, y: trayBounds.y });
  const bounds = window.getBounds();
  const area = display.workArea;
  const x = clamp(Math.round(trayBounds.x + trayBounds.width / 2 - bounds.width / 2), area.x + 8, area.x + area.width - bounds.width - 8);
  const below = trayBounds.y + trayBounds.height + bounds.height + 8 <= area.y + area.height;
  const y = below ? trayBounds.y + trayBounds.height + 6 : trayBounds.y - bounds.height - 6;
  window.setPosition(x, clamp(y, area.y + 8, area.y + area.height - bounds.height - 8), false);
}

function showWindow() {
  if (!window || !tray) return;
  positionWindow();
  window.show();
  window.focus();
  window.webContents.send('ksfassistant:visible');
}

function dashboardParams(forceAccountRefresh = false) {
  const settings = store.get();
  return {
    ksfRoot: settings.ksfRoot,
    pinnedProjectIds: settings.pinnedProjectIds,
    pinnedWorkspaceIds: settings.pinnedWorkspaceIds,
    forceAccountRefresh,
    pricingSelection: {
      planId: settings.selectedPricingPlanId,
      customPlans: settings.customPricingPlans,
    },
  };
}

async function readDashboard(forceAccountRefresh = false) {
  if (dashboardPromise) return dashboardPromise;
  dashboardPromise = core.request('dashboard/read', dashboardParams(forceAccountRefresh));
  try {
    const snapshot = await dashboardPromise;
    authorizedWorkspacePaths = new Map(
      (snapshot.workspaces?.workspaces || [])
        .filter((item) => item.kind === 'workspace' && typeof item.path === 'string' && path.isAbsolute(item.path))
        .map((item) => [item.id, path.resolve(item.path)])
    );
    updateTrayStatus(snapshot);
    return snapshot;
  } catch (error) {
    updateTrayStatus(null);
    throw error;
  } finally {
    dashboardPromise = null;
  }
}

function showWindowWhenReady() {
  if (window.webContents.isLoadingMainFrame()) {
    window.webContents.once('did-finish-load', showWindow);
  } else {
    showWindow();
  }
}

async function persistKSFRoot(ksfRoot) {
  const previousRoot = store.get().ksfRoot;
  await core.request('integration/context/update', { ksfRoot });
  authorizedWorkspacePaths = new Map();
  try {
    return store.update({ ksfRoot });
  } catch (error) {
    try { await core.request('integration/context/update', { ksfRoot: previousRoot }); } catch {}
    throw error;
  }
}

function registerIPC() {
  ipcMain.handle('dashboard:read', (_event, forceAccountRefresh) => readDashboard(forceAccountRefresh === true));
  ipcMain.handle('token-history:read', (_event, options = {}) => {
    const settings = store.get();
    return core.request('token/history/compare', {
      dayCount: options.dayCount || 30,
      repriceOnly: options.repriceOnly === true,
      pricingSelection: { planId: settings.selectedPricingPlanId, customPlans: settings.customPricingPlans },
    });
  });
  ipcMain.handle('pricing-catalog:read', () => {
    const settings = store.get();
    return core.request('pricing/catalog/read', { customPlans: settings.customPricingPlans });
  });
  ipcMain.handle('settings:read', () => store.get());
  ipcMain.handle('external:open', async (_event, target) => {
    const allowed = new Set([
      'https://developers.openai.com/api/docs/models/compare',
      'https://api-docs.deepseek.com/quick_start/pricing/',
    ]);
    if (!allowed.has(target)) throw new Error('不允许打开这个外部链接');
    await shell.openExternal(target);
    return true;
  });
  ipcMain.handle('settings:update', async (_event, patch) => {
    const allowed = {};
    for (const key of ['ksfRoot', 'selectedFeishuTargetAlias', 'launchAtLogin', 'preventSleep', 'selectedPricingPlanId', 'customPricingPlans']) {
      if (Object.prototype.hasOwnProperty.call(patch || {}, key)) allowed[key] = patch[key];
    }
    const previousSleep = store.get().preventSleep;
    if (Object.prototype.hasOwnProperty.call(allowed, 'preventSleep')) {
      sleepInhibitor.setEnabled(allowed.preventSleep === true);
    }
    let settings;
    try { settings = store.update(allowed); }
    catch (error) {
      sleepInhibitor.setEnabled(previousSleep);
      throw error;
    }
    if (Object.prototype.hasOwnProperty.call(allowed, 'launchAtLogin')) {
      app.setLoginItemSettings({ openAtLogin: settings.launchAtLogin, openAsHidden: true });
    }
    if (Object.prototype.hasOwnProperty.call(allowed, 'ksfRoot')) {
      await core.request('integration/context/update', { ksfRoot: settings.ksfRoot });
    }
    return settings;
  });
  ipcMain.handle('directory:choose', async (_event, kind) => {
    if (kind !== 'ksfRoot') throw new Error('不支持的目录类型');
    const result = await dialog.showOpenDialog(window, { properties: ['openDirectory'], title: '选择 KSF 根目录' });
    if (result.canceled || result.filePaths.length !== 1) return null;
    return persistKSFRoot(result.filePaths[0]);
  });
  ipcMain.handle('directory:clear', async (_event, kind) => {
    if (kind !== 'ksfRoot') throw new Error('不支持的目录类型');
    return persistKSFRoot('');
  });
  ipcMain.handle('project:set-pinned', (_event, { projectId, pinned }) => {
    if (typeof projectId !== 'string' || !projectId || /[\u0000-\u001f\u007f]/.test(projectId)) throw new Error('项目标识无效');
    const values = new Set(store.get().pinnedProjectIds);
    if (pinned) values.add(projectId); else values.delete(projectId);
    return store.update({ pinnedProjectIds: [...values] });
  });
  ipcMain.handle('workspace:set-pinned', (_event, { workspaceId, pinned }) => {
    if (typeof workspaceId !== 'string' || !workspaceId || /[\u0000-\u001f\u007f]/.test(workspaceId)) throw new Error('工作区标识无效');
    const values = new Set(store.get().pinnedWorkspaceIds);
    if (pinned) values.add(workspaceId); else values.delete(workspaceId);
    return store.update({ pinnedWorkspaceIds: [...values] });
  });
  ipcMain.handle('path:open', async (_event, targetPath) => {
    const safePath = allowedLocalPath(targetPath);
    const error = await shell.openPath(safePath);
    if (error) throw new Error(error);
    return true;
  });
  ipcMain.handle('workspace:path-open', async (_event, { workspaceId, targetPath }) => {
    const safePath = allowedWorkspacePath(workspaceId, targetPath);
    const error = await shell.openPath(safePath);
    if (error) throw new Error(error);
    return true;
  });
  ipcMain.handle('task:open', async (_event, threadId) => {
    await shell.openExternal(taskURL(threadId));
    return true;
  });
  ipcMain.handle('task:create', async (_event, { projectId, purpose }) => {
    const settings = store.get();
    const created = await core.request('task/create', { projectId, ksfRoot: settings.ksfRoot, purpose });
    await shell.openExternal(taskURL(created.threadId));
    try {
      await core.request('task/submit', { threadId: created.threadId, hostId: 'local', cwd: settings.ksfRoot, prompt: created.prompt });
      return { ...created, submitted: true };
    } catch (error) {
      return { ...created, submitted: false, warning: '任务已创建，但未确认启动。请在 Codex 中查看并继续。' };
    }
  });
  ipcMain.handle('workspace:task-create', async (_event, { workspaceId, path: workspacePath, name }) => {
    const settings = store.get();
    const created = await core.request('workspace/task/create', {
      workspaceId,
      path: allowedWorkspacePath(workspaceId, workspacePath),
      name,
      ksfRoot: settings.ksfRoot,
    });
    await shell.openExternal(taskURL(created.threadId));
    return created;
  });
  ipcMain.handle('project:launch', async (_event, projectId) => {
    const settings = store.get();
    const action = await core.request('project/launch/prepare', { projectId, ksfRoot: settings.ksfRoot });
    const scriptPath = allowedLocalPath(action.scriptPath);
    const workingDirectory = allowedLocalPath(action.workingDirectory);
    if (action.kind !== 'powershell') throw new Error('当前平台不支持这个项目启动动作');
    const child = spawn('powershell.exe', ['-NoLogo', '-NoProfile', '-NoExit', '-File', scriptPath], {
      cwd: workingDirectory,
      detached: true,
      stdio: 'ignore',
      windowsHide: false,
    });
    child.unref();
    return true;
  });
  ipcMain.handle('feishu:task-link-create', async (event, payload) => {
    assertFeishuDesktopSender(event);
    if (feishuConfigurationActionBusy || approvalUnavailableReasons.size) throw new Error('请在桌面完成当前对话框后重试');
    feishuConfigurationActionBusy = true;
    approvalUnavailableReasons.add('task-card');
    try {
      return await require('./task-card-authorization.cjs').createTaskCard(core, payload, async () => {
        assertFeishuDesktopSender(event);
        if (quitting || !window.isVisible()) throw new Error('请在桌面重试');
        const result = await dialog.showMessageBox(window, {
          type: 'question', title: '发送任务卡片到飞书', message: '确认本次发送',
          detail: `接收人：${payload.targetAlias}\n任务：${payload.title}\n项目：${payload.projectName}\n\n以机器人身份发送包含任务状态和交互按钮的卡片。确认仅限本次发送。`,
          buttons: ['取消', '确认发送'], defaultId: 0, cancelId: 0, noLink: true,
        });
        assertFeishuDesktopSender(event);
        if (quitting || !window.isVisible() || [...approvalUnavailableReasons].some((reason) => reason !== 'task-card')) throw new Error('桌面暂不可交互，请重新检查本次发送。');
        return result.response === 1;
      });
    } finally {
      approvalUnavailableReasons.delete('task-card');
      feishuConfigurationActionBusy = false;
    }
  });
  ipcMain.handle('feishu:task-link-release', (_event, payload) => core.request('feishu/taskLink/release', payload));
  ipcMain.handle('feishu:task-link-interrupt', (_event, payload) => core.request('feishu/taskLink/interrupt', payload));
  ipcMain.handle('feishu:configuration-read', (event, options) => {
    assertFeishuDesktopSender(event);
    return core.request('feishu/configuration/read', { refresh: options?.refresh === true });
  });
  ipcMain.handle('feishu:configuration-action', (event, payload) => performFeishuConfigurationAction(event, payload));
  ipcMain.handle('feishu:flow-open', async (event, payload) => {
    assertFeishuDesktopSender(event);
    if (!window.isVisible() || feishuConfigurationActionBusy || approvalUnavailableReasons.size) throw new Error('请在桌面打开当前会话');
    const snapshot = await core.request('feishu/configuration/read', { refresh: false });
    const flow = snapshot?.flow;
    if (snapshot?.schemaVersion !== 2 || !payload?.flowId || flow?.id !== payload.flowId || flow.state !== 'pending'
      || snapshot.epoch !== payload.epoch || snapshot.contextRevision !== payload.contextRevision
      || (flow.expiresAt && (!Number.isFinite(Date.parse(flow.expiresAt)) || Date.parse(flow.expiresAt) <= Date.now()))) throw new Error('飞书会话已失效，请重新检查');
    const target = allowedFeishuURL(flow.verificationURL);
    assertFeishuDesktopSender(event);
    if (!window.isVisible() || feishuConfigurationActionBusy || approvalUnavailableReasons.size) throw new Error('请在桌面打开当前会话');
    await shell.openExternal(target);
    return true;
  });
  ipcMain.on('window:resize', (_event, requestedHeight) => {
    if (!window || !Number.isFinite(requestedHeight)) return;
    const display = screen.getDisplayMatching(window.getBounds());
    const height = clamp(Math.ceil(requestedHeight), 320, Math.max(320, display.workArea.height - 16));
    const contentBounds = window.getContentBounds();
    if (Math.abs(contentBounds.height - height) <= HEIGHT_ROUNDING_TOLERANCE) return;
    const wasVisible = window.isVisible();
    window.setContentBounds({ ...contentBounds, height }, false);
    if (wasVisible) positionWindow();
  });
  ipcMain.on('window:hide', () => window?.hide());
  ipcMain.on('app:quit', () => { quitting = true; app.quit(); });
}

function assertFeishuDesktopSender(event) {
  if (!window || window.isDestroyed() || event.sender !== window.webContents
    || event.senderFrame !== window.webContents.mainFrame
    || event.senderFrame.url !== pathToFileURL(path.join(__dirname, 'renderer', 'index.html')).href) throw new Error('配置操作仅允许当前桌面窗口');
}

const feishuConfigurationActions = new Set([
  'create_app', 'finish_auth', 'finish_app', 'cancel_flow',
  'logout', 'test_message', 'restart',
]);
let feishuConfigurationActionBusy = false;

async function performFeishuConfigurationAction(event, payload) {
  assertFeishuDesktopSender(event);
  if (feishuConfigurationActionBusy || !window.isVisible() || approvalUnavailableReasons.size) throw new Error('请在桌面完成当前对话框后重试');
  const fields = ['action', 'requestId', 'epoch', 'revision', 'contextRevision', 'confirm', 'appId', 'appSecret', 'targetAlias', 'feature', 'mode', 'flowId', 'authorizationRequestId'];
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)
    || Object.keys(payload).some((key) => !fields.includes(key))
    || !feishuConfigurationActions.has(payload.action)
    || !Number.isSafeInteger(payload.revision) || payload.revision < 0
    || typeof payload.confirm !== 'boolean') throw new Error('配置请求无效');
  for (const key of ['requestId', 'epoch', 'contextRevision']) {
    if (typeof payload[key] !== 'string' || (key !== 'contextRevision' && !payload[key]) || payload[key].length > 256 || /[\u0000-\u001f\u007f]/.test(payload[key])) throw new Error('配置上下文无效');
  }
  for (const key of ['appId', 'appSecret', 'targetAlias', 'feature', 'mode', 'flowId', 'authorizationRequestId']) {
    if (payload[key] !== undefined && (typeof payload[key] !== 'string' || payload[key].length > 4096 || /[\u0000-\u001f\u007f]/.test(payload[key]))) throw new Error('配置参数无效');
  }
  const request = Object.fromEntries(fields.filter((key) => key !== 'confirm' && Object.hasOwn(payload, key)).map((key) => [key, payload[key]]));
  const actionFields = { test_message: ['targetAlias'], finish_auth: ['flowId'], finish_app: ['flowId'], cancel_flow: ['flowId'] };
  if (['appId', 'appSecret', 'targetAlias', 'feature', 'mode', 'flowId', 'authorizationRequestId'].some((key) => Object.hasOwn(request, key) && !(actionFields[request.action] || []).includes(key))) throw new Error('操作参数不匹配');
  feishuConfigurationActionBusy = true;
  approvalUnavailableReasons.add('feishu-configuration');
  try {
    const snapshot = await core.request('feishu/configuration/read', { refresh: false });
    const action = snapshot?.actions?.find((item) => item.id === request.action);
    if (snapshot?.schemaVersion !== 2 || snapshot.epoch !== request.epoch
      || !Number.isSafeInteger(snapshot.revision) || snapshot.revision < 0 || request.revision > snapshot.revision
      || snapshot.contextRevision !== request.contextRevision || action?.enabled !== true) throw new Error('配置已变化或操作不可用，请重新检查');
    if (['finish_auth', 'finish_app', 'cancel_flow'].includes(request.action)
      && (!request.flowId || request.flowId !== snapshot.flow?.id)) throw new Error('配置会话已变化，请重新检查');
    if (request.action === 'test_message' && (!request.targetAlias || request.targetAlias !== snapshot.diagnostics?.selfTarget || !snapshot.connection?.targetAliases?.includes(request.targetAlias))) throw new Error('请选择当前可用的测试目标');
    if (action.confirmation !== undefined && typeof action.confirmation !== 'string') throw new Error('核心确认文案无效，请重新检查');
    const confirmation = action.confirmation || '';
    const checksFlow = ['finish_auth', 'finish_app'].includes(request.action);
    if (!confirmation.trim() && !checksFlow) throw new Error('缺少核心确认文案，请重新检查');
    let confirmed = false;
    assertFeishuDesktopSender(event);
    if (!window.isVisible()) throw new Error('请在桌面重试');
    if (confirmation.trim()) {
      const detail = [confirmation,
        request.targetAlias ? `目标别名：${request.targetAlias}` : '',
        request.action === 'test_message' ? '发送身份：机器人（bot）\n消息正文：【KSFAssistant 接入验收】这是一条由本人确认发送的连接测试消息，无需回复。\n本次测试只证明消息发送，不证明用户授权、消息接收或全部功能可用。' : ''].filter(Boolean).join('\n\n');
      const result = await dialog.showMessageBox(window, {
        type: 'warning', title: '飞书配置确认', message: action.title, detail,
        buttons: ['取消', '确认此操作'], defaultId: 0, cancelId: 0, noLink: true,
      });
      confirmed = result.response === 1;
      if (!confirmed) return { outcome: 'failed', cancelled: true, snapshot, message: '已取消，未执行配置操作。' };
    }
    assertFeishuDesktopSender(event);
    if (quitting || !window.isVisible() || [...approvalUnavailableReasons].some((reason) => reason !== 'feishu-configuration')) throw new Error('桌面已不可交互，请重新检查');
    try {
      return await core.request('feishu/configuration/action', { ...request, confirm: confirmed }, { timeoutMs: 125_000 });
    } catch {
      return await core.request('feishu/configuration/result', { requestId: request.requestId }, { timeoutMs: 45_000 });
    }
  } finally {
    request.appSecret = undefined;
    approvalUnavailableReasons.delete('feishu-configuration');
    feishuConfigurationActionBusy = false;
  }
}

function allowedFeishuURL(value) {
  if (typeof value !== 'string' || value.length > 4096) throw new Error('飞书链接无效');
  let target;
  try { target = new URL(value); } catch { throw new Error('飞书链接无效'); }
  const host = target.hostname.toLowerCase();
  const allowed = ['feishu.cn', 'larksuite.com', 'larkoffice.com'];
  if (target.protocol !== 'https:' || !allowed.some((domain) => host === domain || host.endsWith(`.${domain}`))) throw new Error('飞书链接不在允许范围内');
  target.username = '';
  target.password = '';
  return target.toString();
}

function allowedLocalPath(targetPath) {
  if (typeof targetPath !== 'string' || !path.isAbsolute(targetPath) || /[\u0000-\u001f\u007f]/.test(targetPath)) throw new Error('路径无效');
  const target = path.resolve(targetPath);
  const settings = store.get();
  const roots = [settings.ksfRoot].filter(Boolean).map((value) => path.resolve(value));
  if (!roots.some((root) => isPathInside(target, root))) throw new Error('路径不在已授权目录内');
  if (!fs.existsSync(target)) throw new Error('路径不存在');
  return target;
}

function allowedWorkspacePath(workspaceId, targetPath) {
  if (typeof workspaceId !== 'string' || !workspaceId || typeof targetPath !== 'string' || !path.isAbsolute(targetPath) || /[\u0000-\u001f\u007f]/.test(targetPath)) {
    throw new Error('工作区路径无效');
  }
  const target = path.resolve(targetPath);
  if (authorizedWorkspacePaths.get(workspaceId) !== target) throw new Error('工作区路径未获授权');
  if (!fs.existsSync(target)) throw new Error('工作区路径不存在');
  return target;
}

function migrateLegacySettings(currentSettingsPath) {
  return migrateSettings(currentSettingsPath, app.getPath('appData'));
}

function assertLegacyAppStopped() {
  if (process.platform !== 'win32') return;
  const command = "$ErrorActionPreference = 'Stop'; $legacy = @(Get-Process | Where-Object { $_.ProcessName -in @('CodexAssistant', 'Codex Usage Bar', 'CodexUsageBar') }); if ($legacy.Count -gt 0) { exit 10 }; exit 0";
  const result = spawnSync('powershell.exe', ['-NoProfile', '-NonInteractive', '-Command', command], {
    windowsHide: true,
    timeout: 5000,
    stdio: 'ignore',
  });
  if (result.status === 10) throw new Error('请先退出旧版 CodexAssistant / Codex Usage Bar / CodexUsageBar，再启动 KSFAssistant。');
  if (result.error || result.status !== 0) throw new Error('无法确认旧版应用已退出；为避免共享数据并发访问，已停止启动。请检查 PowerShell 后重试。');
}

app.whenReady().then(async () => {
  if (!hasSingleInstanceLock) return;
  const settingsPath = path.join(app.getPath('userData'), 'settings.json');
  try {
    assertLegacyAppStopped();
    migrateLegacySettings(settingsPath);
    store = new ConfigStore(settingsPath);
  } catch (error) {
    dialog.showErrorBox('KSFAssistant 启动与设置检查失败', error.message);
    app.exit(1);
    return;
  }
	removeLegacyFeishuScheduledTask();
	const runtime = feishuRuntime();
  core = new CoreClient({
    executablePath: coreExecutablePath(),
    env: {
      KSF_ASSISTANT_MANAGED: '1',
			KSF_ASSISTANT_FEISHU_BRIDGE: runtime.bridge,
      FEISHU_BRIDGE_DATA_DIR: path.join(os.homedir(), '.config', 'feishu-bridge'),
    },
    integrations: { ksfRoot: store.get().ksfRoot },
  });
  registerIPC();
  createWindow();
  createTray();
  app.setLoginItemSettings({ openAtLogin: store.get().launchAtLogin, openAsHidden: true });
  try { sleepInhibitor.setEnabled(store.get().preventSleep); }
  catch (error) {
    store.update({ preventSleep: false });
    dialog.showErrorBox('防睡眠未启用', error.message);
  }
  await core.start().catch((error) => window.webContents.once('did-finish-load', () => window.webContents.send('ksfassistant:core-error', error.message)));
  for (const [unavailable, available] of [['lock-screen', 'unlock-screen'], ['suspend', 'resume']]) {
    powerMonitor.on(unavailable, () => {
      approvalUnavailableReasons.add(unavailable);
    });
    powerMonitor.on(available, () => { approvalUnavailableReasons.delete(unavailable); });
  }
  startActivityMonitor({
    readRevision: async () => (await core.request('activity/read')).revision,
    stopped: () => quitting,
    refresh: async () => {
      // Wait for an older dashboard before taking a fresh state projection.
      if (dashboardPromise) await dashboardPromise.catch(() => {});
      const snapshot = await readDashboard();
      if (!quitting && window && !window.isDestroyed()) window.webContents.send('ksfassistant:dashboard', snapshot);
    },
  });
  if (!app.isPackaged || process.env.KSF_ASSISTANT_SHOW_ON_START === '1') {
    showWindowWhenReady();
  }
});

app.on('window-all-closed', (event) => event.preventDefault());
app.on('before-quit', (event) => {
  if (shutdownStarted) return;
  event.preventDefault();
  quitting = true;
  shutdownStarted = true;
  sleepInhibitor.setEnabled(false);
  Promise.resolve().then(() => core?.close())
    .catch(() => {})
    .finally(() => app.exit(0));
});
app.on('activate', showWindow);
