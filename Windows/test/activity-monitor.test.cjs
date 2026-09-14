'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { startActivityMonitor } = require('../src/activity-monitor.cjs');

test('hidden-host monitor refreshes on state changes only and stops on shutdown', async () => {
  const queue = [];
  let revision = 'running', reads = 0, stopped = false;
  startActivityMonitor({ readRevision: async () => revision, refresh: async () => { reads++; }, stopped: () => stopped, schedule: (fn, ms) => { assert.equal(ms, 1000); queue.push(fn); } });
  await queue.shift()();
  await queue.shift()();
  assert.equal(reads, 1);
  revision = 'completed';
  await queue.shift()();
  assert.equal(reads, 2);
  stopped = true;
  await queue.shift()();
  assert.equal(queue.length, 0);
});

test('a failed refresh does not acknowledge the new revision', async () => {
  const queue = [];
  let attempts = 0;
  const stop = startActivityMonitor({ readRevision: async () => 'completed', refresh: async () => { if (++attempts === 1) throw Error('offline'); }, stopped: () => false, schedule: fn => queue.push(fn) });
  await queue.shift()();
  await queue.shift()();
  assert.equal(attempts, 2);
  stop();
  await queue.shift()();
  assert.equal(queue.length, 0);
});
