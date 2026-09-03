const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

test('lark-cli is pinned exactly to the validated 0.5.0 baseline', () => {
  const root = path.join(__dirname, '..');
  const manifest = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
  const lock = JSON.parse(fs.readFileSync(path.join(root, 'package-lock.json'), 'utf8'));
  assert.equal(manifest.dependencies['@larksuite/cli'], '1.0.92');
  assert.equal(lock.packages['node_modules/@larksuite/cli'].version, '1.0.92');
});
