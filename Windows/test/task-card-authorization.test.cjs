'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const { createTaskCard } = require('../src/task-card-authorization.cjs');
function fixture() {
  const calls = [];
  const error = Object.assign(new Error('confirmation required'), { code: -32063, data: {
    status: 'authorization_required', challenge: 'fresh', operation: { id: 'OP-fixture', capabilityId: 'im.sdk.message.send', status: 'awaiting_confirmation' },
  } });
  const core = { async request(method, payload) { calls.push([method, payload]); if (calls.length === 1) throw error; return { linkState: 'active' }; } };
  return { core, calls, error };
}
test('task card confirmation precedes send resume and preserves the exact request', async () => {
  const { core, calls } = fixture();
  const payload = { threadId: 'thread', targetAlias: 'me', title: 'task', projectName: 'project' };
  let reviews = 0;
  assert.equal((await createTaskCard(core, payload, async () => { reviews++; assert.equal(calls.length, 1); return true; })).linkState, 'active');
  assert.equal(reviews, 1);
  assert.deepEqual(calls.map(([m]) => m), ['feishu/taskLink/create', 'feishu/operation/confirm', 'feishu/taskLink/create']);
  assert.strictEqual(calls[0][1], calls[2][1]);
});
test('cancelling a card never confirms or resumes delivery and releases its pending link', async () => {
  const { core, calls } = fixture();
  await assert.rejects(createTaskCard(core, { threadId: 'thread' }, async () => false), /已取消/);
  assert.deepEqual(calls.map(([m]) => m), ['feishu/taskLink/create', 'feishu/operation/cancel', 'feishu/taskLink/release']);
});
test('unrelated authorization errors cannot reach task card approval', async () => {
  const { core, calls, error } = fixture();
  error.data.operation.capabilityId = 'unrelated';
  await assert.rejects(createTaskCard(core, {}, async () => { assert.fail('unexpected review'); }), /confirmation required/);
  assert.equal(calls.length, 1);
});
test('a failed confirmation is not automatically retried', async () => {
  const { core, calls } = fixture();
  const request = core.request;
  core.request = async (method, payload) => { if (method === 'feishu/operation/confirm') throw new Error('outcome_unknown'); return request(method, payload); };
  await assert.rejects(createTaskCard(core, {}, async () => true), /outcome_unknown/);
  assert.equal(calls.length, 1);
});
