const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const { chmodPrivate } = require('./platform-runtime');

const DEFAULT_MAX_MEDIA_BYTES = 30 * 1024 * 1024;

function stagingDirectory(runtime, requestId) {
  const digest = crypto.createHash('sha256').update(String(requestId || '')).digest('hex').slice(0, 20);
  return path.join(runtime.mediaStagingDir, digest);
}

function stageOutboundMedia(runtime, requestId, sourcePath, { maxBytes = DEFAULT_MAX_MEDIA_BYTES } = {}) {
  const source = path.resolve(String(sourcePath || ''));
  const stat = fs.lstatSync(source);
  if (stat.isSymbolicLink() || !stat.isFile()) throw new Error('media source must be a regular file');
  if (stat.size <= 0) throw new Error('media source is empty');
  if (stat.size > maxBytes) throw new Error(`media source exceeds ${maxBytes} bytes`);
  const dir = stagingDirectory(runtime, requestId);
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  chmodPrivate(dir, 0o700);
  const safeName = path.basename(source).replace(/[^A-Za-z0-9._-]/g, '_').replace(/^\.+/, '') || 'upload.bin';
  const destination = path.join(dir, safeName);
  fs.copyFileSync(source, destination, fs.constants.COPYFILE_EXCL);
  chmodPrivate(destination, 0o600);
  return destination;
}

function cleanupStagedMedia(runtime, filePath) {
  if (!filePath) return false;
  const root = path.resolve(runtime.mediaStagingDir);
  const target = path.resolve(filePath);
  const relative = path.relative(root, target);
  if (!relative || relative.startsWith('..') || path.isAbsolute(relative)) return false;
  const requestDir = path.dirname(target);
  const requestRelative = path.relative(root, requestDir);
  if (!requestRelative || requestRelative.startsWith('..') || path.isAbsolute(requestRelative)) return false;
  fs.rmSync(requestDir, { recursive: true, force: true });
  return true;
}

module.exports = {
  DEFAULT_MAX_MEDIA_BYTES,
  cleanupStagedMedia,
  stageOutboundMedia,
};
