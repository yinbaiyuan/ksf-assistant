'use strict';

const path = require('node:path');

function taskURL(threadId) {
  if (typeof threadId !== 'string' || !threadId || /[\u0000-\u001f\u007f]/.test(threadId)) throw new Error('任务标识无效');
  return `codex://threads/${encodeURIComponent(threadId)}`;
}

function clamp(value, minimum, maximum) {
  return Math.min(maximum, Math.max(minimum, value));
}

function isPathInside(targetPath, rootPath) {
  const relative = path.relative(path.resolve(rootPath), path.resolve(targetPath));
  return relative === '' || (!relative.startsWith('..') && !path.isAbsolute(relative));
}

module.exports = { taskURL, clamp, isPathInside };
