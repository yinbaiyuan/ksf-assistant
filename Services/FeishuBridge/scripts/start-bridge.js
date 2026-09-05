const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const {
  defaultDataRoot,
  defaultCodexWorkspaceRoot,
  defaultLarkCliBin,
  defaultLogDir,
  spawnSleepInhibitor,
} = require('../lib/platform-runtime');
const { loadRuntimeEnvironment } = require('../lib/runtime-env');

const homeDir = process.env.HOME || os.homedir();
const projectRoot = path.resolve(process.env.FEISHU_BRIDGE_PROJECT_ROOT || path.join(__dirname, '..'));

const runtimeEnv = loadRuntimeEnvironment(projectRoot, { env: process.env, homeDir });
const dataRoot = defaultDataRoot({ env: runtimeEnv, homeDir });
const logDir = defaultLogDir(projectRoot, { env: runtimeEnv, homeDir });
const codexWorkspaceRoot = defaultCodexWorkspaceRoot({
  env: runtimeEnv,
  homeDir,
  dataRoot,
});

const env = {
  ...runtimeEnv,
  FEISHU_BRIDGE_PROJECT_ROOT: projectRoot,
  FEISHU_BRIDGE_DATA_DIR: runtimeEnv.FEISHU_BRIDGE_DATA_DIR || dataRoot,
  FEISHU_BRIDGE_LOG_DIR: runtimeEnv.FEISHU_BRIDGE_LOG_DIR || logDir,
  LARK_CLI_BIN: runtimeEnv.LARK_CLI_BIN
    || runtimeEnv.KSF_ASSISTANT_LARK_CLI
    || defaultLarkCliBin(projectRoot, { env: runtimeEnv }),
  LARK_CLI_AS: runtimeEnv.LARK_CLI_AS || 'bot',
  FEISHU_EVENT_CONSUMER_ENABLED: runtimeEnv.FEISHU_EVENT_CONSUMER_ENABLED || 'true',
  FEISHU_EVENT_TRANSPORT: runtimeEnv.FEISHU_EVENT_TRANSPORT || 'official-sdk',
  CODEX_FEISHU_WORKSPACE_ROOT: codexWorkspaceRoot,
  FEISHU_AUDIT_DIR: runtimeEnv.FEISHU_AUDIT_DIR
    || path.join(logDir, 'audit'),
  CODEX_BYPASS_APPROVALS: runtimeEnv.CODEX_BYPASS_APPROVALS || 'true',
  FEISHU_DIRECT_ALLOWED_OPEN_IDS: runtimeEnv.FEISHU_DIRECT_ALLOWED_OPEN_IDS || '',
  FEISHU_GROUP_ENABLED: runtimeEnv.FEISHU_GROUP_ENABLED || 'false',
};

function hostChildStdio() {
  const hostLogDir = runtimeEnv.FEISHU_BRIDGE_HOST_LOG_DIR;
  if (!hostLogDir) return { stdio: 'inherit', close: () => {} };
  fs.mkdirSync(hostLogDir, { recursive: true, mode: 0o700 });
  const stdoutFd = fs.openSync(path.join(hostLogDir, 'bridge.stdout.log'), 'a', 0o600);
  const stderrFd = fs.openSync(path.join(hostLogDir, 'bridge.stderr.log'), 'a', 0o600);
  return {
    stdio: ['ignore', stdoutFd, stderrFd],
    close() {
      fs.closeSync(stdoutFd);
      fs.closeSync(stderrFd);
    },
  };
}

const inhibitor = spawnSleepInhibitor({ projectRoot, mode: 'bridge', env });
const hostOutput = hostChildStdio();
const child = spawn(process.execPath, [path.join(projectRoot, 'bot-bridge.js')], {
  cwd: projectRoot,
  env,
  stdio: hostOutput.stdio,
});
hostOutput.close();
if (inhibitor) {
  inhibitor.once('error', (error) => {
    process.stderr.write(`sleep inhibitor failed: ${error.message}\n`);
    if (!child.killed) child.kill();
  });
  inhibitor.once('exit', (code, signal) => {
    if (!child.killed && child.exitCode === null) {
      process.stderr.write(`sleep inhibitor exited unexpectedly: ${code ?? signal ?? 'unknown'}\n`);
      child.kill();
    }
  });
}

function stop(signal) {
  if (!child.killed) child.kill(signal);
  if (inhibitor && !inhibitor.killed) inhibitor.kill();
}

process.on('SIGINT', () => stop('SIGINT'));
process.on('SIGTERM', () => stop('SIGTERM'));

child.on('exit', (code, signal) => {
  if (inhibitor && !inhibitor.killed) inhibitor.kill();
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code || 0);
});
