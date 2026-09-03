const crypto = require('node:crypto');
const fs = require('node:fs');

function auditFingerprint(value) {
  if (value === undefined || value === null || value === '') return '';
  return `sha256:${crypto.createHash('sha256').update(String(value)).digest('hex').slice(0, 12)}`;
}

function redactAuditText(value) {
  return String(value || '')
    .replace(/\b(?:ou|oc|om)_[A-Za-z0-9_-]+\b|\bod-[A-Za-z0-9_-]+\b/g, (match) => `[${auditFingerprint(match)}]`)
    .replace(/\b(?:docx|wikcn|bascn|shtcn|obcn|tbl|rec)[A-Za-z0-9_-]{8,}\b/g, (match) => `[${auditFingerprint(match)}]`)
    .replace(
      /https:\/\/[^\s/]+\.feishu\.cn\/(?:wiki|docx|base|sheets|minutes)\/[A-Za-z0-9_-]+(?:\?[^\s]*)?/gi,
      (match) => `[feishu-url:${auditFingerprint(match)}]`,
    )
    .replace(
      /https:\/\/vc\.feishu\.cn\/j\/[A-Za-z0-9_-]+(?:\?[^\s]*)?/gi,
      (match) => `[feishu-url:${auditFingerprint(match)}]`,
    )
    .replace(/(app[_-]?secret|tenant[_-]?access[_-]?token|authorization)\s*[:=]\s*\S+/gi, '$1=[REDACTED]');
}

function auditTargetDescriptor(target = {}) {
  const kind = target.type || target.kind || 'unknown';
  const value = target.id || target.value || '';
  return `${kind}:${auditFingerprint(value) || '-'}`;
}

function auditContentDescriptor(value) {
  const content = Buffer.isBuffer(value) ? value : Buffer.from(String(value || ''), 'utf8');
  return `length=${content.length}, ${auditFingerprint(content) || '-'}`;
}

function auditStructuredDescriptor(value) {
  const serialized = JSON.stringify(value || {});
  const topLevelKeys = value && typeof value === 'object' && !Array.isArray(value)
    ? Object.keys(value).sort()
    : [];
  let itemCount = 0;
  if (Array.isArray(value)) itemCount = value.length;
  else if (Array.isArray(value?.records)) itemCount = value.records.length;
  else if (value?.updates && typeof value.updates === 'object') itemCount = Object.keys(value.updates).length;
  else if (Array.isArray(value?.todos)) itemCount = value.todos.length;
  else if (Array.isArray(value?.replacements)) itemCount = value.replacements.length;
  else if (Array.isArray(value?.sheets?.sheets)) {
    itemCount = value.sheets.sheets.reduce((total, sheet) => total + (Array.isArray(sheet.data) ? sheet.data.length : 0), 0);
  }
  return `json_bytes=${Buffer.byteLength(serialized, 'utf8')}, keys=${topLevelKeys.join(',') || '-'}, items=${itemCount}, ${auditFingerprint(serialized)}`;
}

function auditRequestContent(request = {}) {
  if (typeof request.text === 'string') return auditContentDescriptor(request.text);
  if (typeof request.content?.text === 'string') return auditContentDescriptor(request.content.text);
  if (request.filePath) {
    try {
      return `file_bytes=${fs.statSync(request.filePath).size}, ${auditFingerprint(fs.readFileSync(request.filePath))}`;
    } catch {
      return `file_unavailable, ${auditFingerprint(request.filePath)}`;
    }
  }
  return 'length=0, -';
}

module.exports = {
  auditContentDescriptor,
  auditFingerprint,
  auditRequestContent,
  auditStructuredDescriptor,
  auditTargetDescriptor,
  redactAuditText,
};
