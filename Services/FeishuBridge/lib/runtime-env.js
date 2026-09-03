const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const MAXIMUM_PRIVATE_JSON_BYTES = 1024 * 1024;

function readEnvironmentFile(filePath) {
  if (!fs.existsSync(filePath)) return {};
  const values = {};
  for (const line of fs.readFileSync(filePath, 'utf8').split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const index = trimmed.indexOf('=');
    if (index <= 0) continue;
    const key = trimmed.slice(0, index).trim();
    if (!/^[A-Z][A-Z0-9_]*$/.test(key)) continue;
    let value = trimmed.slice(index + 1).trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    values[key] = value;
  }
  return values;
}

function booleanEnvironment(value) {
  return value ? 'true' : 'false';
}

function assertExactKeys(value, allowed, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`invalid ${label} in managed Feishu settings`);
  }
  for (const key of Object.keys(value)) {
    if (!allowed.includes(key)) throw new Error(`unknown ${label} field in managed Feishu settings`);
  }
}

function managedSwitch(settings, key, { dryRun = false } = {}) {
  const value = settings[key];
  assertExactKeys(value, dryRun ? ['enabled', 'dryRun'] : ['enabled'], key);
  if (typeof value.enabled !== 'boolean' || (dryRun && typeof value.dryRun !== 'boolean')) {
    throw new Error(`invalid ${key} switch in managed Feishu settings`);
  }
  return value;
}

function readManagedSettings(filePath) {
  let info;
  try {
    info = fs.lstatSync(filePath);
  } catch (error) {
    if (error.code === 'ENOENT') return {};
    throw error;
  }
  if (!info.isFile() || info.isSymbolicLink() || info.size > MAXIMUM_PRIVATE_JSON_BYTES) {
    throw new Error('unsafe managed Feishu settings file');
  }
  if (process.platform !== 'win32' && (info.mode & 0o077) !== 0) {
    throw new Error('insecure managed Feishu settings permissions');
  }
  const settings = JSON.parse(fs.readFileSync(filePath, 'utf8'));
  assertExactKeys(settings, [
    'version', 'profile', 'group', 'outbound', 'directory', 'groupDirectory',
    'docbox', 'actionbox', 'codex',
  ], 'root');
  if (settings.version !== 1 || !['primary', 'manual-only'].includes(settings.profile)) {
    throw new Error('unsupported managed Feishu settings version or profile');
  }
  const group = managedSwitch(settings, 'group');
  const outbound = managedSwitch(settings, 'outbound', { dryRun: true });
  const directory = managedSwitch(settings, 'directory');
  const groupDirectory = managedSwitch(settings, 'groupDirectory');
  const docbox = managedSwitch(settings, 'docbox', { dryRun: true });
  const actionbox = managedSwitch(settings, 'actionbox', { dryRun: true });
  assertExactKeys(settings.codex, ['defaultThreadTitle'], 'codex');
  const title = settings.codex.defaultThreadTitle;
  if (typeof title !== 'string' || !title || title.length > 200 || /[\u0000-\u001f\u007f]/.test(title)) {
    throw new Error('invalid Codex title in managed Feishu settings');
  }
  return {
    FEISHU_GROUP_ENABLED: booleanEnvironment(group.enabled),
    FEISHU_OUTBOUND_ENABLED: booleanEnvironment(outbound.enabled),
    FEISHU_OUTBOUND_DRY_RUN: booleanEnvironment(outbound.dryRun),
    FEISHU_DIRECTORY_ENABLED: booleanEnvironment(directory.enabled),
    FEISHU_GROUP_DIRECTORY_ENABLED: booleanEnvironment(groupDirectory.enabled),
    FEISHU_DOCBOX_ENABLED: booleanEnvironment(docbox.enabled),
    FEISHU_DOCBOX_DRY_RUN: booleanEnvironment(docbox.dryRun),
    FEISHU_ACTIONBOX_ENABLED: booleanEnvironment(actionbox.enabled),
    FEISHU_ACTIONBOX_DRY_RUN: booleanEnvironment(actionbox.dryRun),
    CODEX_FEISHU_DEFAULT_THREAD_TITLE: title,
  };
}

function loadRuntimeEnvironment(projectRoot, { env = process.env, homeDir = os.homedir() } = {}) {
  const local = readEnvironmentFile(path.join(projectRoot, '.env.local'));
  const initial = { ...local, ...env };
  if (initial.CODEX_USAGE_BAR_MANAGED !== '1') return initial;
  const dataRoot = initial.FEISHU_BRIDGE_DATA_DIR
    ? path.resolve(initial.FEISHU_BRIDGE_DATA_DIR)
    : path.join(homeDir, '.config', 'feishu-bridge');
  const privateValues = readEnvironmentFile(path.join(dataRoot, 'runtime.env'));
  const managedSettings = readManagedSettings(path.join(dataRoot, 'feishu-settings-v1.json'));
  return { ...local, ...privateValues, ...env, ...managedSettings };
}

module.exports = { loadRuntimeEnvironment, readEnvironmentFile, readManagedSettings };
