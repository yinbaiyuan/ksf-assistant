'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../src/renderer/app.js'), 'utf8');

test('main preserves the tray during forced refresh and clears it on RPC failure', async () => {
  const main = fs.readFileSync(path.join(__dirname, '../src/main.cjs'), 'utf8');
  const calls = [];
  const tray = [];
  let failRequest;
  const context = vm.createContext({
    dashboardPromise: null,
    store: { get: () => ({ pinnedProjectIds: [], pinnedWorkspaceIds: [] }) },
    core: { request: (method, params) => { calls.push([method, params.forceAccountRefresh]); return new Promise((_resolve, reject) => { failRequest = reject; }); } },
    updateTrayStatus: (snapshot) => tray.push(snapshot),
  });
  vm.runInContext(main.slice(main.indexOf('function dashboardParams('), main.indexOf('function showWindowWhenReady(')), context);
  const refresh = context.readDashboard(true);
  assert.equal(tray.length, 0);
  failRequest(new Error('offline'));
  await assert.rejects(refresh, /offline/);
  assert.deepEqual(calls, [['dashboard/read', true]]);
  assert.equal(tray.at(-1), null);
  assert.equal(context.dashboardPromise, null);
});

test('failed dashboard refresh hides account data but retains device usage', async () => {
  const usage = { buckets: [{ limitId: 'codex' }], tokenSummary: { lifetimeTokens: 300 }, dailyUsageBuckets: [{ tokens: 300 }], localDailyUsage: { tokens: 35 }, localDailyCost: { totalMicroUsd: 100 } };
  const context = vm.createContext({
    state: { dashboard: { usage }, refreshing: false },
    api: { dashboard: async () => { throw new Error('offline'); } },
    render() {}, showToast() {}, scheduleRefresh() {},
  });
  vm.runInContext(source.slice(source.indexOf('async function refreshDashboard('), source.indexOf('function scheduleRefresh()')), context);
  await context.refreshDashboard();
  assert.equal(context.state.dashboard.usage.buckets.length, 0);
  assert.equal(context.state.dashboard.usage.dailyUsageBuckets.length, 0);
  assert.equal(context.state.dashboard.usage.tokenSummary, null);
  assert.equal(context.state.dashboard.usage.localDailyUsage.tokens, 35);
  assert.equal(context.state.dashboard.usage.localDailyCost.totalMicroUsd, 100);
});

test('foreground refresh requests a fresh account sample', async () => {
  const calls = [];
  const context = vm.createContext({
    state: { dashboard: null, refreshing: false },
    api: { dashboard: async (force) => { calls.push(force); return { usage: { buckets: [] } }; } },
    render() {}, showToast() {}, scheduleRefresh() {},
  });
  vm.runInContext(source.slice(source.indexOf('async function refreshDashboard('), source.indexOf('function scheduleRefresh()')), context);
  await context.refreshDashboard({ forceAccountRefresh: true });
  assert.equal(calls[0], true);
});

test('forced refresh keeps the last account sample visible while loading', async () => {
  let finishRequest;
  const previous = { usage: { buckets: [{ remaining: 80 }], tokenSummary: { lifetimeTokens: 300 }, dailyUsageBuckets: [{ tokens: 300 }] } };
  const context = vm.createContext({
    state: { dashboard: previous, loading: false, refreshing: false },
    api: { dashboard: () => new Promise((resolve) => { finishRequest = resolve; }) },
    render() {}, showToast() {}, scheduleRefresh() {},
  });
  vm.runInContext(source.slice(source.indexOf('async function refreshDashboard('), source.indexOf('function scheduleRefresh()')), context);
  const refresh = context.refreshDashboard({ forceAccountRefresh: true });
  assert.equal(context.state.refreshing, true);
  assert.equal(context.state.dashboard.usage.buckets[0].remaining, 80);
  assert.equal(context.state.dashboard.usage.tokenSummary.lifetimeTokens, 300);
  finishRequest({ usage: { buckets: [{ remaining: 99 }] } });
  await refresh;
  assert.equal(context.state.dashboard.usage.buckets[0].remaining, 99);
});

test('account refresh queued during an older request discards the older response', async () => {
  const calls = [];
  let finishOld;
  const oldRequest = new Promise((resolve) => { finishOld = resolve; });
  const context = vm.createContext({
    state: { dashboard: { usage: { buckets: [{ remaining: 80 }] } }, refreshing: false },
    api: { dashboard: (force) => { calls.push(force); return calls.length === 1 ? oldRequest : Promise.resolve({ usage: { buckets: [{ remaining: 100 }] } }); } },
    render() {}, showToast() {}, scheduleRefresh() {},
  });
  vm.runInContext(source.slice(source.indexOf('async function refreshDashboard('), source.indexOf('function scheduleRefresh()')), context);
  const refreshing = context.refreshDashboard({ quiet: true });
  await context.refreshDashboard({ quiet: true, forceAccountRefresh: true });
  finishOld({ usage: { buckets: [{ remaining: 80 }] } });
  await refreshing;
  assert.deepEqual(calls, [false, true]);
  assert.equal(context.state.dashboard.usage.buckets[0].remaining, 100);
});
