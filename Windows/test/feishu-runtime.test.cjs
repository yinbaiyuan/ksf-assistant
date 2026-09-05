'use strict';

const assert = require('node:assert/strict');
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const repoRoot = path.resolve(__dirname, '..', '..');

test('bundled Feishu runtime manifest is complete and pinned', () => {
  const result = spawnSync(process.execPath, ['scripts/prepare-feishu-runtime.mjs', '--verify'], {
    cwd: repoRoot,
    encoding: 'utf8',
  });
  assert.equal(result.status, 0, result.stderr);
	assert.match(result.stdout, /lark-cli 1\.0\.92/);
	assert.doesNotMatch(result.stdout, /Node/);
});

test('runtime preparation can unpack Windows artifacts on non-Windows build hosts', () => {
	const source = fs.readFileSync(path.join(repoRoot, 'scripts', 'prepare-feishu-runtime.mjs'), 'utf8');
	assert.match(source, /process\.platform === 'win32'/);
	assert.match(source, /run\('unzip', \['-q', '-o', archive, '-d', unpack\]\)/);
});

test('Windows package copies only Go Feishu production dependencies', () => {
  const packageConfig = JSON.parse(fs.readFileSync(path.join(repoRoot, 'Windows', 'package.json'), 'utf8'));
  const resources = packageConfig.build.extraResources;
	assert.ok(resources.some((item) => item.from === '../dist/runtime/feishu-bridge/windows-${arch}'
    && item.to === 'runtime/feishu-bridge/windows-${arch}'));
	assert.ok(resources.some((item) => item.from === '../dist/runtime/lark-cli/windows-${arch}'
		&& item.to === 'runtime/lark-cli/windows-${arch}'));
	assert.ok(!resources.some((item) => /runtime\/node|services\/feishu-bridge/.test(`${item.from} ${item.to}`)));
});

test('Windows and macOS both inject only the native bridge and lark-cli', () => {
  const windowsMain = fs.readFileSync(path.join(repoRoot, 'Windows', 'src', 'main.cjs'), 'utf8');
  const macClient = fs.readFileSync(path.join(repoRoot, 'Sources', 'KSFAssistant', 'CoreServiceProcessClient.swift'), 'utf8');
	assert.match(windowsMain, /KSF_ASSISTANT_LARK_CLI:\s*runtime\.larkCLI/);
	assert.match(windowsMain, /KSF_ASSISTANT_FEISHU_BRIDGE:\s*runtime\.bridge/);
	assert.doesNotMatch(windowsMain, /KSF_ASSISTANT_NODE|KSF_ASSISTANT_FEISHU_SERVICE_ROOT|FEISHU_GO_PREVIEW/);
  assert.match(macClient, /environment\["KSF_ASSISTANT_FEISHU_BRIDGE"\] = runtime\.bridge\.path/);
  assert.match(macClient, /environment\["KSF_ASSISTANT_LARK_CLI"\] = runtime\.larkCLI\.path/);
  assert.doesNotMatch(macClient, /KSF_ASSISTANT_NODE/);
  assert.doesNotMatch(macClient, /KSF_ASSISTANT_FEISHU_SERVICE_ROOT/);
});

test('macOS package has no Node build or runtime dependency', () => {
  const buildScript = fs.readFileSync(path.join(repoRoot, 'scripts', 'build-app.sh'), 'utf8');
  assert.doesNotMatch(buildScript, /node\s+"\$repo_root\/scripts\//);
  assert.doesNotMatch(buildScript, /runtime\/node\/darwin/);
  assert.doesNotMatch(buildScript, /services\/feishu-bridge\/scripts\/bridge-client\.js/);
  assert.match(buildScript, /go run \.\/cmd\/ksf-assistant-build-assets/);
});
