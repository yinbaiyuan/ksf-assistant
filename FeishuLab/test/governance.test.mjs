import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { createApp, queryArguments, validateInput } from '../server/app.mjs';
import { withInputFiles, LabError } from '../server/bridge.mjs';
import { readFile, stat } from 'node:fs/promises';
import { browserLimit, decision, filterCapabilities, parseFields, reportRows } from '../shared/model.mjs';

const capability = { id: 'im.fixture.send', domain: 'im', risk: 'write', published: true, identity: 'bot', inputFields: [{ name: 'text', type: 'string', required: true }, { name: 'file', type: 'path' }] };
const policy = { version: 1, revision: 1, riskDefaults: { read: 'allowed', write: 'allowed' }, capabilityOverrides: {} };

async function fixture(run) {
  const calls = [];
  let permission = 'allowed';
  let clock = 1000;
  let failure = false;
  let policyResult = { status: 'updated', policy: { ...policy, revision: 2 } };
  const bridge = { async call(args, input) {
    calls.push({ args, input });
    if (args.join(' ') === 'capability catalog') return { capabilities: [capability] };
    if (args.join(' ') === 'policy read') return { policy: { ...policy, capabilityOverrides: { [capability.id]: permission } } };
    if (args.includes('--dry-run')) return { status: 'dry_run', submitted: false };
    if (args.join(' ').startsWith('policy update')) return policyResult;
    if (failure) throw new LabError('outcome_unknown', 'fixture timeout', 504);
    return { status: 'ok', operation: { id: 'OP-20260906000000-ABCDEF12', status: permission === 'confirm_each' ? 'awaiting_confirmation' : 'succeeded' }, challenge: permission === 'confirm_each' ? 'original-challenge' : undefined };
  } };
  const server = createServer(createApp({ bridge, now: () => clock }));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  const token = (await (await fetch(`${origin}/api/session`, { headers: { 'X-Lab-Client': '1' } })).json()).token;
  const post = async (endpoint, body, withToken = true) => {
    const response = await fetch(origin + endpoint, { method: 'POST', headers: { Origin: origin, 'Content-Type': 'application/json', ...(withToken ? { 'X-Lab-Token': token } : {}) }, body: JSON.stringify(body) });
    return { status: response.status, data: await response.json() };
  };
  try { await run({ post, calls, policyResult: value => { policyResult = value; }, permission: value => { permission = value; }, advance: () => { clock += 120001; }, fail: () => { failure = true; } }); }
  finally { server.closeAllConnections(); await new Promise(resolve => server.close(resolve)); }
}

const input = { capabilityId: capability.id, input: { text: 'fixture' }, files: {} };
const execute = (proof, requestId = 'request-fixture-123456') => ({ ...input, proof, requestId, acknowledge: true });

test('validation never executes; execution requires proof, intent and current policy', () => fixture(async ({ post, calls, permission }) => {
  assert.equal((await post('/api/execute', execute('missing'))).status, 400);
  const validation = await post('/api/validate', input);
  assert.equal(validation.data.status, 'dry_run');
  assert.equal(calls.filter(call => call.args[0] === 'operation').length, 0);
  permission('disabled');
  const rejected = await post('/api/execute', execute(validation.data.proof));
  assert.equal(rejected.data.error, 'capability_disabled');
  assert.equal(calls.filter(call => call.args[0] === 'operation').length, 0);
}));

test('same request executes once, changed input and reused proof are rejected', () => fixture(async ({ post, calls }) => {
  const validation = await post('/api/validate', input);
  const body = execute(validation.data.proof);
  const responses = await Promise.all([post('/api/execute', body), post('/api/execute', body)]);
  assert.equal(responses[0].data.operation.status, 'succeeded');
  assert.deepEqual(responses[0], responses[1]);
  assert.equal(calls.filter(call => call.args[0] === 'operation').length, 1);
  assert.equal((await post('/api/execute', { ...body, input: { text: 'changed' } })).status, 400);
  assert.equal((await post('/api/execute', { ...body, requestId: 'another-request-123456' })).data.error, 'validation_required');
}));

test('expired and changed proofs never execute', () => fixture(async ({ post, advance, calls }) => {
  const validation = await post('/api/validate', input);
  assert.equal((await post('/api/execute', { ...execute(validation.data.proof), input: { text: 'changed' } })).status, 400);
  advance();
  assert.equal((await post('/api/execute', execute(validation.data.proof))).data.error, 'validation_required');
  assert.equal(calls.filter(call => call.args[0] === 'operation').length, 0);
}));

test('approval retains challenge and confirm routes only to original Operation', () => fixture(async ({ post, permission, calls }) => {
  permission('confirm_each');
  const validation = await post('/api/validate', input);
  const result = (await post('/api/execute', execute(validation.data.proof))).data;
  assert.equal(result.operation.status, 'awaiting_confirmation');
  assert.equal(result.challenge, 'original-challenge');
  assert.equal(calls.filter(call => call.args[1] === 'confirm').length, 0);
  await post('/api/operation', { action: 'confirm', id: result.operation.id, challenge: result.challenge, acknowledge: true });
  assert.equal(calls.at(-1).input, result.challenge);
  assert.equal((await post('/api/operation', { action: 'confirm', id: result.operation.id, challenge: result.challenge })).status, 400);
}));

test('timeout is remembered; retrying the same request does not replay', () => fixture(async ({ post, calls, fail }) => {
  const validation = await post('/api/validate', input);
  fail();
  const body = execute(validation.data.proof);
  assert.equal((await post('/api/execute', body)).data.error, 'outcome_unknown');
  await post('/api/execute', body);
  assert.equal(calls.filter(call => call.args[0] === 'operation').length, 1);
}));

test('session, allowlist, limits and server paths fail closed', () => fixture(async ({ post, calls }) => {
  assert.equal((await post('/api/query', { kind: 'status' }, false)).status, 403);
  assert.equal((await post('/api/query', { kind: 'exec', command: 'anything' })).status, 400);
  assert.equal((await post('/api/query', { kind: 'recent', box: 'audit', limit: 999 })).status, 400);
  assert.equal((await post('/api/validate', { ...input, input: { ...input.input, file: '/etc/passwd' } })).status, 400);
  assert.equal(calls.filter(call => call.args[0] !== 'capability').length, 0);
}));

test('policy update carries revision and requires explicit confirmation', () => fixture(async ({ post, calls }) => {
  assert.equal((await post('/api/policy', { policy, expectedRevision: 1 })).status, 400);
  await post('/api/policy', { policy, expectedRevision: 1, acknowledge: true });
  assert.deepEqual(calls.at(-1).args, ['policy', 'update', '--expected-revision', '1', '--payload-file', '-']);
}));

test('policy rejection or malformed success never replaces the current policy', () => fixture(async ({ post, policyResult }) => {
  policyResult({ status: 'rejected', errorCode: 'capability_policy_revision_conflict' });
  const rejection = await post('/api/policy', { policy, expectedRevision: 1, acknowledge: true });
  assert.equal(rejection.status, 409);
  assert.equal(rejection.data.error, 'policy_not_updated');
  policyResult({ status: 'updated' });
  assert.equal((await post('/api/policy', { policy, expectedRevision: 1, acknowledge: true })).status, 409);
}));

test('inherited object properties cannot become query commands', () => {
  for (const kind of ['constructor', 'toString', '__proto__']) assert.throws(() => queryArguments({ kind }));
});

test('file handling uses private temporary files and cleans up on failure', async () => {
  let filename;
  const files = { file: { name: '../../fixture.txt', data: Buffer.from('fixture').toString('base64') } };
  validateInput(capability, input.input, files);
  await assert.rejects(withInputFiles(capability, input.input, files, async payload => {
    filename = JSON.parse(payload).file;
    assert.equal(await readFile(filename, 'utf8'), 'fixture');
    assert.equal((await stat(filename)).mode & 0o777, 0o600);
    throw new Error('fixture error');
  }));
  await assert.rejects(stat(filename));
  assert.throws(() => validateInput(capability, input.input, { file: { name: 'x', data: '***' } }));
  assert.throws(() => validateInput(capability, { text: 'a'.repeat(4 * 1024 * 1024 + 1) }));
});

test('display decisions never mistake missing policy or unknown scope for allowed', () => {
  assert.equal(browserLimit(capability), '');
  assert.match(browserLimit({ inputFields: [{ name: 'output', type: 'path', required: true }] }), /原生 CLI/);
  assert.equal(decision(capability, null).value, 'unknown');
  assert.equal(decision(capability, { ...policy, capabilityOverrides: { [capability.id]: 'disabled' } }).value, 'disabled');
  assert.equal(filterCapabilities([capability], { permission: 'allowed', query: 'fixture' }, policy).length, 1);
  assert.deepEqual(parseFields([{ name: 'count', type: 'integer' }, { name: 'dry', type: 'boolean' }], { count: '2', dry: 'false' }), { count: 2, dry: false });
  assert.throws(() => parseFields([{ name: 'count', type: 'integer' }], { count: '1.5' }));
  assert.equal(JSON.stringify(reportRows([{ time: 'now', capabilityId: 'read', status: 'ok', input: 'secret', challenge: 'secret', result: 'secret' }])).includes('secret'), false);
  assert.throws(() => queryArguments({ kind: 'auth', action: 'login' }));
});
