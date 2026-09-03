const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const {
  replyIdempotencyKey,
} = require('../lib/reply-policy');

test('reply idempotency keys stay stable for retries of the same phase and part', () => {
  const first = replyIdempotencyKey('om_source', 'processing', 0);
  const retry = replyIdempotencyKey('om_source', 'processing', 0);
  assert.equal(first, retry);
  assert.match(first, /^codex-[a-f0-9]{32}$/);
  assert.ok(first.length <= 50);
});

test('processing, final, and error replies never share an idempotency key', () => {
  const keys = new Set([
    replyIdempotencyKey('om_source', 'processing', 0),
    replyIdempotencyKey('om_source', 'final', 0),
    replyIdempotencyKey('om_source', 'error', 0),
  ]);
  assert.equal(keys.size, 3);
});

test('multipart replies receive distinct stable keys', () => {
  const first = replyIdempotencyKey('om_source', 'final', 0);
  const second = replyIdempotencyKey('om_source', 'final', 1);
  assert.notEqual(first, second);
  assert.equal(second, replyIdempotencyKey('om_source', 'final', 1));
});

test('reply idempotency policy rejects missing identity and invalid parts', () => {
  assert.throws(() => replyIdempotencyKey('', 'final', 0), /source message id/);
  assert.throws(() => replyIdempotencyKey('om_source', '', 0), /reply phase/);
  assert.throws(() => replyIdempotencyKey('om_source', 'final', -1), /part index/);
});

test('production reply flow assigns distinct processing, final, and error phases', () => {
  const source = fs.readFileSync(path.join(__dirname, '..', 'bot-bridge.js'), 'utf8');
  assert.match(source, /\{ phase: 'processing' \}/);
  assert.match(source, /: 'final';/);
  assert.match(source, /\{ phase: `error:\$\{route \|\| 'unknown'\}` \}/);
  assert.match(source, /if \(cardUpdated\) \{\s*return \{ mode: 'card'/);
  assert.match(source, /const delivery = await deliverProgressResult\(/);
});
