'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { EventEmitter } = require('node:events');
const { PassThrough } = require('node:stream');

const settle = () => new Promise((resolve) => setImmediate(resolve));

function fixture() {
  const children = [];
  const module = { exports: {} };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, '../src/core-client.cjs'), 'utf8'), {
    module, process, Buffer, setTimeout, clearTimeout,
    require: (name) => name === 'node:child_process' ? {
      spawn: () => {
        const child = new EventEmitter();
        child.stdout = new PassThrough();
        child.stderr = new PassThrough();
        child.exitCode = null;
        child.killed = false;
        child.requests = [];
        child.stdin = {
          writable: true,
          write: (data, callback) => { child.requests.push(JSON.parse(data)); callback?.(); },
          end: () => { child.exitCode = 0; child.emit('exit', 0); },
        };
        child.kill = () => { child.killed = true; child.exitCode = 0; child.emit('exit', 0); };
        child.reply = (request, result) => child.stdout.write(`${JSON.stringify({ jsonrpc: '2.0', id: request.id, result })}\n`);
        children.push(child);
        return child;
      },
      spawnSync: () => { throw new Error('unexpected process tree termination'); },
    } : require(name),
  });
  return { client: new module.exports.CoreClient({ executablePath: '/fixture/core', timeoutMs: 1000 }), children };
}

test('initialize is serialized but overlapping RPC replies are demultiplexed by ID', async () => {
  const { client, children } = fixture();
  const business = client.request('business/write');
  const polling = client.request('userApproval/poll', { interactive: true });
  const child = children[0];
  assert.deepEqual(child.requests.map((request) => request.method), ['initialize']);
  child.reply(child.requests[0], {});
  await settle();
  assert.equal(children.length, 1);
  child.reply(child.requests[2], { kind: 'poll' });
  child.reply(child.requests[1], { kind: 'business' });
  assert.equal((await business).kind, 'business');
  assert.equal((await polling).kind, 'poll');
  child.emit('exit', 0);
});

for (const termination of ['exit', 'stdout-eof', 'error']) {
  test(`pending requests reject promptly on ${termination}`, async () => {
    const { client, children } = fixture();
    const starting = client.start();
    const child = children[0];
    child.reply(child.requests[0], {});
    await starting;
    const pending = client.request('business/write');
    const rejected = assert.rejects(pending);
    await settle();
    if (termination === 'exit') child.emit('exit', 0);
    if (termination === 'error') child.emit('error', new Error('fixture failure'));
    if (termination === 'stdout-eof') child.stdout.end();
    await rejected;
    assert.equal(client.pending.size, 0);
  });
}

test('close cleans pending requests and no later request silently restarts Core', async () => {
  const { client, children } = fixture();
  const starting = client.start();
  const child = children[0];
  child.reply(child.requests[0], {});
  await starting;
  const pending = client.request('business/write');
  const rejected = assert.rejects(pending);
  await settle();
  const closing = client.close();
  await settle();
  const shutdown = child.requests.find((request) => request.method === 'shutdown');
  child.reply(shutdown, {});
  await closing;
  await rejected;
  await assert.rejects(client.request('userApproval/poll'), /退出/);
  assert.equal(client.pending.size, 0);
  assert.equal(children.length, 1);
});

test('late exit of an old child cannot fail new-generation requests', async () => {
  const { client, children } = fixture();
  let starting = client.start();
  children[0].reply(children[0].requests[0], {});
  await starting;
  children[0].emit('error', new Error('old generation disconnected'));
  starting = client.start();
  const child = children[1];
  children[0].emit('exit', 0);
  child.reply(child.requests[0], {});
  await starting;
  const pending = client.request('userApproval/poll');
  await settle();
  children[0].emit('exit', 0);
  child.reply(child.requests[1], { schemaVersion: 1, request: null });
  assert.equal((await pending).schemaVersion, 1);
  child.emit('exit', 0);
});
