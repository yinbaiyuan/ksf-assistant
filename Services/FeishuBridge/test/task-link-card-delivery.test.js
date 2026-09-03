const test = require('node:test');
const assert = require('node:assert/strict');
const { deliverTaskLinkCard } = require('../lib/task-link-card-delivery');

test('task-link cards prefer the message patch transport', async () => {
  const calls = [];
  const result = await deliverTaskLinkCard({
    messageId: 'message-1',
    token: 'token-1',
    card: { schema: '2.0' },
    patchByMessage: async (input) => calls.push(['message', input]),
    updateByToken: async (input) => calls.push(['token', input]),
  });

  assert.equal(result.ok, true);
  assert.equal(result.transport, 'message_patch');
  assert.deepEqual(calls.map(([transport]) => transport), ['message']);
});

test('task-link cards use the callback token only as a serialized fallback', async () => {
  const calls = [];
  const patchError = new Error('message patch failed');
  const result = await deliverTaskLinkCard({
    messageId: 'message-1',
    token: 'token-1',
    card: { schema: '2.0' },
    patchByMessage: async () => {
      calls.push('message');
      throw patchError;
    },
    updateByToken: async () => calls.push('token'),
  });

  assert.equal(result.ok, true);
  assert.equal(result.transport, 'card_token_fallback');
  assert.equal(result.patchError, patchError);
  assert.deepEqual(calls, ['message', 'token']);
});

test('task-link card delivery reports both transport failures', async () => {
  const patchError = new Error('message patch failed');
  const tokenError = new Error('token update failed');
  const result = await deliverTaskLinkCard({
    messageId: 'message-1',
    token: 'token-1',
    card: { schema: '2.0' },
    patchByMessage: async () => { throw patchError; },
    updateByToken: async () => { throw tokenError; },
  });

  assert.equal(result.ok, false);
  assert.equal(result.patchError, patchError);
  assert.equal(result.tokenError, tokenError);
});
