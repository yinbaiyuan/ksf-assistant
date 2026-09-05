const fs = require('node:fs');
const path = require('node:path');

const PROTOCOL = 'ksfassistant-host-context-v1';
const SCHEMA_VERSION = 1;
const STATES = new Set(['not_configured', 'ready', 'invalid']);
const MAX_BYTES = 64 * 1024;

class HostContextError extends Error {
  constructor(code, message) {
    super(message);
    this.name = 'HostContextError';
    this.code = code;
  }
}

function readHostContext(filePath, { platform = process.platform, dataRoot = '' } = {}) {
  const configuredPath = String(filePath || '').trim();
  if (!path.isAbsolute(configuredPath)) {
    throw new HostContextError('host_context_unavailable', 'KSFAssistant 宿主上下文不可用，请重新启动应用。');
  }
  if (dataRoot && path.resolve(configuredPath) !== path.join(path.resolve(dataRoot), 'ksfassistant-host-context-v1.json')) {
    throw new HostContextError('host_context_unsafe', 'KSFAssistant 宿主上下文路径越界。');
  }
  let stat;
  try {
    stat = fs.lstatSync(configuredPath);
  } catch {
    throw new HostContextError('host_context_unavailable', 'KSFAssistant 宿主上下文不可用，请重新启动应用。');
  }
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size > MAX_BYTES
    || (platform !== 'win32' && (stat.mode & 0o077) !== 0)) {
    throw new HostContextError('host_context_unsafe', 'KSFAssistant 宿主上下文不安全，请重新启动应用。');
  }
  let value;
  try {
    value = JSON.parse(fs.readFileSync(configuredPath, 'utf8'));
  } catch {
    throw new HostContextError('host_context_invalid', 'KSFAssistant 宿主上下文已损坏，请重新启动应用。');
  }
  const state = String(value?.ksf?.state || '');
  if (value?.protocol !== PROTOCOL || value?.schemaVersion !== SCHEMA_VERSION || !STATES.has(state)) {
    throw new HostContextError('host_context_invalid', 'KSFAssistant 宿主上下文无法识别，请重新启动应用。');
  }
  if (state === 'ready') {
    const root = String(value.ksf.root || '');
    if (!path.isAbsolute(root)) throw new HostContextError('host_context_invalid', 'KSF 宿主目录无效。');
    let realRoot;
    try {
      realRoot = fs.realpathSync(root);
      if (!fs.statSync(realRoot).isDirectory()) throw new Error('not a directory');
    } catch {
      throw new HostContextError(
        'ksf_invalid',
        'KSF 目录已失效。请在 KSFAssistant → 设置 → KSF 知识库重新选择目录，然后重新发送本消息。',
      );
    }
    return { ...value, ksf: { state, root: realRoot } };
  }
  if (value?.ksf?.root) throw new HostContextError('host_context_invalid', '非就绪 KSF 上下文包含目录。');
  return value;
}

function resolveRootMessageWorkspace({
  managed,
  hostContextPath,
  fallbackWorkspace,
  dataRoot,
}) {
  if (!managed) return { cwd: fallbackWorkspace, ksfState: 'developer', ksfRoot: '' };
  const context = readHostContext(hostContextPath, { dataRoot });
  if (context.ksf.state === 'invalid') {
    throw new HostContextError(
      'ksf_invalid',
      'KSF 目录已失效。请在 KSFAssistant → 设置 → KSF 知识库重新选择目录，然后重新发送本消息。',
    );
  }
  if (context.ksf.state === 'ready') {
    return { cwd: context.ksf.root, ksfState: 'ready', ksfRoot: context.ksf.root };
  }
  return { cwd: fallbackWorkspace, ksfState: 'not_configured', ksfRoot: '' };
}

function firstMessageTaskTitle(text, messageType = '', maximum = 64) {
  const prefix = '飞书 · ';
  const lines = String(text || '').normalize('NFKC').split(/\r?\n/u);
  let summary = lines.find((line) => line.trim()) || '';
  summary = summary.trim().replace(/\s+/gu, ' ');
  if (!summary) {
    const attachmentNames = {
      image: '图片', file: '文件', audio: '音频', media: '视频', post: '富文本', sticker: '贴纸',
    };
    summary = attachmentNames[String(messageType || '').toLowerCase()] || '飞书任务';
  }
  const segmenter = typeof Intl.Segmenter === 'function'
    ? new Intl.Segmenter('zh-CN', { granularity: 'grapheme' }) : null;
  const units = (value) => (segmenter
    ? [...segmenter.segment(value)].map((item) => item.segment)
    : Array.from(value));
  const room = Math.max(0, maximum - units(prefix).length);
  const truncated = units(summary).slice(0, room).join('').trim();
  return `${prefix}${truncated || '飞书任务'}`;
}

module.exports = {
  HostContextError,
  PROTOCOL,
  SCHEMA_VERSION,
  firstMessageTaskTitle,
  readHostContext,
  resolveRootMessageWorkspace,
};
