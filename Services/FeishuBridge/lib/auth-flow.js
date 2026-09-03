const fs = require('node:fs');
const path = require('node:path');
const { spawn, spawnSync } = require('node:child_process');
const { REQUIRED_SCOPES } = require('./capability-policy');
const {
  chmodPrivate,
  commandForNodeScript,
  windowsPowerShellEnv,
} = require('./platform-runtime');

const AUTH_STATE_SCHEMA = 1;

function safeOneLine(value, limit = 500) {
  const text = String(value || '').replace(/\s+/g, ' ').trim();
  if (text.length <= limit) return text;
  return `${text.slice(0, Math.max(0, limit - 16))}...`;
}

function authDir(runtime) {
  const directory = path.join(runtime.dataRoot, 'auth');
  fs.mkdirSync(directory, { recursive: true, mode: 0o700 });
  chmodPrivate(directory, 0o700, { platform: runtime.platform });
  return directory;
}

function statePath(runtime, flow) {
  return path.join(authDir(runtime), `${flow}.json`);
}

function writeState(runtime, flow, value) {
  const filePath = statePath(runtime, flow);
  fs.writeFileSync(filePath, `${JSON.stringify({
    schemaVersion: AUTH_STATE_SCHEMA,
    ...value,
  }, null, 2)}\n`, { mode: 0o600 });
  chmodPrivate(filePath, 0o600, { platform: runtime.platform });
  return filePath;
}

function readState(runtime, flow) {
  return JSON.parse(fs.readFileSync(statePath(runtime, flow), 'utf8'));
}

function larkInvocation(runtime, args) {
  const invocation = commandForNodeScript(runtime.larkCliBin, args);
  return {
    command: invocation.command,
    args: invocation.args,
  };
}

function extractUrls(value, output = []) {
  if (typeof value === 'string') {
    const matches = value.match(/https?:\/\/[^\s"'<>]+/g) || [];
    output.push(...matches.map((url) => url.replace(/[),.]+$/, '')));
    return output;
  }
  if (Array.isArray(value)) {
    value.forEach((item) => extractUrls(item, output));
    return output;
  }
  if (value && typeof value === 'object') {
    Object.values(value).forEach((item) => extractUrls(item, output));
  }
  return output;
}

function firstVerificationUrl(value) {
  return extractUrls(value).find((url) => /open|feishu|larksuite|larkoffice|passport|oauth|device|verify/i.test(url))
    || extractUrls(value)[0]
    || '';
}

function findValue(value, names) {
  if (!value || typeof value !== 'object') return '';
  if (Array.isArray(value)) {
    for (const item of value) {
      const found = findValue(item, names);
      if (found) return found;
    }
    return '';
  }
  for (const name of names) {
    if (typeof value[name] === 'string' && value[name]) return value[name];
  }
  for (const item of Object.values(value)) {
    const found = findValue(item, names);
    if (found) return found;
  }
  return '';
}

function parseJsonOutput(result, label) {
  const stdout = String(result.stdout || '').replace(/^\uFEFF/, '').trim();
  const stderr = String(result.stderr || '').replace(/^\uFEFF/, '').trim();
  const text = stdout || stderr;
  let parsed;
  try {
    parsed = JSON.parse(text);
  } catch {
    parsed = { raw: text };
  }
  if (result.status !== 0) {
    throw new Error(`${label} failed: ${safeOneLine(text || result.error?.message)}`);
  }
  return parsed;
}

function redactSecrets(text, secrets = []) {
  let value = String(text || '');
  for (const secret of secrets.filter(Boolean)) {
    value = value.split(String(secret)).join('[redacted]');
  }
  return value;
}

function generateQr(runtime, url, flow) {
  if (!url) throw new Error('verification URL is unavailable');
  const directory = authDir(runtime);
  const fileName = `${flow}.png`;
  const qr = larkInvocation(runtime, ['auth', 'qrcode', url, '--output', fileName, '--size', '360']);
  const result = spawnSync(qr.command, qr.args, {
    cwd: directory,
    env: runtime.env,
    encoding: 'utf8',
    timeout: 10000,
  });
  if (result.status !== 0) {
    throw new Error(`QR code generation failed: ${safeOneLine(result.stderr || result.stdout || result.error?.message)}`);
  }
  const qrPath = path.join(directory, fileName);
  chmodPrivate(qrPath, 0o600, { platform: runtime.platform });
  return qrPath;
}

function existingConfigError() {
  return [
    'existing_app_credentials_required',
    'Reusing an existing Feishu bot requires an existing lark-cli profile, agent binding credentials, or one-time local secure App ID/App Secret input.',
    'QR-code OAuth can authorize a configured app, but it cannot reveal an existing app secret to this machine.',
    'Run with --create-new only if the user explicitly wants a new Feishu CLI application.',
  ].join(' ');
}

function configuredProfileStatus(runtime, profile = 'default') {
  const args = [];
  if (profile) args.push('--profile', String(profile));
  args.push('auth', 'status', '--json');
  const invocation = larkInvocation(runtime, args);
  const result = spawnSync(invocation.command, invocation.args, {
    cwd: runtime.projectRoot,
    env: runtime.env,
    encoding: 'utf8',
    timeout: 10000,
    maxBuffer: 128 * 1024,
  });
  if (result.status !== 0) {
    const text = String(result.stderr || result.stdout || result.error?.message || '').replace(/^\uFEFF/, '').trim();
    try {
      const parsed = JSON.parse(text);
      const type = [parsed?.error?.type, parsed?.error?.subtype].filter(Boolean).join('/');
      const message = safeOneLine(parsed?.error?.message || parsed?.message || 'not configured', 180);
      return {
        configured: false,
        detail: `${type || 'lark-cli'}: ${message}`,
      };
    } catch {
      // Do not pass through lark-cli remediation hints here; this runner owns the safe next step.
    }
    return {
      configured: false,
      detail: safeOneLine(text, 180),
    };
  }
  return { configured: true };
}

function validateExistingCredential({ appId, appSecret, brand }) {
  const id = String(appId || '').trim();
  const secret = String(appSecret || '').trim();
  const resolvedBrand = String(brand || 'feishu').trim().toLowerCase();
  if (!/^[A-Za-z0-9_-]{3,128}$/.test(id)) {
    throw new Error('payload.appId must be a Feishu App ID');
  }
  if (!secret) throw new Error('payload.appSecret is required');
  if (!['feishu', 'lark'].includes(resolvedBrand)) {
    throw new Error('payload.brand must be feishu or lark');
  }
  return { appId: id, appSecret: secret, brand: resolvedBrand };
}

function configureExisting(runtime, {
  appId,
  appSecret,
  brand = 'feishu',
  profile = 'default',
  storeSdkCredential = runtime.platform === 'win32',
} = {}) {
  const credential = validateExistingCredential({ appId, appSecret, brand });
  const invocation = larkInvocation(runtime, [
    'config', 'init',
    '--name', String(profile || 'default'),
    '--app-id', credential.appId,
    '--app-secret-stdin',
    '--brand', credential.brand,
    '--lang', 'zh_cn',
  ]);
  const result = spawnSync(invocation.command, invocation.args, {
    cwd: runtime.projectRoot,
    env: runtime.env,
    input: `${credential.appSecret}\n`,
    encoding: 'utf8',
    timeout: 60000,
    maxBuffer: 256 * 1024,
  });
  if (result.status !== 0) {
    const detail = safeOneLine(redactSecrets(result.stderr || result.stdout || result.error?.message, [credential.appSecret]));
    throw new Error(`lark-cli existing app configuration failed${detail ? `: ${detail}` : ''}`);
  }

  let windowsSdkCredential = 'not_required';
  if (runtime.platform === 'win32' && storeSdkCredential !== false) {
    const script = path.join(runtime.projectRoot, 'windows', 'Set-FeishuBridgeCredential.ps1');
    const dpapi = spawnSync('powershell.exe', [
      '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass',
      '-File', script,
      '-AppId', credential.appId,
      '-Brand', credential.brand,
      '-AppSecretStdin',
    ], {
      cwd: runtime.projectRoot,
      env: windowsPowerShellEnv(runtime.env),
      input: `${credential.appSecret}\n`,
      encoding: 'utf8',
      timeout: 30000,
      maxBuffer: 64 * 1024,
    });
    if (dpapi.status !== 0) {
      const detail = safeOneLine(redactSecrets(dpapi.stderr || dpapi.stdout || dpapi.error?.message, [credential.appSecret]));
      throw new Error(`Windows official SDK credential store failed${detail ? `: ${detail}` : ''}`);
    }
    windowsSdkCredential = 'stored';
  }

  return {
    status: 'configured',
    flow: 'existing-app',
    profile: String(profile || 'default'),
    brand: credential.brand,
    larkCliProfile: 'configured',
    windowsSdkCredential,
    next: 'run_auth_start_user_for_qr_oauth',
  };
}

function startConfig(runtime, { profile = 'default', timeoutMs = 15000, createNew = false } = {}) {
  if (!createNew) {
    const existing = configuredProfileStatus(runtime, profile);
    if (existing.configured) {
      return {
        status: 'configured',
        flow: 'existing-config',
        profile,
        next: 'run_auth_start_user_for_qr_oauth',
      };
    }
    throw new Error(`${existingConfigError()} Detection: ${existing.detail || 'lark-cli profile is not configured'}`);
  }
  const directory = authDir(runtime);
  const logPath = path.join(directory, 'config-init.log');
  const logFd = fs.openSync(logPath, 'w', 0o600);
  const args = ['config', 'init', '--new', '--name', profile, '--lang', 'zh_cn'];
  const invocation = larkInvocation(runtime, args);
  const child = spawn(invocation.command, invocation.args, {
    cwd: runtime.projectRoot,
    env: runtime.env,
    detached: true,
    windowsHide: true,
    stdio: ['ignore', logFd, logFd],
  });
  child.unref();
  fs.closeSync(logFd);

  const startedAt = Date.now();
  let verificationUrl = '';
  while (Date.now() - startedAt < timeoutMs) {
    if (fs.existsSync(logPath)) {
      verificationUrl = firstVerificationUrl(fs.readFileSync(logPath, 'utf8'));
      if (verificationUrl) break;
    }
    if (child.exitCode !== null) break;
    Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 100);
  }
  if (!verificationUrl) {
    throw new Error(`lark-cli config init did not return a verification URL yet; see private log: ${logPath}`);
  }
  const qrPath = generateQr(runtime, verificationUrl, 'config-init');
  const savedStatePath = writeState(runtime, 'config-init', {
    flow: 'config-init',
    profile,
    pid: child.pid,
    startedAt: new Date().toISOString(),
    verificationUrl,
    qrPath,
    logPath,
  });
  return {
    status: 'pending',
    flow: 'config-init',
    profile,
    pid: child.pid,
    verificationUrl,
    qrPath,
    statePath: savedStatePath,
    next: 'scan_qr_then_run_auth_start_user',
  };
}

function scopeArgument(scope) {
  if (!scope || scope === 'required') return REQUIRED_SCOPES.user.join(',');
  if (scope === 'recommend') return '';
  return String(scope);
}

function startUser(runtime, { scope = 'required' } = {}) {
  const args = ['auth', 'login', '--no-wait', '--json'];
  const resolvedScope = scopeArgument(scope);
  if (resolvedScope) args.push('--scope', resolvedScope);
  else args.push('--recommend');
  const invocation = larkInvocation(runtime, args);
  const result = spawnSync(invocation.command, invocation.args, {
    cwd: runtime.projectRoot,
    env: runtime.env,
    encoding: 'utf8',
    timeout: 15000,
    maxBuffer: 512 * 1024,
  });
  const parsed = parseJsonOutput(result, 'lark-cli auth login --no-wait');
  const verificationUrl = firstVerificationUrl(parsed);
  const deviceCode = findValue(parsed, ['device_code', 'deviceCode']);
  const userCode = findValue(parsed, ['user_code', 'userCode']);
  if (!verificationUrl || !deviceCode) {
    throw new Error('lark-cli auth login did not return a verification URL and device code');
  }
  const qrPath = generateQr(runtime, verificationUrl, 'user-oauth');
  const savedStatePath = writeState(runtime, 'user-oauth', {
    flow: 'user-oauth',
    startedAt: new Date().toISOString(),
    verificationUrl,
    userCode,
    deviceCode,
    qrPath,
    scope: resolvedScope ? 'required' : 'recommend',
    scopeCount: resolvedScope ? resolvedScope.split(',').filter(Boolean).length : undefined,
  });
  return {
    status: 'pending',
    flow: 'user-oauth',
    verificationUrl,
    userCode,
    qrPath,
    statePath: savedStatePath,
    next: 'scan_qr_then_run_auth_finish_user',
  };
}

function finishUser(runtime, { deviceCode = '' } = {}) {
  const saved = deviceCode ? {} : readState(runtime, 'user-oauth');
  const code = deviceCode || saved.deviceCode;
  if (!code) throw new Error('device code is unavailable; run auth start-user first');
  const invocation = larkInvocation(runtime, ['auth', 'login', '--device-code', code, '--json']);
  const result = spawnSync(invocation.command, invocation.args, {
    cwd: runtime.projectRoot,
    env: runtime.env,
    encoding: 'utf8',
    timeout: 120000,
    maxBuffer: 512 * 1024,
  });
  const parsed = parseJsonOutput(result, 'lark-cli auth login --device-code');
  return {
    status: 'completed',
    flow: 'user-oauth',
    result: parsed,
  };
}

module.exports = {
  configureExisting,
  extractUrls,
  finishUser,
  firstVerificationUrl,
  findValue,
  generateQr,
  startConfig,
  startUser,
};
