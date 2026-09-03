const os = require('node:os');
const fs = require('node:fs');
const path = require('node:path');
const { spawn, spawnSync } = require('node:child_process');

const WINDOWS_TASK_NAME = 'FeishuBotBridge';
const MACOS_LAUNCHD_LABEL = 'com.example.feishu-bot-bridge';

function defaultDataRoot({ platform = process.platform, env = process.env, homeDir = os.homedir() } = {}) {
  if (env.FEISHU_BRIDGE_DATA_DIR) return path.resolve(env.FEISHU_BRIDGE_DATA_DIR);
  const pathApi = platform === 'darwin' ? path.posix : path;
  return pathApi.join(homeDir, '.config', 'feishu-bridge');
}

function defaultLogDir(projectRoot, options = {}) {
  const { platform = process.platform, env = process.env } = options;
  if (env.FEISHU_BRIDGE_LOG_DIR) return path.resolve(projectRoot, env.FEISHU_BRIDGE_LOG_DIR);
  if (platform === 'win32' || env.CODEX_USAGE_BAR_MANAGED === '1' || env.FEISHU_BRIDGE_DATA_DIR) {
    return path.join(defaultDataRoot(options), 'logs');
  }
  const pathApi = platform === 'darwin' ? path.posix : path;
  return pathApi.join(projectRoot, 'logs');
}

function defaultLarkCliBin(projectRoot, { platform = process.platform, env = process.env } = {}) {
  const managedBinary = env.LARK_CLI_BIN || env.CODEX_USAGE_BAR_LARK_CLI;
  if (managedBinary) return path.resolve(projectRoot, managedBinary);
  return platform === 'win32'
    ? path.join(projectRoot, 'node_modules', '@larksuite', 'cli', 'scripts', 'run.js')
    : path.join(projectRoot, 'node_modules', '.bin', 'lark-cli');
}

function defaultCodexBin({ platform = process.platform, env = process.env } = {}) {
  return env.CODEX_BIN || (platform === 'win32' ? 'codex.exe' : 'codex');
}

function defaultDesktopIPCPath({ platform = process.platform, env = process.env, homeDir = os.homedir() } = {}) {
  if (env.CODEX_DESKTOP_IPC_PATH) return env.CODEX_DESKTOP_IPC_PATH;
  if (platform === 'darwin') {
    const codexHome = env.CODEX_HOME || path.posix.join(homeDir, '.codex');
    return path.posix.join(codexHome, 'ipc', 'ipc.sock');
  }
  return '';
}

function commandForNodeScript(bin, args = []) {
  return /\.m?js$/i.test(bin)
    ? { command: process.execPath, args: [bin, ...args] }
    : { command: bin, args };
}

function parseJsonOutput(result, label) {
  if (result.status !== 0) {
    const detail = String(result.stderr || result.stdout || result.error?.message || '').replace(/\s+/g, ' ').trim();
    throw new Error(`${label} failed${detail ? `: ${detail}` : ''}`);
  }
  try {
    return JSON.parse(String(result.stdout || '').trim() || '{}');
  } catch {
    throw new Error(`${label} returned invalid JSON`);
  }
}

function windowsPowerShellEnv(env = process.env) {
  const next = { ...env };
  const modulePath = String(next.PSModulePath || '');
  if (modulePath) {
    const safeEntries = modulePath
      .split(path.delimiter)
      .filter((entry) => /\\WindowsPowerShell\\Modules(?:\\)?$/i.test(entry)
        || /\\WindowsPowerShell\\v1\.0\\Modules(?:\\)?$/i.test(entry));
    if (safeEntries.length > 0) next.PSModulePath = safeEntries.join(path.delimiter);
  }
  return next;
}

function bridgeServiceStatus(config, {
  platform = process.platform,
  projectRoot,
  spawnSyncFn = spawnSync,
  uid = process.getuid?.(),
  env = process.env,
} = {}) {
  if (platform === 'darwin') {
    const label = env.FEISHU_BRIDGE_LAUNCHD_LABEL || config.launchdLabel || MACOS_LAUNCHD_LABEL;
    const domain = `gui/${uid}/${label}`;
    const result = spawnSyncFn('launchctl', ['print', domain], { encoding: 'utf8', timeout: 5000 });
    return {
      manager: 'launchd', label, domain,
      loaded: result.status === 0,
      running: result.status === 0 && /\bstate\s*=\s*running\b/.test(result.stdout || ''),
      error: result.status === 0 ? undefined : String(result.stderr || result.stdout || result.error?.message || '').replace(/\s+/g, ' ').trim(),
    };
  }
  if (platform === 'win32') {
    const script = path.join(projectRoot, 'windows', 'Manage-FeishuBridgeTask.ps1');
    const result = spawnSyncFn('powershell.exe', [
      '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', script,
    ], {
      encoding: 'utf8', timeout: 10000,
      env: windowsPowerShellEnv({
        ...env,
        FEISHU_BRIDGE_TASK_ACTION: 'status',
        FEISHU_BRIDGE_TASK_NAME: env.FEISHU_BRIDGE_WINDOWS_TASK_NAME || config.windowsTaskName || WINDOWS_TASK_NAME,
      }),
    });
    try {
      return { manager: 'scheduled-task', ...parseJsonOutput(result, 'Windows Scheduled Task status') };
    } catch (error) {
      return {
        manager: 'scheduled-task',
        taskName: env.FEISHU_BRIDGE_WINDOWS_TASK_NAME || config.windowsTaskName || WINDOWS_TASK_NAME,
        loaded: false, running: false, error: error.message,
      };
    }
  }
  return { manager: 'process', loaded: false, running: false, error: `unsupported service manager: ${platform}` };
}

function controlBridgeService(config, {
  restart = false,
  platform = process.platform,
  projectRoot,
  spawnSyncFn = spawnSync,
  uid = process.getuid?.(),
  env = process.env,
} = {}) {
  if (platform === 'darwin') {
    const label = env.FEISHU_BRIDGE_LAUNCHD_LABEL || config.launchdLabel || MACOS_LAUNCHD_LABEL;
    const domain = `gui/${uid}/${label}`;
    const result = spawnSyncFn('launchctl', restart ? ['kickstart', '-k', domain] : ['kickstart', domain], {
      encoding: 'utf8', timeout: 10000,
    });
    if (result.status !== 0) {
      const detail = String(result.stderr || result.stdout || result.error?.message || '').replace(/\s+/g, ' ').trim();
      throw new Error(`launchctl ${restart ? 'restart' : 'start'} failed${detail ? `: ${detail}` : ''}`);
    }
    return { manager: 'launchd', label, domain, action: restart ? 'restart' : 'start', accepted: true };
  }
  if (platform === 'win32') {
    const script = path.join(projectRoot, 'windows', 'Manage-FeishuBridgeTask.ps1');
    const action = restart ? 'restart' : 'start';
    const result = spawnSyncFn('powershell.exe', [
      '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', script,
    ], {
      encoding: 'utf8', timeout: 15000,
      env: windowsPowerShellEnv({
        ...env,
        FEISHU_BRIDGE_TASK_ACTION: action,
        FEISHU_BRIDGE_TASK_NAME: env.FEISHU_BRIDGE_WINDOWS_TASK_NAME || config.windowsTaskName || WINDOWS_TASK_NAME,
      }),
    });
    return { manager: 'scheduled-task', ...parseJsonOutput(result, `Windows Scheduled Task ${action}`), action, accepted: true };
  }
  throw new Error(`service control is unsupported on ${platform}`);
}

function spawnSleepInhibitor({
  platform = process.platform,
  projectRoot,
  spawnFn = spawn,
  mode = 'active-task',
  env = process.env,
} = {}) {
  if (platform === 'darwin') {
    const args = mode === 'bridge' ? ['-dimsu'] : ['-i'];
    return spawnFn('/usr/bin/caffeinate', args, { stdio: 'ignore', env });
  }
  if (platform === 'win32') {
    return spawnFn('powershell.exe', [
      '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass',
      '-File', path.join(projectRoot, 'windows', 'Hold-FeishuBridgeAwake.ps1'),
    ], { stdio: 'ignore', env: windowsPowerShellEnv({ ...env, FEISHU_BRIDGE_AWAKE_MODE: mode }), windowsHide: true });
  }
  return null;
}

function privateRootSecurityStatus(dataRoot, {
  platform = process.platform,
  projectRoot,
  spawnSyncFn = spawnSync,
  env = process.env,
} = {}) {
  if (platform === 'win32') {
    const script = path.join(projectRoot, 'windows', 'Test-FeishuBridgeAcl.ps1');
    const result = spawnSyncFn('powershell.exe', [
      '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', script,
    ], {
      encoding: 'utf8', timeout: 10000,
      env: windowsPowerShellEnv({ ...env, FEISHU_BRIDGE_DATA_DIR: dataRoot }),
    });
    try {
      return { model: 'windows-acl', ...parseJsonOutput(result, 'Windows private-root ACL check') };
    } catch (error) {
      return { model: 'windows-acl', exists: fs.existsSync(dataRoot), secure: false, error: error.message };
    }
  }
  try {
    const stat = fs.lstatSync(dataRoot);
    return {
      model: 'posix-mode', exists: true,
      secure: stat.isDirectory() && !stat.isSymbolicLink() && (stat.mode & 0o077) === 0,
      mode: `0${(stat.mode & 0o777).toString(8)}`,
    };
  } catch (error) {
    return { model: 'posix-mode', exists: false, secure: false, error: error.code === 'ENOENT' ? undefined : error.message };
  }
}

function chmodPrivate(filePath, mode, { platform = process.platform, fsModule } = {}) {
  if (platform === 'win32') return;
  (fsModule || require('node:fs')).chmodSync(filePath, mode);
}

function privatePermissionsSatisfied(stat, platform = process.platform) {
  return platform === 'win32' || (stat.mode & 0o077) === 0;
}

function privatePathBoundary(dataRoot, paths, platform = process.platform) {
  if (platform !== 'win32') return { enforced: false, secure: true, escaped: [] };
  const pathApi = path.win32;
  const root = pathApi.resolve(dataRoot);
  const escaped = Object.entries(paths || {}).flatMap(([name, value]) => {
    const relative = pathApi.relative(root, pathApi.resolve(String(value || '')));
    return !relative || (!relative.startsWith('..') && !pathApi.isAbsolute(relative)) ? [] : [name];
  });
  return { enforced: true, secure: escaped.length === 0, escaped };
}

function assertPrivatePathBoundary(dataRoot, paths, platform = process.platform) {
  const boundary = privatePathBoundary(dataRoot, paths, platform);
  if (!boundary.secure) throw new Error(`Windows private paths escape FEISHU_BRIDGE_DATA_DIR: ${boundary.escaped.join(',')}`);
  return boundary;
}

module.exports = {
  MACOS_LAUNCHD_LABEL,
  WINDOWS_TASK_NAME,
  bridgeServiceStatus,
  chmodPrivate,
  commandForNodeScript,
  controlBridgeService,
  defaultCodexBin,
  defaultDataRoot,
  defaultDesktopIPCPath,
  defaultLarkCliBin,
  defaultLogDir,
  privateRootSecurityStatus,
  privatePermissionsSatisfied,
  privatePathBoundary,
  assertPrivatePathBoundary,
  spawnSleepInhibitor,
  windowsPowerShellEnv,
};
