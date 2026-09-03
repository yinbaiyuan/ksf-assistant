const crypto = require('node:crypto');

function normalizeReplyPhase(phase) {
  const value = String(phase || '').trim();
  if (!value) throw new Error('reply phase is required');
  return value;
}

function replyIdempotencyKey(messageId, phase, partIndex = 0) {
  const sourceMessageId = String(messageId || '').trim();
  if (!sourceMessageId) throw new Error('source message id is required');
  const normalizedPhase = normalizeReplyPhase(phase);
  if (!Number.isInteger(partIndex) || partIndex < 0) {
    throw new Error('reply part index must be a non-negative integer');
  }
  const digest = crypto
    .createHash('sha256')
    .update(`${sourceMessageId}\0${normalizedPhase}\0${partIndex}`)
    .digest('hex')
    .slice(0, 32);
  return `codex-${digest}`;
}

module.exports = {
  normalizeReplyPhase,
  replyIdempotencyKey,
};
