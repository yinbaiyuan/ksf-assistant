import test from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { createBridge } from '../server/bridge.mjs';

async function fixture() {
  const calls = [];
  const bridge = await createBridge(process.execPath, { spawnProcess(binary, args, options) {
    calls.push({ args, options });
    return spawn(binary, [fileURLToPath(new URL('./fixtures/client.mjs', import.meta.url)), ...args], options);
  } });
  return { bridge, calls };
}

test('process adapter uses client argv and private stdin without shell interpolation', async () => {
  const { bridge, calls } = await fixture();
  const result = await bridge.call(['echo', '; never a command'], 'isolated input');
  assert.deepEqual(result, { args: ['client', 'echo', '; never a command'], input: 'isolated input' });
  assert.equal(calls[0].options.shell, undefined);
  assert.deepEqual(calls[0].options.stdio, ['pipe', 'pipe', 'pipe']);
});

test('process adapter distinguishes JSON, offline service, and original approval challenge', async () => {
  const { bridge } = await fixture();
  await assert.rejects(bridge.call(['offline']), { code: 'service_unavailable' });
  await assert.rejects(bridge.call(['invalid']), { code: 'invalid_response' });
  assert.equal((await bridge.call(['approval'])).challenge, 'isolated-challenge');
  assert.equal((await bridge.call(['rejected'])).errorCode, 'capability_disabled');
});

test('pre-cancelled requests never spawn a process; active cancellation terminates the child', async () => {
  const { bridge, calls } = await fixture();
  await assert.rejects(bridge.call(['wait'], undefined, AbortSignal.abort()), { code: 'request_cancelled' });
  assert.equal(calls.length, 0);
  const controller = new AbortController();
  const pending = bridge.call(['wait'], undefined, controller.signal);
  setTimeout(() => controller.abort(), 30);
  await assert.rejects(pending, { code: 'outcome_unknown' });
  assert.equal(calls.length, 1);
});

test('process output is bounded and invalid binary paths fail before execution', async () => {
  const { bridge } = await fixture();
  await assert.rejects(bridge.call(['huge']), { code: 'response_too_large' });
  await assert.rejects(createBridge('relative-binary'), { code: 'invalid_binary' });
});
