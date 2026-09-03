'use strict';

const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const repoRoot = path.resolve(__dirname, '..', '..');

test('bundled Feishu runtime manifest is complete and pinned', () => {
  const result = spawnSync(process.execPath, ['scripts/prepare-feishu-runtime.mjs', '--verify'], {
    cwd: repoRoot,
    encoding: 'utf8',
  });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.stdout, /Node 24\.20\.0/);
});

test('Windows package copies Feishu production dependencies as an explicit resource', () => {
  const packageConfig = JSON.parse(fs.readFileSync(path.join(repoRoot, 'Windows', 'package.json'), 'utf8'));
  const resources = packageConfig.build.extraResources;
  assert.ok(resources.some((item) => item.from === '../dist/services/feishu-bridge/node_modules'
    && item.to === 'services/feishu-bridge/node_modules'));
});
