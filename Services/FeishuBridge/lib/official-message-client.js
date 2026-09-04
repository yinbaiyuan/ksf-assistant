function silentLogger() {
  return {
    trace() {}, debug() {}, info() {}, warn() {}, error() {},
  };
}

function assertSuccess(response, operation) {
  const code = Number(response?.code || 0);
  if (code !== 0) {
    throw new Error(`${operation} failed with code ${code}`);
  }
  return response;
}

function createOfficialMessageClient({
  credentials,
  sdk = require('@larksuiteoapi/node-sdk'),
} = {}) {
  if (!credentials?.appId || !credentials?.appSecret) {
    throw new Error('official SDK credentials are required');
  }
  const client = new sdk.Client({
    appId: credentials.appId,
    appSecret: credentials.appSecret,
    domain: credentials.brand === 'lark' ? sdk.Domain.Lark : sdk.Domain.Feishu,
    logger: silentLogger(),
    loggerLevel: sdk.LoggerLevel.error,
  });
  return {
    async warmup() {
      if (typeof client.tokenManager?.getTenantAccessToken !== 'function') return false;
      await client.tokenManager.getTenantAccessToken({});
      return true;
    },

    async replyCard({ messageId, card, idempotencyKey }) {
      const response = assertSuccess(await client.im.v1.message.reply({
        path: { message_id: messageId },
        data: {
          content: typeof card === 'string' ? card : JSON.stringify(card),
          msg_type: 'interactive',
          uuid: idempotencyKey,
        },
      }), 'Feishu card reply');
      return { response, messageId: response?.data?.message_id || '' };
    },

    async patchCard({ messageId, card }) {
      const response = assertSuccess(await client.im.v1.message.patch({
        path: { message_id: messageId },
        data: { content: typeof card === 'string' ? card : JSON.stringify(card) },
      }), 'Feishu card patch');
      return { response, messageId };
    },
  };
}

module.exports = { createOfficialMessageClient };
