const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const { chmodPrivate } = require('./platform-runtime');

const SUPPORTED_INBOUND_MESSAGE_TYPES = new Set(['text', 'image', 'file', 'audio', 'media', 'post']);

function parseContent(content) {
  if (content && typeof content === 'object') return content;
  try {
    return JSON.parse(String(content || '{}'));
  } catch {
    return {};
  }
}

function safeFileName(value, fallback) {
  const base = path.basename(String(value || '')).normalize('NFKC')
    .replace(/[\u0000-\u001f\u007f]/g, '')
    .replace(/[\\/:*?"<>|]/g, '_')
    .replace(/^\.+/, '')
    .trim();
  return (base || fallback).slice(0, 160);
}

function postDetails(content) {
  const textParts = [];
  const resources = [];
  const seen = new Set();
  function visit(value) {
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    if (!value || typeof value !== 'object') return;
    if (value.tag === 'text' && typeof value.text === 'string') textParts.push(value.text);
    if (typeof value.title === 'string') textParts.push(value.title);
    for (const [key, resourceType] of [['image_key', 'image'], ['file_key', 'file']]) {
      if (typeof value[key] !== 'string' || seen.has(value[key])) continue;
      seen.add(value[key]);
      resources.push({
        fileKey: value[key],
        resourceType,
        displayName: safeFileName(value.file_name, `${resourceType}-${resources.length + 1}`),
      });
    }
    Object.values(value).forEach(visit);
  }
  visit(content);
  return { text: textParts.join('\n').trim(), resources };
}

function inboundMessageDetails(message) {
  const messageType = String(message?.message_type || '');
  const content = parseContent(message?.content);
  if (messageType === 'text') return { messageType, text: String(content.text || ''), resources: [] };
  if (messageType === 'post') return { messageType, ...postDetails(content) };
  if (messageType === 'image' && content.image_key) {
    return {
      messageType,
      text: '',
      resources: [{ fileKey: content.image_key, resourceType: 'image', displayName: 'image' }],
    };
  }
  if (['file', 'audio', 'media'].includes(messageType) && content.file_key) {
    return {
      messageType,
      text: String(content.text || ''),
      resources: [{
        fileKey: content.file_key,
        resourceType: 'file',
        displayName: safeFileName(content.file_name, `${messageType}-file`),
      }],
    };
  }
  return { messageType, text: '', resources: [] };
}

function inboundRoot(cwd) {
  return path.join(path.resolve(cwd), 'node_modules', '.cache', 'feishu-bridge', 'inbound-assets');
}

function assertScopedDirectory(cwd, directory) {
  const root = inboundRoot(cwd);
  const relative = path.relative(root, path.resolve(directory));
  if (!relative || relative.startsWith('..') || path.isAbsolute(relative)) {
    throw new Error('unsafe inbound asset cleanup path');
  }
  return root;
}

function cleanupInboundAssets(cwd, directory) {
  if (!directory) return;
  assertScopedDirectory(cwd, directory);
  fs.rmSync(directory, { recursive: true, force: true });
}

function regularFiles(directory) {
  if (!fs.existsSync(directory)) return [];
  return fs.readdirSync(directory).filter((name) => {
    const stat = fs.lstatSync(path.join(directory, name));
    return stat.isFile() && !stat.isSymbolicLink();
  });
}

async function stageInboundMessage({
  lark,
  message,
  cwd,
  maxBytes = 25 * 1024 * 1024,
  maxResources = 10,
}) {
  const details = inboundMessageDetails(message);
  if (!SUPPORTED_INBOUND_MESSAGE_TYPES.has(details.messageType)) {
    throw new Error(`unsupported inbound message type: ${details.messageType || '-'}`);
  }
  if (details.resources.length > maxResources) throw new Error('too many inbound resources');
  if (!details.resources.length) return { ...details, assets: [], cleanupDir: '' };

  const root = inboundRoot(cwd);
  fs.mkdirSync(root, { recursive: true, mode: 0o700 });
  const rootStat = fs.lstatSync(root);
  if (rootStat.isSymbolicLink() || !rootStat.isDirectory()) throw new Error('unsafe inbound asset root');
  const realRoot = fs.realpathSync(root);
  const realCwd = fs.realpathSync(path.resolve(cwd));
  const rootRelative = path.relative(realCwd, realRoot);
  if (!rootRelative || rootRelative.startsWith('..') || path.isAbsolute(rootRelative)) {
    throw new Error('inbound asset root escapes the bridge project');
  }
  chmodPrivate(root, 0o700);
  const digest = crypto.createHash('sha256').update(String(message.message_id || '')).digest('hex').slice(0, 24);
  const cleanupDir = path.join(root, digest);
  cleanupInboundAssets(cwd, cleanupDir);
  fs.mkdirSync(cleanupDir, { mode: 0o700 });

  const assets = [];
  let totalBytes = 0;
  try {
    for (const [index, resource] of details.resources.entries()) {
      const outputName = `${String(index + 1).padStart(2, '0')}-${safeFileName(resource.displayName, 'resource')}`;
      const outputPath = path.join(cleanupDir, outputName);
      const before = new Set(regularFiles(cleanupDir));
      const relativeOutput = path.relative(path.resolve(cwd), outputPath).split(path.sep).join('/');
      await lark.larkImDownloadMessageResource({
        messageId: message.message_id,
        fileKey: resource.fileKey,
        resourceType: resource.resourceType,
        output: relativeOutput,
        timeoutMs: 120000,
      });
      const candidates = regularFiles(cleanupDir).filter((name) => !before.has(name));
      const downloaded = fs.existsSync(outputPath) ? outputName : candidates[0];
      if (!downloaded) throw new Error('resource download did not create a regular file');
      const localPath = path.join(cleanupDir, downloaded);
      const stat = fs.lstatSync(localPath);
      if (stat.isSymbolicLink() || !stat.isFile()) throw new Error('downloaded resource is not a regular file');
      totalBytes += stat.size;
      if (totalBytes > maxBytes) throw new Error('inbound resources exceed size limit');
      chmodPrivate(localPath, 0o600);
      assets.push({
        messageType: details.messageType,
        resourceType: resource.resourceType,
        displayName: safeFileName(resource.displayName, downloaded),
        localPath,
        sizeBytes: stat.size,
      });
    }
    return { ...details, assets, cleanupDir };
  } catch (error) {
    cleanupInboundAssets(cwd, cleanupDir);
    throw error;
  }
}

function inboundAssetsPrompt(prompt, details) {
  if (!details?.assets?.length) return String(prompt || '');
  const files = details.assets.map((asset, index) => (
    `${index + 1}. ${asset.displayName} (${asset.messageType}/${asset.resourceType}, ${asset.sizeBytes} bytes): ${asset.localPath}`
  ));
  return [
    String(prompt || '').trim() || '请分析我发送的附件并给出有用的回复。',
    '',
    '飞书桥已将本轮附件暂存为以下本机只读输入。请按用户意图读取这些文件；不要移动、改写或删除它们：',
    ...files,
  ].join('\n');
}

module.exports = {
  SUPPORTED_INBOUND_MESSAGE_TYPES,
  cleanupInboundAssets,
  inboundAssetsPrompt,
  inboundMessageDetails,
  safeFileName,
  stageInboundMessage,
};
