const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const {
  bridgeServiceStatus,
  commandForNodeScript,
  controlBridgeService,
  defaultCodexBin,
  defaultDataRoot,
  defaultDesktopIPCPath,
  defaultLarkCliBin,
  defaultLogDir,
  privatePathBoundary,
  spawnSleepInhibitor,
} = require('../lib/platform-runtime');

test('Windows defaults keep mutable private state outside packaged-app virtualization', () => {
  const projectRoot = path.join('/workspace', 'feishu-bot-bridge');
  const env = { LOCALAPPDATA: path.join('/private', 'LocalAppData') };
  const dataRoot = defaultDataRoot({ platform: 'win32', env, homeDir: '/home/test' });
  assert.equal(dataRoot, path.join('/home/test', '.config', 'feishu-bridge'));
  assert.equal(defaultLogDir(projectRoot, { platform: 'win32', env, homeDir: '/home/test' }), path.join(dataRoot, 'logs'));
  assert.equal(
    defaultLarkCliBin(projectRoot, { platform: 'win32', env }),
    path.join(projectRoot, 'node_modules', '@larksuite', 'cli', 'scripts', 'run.js'),
  );
  assert.equal(defaultCodexBin({ platform: 'win32', env }), 'codex.exe');
  assert.equal(defaultDesktopIPCPath({ platform: 'win32', env, homeDir: '/home/test' }), '');
});

test('macOS defaults remain backward compatible', () => {
  const projectRoot = '/workspace/bridge';
  assert.equal(defaultDataRoot({ platform: 'darwin', env: {}, homeDir: '/Users/test' }), '/Users/test/.config/feishu-bridge');
  assert.equal(defaultLogDir(projectRoot, { platform: 'darwin', env: {}, homeDir: '/Users/test' }), '/workspace/bridge/logs');
  assert.equal(defaultDesktopIPCPath({ platform: 'darwin', env: {}, homeDir: '/Users/test' }), '/Users/test/.codex/ipc/ipc.sock');
});

test('managed macOS runtime keeps mutable logs outside the signed application bundle', () => {
  const projectRoot = '/Applications/CodexAssistant.app/Contents/Resources/services/feishu-bridge';
  const dataRoot = '/Users/test/.config/feishu-bridge';
  const env = {
    CODEX_USAGE_BAR_MANAGED: '1',
    FEISHU_BRIDGE_DATA_DIR: dataRoot,
  };
  assert.equal(defaultLogDir(projectRoot, { platform: 'darwin', env, homeDir: '/Users/test' }), path.join(dataRoot, 'logs'));
});

test('managed compatibility launcher uses the host-provided pinned lark-cli', () => {
  const projectRoot = '/Applications/CodexAssistant.app/Contents/Resources/services/feishu-bridge';
  const larkCLI = '/Applications/CodexAssistant.app/Contents/Resources/runtime/lark-cli/darwin-arm64/lark-cli';
  assert.equal(defaultLarkCliBin(projectRoot, {
    platform: 'darwin', env: { CODEX_USAGE_BAR_LARK_CLI: larkCLI },
  }), larkCLI);
  const source = fs.readFileSync(path.join(__dirname, '..', 'scripts', 'start-bridge.js'), 'utf8');
  assert.match(source, /runtimeEnv\.CODEX_USAGE_BAR_LARK_CLI/);
  assert.match(source, /FEISHU_AUDIT_DIR:[\s\S]*path\.join\(logDir, 'audit'\)/);
});

test('JavaScript CLI entrypoints are launched through the current Node runtime', () => {
  const invocation = commandForNodeScript('/workspace/run.js', ['--version']);
  assert.equal(invocation.command, process.execPath);
  assert.deepEqual(invocation.args, ['/workspace/run.js', '--version']);
  assert.deepEqual(commandForNodeScript('/workspace/lark-cli', ['--version']), {
    command: '/workspace/lark-cli', args: ['--version'],
  });
});

test('Windows service management invokes only the fixed PowerShell adapter', () => {
  const calls = [];
  const spawnSyncFn = (command, args, options) => {
    calls.push({ command, args, options });
    return { status: 0, stdout: '{"taskName":"Bridge.Test","loaded":true,"running":true,"state":"Running"}' };
  };
  const config = { windowsTaskName: 'Bridge.Test' };
  const status = bridgeServiceStatus(config, {
    platform: 'win32', projectRoot: '/workspace/bridge', spawnSyncFn, env: {},
  });
  assert.equal(status.manager, 'scheduled-task');
  assert.equal(status.loaded, true);
  const action = controlBridgeService(config, {
    restart: true, platform: 'win32', projectRoot: '/workspace/bridge', spawnSyncFn, env: {},
  });
  assert.equal(action.action, 'restart');
  assert.equal(calls.length, 2);
  assert.equal(calls[0].command, 'powershell.exe');
  assert.equal(calls[0].options.env.FEISHU_BRIDGE_TASK_ACTION, 'status');
  assert.equal(calls[1].options.env.FEISHU_BRIDGE_TASK_ACTION, 'restart');
  assert.match(calls[1].args.at(-1), /Manage-FeishuBridgeTask\.ps1$/);
});

test('Windows restart adapter validates repository bridge commands before stopping orphan processes', () => {
  const source = fs.readFileSync(path.join(__dirname, '..', 'windows', 'Manage-FeishuBridgeTask.ps1'), 'utf8');
  assert.match(source, /Get-CimInstance Win32_Process/);
  assert.match(source, /scripts\\start-bridge\.js/);
  assert.match(source, /bot-bridge\.js/);
  assert.match(source, /IndexOf\(\$_, \[StringComparison\]::OrdinalIgnoreCase\)/);
  assert.match(source, /Stop-Process -Id \$process\.ProcessId -Force/);
  assert.match(source, /did not become ready within 10 seconds/);
});

test('service identifiers are generic and can be overridden per installation', () => {
  const calls = [];
  const spawnSyncFn = (command, args, options) => {
    calls.push({ command, args, options });
    if (command === 'launchctl') return { status: 1, stdout: '', stderr: 'not loaded' };
    return { status: 0, stdout: '{"taskName":"Team.Bridge","loaded":true,"running":true}' };
  };
  const windows = bridgeServiceStatus({}, {
    platform: 'win32', projectRoot: '/workspace/bridge', spawnSyncFn,
    env: { FEISHU_BRIDGE_WINDOWS_TASK_NAME: 'Team.Bridge' },
  });
  assert.equal(windows.taskName, 'Team.Bridge');
  assert.equal(calls[0].options.env.FEISHU_BRIDGE_TASK_NAME, 'Team.Bridge');

  const mac = bridgeServiceStatus({}, {
    platform: 'darwin', projectRoot: '/workspace/bridge', spawnSyncFn, uid: 501,
    env: { FEISHU_BRIDGE_LAUNCHD_LABEL: 'com.team.bridge' },
  });
  assert.equal(mac.label, 'com.team.bridge');
  assert.equal(mac.domain, 'gui/501/com.team.bridge');
});

test('Windows private files fail closed when configuration escapes the ACL root', () => {
  const root = 'C:\\Users\\test\\AppData\\Local\\FeishuBridge';
  assert.deepEqual(privatePathBoundary(root, {
    logs: `${root}\\logs`,
    events: `${root}\\events`,
  }, 'win32'), { enforced: true, secure: true, escaped: [] });
  assert.deepEqual(privatePathBoundary(root, {
    logs: 'C:\\workspace\\bridge\\logs',
  }, 'win32'), { enforced: true, secure: false, escaped: ['logs'] });
});

test('Windows sleep inhibition uses a fixed script without business payloads', () => {
  let captured;
  const child = {};
  const result = spawnSleepInhibitor({
    platform: 'win32', projectRoot: 'C:\\bridge', env: { SAFE_BASE: '1' }, mode: 'active-task',
    spawnFn(command, args, options) {
      captured = { command, args, options };
      return child;
    },
  });
  assert.equal(result, child);
  assert.equal(captured.command, 'powershell.exe');
  assert.match(captured.args.at(-1), /Hold-FeishuBridgeAwake\.ps1$/);
  assert.equal(captured.options.env.FEISHU_BRIDGE_AWAKE_MODE, 'active-task');
});
