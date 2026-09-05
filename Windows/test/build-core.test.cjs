'use strict';

const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const path = require('node:path');
const test = require('node:test');

const windowsRoot = path.resolve(__dirname, '..');

test('core build explains when an explicit Go toolchain is unavailable', () => {
  const missingGo = path.join(windowsRoot, 'test', 'fixtures', 'missing-go.exe');
  const result = spawnSync(process.execPath, ['scripts/build-core.mjs'], {
    cwd: windowsRoot,
    encoding: 'utf8',
    env: { ...process.env, PATH: '', KSF_ASSISTANT_GO: missingGo },
  });

  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Go toolchain not found/i);
  assert.match(result.stderr, /go.*PATH/i);
});
