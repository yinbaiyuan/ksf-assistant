'use strict';

const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { deployFeishuService } = require('../src/feishu-service-deployment.cjs');

function fixture(root, version, valid = true) {
  const service = path.join(root, 'source');
  fs.mkdirSync(path.join(service, 'scripts'), { recursive: true });
  fs.mkdirSync(path.join(service, 'node_modules', '@larksuite', 'cli', 'scripts'), { recursive: true });
  fs.mkdirSync(path.join(service, 'node_modules', '@larksuiteoapi', 'node-sdk'), { recursive: true });
  fs.writeFileSync(path.join(service, 'scripts', 'bridge-client.js'), valid ? '' : 'syntax error {');
  fs.writeFileSync(path.join(service, 'bot-bridge.js'), '');
  fs.writeFileSync(path.join(service, 'node_modules', '@larksuite', 'cli', 'scripts', 'run.js'), '');
  fs.writeFileSync(path.join(service, 'node_modules', '@larksuiteoapi', 'node-sdk', 'package.json'), '{}');
  fs.writeFileSync(path.join(service, 'usage-bar-service.json'), JSON.stringify({ productVersion: version }));
  return service;
}

test('service deployment keeps current, one previous release, and rolls back failed staging', (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'usagebar-feishu-deploy-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const node = process.execPath;
  const first = deployFeishuService({ sourceServiceRoot: fixture(path.join(root, 'v1'), 'v1'), sourceNode: node, dataRoot: path.join(root, 'data'), productVersion: 'v1' });
  assert.equal(first.changed, true);
  const same = deployFeishuService({ sourceServiceRoot: fixture(path.join(root, 'v1-again'), 'v1'), sourceNode: node, dataRoot: path.join(root, 'data'), productVersion: 'v1' });
  assert.equal(same.changed, false);
  fs.rmSync(path.join(first.serviceRoot, 'node_modules', '@larksuite', 'cli', 'scripts', 'run.js'));
  const repaired = deployFeishuService({ sourceServiceRoot: fixture(path.join(root, 'v1-repair'), 'v1'), sourceNode: node, dataRoot: path.join(root, 'data'), productVersion: 'v1' });
  assert.equal(repaired.changed, true);
  const second = deployFeishuService({ sourceServiceRoot: fixture(path.join(root, 'v2'), 'v2'), sourceNode: node, dataRoot: path.join(root, 'data'), productVersion: 'v2' });
  assert.equal(second.changed, true);
  assert.equal(JSON.parse(fs.readFileSync(path.join(second.releaseRoot, 'service', 'usage-bar-service.json'))).productVersion, 'v2');
  assert.equal(JSON.parse(fs.readFileSync(path.join(path.dirname(second.releaseRoot), 'previous', 'service', 'usage-bar-service.json'))).productVersion, 'v1');
  assert.throws(() => deployFeishuService({ sourceServiceRoot: fixture(path.join(root, 'bad'), 'v3', false), sourceNode: node, dataRoot: path.join(root, 'data'), productVersion: 'v3' }), /健康检查失败/);
  assert.equal(JSON.parse(fs.readFileSync(path.join(second.releaseRoot, 'service', 'usage-bar-service.json'))).productVersion, 'v2');
});
