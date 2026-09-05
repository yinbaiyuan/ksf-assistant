'use strict';

const { app, BrowserWindow, Tray, Menu, dialog, ipcMain, nativeImage, screen, shell } = require('electron');
const { spawn, spawnSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { CoreClient } = require('./core-client.cjs');
const { ConfigStore } = require('./config-store.cjs');
const { taskURL, clamp, isPathInside } = require('./security.cjs');
const { buildTrayStatus, trayIconDataURL } = require('./tray-status.cjs');

const APP_WIDTH = 392;
let window = null;
let tray = null;
let core = null;
let store = null;
let quitting = false;
let shutdownStarted = false;
let dashboardPromise = null;

app.setAppUserModelId('com.codexassistant.desktop');

function coreExecutablePath() {
  if (app.isPackaged) {
    const arch = process.arch === 'arm64' ? 'windows-arm64' : 'windows-x64';
    return path.join(process.resourcesPath, 'core', arch, 'codex-usage-core.exe');
  }
  const repoRoot = path.resolve(__dirname, '..', '..');
  if (process.platform === 'win32') {
    const arch = process.arch === 'arm64' ? 'windows-arm64' : 'windows-x64';
    return path.join(repoRoot, 'dist', 'core', arch, 'codex-usage-core.exe');
  }
  return path.join(repoRoot, 'dist', 'core', `darwin-${process.arch}`, 'codex-usage-core');
}

function createWindow() {
  window = new BrowserWindow({
    width: APP_WIDTH,
    height: 760,
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
    if (!window?.webContents.isDevToolsOpened()) window?.hide();
  });
  window.on('close', (event) => {
    if (!quitting) {
      event.preventDefault();
      window.hide();
    }
  });
}

function createTray() {
  const iconPath = path.join(__dirname, '..', 'assets', 'icon.png');
  const icon = nativeImage.createFromPath(iconPath).resize({ width: 20, height: 20 });
  tray = new Tray(icon);
  updateTrayStatus(null);
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: '打开 CodexAssistant', click: () => showWindow() },
    { type: 'separator' },
    { label: '退出', click: () => { quitting = true; app.quit(); } },
  ]));
  tray.on('click', () => window?.isVisible() ? window.hide() : showWindow());
}

function feishuRuntime() {
  const arch = process.arch === 'arm64' ? 'windows-arm64' : 'windows-x64';
  const repoRoot = path.resolve(__dirname, '..', '..');
  const bundledBridge = app.isPackaged
    ? path.join(process.resourcesPath, 'runtime', 'feishu-bridge', arch, 'codex-feishu-bridge.exe')
    : path.join(repoRoot, 'dist', 'runtime', 'feishu-bridge', arch, 'codex-feishu-bridge.exe');
  const bundledLarkCLI = app.isPackaged
    ? path.join(process.resourcesPath, 'runtime', 'lark-cli', arch, 'lark-cli.exe')
    : path.join(repoRoot, 'dist', 'runtime', 'lark-cli', arch, 'lark-cli.exe');
	return {
		bridge: fs.existsSync(bundledBridge) ? bundledBridge : '',
		larkCLI: fs.existsSync(bundledLarkCLI) ? bundledLarkCLI : '',
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
  const icon = nativeImage.createFromDataURL(trayIconDataURL(status)).resize({ width: 20, height: 20 });
  if (!icon.isEmpty()) tray.setImage(icon);
  tray.setToolTip(status.tooltip);
}

function showWindow() {
  if (!window || !tray) return;
  const trayBounds = tray.getBounds();
  const display = screen.getDisplayNearestPoint({ x: trayBounds.x, y: trayBounds.y });
  const bounds = window.getBounds();
  const area = display.workArea;
  const x = clamp(Math.round(trayBounds.x + trayBounds.width / 2 - bounds.width / 2), area.x + 8, area.x + area.width - bounds.width - 8);
  const below = trayBounds.y + trayBounds.height + bounds.height + 8 <= area.y + area.height;
  const y = below ? trayBounds.y + trayBounds.height + 6 : trayBounds.y - bounds.height - 6;
  window.setPosition(x, clamp(y, area.y + 8, area.y + area.height - bounds.height - 8), false);
  window.show();
  window.focus();
  window.webContents.send('usagebar:visible');
}

function dashboardParams() {
  const settings = store.get();
  return {
    ksfRoot: settings.ksfRoot,
    pinnedProjectIds: settings.pinnedProjectIds,
    pricingSelection: {
      planId: settings.selectedPricingPlanId,
      customPlans: settings.customPricingPlans,
    },
  };
}

async function readDashboard() {
  if (dashboardPromise) return dashboardPromise;
  dashboardPromise = core.request('dashboard/read', dashboardParams());
  try {
    const snapshot = await dashboardPromise;
    updateTrayStatus(snapshot);
    return snapshot;
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

function registerIPC() {
  ipcMain.handle('dashboard:read', readDashboard);
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
    for (const key of ['ksfRoot', 'selectedFeishuTargetAlias', 'launchAtLogin', 'selectedPricingPlanId', 'customPricingPlans']) {
      if (Object.prototype.hasOwnProperty.call(patch || {}, key)) allowed[key] = patch[key];
    }
    const settings = store.update(allowed);
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
    const settings = store.update({ [kind]: result.filePaths[0] });
    await core.request('integration/context/update', { ksfRoot: settings.ksfRoot });
    return settings;
  });
  ipcMain.handle('project:set-pinned', (_event, { projectId, pinned }) => {
    if (typeof projectId !== 'string' || !projectId || /[\u0000-\u001f\u007f]/.test(projectId)) throw new Error('项目标识无效');
    const values = new Set(store.get().pinnedProjectIds);
    if (pinned) values.add(projectId); else values.delete(projectId);
    return store.update({ pinnedProjectIds: [...values] });
  });
  ipcMain.handle('path:open', async (_event, targetPath) => {
    const safePath = allowedLocalPath(targetPath);
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
    await new Promise((resolve) => setTimeout(resolve, 700));
    try {
      await core.request('task/submit', { threadId: created.threadId, hostId: 'local', cwd: settings.ksfRoot, prompt: created.prompt });
      return { ...created, submitted: true };
    } catch (error) {
      return { ...created, submitted: false, warning: `任务已创建并打开，但首次说明未自动提交：${error.message}` };
    }
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
  ipcMain.handle('feishu:task-link-create', (_event, payload) => core.request('feishu/taskLink/create', payload));
  ipcMain.handle('feishu:task-link-release', (_event, payload) => core.request('feishu/taskLink/release', payload));
  ipcMain.handle('feishu:task-link-interrupt', (_event, payload) => core.request('feishu/taskLink/interrupt', payload));
  ipcMain.handle('feishu:test', (_event, targetAlias) => core.request('feishu/test', { targetAlias }));
  ipcMain.handle('feishu:profile-set', (_event, profile) => core.request('feishu/profile/set', { profile }));
  ipcMain.handle('feishu:setup-read', () => core.request('feishu/setup/read'));
  ipcMain.handle('feishu:setup-begin', (_event, payload) => core.request('feishu/setup/begin', payload));
  ipcMain.handle('feishu:setup-continue', () => core.request('feishu/setup/continue'));
  ipcMain.handle('feishu:setup-verify', () => core.request('feishu/setup/verify'));
  ipcMain.handle('feishu:setup-activate', (_event, targetAlias) => core.request('feishu/setup/activate', { targetAlias }));
  ipcMain.handle('feishu:setup-cancel', () => core.request('feishu/setup/cancel'));
  ipcMain.handle('feishu:overview-read', () => core.request('feishu/settings/overview/read'));
  ipcMain.handle('feishu:feature-update', (_event, payload) => core.request('feishu/features/update', payload));
  ipcMain.handle('feishu:supervisor-restart', () => core.request('feishu/supervisor/restart'));
  ipcMain.handle('feishu:open-external', async (_event, value) => {
    const target = allowedFeishuURL(value);
    await shell.openExternal(target);
    return true;
  });
  ipcMain.on('window:resize', (_event, requestedHeight) => {
    if (!window || !Number.isFinite(requestedHeight)) return;
    const display = screen.getDisplayMatching(window.getBounds());
    const height = clamp(Math.ceil(requestedHeight), 320, Math.max(320, display.workArea.height - 16));
    if (window.getBounds().height === height) return;
    const wasVisible = window.isVisible();
    window.setSize(APP_WIDTH, height, false);
    if (wasVisible) showWindow();
  });
  ipcMain.on('window:hide', () => window?.hide());
  ipcMain.on('app:quit', () => { quitting = true; app.quit(); });
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

function migrateLegacySettings(currentSettingsPath) {
  if (fs.existsSync(currentSettingsPath)) return;
  const legacyProductName = ['Codex', ' Usage', ' Bar'].join('');
  const legacySettings = path.join(app.getPath('appData'), legacyProductName, 'settings.json');
  if (!fs.existsSync(legacySettings)) return;
  fs.mkdirSync(path.dirname(currentSettingsPath), { recursive: true });
  fs.copyFileSync(legacySettings, currentSettingsPath, fs.constants.COPYFILE_EXCL);
}

app.whenReady().then(async () => {
  const settingsPath = path.join(app.getPath('userData'), 'settings.json');
  migrateLegacySettings(settingsPath);
	store = new ConfigStore(settingsPath);
	removeLegacyFeishuScheduledTask();
	const runtime = feishuRuntime();
  core = new CoreClient({
    executablePath: coreExecutablePath(),
    env: {
      CODEX_USAGE_BAR_MANAGED: '1',
			CODEX_USAGE_BAR_FEISHU_BRIDGE: runtime.bridge,
      CODEX_USAGE_BAR_LARK_CLI: runtime.larkCLI,
      FEISHU_BRIDGE_DATA_DIR: path.join(os.homedir(), '.config', 'feishu-bridge'),
    },
    integrations: { ksfRoot: store.get().ksfRoot },
  });
  registerIPC();
  createWindow();
  createTray();
  app.setLoginItemSettings({ openAtLogin: store.get().launchAtLogin, openAsHidden: true });
  await core.start().catch((error) => window.webContents.once('did-finish-load', () => window.webContents.send('usagebar:core-error', error.message)));
  if (!app.isPackaged || process.env.CODEX_USAGE_BAR_SHOW_ON_START === '1') {
    showWindowWhenReady();
  }
});

app.on('window-all-closed', (event) => event.preventDefault());
app.on('before-quit', (event) => {
  if (shutdownStarted) return;
  event.preventDefault();
  quitting = true;
  shutdownStarted = true;
  Promise.resolve(core?.close())
    .catch(() => {})
    .finally(() => app.exit(0));
});
app.on('activate', showWindow);
