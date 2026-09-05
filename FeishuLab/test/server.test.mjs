import test from 'node:test';
import assert from 'node:assert/strict';
import { createServer, request as httpRequest } from 'node:http';
import { createApp } from '../server/app.mjs';

test('same-origin session works but hostile host and origin cannot reach the bridge', async () => {
  let calls = 0;
  const server = createServer(createApp({ bridge: { call: async () => { calls++; return {}; } } }));
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
  const origin = `http://127.0.0.1:${server.address().port}`;
  try {
    const session = await fetch(`${origin}/api/session`, { headers: { 'X-Lab-Client': '1' } });
    assert.equal(session.status, 200);
    assert.equal(typeof (await session.json()).token, 'string');
    const hostile = await new Promise(resolve => {
      const request = httpRequest(`${origin}/api/session`, { headers: { Host: 'evil.test', 'X-Lab-Client': '1' } }, response => { response.resume(); resolve(response.statusCode); });
      request.end();
    });
    assert.equal(hostile, 403);
    assert.equal((await fetch(`${origin}/api/session`, { headers: { Origin: 'https://evil.test', 'X-Lab-Client': '1' } })).status, 403);
    assert.equal(calls, 0);
  } finally { await new Promise(resolve => server.close(resolve)); }
});
