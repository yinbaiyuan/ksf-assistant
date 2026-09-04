const assert = require('node:assert/strict');
const test = require('node:test');
const { createOfficialMessageClient } = require('../lib/official-message-client');

function fakeSDK(calls, responses = {}) {
  return {
    Domain: { Feishu: 'feishu', Lark: 'lark' },
    LoggerLevel: { error: 'error' },
    Client: class {
      constructor(options) {
        calls.push({ operation: 'construct', options });
        this.tokenManager = {
          getTenantAccessToken: async () => {
            calls.push({ operation: 'warmup' });
            return 'token';
          },
        };
        this.im = { v1: { message: {
          reply: async (payload) => {
            calls.push({ operation: 'reply', payload });
            return responses.reply || { code: 0, data: { message_id: 'om_reply' } };
          },
          patch: async (payload) => {
            calls.push({ operation: 'patch', payload });
            return responses.patch || { code: 0, data: {} };
          },
        } } };
      }
    },
  };
}

test('official message client reuses one SDK client for idempotent card reply and patch', async () => {
  const calls = [];
  const client = createOfficialMessageClient({
    credentials: { appId: 'app', appSecret: 'secret', brand: 'feishu' },
    sdk: fakeSDK(calls),
  });
  assert.equal(await client.warmup(), true);
  assert.deepEqual(await client.replyCard({
    messageId: 'om_source', card: { schema: '2.0' }, idempotencyKey: 'once',
  }), { response: { code: 0, data: { message_id: 'om_reply' } }, messageId: 'om_reply' });
  await client.patchCard({ messageId: 'om_reply', card: { schema: '2.0', body: {} } });
  assert.equal(calls.filter((item) => item.operation === 'construct').length, 1);
  assert.equal(calls.filter((item) => item.operation === 'warmup').length, 1);
  assert.deepEqual(calls.find((item) => item.operation === 'reply').payload, {
    path: { message_id: 'om_source' },
    data: { content: '{"schema":"2.0"}', msg_type: 'interactive', uuid: 'once' },
  });
  assert.deepEqual(calls.find((item) => item.operation === 'patch').payload, {
    path: { message_id: 'om_reply' },
    data: { content: '{"schema":"2.0","body":{}}' },
  });
});

test('official message client fails closed on API errors', async () => {
  const client = createOfficialMessageClient({
    credentials: { appId: 'app', appSecret: 'secret', brand: 'lark' },
    sdk: fakeSDK([], { reply: { code: 999, msg: 'denied' } }),
  });
  await assert.rejects(() => client.replyCard({
    messageId: 'om_source', card: {}, idempotencyKey: 'once',
  }), /code 999/);
});
