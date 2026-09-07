'use strict';

async function createTaskCard(core, payload, review) {
  try { return await core.request('feishu/taskLink/create', payload); }
  catch (error) {
    const authorization = error.data;
    if (error.code !== -32063 || authorization?.status !== 'authorization_required'
      || authorization.operation?.status !== 'awaiting_confirmation'
      || authorization.operation?.capabilityId !== 'im.sdk.message.send'
      || typeof authorization.operation.id !== 'string' || !authorization.operation.id
      || typeof authorization.challenge !== 'string' || !authorization.challenge) throw error;
    if (!await review()) {
      await core.request('feishu/operation/cancel', { operationId: authorization.operation.id });
      await core.request('feishu/taskLink/release', { threadId: payload.threadId });
      throw new Error('已取消发送任务卡片。');
    }
    await core.request('feishu/operation/confirm', { operationId: authorization.operation.id, challenge: authorization.challenge });
    return core.request('feishu/taskLink/create', payload);
  }
}
module.exports = { createTaskCard };
