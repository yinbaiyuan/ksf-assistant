'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const { buildTrayStatus, trayIconDataURL } = require('../src/tray-status.cjs');

test('tray status exposes the general quota and live activity', () => {
  const status = buildTrayStatus({
    usage: {
      buckets: [
        { limitId: 'codex_bengalfox', primary: { usedPercent: 12 } },
        { limitId: 'codex', primary: { usedPercent: 27 }, secondary: { usedPercent: 41 } },
      ],
      localDailyUsage: { startDate: '2026-09-09', tokens: 81_596_442 },
    },
    activity: { runningCount: 2, waitingCount: 1 },
  }, new Date(2026, 8, 9, 11, 38, 56));

  assert.equal(status.remainingPercent, 59);
  assert.equal(status.label, '59');
  assert.match(status.tooltip, /通用额度剩余 59%/);
  assert.match(status.tooltip, /2 个运行中，1 个等待/);
  assert.match(status.tooltip, /本机今日 81\.6M Token/);
});

test('tray never labels a cached prior-day amount as today', () => {
  const now = new Date(2026, 8, 9, 0, 0, 1);
  const stale = buildTrayStatus({ usage: { localDailyUsage: { startDate: '2026-09-08', tokens: 120 } } }, now);
  assert.doesNotMatch(stale.tooltip, /本机今日/);
  const zero = buildTrayStatus({ usage: { localDailyUsage: { startDate: '2026-09-09', tokens: 0 } } }, now);
  assert.match(zero.tooltip, /本机今日 0 Token/);
});

test('tray icon is a colored PNG data URL and unavailable data stays explicit', () => {
  const status = buildTrayStatus({ usage: { buckets: [] }, activity: {} });
  assert.equal(status.remainingPercent, null);
  assert.equal(status.label, '--');
  assert.match(status.tooltip, /额度暂不可用/);
  const dataURL = trayIconDataURL(status);
  assert.match(dataURL, /^data:image\/png;base64,/);
  assert.deepEqual(Buffer.from(dataURL.split(',')[1], 'base64').subarray(0, 8), Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
});
