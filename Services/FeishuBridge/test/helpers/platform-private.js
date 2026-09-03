const assert = require('node:assert/strict');
const fs = require('node:fs');

function bridgeTestEnv(baseDir, overrides = {}) {
  const dataRoot = overrides.FEISHU_BRIDGE_DATA_DIR || baseDir;
  return {
    ...overrides,
    LARK_CLI_PROFILE: overrides.LARK_CLI_PROFILE ?? '',
    FEISHU_BRIDGE_DATA_DIR: dataRoot,
  };
}

function assertPrivateMode(filePath, expectedMode) {
  const stat = fs.statSync(filePath);
  if (process.platform === 'win32') {
    assert.ok(stat.isFile() || stat.isDirectory());
    return;
  }
  assert.equal(stat.mode & 0o777, expectedMode);
}

function assertNoPublicPermissionBits(filePath) {
  const stat = fs.statSync(filePath);
  if (process.platform === 'win32') {
    assert.ok(stat.isFile() || stat.isDirectory());
    return;
  }
  assert.equal(stat.mode & 0o077, 0);
}

module.exports = {
  assertNoPublicPermissionBits,
  assertPrivateMode,
  bridgeTestEnv,
};
