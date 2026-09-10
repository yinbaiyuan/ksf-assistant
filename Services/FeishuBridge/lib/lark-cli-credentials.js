const crypto = require('node:crypto');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { defaultDataRoot, privatePathBoundary, windowsPowerShellEnv } = require('./platform-runtime');

const LARK_CLI_KEYCHAIN_SERVICE = 'ksfassistant-lark-cli';
const LARK_CLI_MASTER_KEY_ACCOUNT = 'master.key';
const MASTER_KEY_BYTES = 32;
const GCM_IV_BYTES = 12;
const GCM_TAG_BYTES = 16;
const GO_KEYRING_BASE64_PREFIX = 'go-keyring-base64:';
const GO_KEYRING_HEX_PREFIX = 'go-keyring-encoded:';

function assertPrivateRegularFile(filePath, label) {
  const stat = fs.lstatSync(filePath);
  if (!stat.isFile() || stat.isSymbolicLink()) {
    throw new Error(`${label} must be a regular file`);
  }
  if (process.platform !== 'win32' && (stat.mode & 0o077) !== 0) {
    throw new Error(`${label} permissions must not allow group or other access`);
  }
}

function safeCredentialFileName(account) {
  return `${String(account || '').replace(/[^a-zA-Z0-9._-]/g, '_')}.enc`;
}

function decryptCredential(data, key) {
  if (!Buffer.isBuffer(data) || data.length < GCM_IV_BYTES + GCM_TAG_BYTES) {
    throw new Error('encrypted credential is invalid');
  }
  if (!Buffer.isBuffer(key) || key.length !== MASTER_KEY_BYTES) {
    throw new Error('lark-cli master key is invalid');
  }
  const iv = data.subarray(0, GCM_IV_BYTES);
  const authTag = data.subarray(data.length - GCM_TAG_BYTES);
  const ciphertext = data.subarray(GCM_IV_BYTES, data.length - GCM_TAG_BYTES);
  const decipher = crypto.createDecipheriv('aes-256-gcm', key, iv);
  decipher.setAuthTag(authTag);
  return Buffer.concat([decipher.update(ciphertext), decipher.final()]).toString('utf8');
}

function decodeStrictBase64(value, label) {
  const encoded = String(value || '').trim();
  if (!encoded || encoded.length % 4 !== 0 || !/^[A-Za-z0-9+/]+={0,2}$/.test(encoded)) {
    throw new Error(`${label} is invalid`);
  }
  const decoded = Buffer.from(encoded, 'base64');
  const normalizedInput = encoded.replace(/=+$/, '');
  const normalizedOutput = decoded.toString('base64').replace(/=+$/, '');
  if (normalizedInput !== normalizedOutput) {
    decoded.fill(0);
    throw new Error(`${label} is invalid`);
  }
  return decoded;
}

function decodeMacKeychainValue(value) {
  const stored = String(value || '').trim();
  if (stored.startsWith(GO_KEYRING_BASE64_PREFIX)) {
    const decoded = decodeStrictBase64(
      stored.slice(GO_KEYRING_BASE64_PREFIX.length),
      'macOS Keychain envelope',
    );
    try {
      return decoded.toString('utf8');
    } finally {
      decoded.fill(0);
    }
  }
  if (stored.startsWith(GO_KEYRING_HEX_PREFIX)) {
    const encoded = stored.slice(GO_KEYRING_HEX_PREFIX.length);
    if (!encoded || encoded.length % 2 !== 0 || !/^[0-9a-fA-F]+$/.test(encoded)) {
      throw new Error('macOS Keychain envelope is invalid');
    }
    const decoded = Buffer.from(encoded, 'hex');
    try {
      return decoded.toString('utf8');
    } finally {
      decoded.fill(0);
    }
  }
  return stored;
}

function readSystemMasterKey() {
  if (process.platform !== 'darwin') {
    throw new Error('lark-cli system Keychain credentials require macOS');
  }
  const result = spawnSync('/usr/bin/security', [
    'find-generic-password',
    '-s',
    LARK_CLI_KEYCHAIN_SERVICE,
    '-a',
    LARK_CLI_MASTER_KEY_ACCOUNT,
    '-w',
  ], {
    encoding: 'utf8',
    timeout: 5000,
    maxBuffer: 4096,
  });
  if (result.status !== 0) {
    throw new Error('lark-cli master key is unavailable from the macOS Keychain');
  }
  const encodedKey = decodeMacKeychainValue(result.stdout);
  const key = decodeStrictBase64(encodedKey, 'lark-cli master key');
  if (key.length !== MASTER_KEY_BYTES) {
    key.fill(0);
    throw new Error('lark-cli master key is invalid');
  }
  return key;
}

function selectProfile(config, profileName = '') {
  const apps = Array.isArray(config?.apps) ? config.apps : [];
  if (!apps.length) throw new Error('lark-cli has no configured application profile');
  if (profileName) {
    const selected = apps.find((item) => item?.name === profileName);
    if (!selected) throw new Error(`lark-cli profile is not configured: ${profileName}`);
    return selected;
  }
  if (config.currentApp) {
    const selected = apps.find((item) => item?.name === config.currentApp);
    if (selected) return selected;
  }
  if (apps.length === 1) return apps[0];
  throw new Error('lark-cli profile is ambiguous; set LARK_CLI_PROFILE');
}

function resolveSecretReference(appSecret) {
  if (typeof appSecret === 'string') return { source: 'plain', value: appSecret };
  const ref = appSecret?.ref || appSecret;
  return {
    source: String(ref?.source || ''),
    id: String(ref?.id || ''),
  };
}

function loadLarkCliMasterKey(storageDir, masterKeyReader) {
  const filePath = path.join(storageDir, 'master.key.file');
  if (fs.existsSync(filePath)) {
    assertPrivateRegularFile(filePath, 'lark-cli master key file');
    const key = fs.readFileSync(filePath);
    if (key.length !== MASTER_KEY_BYTES) {
      key.fill(0);
      throw new Error('lark-cli master key file is invalid');
    }
    return key;
  }
  return masterKeyReader();
}

function readWindowsDpapiCredential({
  env,
  homeDir,
  projectRoot,
  spawnSyncFn = spawnSync,
} = {}) {
  const dataRoot = defaultDataRoot({ platform: 'win32', env, homeDir });
  const credentialPath = env.FEISHU_BRIDGE_CREDENTIAL_FILE
    ? path.resolve(env.FEISHU_BRIDGE_CREDENTIAL_FILE)
    : path.join(dataRoot, 'credentials', 'official-sdk.json');
  const boundary = privatePathBoundary(dataRoot, { credentialPath }, 'win32');
  if (!boundary.secure) throw new Error('Windows bridge credential must stay inside FEISHU_BRIDGE_DATA_DIR');
  if (!fs.existsSync(credentialPath)) return null;
  const stat = fs.lstatSync(credentialPath);
  if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('Windows bridge credential must be a regular file');
  const script = path.join(projectRoot, 'windows', 'Read-FeishuBridgeCredential.ps1');
  const result = spawnSyncFn('powershell.exe', [
    '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', script,
  ], {
    encoding: 'utf8', timeout: 10000, maxBuffer: 64 * 1024,
    env: windowsPowerShellEnv({ ...env, FEISHU_BRIDGE_CREDENTIAL_FILE: credentialPath }),
  });
  if (result.status !== 0) throw new Error('Windows DPAPI credential could not be decrypted for the current user');
  let parsed;
  try {
    parsed = JSON.parse(String(result.stdout || '').replace(/^\uFEFF/, '').trim());
  } catch {
    throw new Error('Windows DPAPI credential returned invalid data');
  }
  const appId = String(parsed.appId || '').trim();
  const appSecret = String(parsed.appSecret || '').trim();
  if (!appId || !appSecret) throw new Error('Windows DPAPI credential is incomplete');
  return {
    appId,
    appSecret,
    brand: String(parsed.brand || 'feishu').toLowerCase(),
    source: 'windows-dpapi',
  };
}

function loadOfficialCredentials({
  env = process.env,
  homeDir = os.homedir(),
  masterKeyReader = readSystemMasterKey,
  platform = process.platform,
  projectRoot = path.resolve(__dirname, '..'),
  spawnSyncFn = spawnSync,
} = {}) {
	const dataRoot = defaultDataRoot({ platform, env, homeDir });
	const configDir = path.join(dataRoot, 'lark-cli');
  const configPath = path.join(configDir, 'config.json');
  assertPrivateRegularFile(configPath, 'lark-cli config');
  const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));
  const selected = selectProfile(config, 'default');
  const appId = String(selected?.appId || '').trim();
  if (!appId) throw new Error('lark-cli profile has no app id');

  const secretRef = resolveSecretReference(selected.appSecret);
  if (secretRef.source === 'plain') {
    if (!secretRef.value) throw new Error('lark-cli profile has no app secret');
    return {
      appId,
      appSecret: secretRef.value,
      brand: String(selected.brand || 'feishu').toLowerCase(),
      source: 'lark-cli-config',
    };
  }
  if (secretRef.source === 'file') {
    const secretPath = path.resolve(secretRef.id);
    assertPrivateRegularFile(secretPath, 'lark-cli secret file');
    const appSecret = fs.readFileSync(secretPath, 'utf8').trim();
    if (!appSecret) throw new Error('lark-cli secret file is empty');
    return {
      appId,
      appSecret,
      brand: String(selected.brand || 'feishu').toLowerCase(),
      source: 'lark-cli-file',
    };
  }
  if (secretRef.source !== 'keychain' || !secretRef.id) {
    throw new Error('lark-cli profile has an unsupported app secret reference');
  }
  if (secretRef.id !== `appsecret:${appId}`) {
    throw new Error('lark-cli app id and app secret reference do not match');
  }

  const storageDir = path.join(homeDir, 'Library', 'Application Support', LARK_CLI_KEYCHAIN_SERVICE);
  const encryptedPath = path.join(storageDir, safeCredentialFileName(secretRef.id));
  assertPrivateRegularFile(encryptedPath, 'lark-cli encrypted credential');
  const encrypted = fs.readFileSync(encryptedPath);
  const masterKey = loadLarkCliMasterKey(storageDir, masterKeyReader);
  try {
    const appSecret = decryptCredential(encrypted, masterKey);
    if (!appSecret) throw new Error('lark-cli encrypted credential is empty');
    return {
      appId,
      appSecret,
      brand: String(selected.brand || 'feishu').toLowerCase(),
      source: 'lark-cli-keychain',
    };
  } finally {
    masterKey.fill(0);
    encrypted.fill(0);
  }
}

module.exports = {
  decodeMacKeychainValue,
  decodeStrictBase64,
  decryptCredential,
  loadOfficialCredentials,
  readWindowsDpapiCredential,
  safeCredentialFileName,
  selectProfile,
};
