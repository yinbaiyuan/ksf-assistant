const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const test = require('node:test');
const { loadClientConfig } = require('../lib/bridge-client-core');
const { assertPrivateMode, bridgeTestEnv } = require('./helpers/platform-private');

function run(command, args, options = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, options);
    let stdout = '';
    let stderr = '';
    child.stdout.on('data', (chunk) => { stdout += chunk; });
    child.stderr.on('data', (chunk) => { stderr += chunk; });
    child.on('error', reject);
    child.on('exit', (code) => resolve({ code, stdout, stderr }));
    child.stdin.end(options.input || '');
  });
}

test('direct Wiki node read can save only a reviewed Mindnote object identifier', async (t) => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-read-asset-save-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  const fakeLarkPath = path.join(dir, 'fake-lark.js');
  fs.writeFileSync(fakeLarkPath, `#!/usr/bin/env node
const args = process.argv.slice(2);
if (args.includes('--version')) process.stdout.write('lark-cli version 1.0.92\\n');
else if (args[0] === 'auth' && args[1] === 'status') process.stdout.write('{"ok":true}\\n');
else process.stdout.write(JSON.stringify({ok:true,data:{node:{obj_type:'mindnote',obj_token:'mindnote_private',node_token:'wiki_private'}}}) + '\\n');
`);
  fs.chmodSync(fakeLarkPath, 0o700);
  const configPath = path.join(dir, 'private', 'client.json');
  const result = await run(process.execPath, [
    path.join(__dirname, '..', 'scripts', 'bridge-client.js'),
    'capability', 'read', 'wiki.node.get',
    '--payload-file', '-',
    '--save-as', 'Codex桥测试-Mindnote解析-1.0.0',
    '--config', configPath,
  ], {
    cwd: path.join(__dirname, '..'),
    env: bridgeTestEnv(dir, {
      ...process.env,
      LARK_CLI_BIN: fakeLarkPath,
      FEISHU_BRIDGE_LOG_DIR: dir,
      FEISHU_AUDIT_DIR: path.join(dir, 'audit'),
    }),
    stdio: ['pipe', 'pipe', 'pipe'],
    input: JSON.stringify({ 'node-token': 'wiki_input_private' }),
  });
  assert.equal(result.code, 0, result.stderr || result.stdout);
  assert.equal(result.stdout.includes('mindnote_private'), false);
  assert.equal(result.stdout.includes('wiki_input_private'), false);
  const config = loadClientConfig(configPath);
  assert.deepEqual(config.testAssets['Codex桥测试-Mindnote解析-1.0.0'], {
    kind: 'mindnote_id',
    value: 'mindnote_private',
    capabilityId: 'wiki.node.get',
    savedAt: config.testAssets['Codex桥测试-Mindnote解析-1.0.0'].savedAt,
  });
  assertPrivateMode(configPath, 0o600);
});
