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
	assert.match(result.stdout, /lark-cli 1\.0\.93/);
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

test('macOS uses Node only for build tooling and bundles no Node runtime or service', () => {
  const buildScript = fs.readFileSync(path.join(repoRoot, 'scripts', 'build-app.sh'), 'utf8');
  assert.match(buildScript, /node\s+"\$repo_root\/scripts\/prepare-feishu-runtime\.mjs"/);
  assert.match(buildScript, /node\s+"\$repo_root\/scripts\/generate-sbom\.mjs"/);
  assert.doesNotMatch(buildScript, /runtime\/node\/darwin/);
  assert.doesNotMatch(buildScript, /services\/feishu-bridge\/scripts\/bridge-client\.js/);
  assert.doesNotMatch(buildScript, /cp.*node_modules|cp.*node\.exe|cp.*node-runtime/);
});

test('official CLI and Skills share a pinned tag, SHA256 hashes and license', () => {
  const cli = JSON.parse(fs.readFileSync(path.join(repoRoot, 'runtime/lark-cli-runtime.json')));
  const skills = JSON.parse(fs.readFileSync(path.join(repoRoot, 'runtime/lark-skills.json')));
  assert.equal(cli.version, '1.0.93');
  assert.equal(skills.version, cli.version);
  assert.equal(skills.source.tag, `v${cli.version}`);
  assert.equal(cli.automaticUpdate, false);
  assert.equal(cli.license, 'MIT');
  assert.equal(skills.license, 'MIT');
  assert.match(skills.licenseSha256, /^[a-f0-9]{64}$/);
  assert.match(skills.source.sha256, /^[a-f0-9]{64}$/);
  assert.equal(skills.skills.length, 28);
  for (const target of ['darwin-arm64', 'darwin-x64', 'windows-arm64', 'windows-x64']) {
    const artifact = cli.artifacts[target];
    assert.match(artifact.sha256, /^[a-f0-9]{64}$/);
    assert.match(artifact.executableSha256, /^[a-f0-9]{64}$/);
    assert.ok(artifact.embeddedGoModules.some(module => module.name === 'github.com/larksuite/oapi-sdk-go/v3' && module.version === 'v3.7.2'));
  }
  for (const skill of skills.skills) {
    assert.match(skill.name, /^lark-[a-z0-9-]+$/);
    assert.match(skill.files['SKILL.md'], /^[a-f0-9]{64}$/);
    for (const [name, digest] of Object.entries(skill.files)) {
      assert.ok(!name.split('/').includes('..') && !path.isAbsolute(name));
      assert.match(digest, /^[a-f0-9]{64}$/);
    }
  }
});

test('both platform packages include task and toolchain executables plus full Skills', () => {
  const config = JSON.parse(fs.readFileSync(path.join(repoRoot, 'Windows/package.json')));
  assert.equal(config.build.afterSign, 'scripts/seal-runtime.cjs');
  for (const component of ['toolchain', 'task']) {
    assert.ok(config.build.extraResources.some(item => item.from === `../dist/runtime/${component}/windows-\${arch}` && item.filter.includes(`ksf-assistant-${component}.exe`)));
  }
  assert.ok(config.build.extraResources.some(item => item.to === 'runtime/lark-skills'));
  assert.ok(config.build.extraResources.some(item => item.to === 'runtime/lark-cli-runtime.json'));
  const mac = fs.readFileSync(path.join(repoRoot, 'scripts/build-app.sh'), 'utf8');
  assert.match(mac, /for component in toolchain task/);
  assert.match(mac, /lipo -create/);
  assert.match(mac, /cp -R.*dist\/runtime\/lark-skills/);
  const build = fs.readFileSync(path.join(repoRoot, 'scripts/build-core.sh'), 'utf8');
  assert.match(build, /darwin-arm64 darwin-x64 windows-x64 windows-arm64/);
  assert.match(build, /for component in toolchain task/);
  assert.equal((build.match(/go build -buildvcs=false/g) || []).length, 3);
  const windowsBuild = fs.readFileSync(path.join(repoRoot, 'Windows/scripts/build-core.mjs'), 'utf8');
  assert.equal((windowsBuild.match(/'build', '-buildvcs=false'/g) || []).length, 3);
});

test('Windows signing reseals packaged hashes without losing official provenance', async () => {
  const os = require('node:os');
  const crypto = require('node:crypto');
  const sealRuntime = require('../scripts/seal-runtime.cjs');
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ksfas-runtime-seal-'));
  try {
    const runtime = path.join(root, 'resources/runtime');
    const cli = path.join(runtime, 'lark-cli/windows-x64/lark-cli.exe');
    const task = path.join(runtime, 'task/windows-x64/ksf-assistant-task.exe');
    fs.mkdirSync(path.dirname(cli), { recursive: true });
    fs.mkdirSync(path.dirname(task), { recursive: true });
    fs.writeFileSync(cli, 'signed-cli-fixture');
    fs.writeFileSync(task, 'signed-task-fixture');
    const filename = path.join(runtime, 'lark-cli-runtime.json');
    fs.writeFileSync(filename, JSON.stringify({ artifacts: { 'windows-x64': { sha256: 'official-archive', executableSha256: 'upstream-executable' } } }));
    await sealRuntime({ appOutDir: root, electronPlatformName: 'win32' });
    const artifact = JSON.parse(fs.readFileSync(filename)).artifacts['windows-x64'];
    const hash = content => crypto.createHash('sha256').update(content).digest('hex');
    assert.equal(artifact.sha256, 'official-archive');
    assert.equal(artifact.upstreamExecutableSha256, 'upstream-executable');
    assert.equal(artifact.executableSha256, hash('signed-cli-fixture'));
    assert.equal(artifact.taskExecutableSha256, hash('signed-task-fixture'));
  } finally {
    fs.rmSync(root, { recursive: true, force: true });
  }
});

test('staged runtime binaries and every Skill match the bundled hash manifests', {
  skip: !fs.existsSync(path.join(repoRoot, 'dist/runtime/lark-skills/manifest.json')),
}, () => {
  const crypto = require('node:crypto');
  const hash = file => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
  const runtime = path.join(repoRoot, 'dist/runtime');
  const cli = JSON.parse(fs.readFileSync(path.join(runtime, 'lark-cli-runtime.json')));
  for (const [target, artifact] of Object.entries(cli.artifacts)) {
    const binary = path.join(runtime, 'lark-cli', target, artifact.executable);
    if (!fs.existsSync(binary)) continue;
    assert.equal(hash(binary), artifact.executableSha256);
    const task = path.join(runtime, 'task', target, `ksf-assistant-task${target.startsWith('windows') ? '.exe' : ''}`);
    assert.ok(fs.statSync(task).isFile());
    assert.equal(hash(task), artifact.taskExecutableSha256);
    const manager = path.join(runtime, 'toolchain', target, `ksf-assistant-toolchain${target.startsWith('windows') ? '.exe' : ''}`);
    assert.ok(fs.statSync(manager).isFile());
    if (!target.startsWith('windows')) {
      fs.accessSync(task, fs.constants.X_OK);
      fs.accessSync(manager, fs.constants.X_OK);
      fs.accessSync(binary, fs.constants.X_OK);
    }
  }
  const skills = JSON.parse(fs.readFileSync(path.join(runtime, 'lark-skills/manifest.json')));
  const upstream = JSON.parse(fs.readFileSync(path.join(repoRoot, 'runtime/lark-skills.json')));
  assert.deepEqual(skills.skills.map(skill => skill.name), upstream.skills.map(skill => `ksf-${skill.name}`));
  assert.ok(!skills.skills.some(skill => skill.name === 'ksfas'));
  assert.equal(skills.adaptation.revision, 'ksf-names-v2');
  assert.equal(skills.adaptation.upstreamManifestSha256, hash(path.join(repoRoot, 'runtime/lark-skills.json')));
  assert.equal(skills.adaptation.digest, hash(path.join(runtime, 'lark-skills/adaptation-report.json')));
  assert.equal(hash(path.join(runtime, 'lark-skills/LICENSE')), skills.licenseSha256);
  for (const skill of skills.skills) {
    for (const [name, digest] of Object.entries(skill.files)) {
      assert.equal(hash(path.join(runtime, 'lark-skills/skills', skill.name, name)), digest);
    }
  }
});
