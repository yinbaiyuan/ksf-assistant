'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const { buildTrayStatus, trayIconDataURL } = require('../src/tray-status.cjs');

test('connected task count uses shared Core state and distinguishes unknown from zero', () => {
 assert.match(buildTrayStatus({feishu:{connectedTaskCount:3}}).tooltip,/3 个任务已连接飞书/);
 assert.match(buildTrayStatus({feishu:{connectedTaskCount:0}}).tooltip,/0 个任务已连接飞书/);
 assert.match(buildTrayStatus({feishu:{}}).tooltip,/飞书连接任务数不可用/);
});

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
  const png = Buffer.from(dataURL.split(',')[1], 'base64');
  assert.deepEqual(png.subarray(0, 8), Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]));
  assert.equal(png.readUInt32BE(16), 48);
  assert.equal(png.readUInt32BE(20), 48);
});

test('tray icon fills every Windows DPI representation without outer transparent padding', () => {
  const status = buildTrayStatus({ usage: { buckets: [{ primary: { usedPercent: 41 } }] } });
  for (const size of [16, 20, 24, 32]) {
    const png = Buffer.from(trayIconDataURL(status, size).split(',')[1], 'base64');
    assert.equal(png.readUInt32BE(16), size);
    assert.equal(png.readUInt32BE(20), size);
    const chunks = [];
    for (let offset = 8; offset < png.length;) {
      const length = png.readUInt32BE(offset);
      const type = png.subarray(offset + 4, offset + 8).toString('ascii');
      if (type === 'IDAT') chunks.push(png.subarray(offset + 8, offset + 8 + length));
      offset += 12 + length;
    }
    const scanlines = require('node:zlib').inflateSync(Buffer.concat(chunks));
    const alphaAt = (x, y) => scanlines[y * (1 + size * 4) + 1 + x * 4 + 3];
    assert.ok(Array.from({ length: size }, (_, x) => alphaAt(x, 0)).some(Boolean), `top edge missing at ${size}px`);
    assert.ok(Array.from({ length: size }, (_, x) => alphaAt(x, size - 1)).some(Boolean), `bottom edge missing at ${size}px`);
    assert.ok(Array.from({ length: size }, (_, y) => alphaAt(0, y)).some(Boolean), `left edge missing at ${size}px`);
    assert.ok(Array.from({ length: size }, (_, y) => alphaAt(size - 1, y)).some(Boolean), `right edge missing at ${size}px`);
  }
});

test('a single tray digit keeps the same compact scale as a two-digit value', () => {
  const decode = (status) => {
    const size = 24;
    const png = Buffer.from(trayIconDataURL(status, size).split(',')[1], 'base64');
    const chunks = [];
    for (let offset = 8; offset < png.length;) {
      const length = png.readUInt32BE(offset);
      if (png.subarray(offset + 4, offset + 8).toString('ascii') === 'IDAT') chunks.push(png.subarray(offset + 8, offset + 8 + length));
      offset += 12 + length;
    }
    const scanlines = require('node:zlib').inflateSync(Buffer.concat(chunks));
    const white = [];
    for (let y = 0; y < size; y += 1) {
      for (let x = 0; x < size; x += 1) {
        const offset = y * (1 + size * 4) + 1 + x * 4;
        if (scanlines[offset] === 255 && scanlines[offset + 1] === 255 && scanlines[offset + 2] === 255 && scanlines[offset + 3] === 255) white.push([x, y]);
      }
    }
    return {
      width: Math.max(...white.map(([x]) => x)) - Math.min(...white.map(([x]) => x)) + 1,
      height: Math.max(...white.map(([, y]) => y)) - Math.min(...white.map(([, y]) => y)) + 1,
    };
  };
  const oneDigit = decode({ remainingPercent: 8, label: '8' });
  const twoDigits = decode({ remainingPercent: 88, label: '88' });
  assert.equal(oneDigit.height, twoDigits.height);
  assert.ok(oneDigit.width < twoDigits.width * 0.6, `${oneDigit.width}px should stay compact beside ${twoDigits.width}px`);
  assert.ok(oneDigit.height <= 15, `single digit is too tall at ${oneDigit.height}px`);
});
