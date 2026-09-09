'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const source = fs.readFileSync(path.join(__dirname, '../src/renderer/app.js'), 'utf8');

function render(usage) {
  const context = vm.createContext({
    Date: class extends Date {
      constructor(...args) { super(...(args.length ? args : [2026, 8, 9, 0, 0, 1])); }
    },
    selectedPricingPlan: () => ({}),
    compactPricingName: () => 'fixture',
    escapeHTML: String,
    formatTokens: String,
    accountLatestLabel: (date) => `账号最新 ${date}`,
    formatCostEstimate: (cost) => cost ? `$${cost.totalMicroUsd / 1e6}` : '—',
  });
  vm.runInContext(source.slice(source.indexOf('function renderTokens('), source.indexOf('function renderHistoryPage(')), context);
  return context.renderTokens(usage);
}

test('cached previous dates never appear as today tokens or cost', () => {
  const html = render({
    localDailyUsage: { startDate: '2026-09-08', tokens: 120 },
    localPreviousDailyUsage: { startDate: '2026-09-07', tokens: 90 },
    localDailyCost: { totalMicroUsd: 157110000 },
    dailyUsageBuckets: [{ startDate: '2026-09-07', tokens: 304600000 }],
  });
  assert.doesNotMatch(html, /157\.11|>120<|>90</);
  assert.match(html, /账号最新 2026-09-07/);
  assert.match(html, /今日 —/);
});

test('fresh zero usage remains zero while missing local usage stays unavailable', () => {
  const html = render({
    localDailyUsage: { startDate: '2026-09-09', tokens: 0, breakdown: { regularInputTokens: 0, cachedInputTokens: 0, outputTokens: 0 } },
    localPreviousDailyUsage: { startDate: '2026-09-08', tokens: 120 },
    localDailyCost: { totalMicroUsd: 0 },
  });
  assert.match(html, /今日 \$0/);
  assert.match(html, /本机昨日<\/div><div class="metric-value">120/);
  assert.match(html, /本机今日<\/div><div class="metric-value">0/);
  assert.match(render({ localDailyCost: { totalMicroUsd: 157110000 } }), /今日 —/);
});
