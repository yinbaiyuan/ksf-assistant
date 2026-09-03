const assert = require('node:assert/strict');
const crypto = require('node:crypto');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const {
  decodeMacKeychainValue,
  decodeStrictBase64,
  decryptCredential,
  loadOfficialCredentials,
  safeCredentialFileName,
  selectProfile,
  readWindowsDpapiCredential,
} = require('../lib/lark-cli-credentials');

test('macOS go-keyring envelopes unwrap before lark-cli master-key decoding', () => {
  const key = crypto.randomBytes(32);
  const encodedKey = key.toString('base64');
  const wrapped = `go-keyring-base64:${Buffer.from(encodedKey).toString('base64')}`;
  const unwrapped = decodeMacKeychainValue(wrapped);
  assert.equal(unwrapped, encodedKey);
  assert.deepEqual(decodeStrictBase64(unwrapped, 'test key'), key);
  assert.equal(
    decodeMacKeychainValue(`go-keyring-encoded:${Buffer.from(encodedKey).toString('hex')}`),
    encodedKey,
  );
  assert.throws(() => decodeMacKeychainValue('go-keyring-base64:not-valid'), /envelope is invalid/);
});

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-credential-test-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function encryptCredential(plaintext, key) {
  const iv = crypto.randomBytes(12);
  const cipher = crypto.createCipheriv('aes-256-gcm', key, iv);
  const ciphertext = Buffer.concat([cipher.update(plaintext, 'utf8'), cipher.final()]);
  return Buffer.concat([iv, ciphertext, cipher.getAuthTag()]);
}

test('credential decryption matches lark-cli AES-256-GCM storage format', () => {
  const key = crypto.randomBytes(32);
  const encrypted = encryptCredential('private-secret', key);
  assert.equal(decryptCredential(encrypted, key), 'private-secret');
  assert.throws(() => decryptCredential(encrypted, crypto.randomBytes(32)));
});

test('official credentials reuse one selected lark-cli keychain profile without writing plaintext', (t) => {
  const homeDir = temporaryDirectory(t);
  const configDir = path.join(homeDir, '.lark-cli');
  const storageDir = path.join(homeDir, 'Library', 'Application Support', 'lark-cli');
  fs.mkdirSync(configDir, { recursive: true, mode: 0o700 });
  fs.mkdirSync(storageDir, { recursive: true, mode: 0o700 });
  const appId = 'cli_test_app';
  const secretAccount = `appsecret:${appId}`;
  fs.writeFileSync(path.join(configDir, 'config.json'), JSON.stringify({
    apps: [{
      name: 'default',
      appId,
      appSecret: { source: 'keychain', id: secretAccount },
      brand: 'feishu',
    }],
  }), { mode: 0o600 });
  const key = crypto.randomBytes(32);
  fs.writeFileSync(
    path.join(storageDir, safeCredentialFileName(secretAccount)),
    encryptCredential('test-app-secret', key),
    { mode: 0o600 },
  );

  const credential = loadOfficialCredentials({
    env: { LARK_CLI_PROFILE: 'default' },
    homeDir,
    masterKeyReader: () => Buffer.from(key),
  });
  assert.deepEqual(credential, {
    appId,
    appSecret: 'test-app-secret',
    brand: 'feishu',
    source: 'lark-cli-keychain',
  });
  assert.equal(fs.existsSync(path.join(storageDir, 'master.key.file')), false);
});

test('credential selection is fail-closed for ambiguous profiles and partial env overrides', () => {
  assert.throws(() => selectProfile({ apps: [{ name: 'a' }, { name: 'b' }] }), /ambiguous/);
  assert.equal(selectProfile({ apps: [{ name: 'a' }] }).name, 'a');
  assert.throws(
    () => loadOfficialCredentials({ env: { FEISHU_APP_ID: 'only-id' } }),
    /configured together/,
  );
});

test('Windows official SDK credentials are decrypted through a fixed DPAPI adapter', (t) => {
  const homeDir = temporaryDirectory(t);
  const credentialPath = path.join(homeDir, 'official-sdk.json');
  fs.writeFileSync(credentialPath, '{"protectedSecret":"opaque-dpapi-value"}');
  let captured;
  const credential = readWindowsDpapiCredential({
    env: { FEISHU_BRIDGE_DATA_DIR: homeDir, FEISHU_BRIDGE_CREDENTIAL_FILE: credentialPath },
    homeDir,
    projectRoot: 'C:\\bridge',
    spawnSyncFn(command, args, options) {
      captured = { command, args, options };
      return {
        status: 0,
        stdout: '{"appId":"cli_windows_app","appSecret":"private-secret","brand":"feishu"}',
      };
    },
  });
  assert.deepEqual(credential, {
    appId: 'cli_windows_app', appSecret: 'private-secret', brand: 'feishu', source: 'windows-dpapi',
  });
  assert.equal(captured.command, 'powershell.exe');
  assert.equal(captured.args.some((item) => item.includes('private-secret')), false);
  assert.equal(captured.options.env.FEISHU_BRIDGE_CREDENTIAL_FILE, credentialPath);
  assert.throws(() => readWindowsDpapiCredential({
    env: {
      FEISHU_BRIDGE_DATA_DIR: path.join(homeDir, 'private-root'),
      FEISHU_BRIDGE_CREDENTIAL_FILE: credentialPath,
    },
    homeDir,
    projectRoot: 'C:\\bridge',
  }), /must stay inside/);
});
