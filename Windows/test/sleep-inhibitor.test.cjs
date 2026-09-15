'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { sanitize } = require('../src/config-store.cjs');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { ConfigStore } = require('../src/config-store.cjs');

test('sleep selection persists across host restarts', (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ksfas-sleep-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const file = path.join(root, 'settings.json');
  new ConfigStore(file).update({ preventSleep: true });
  assert.equal(new ConfigStore(file).get().preventSleep, true);
});

test('host connects settings, startup restore and explicit exit release', () => {
  const source = fs.readFileSync(path.join(__dirname, '../src/main.cjs'), 'utf8');
  assert.match(source, /sleepInhibitor.setEnabled\(store.get\(\).preventSleep\)/);
  assert.match(source, /sleepInhibitor.setEnabled\(allowed.preventSleep === true\)/);
  assert.match(source.slice(source.indexOf("app.on('before-quit'")), /sleepInhibitor.setEnabled\(false\)/);
});

test('sleep preference is opt-in and strictly boolean', () => {
  assert.equal(sanitize({}).preventSleep, false);
  assert.equal(sanitize({ preventSleep: 'true' }).preventSleep, false);
  assert.equal(sanitize({ preventSleep: true }).preventSleep, true);
});

test('sleep inhibitor owns one application-suspension blocker and releases it', () => {
  const { SleepInhibitor } = require('../src/sleep-inhibitor.cjs');
  const calls = [];
  const blocker = new SleepInhibitor({
    start(type) { calls.push(type); return 0; },
    stop(id) { calls.push(id); },
    isStarted(id) { return id === 0; },
  });
  blocker.setEnabled(false);
  blocker.setEnabled(true);
  blocker.setEnabled(true);
  blocker.setEnabled(false);
  blocker.setEnabled(false);
  assert.deepEqual(calls, ['prevent-app-suspension', 0]);
});

test('failed acquisition is not treated as enabled and can retry', () => {
  const { SleepInhibitor } = require('../src/sleep-inhibitor.cjs');
  let fail = true;
  const blocker = new SleepInhibitor({
    start() { if (fail) throw new Error('denied'); return 1; },
    stop() {}, isStarted() { return true; },
  });
  assert.throws(() => blocker.setEnabled(true), /denied/);
  fail = false;
  blocker.setEnabled(true);
  blocker.setEnabled(false);
});
