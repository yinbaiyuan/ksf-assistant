'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const path = require('node:path');
const { taskURL, clamp, isPathInside } = require('../src/security.cjs');

test('task deep links encode ids and reject controls', () => {
  assert.equal(taskURL('thread / value'), 'codex://threads/thread%20%2F%20value');
  assert.throws(() => taskURL('bad\nthread'), /无效/);
});

test('local path boundary rejects prefix collisions', () => {
  const root = path.resolve('C:\\Users\\tester\\KSF');
  assert.equal(isPathInside(path.join(root, '10项目'), root), true);
  assert.equal(isPathInside(`${root}-copy`, root), false);
  assert.equal(clamp(12, 20, 30), 20);
});
