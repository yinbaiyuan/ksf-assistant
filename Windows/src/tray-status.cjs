'use strict';

const zlib = require('node:zlib');

const DEFAULT_TRAY_ICON_SOURCE_SIZE = 48;

const DIGITS = {
  '-': ['000', '000', '111', '000', '000'],
  0: ['111', '101', '101', '101', '111'],
  1: ['010', '110', '010', '010', '111'],
  2: ['111', '001', '111', '100', '111'],
  3: ['111', '001', '111', '001', '111'],
  4: ['101', '101', '111', '001', '001'],
  5: ['111', '100', '111', '001', '111'],
  6: ['111', '100', '111', '101', '111'],
  7: ['111', '001', '010', '010', '010'],
  8: ['111', '101', '111', '101', '111'],
  9: ['111', '101', '111', '001', '111'],
};

const CRC_TABLE = Array.from({ length: 256 }, (_, index) => {
  let value = index;
  for (let bit = 0; bit < 8; bit += 1) value = value & 1 ? 0xedb88320 ^ (value >>> 1) : value >>> 1;
  return value >>> 0;
});

function clampPercent(value) {
  return Math.max(0, Math.min(100, Math.round(value)));
}

function generalRemaining(snapshot) {
  const buckets = snapshot?.usage?.buckets || [];
  const bucket = buckets.find((item) => item.limitId === 'codex') || buckets[0];
  const windows = [bucket?.primary, bucket?.secondary]
    .filter((item) => Number.isFinite(item?.usedPercent));
  if (!windows.length) return null;
  return Math.min(...windows.map((item) => clampPercent(100 - item.usedPercent)));
}

function formatTokens(value) {
  if (!Number.isFinite(value)) return null;
  if (value >= 1_000_000_000) return `${(value / 1_000_000_000).toFixed(1)}B`;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`;
  return String(Math.round(value));
}

function buildTrayStatus(snapshot, now = new Date()) {
  const remainingPercent = generalRemaining(snapshot);
  const running = Math.max(0, Math.floor(snapshot?.activity?.runningCount || 0));
  const waiting = Math.max(0, Math.floor(snapshot?.activity?.waitingCount || 0));
  const today = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
  const local = snapshot?.usage?.localDailyUsage;
  const tokens = local?.startDate === today ? formatTokens(local.tokens) : null;
  const lines = [
    'KSFAssistant',
    remainingPercent == null ? '额度暂不可用' : `通用额度剩余 ${remainingPercent}%`,
    `${running} 个运行中，${waiting} 个等待`,
  ];
  if (tokens) lines.push(`本机今日 ${tokens} Token`);
  const connected = snapshot?.feishu?.connectedTaskCount;
  lines.push(Number.isInteger(connected) && connected >= 0 ? `${connected} 个任务已连接飞书` : '飞书连接任务数不可用');
  return {
    remainingPercent,
    label: remainingPercent == null ? '--' : String(remainingPercent),
    tooltip: lines.join('\n'),
  };
}

function pngChunk(type, data) {
  const typeBuffer = Buffer.from(type, 'ascii');
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length);
  let crc = 0xffffffff;
  for (const byte of Buffer.concat([typeBuffer, data])) crc = CRC_TABLE[(crc ^ byte) & 0xff] ^ (crc >>> 8);
  const checksum = Buffer.alloc(4);
  checksum.writeUInt32BE((crc ^ 0xffffffff) >>> 0);
  return Buffer.concat([length, typeBuffer, data, checksum]);
}

function trayIconDataURL(status, requestedSize = DEFAULT_TRAY_ICON_SOURCE_SIZE) {
  const percent = status.remainingPercent;
  const background = percent == null ? [95, 104, 115] : percent < 20 ? [196, 61, 61] : percent < 50 ? [193, 123, 22] : [24, 121, 78];
  const width = Math.max(16, Math.min(64, Math.round(requestedSize)));
  const height = width;
  const pixels = Buffer.alloc(width * height * 4);
  const radius = Math.max(3, Math.round(width / 6));
  const maximum = width - 1;
  for (let y = 0; y < height; y += 1) {
    for (let x = 0; x < width; x += 1) {
      const cornerX = x < radius ? radius : x > maximum - radius ? maximum - radius : x;
      const cornerY = y < radius ? radius : y > maximum - radius ? maximum - radius : y;
      if ((x - cornerX) ** 2 + (y - cornerY) ** 2 > radius ** 2) continue;
      const offset = (y * width + x) * 4;
      pixels.set([...background, 255], offset);
    }
  }

  const horizontalUnits = status.label.length * 3 + status.label.length - 1;
  // Reserve a two-digit slot even for a one-digit percentage. The colored
  // plate stays full-size while a lone digit no longer expands to fill it.
  const layoutUnits = Math.max(7, horizontalUnits);
  const verticalPadding = Math.max(2, Math.round(height * 0.16));
  const scaleX = Math.max(1, Math.floor((width - 2) / layoutUnits));
  const scaleY = Math.max(1, Math.floor((height - verticalPadding * 2) / 5));
  const totalWidth = horizontalUnits * scaleX;
  const startX = Math.floor((width - totalWidth) / 2);
  const startY = Math.floor((height - 5 * scaleY) / 2);
  for (const [characterIndex, character] of [...status.label].entries()) {
    const glyph = DIGITS[character];
    for (let row = 0; row < glyph.length; row += 1) {
      for (let column = 0; column < glyph[row].length; column += 1) {
        if (glyph[row][column] !== '1') continue;
        for (let dy = 0; dy < scaleY; dy += 1) {
          for (let dx = 0; dx < scaleX; dx += 1) {
            const x = startX + characterIndex * 4 * scaleX + column * scaleX + dx;
            const y = startY + row * scaleY + dy;
            pixels.set([255, 255, 255, 255], (y * width + x) * 4);
          }
        }
      }
    }
  }

  const header = Buffer.alloc(13);
  header.writeUInt32BE(width, 0);
  header.writeUInt32BE(height, 4);
  header.set([8, 6, 0, 0, 0], 8);
  const scanlines = Buffer.alloc(height * (1 + width * 4));
  for (let y = 0; y < height; y += 1) pixels.copy(scanlines, y * (1 + width * 4) + 1, y * width * 4, (y + 1) * width * 4);
  const png = Buffer.concat([
    Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]),
    pngChunk('IHDR', header),
    pngChunk('IDAT', zlib.deflateSync(scanlines)),
    pngChunk('IEND', Buffer.alloc(0)),
  ]);
  return `data:image/png;base64,${png.toString('base64')}`;
}

module.exports = { buildTrayStatus, trayIconDataURL };
