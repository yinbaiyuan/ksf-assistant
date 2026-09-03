async function deliverTaskLinkCard({
  messageId,
  token = '',
  card,
  patchByMessage,
  updateByToken,
}) {
  let patchError = null;
  try {
    if (!messageId) throw new Error('task link root message is unavailable');
    await patchByMessage({ messageId, card });
    return { ok: true, transport: 'message_patch' };
  } catch (error) {
    patchError = error;
  }

  if (!token) return { ok: false, patchError, tokenError: null };
  try {
    await updateByToken({ token, card });
    return { ok: true, transport: 'card_token_fallback', patchError };
  } catch (tokenError) {
    return { ok: false, patchError, tokenError };
  }
}

module.exports = { deliverTaskLinkCard };
