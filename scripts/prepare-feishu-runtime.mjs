import { createHash } from 'node:crypto';
import { createWriteStream, existsSync, mkdirSync, readFileSync, rmSync, cpSync, chmodSync, writeFileSync, lstatSync, mkdtempSync, renameSync } from 'node:fs';
import { get } from 'node:https';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';
import { adaptSkills } from './adapt-lark-skills.mjs';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const larkCliManifest = JSON.parse(readFileSync(path.join(repoRoot, 'runtime', 'lark-cli-runtime.json'), 'utf8'));
const skillsManifest = JSON.parse(readFileSync(path.join(repoRoot, 'runtime', 'lark-skills.json'), 'utf8'));
const args = process.argv.slice(2);
const verifyOnly = args.includes('--verify');
const updateControlledHashes = args.includes('--update-controlled-hashes');
const windowsDevelopmentRuntime = args.includes('--windows-development-runtime');
const platform = valueAfter('--platform');
const arches = valuesAfter('--arch');

function valueAfter(flag) {
  const index = args.indexOf(flag);
  return index >= 0 ? args[index + 1] : '';
}

function valuesAfter(flag) {
  const values = [];
  for (let index = 0; index < args.length; index += 1) {
    if (args[index] === flag && args[index + 1]) values.push(args[index + 1]);
  }
  return values;
}

function assertLayout() {
	if (!/^\d+\.\d+\.\d+-ksfassistant\.\d+$/.test(larkCliManifest.version) || !/^\d+\.\d+\.\d+$/.test(larkCliManifest.upstreamVersion) || !larkCliManifest.version.startsWith(`${larkCliManifest.upstreamVersion}-`) || skillsManifest.version !== larkCliManifest.upstreamVersion || skillsManifest.source.tag !== `v${larkCliManifest.upstreamVersion}` || skillsManifest.license !== 'MIT' || !/^[a-f0-9]{64}$/.test(skillsManifest.source.sha256)) throw new Error('managed CLI/Skills version or license mismatch');
	if (!safeRelative(larkCliManifest.source?.archive) || !safeRelative(larkCliManifest.source?.root) || !safeRelative(larkCliManifest.patch?.path) || Object.hasOwn(larkCliManifest.patch || {}, 'version') || !safeRelative(larkCliManifest.apiMetadata?.archive) || !safeRelative(larkCliManifest.apiMetadata?.path) || !/^https:\/\/open\.feishu\.cn\//.test(larkCliManifest.apiMetadata?.url || '') || !/^[a-f0-9]{64}$/.test(larkCliManifest.source?.sha256 || '') || !/^[a-f0-9]{64}$/.test(larkCliManifest.patch?.sha256 || '') || !/^[a-f0-9]{64}$/.test(larkCliManifest.apiMetadata?.sha256 || '') || !Number.isInteger(larkCliManifest.apiMetadata?.serviceCount) || larkCliManifest.apiMetadata.serviceCount <= 0 || larkCliManifest.apiMetadata.normalization !== `release-schema-v${larkCliManifest.upstreamVersion}` || sha256(path.join(repoRoot, larkCliManifest.patch.path)) !== larkCliManifest.patch.sha256) throw new Error('Invalid controlled lark-cli source, metadata, or patch manifest');
	const names = new Set();
	for (const skill of skillsManifest.skills) {
		if (!/^[a-z][a-z0-9-]+$/.test(skill.name) || names.has(skill.name) || !skill.files['SKILL.md']) throw new Error('Invalid skill name');
		names.add(skill.name);
		for (const [name, digest] of Object.entries(skill.files)) {
			if (!safeRelative(name) || !/^[a-f0-9]{64}$/.test(digest)) throw new Error('Invalid skill file manifest');
		}
	}
	for (const key of ['windows-x64', 'windows-arm64', 'darwin-x64', 'darwin-arm64']) {
		const cli = larkCliManifest.artifacts?.[key];
    if (!cli?.archive || !/^[a-f0-9]{64}$/.test(cli.sha256) || !cli.executable || !/^[a-f0-9]{64}$/.test(cli.executableSha256 || '') || cli.managedVersion !== larkCliManifest.version) {
      throw new Error(`Invalid lark-cli runtime manifest entry: ${key}`);
    }
  }
}

function safeRelative(name) {
  return !!name && !name.includes('\\') && !name.includes(':') && !name.startsWith('/') && name.split('/').every(part => part && part !== '.' && part !== '..');
}

async function download(url, destination) {
  const parsed = new URL(url);
  if (parsed.protocol !== 'https:' || !['github.com', 'codeload.github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com', 'open.feishu.cn'].includes(parsed.hostname)) throw new Error('Non-official download host refused');
  await new Promise((resolve, reject) => {
    const request = get(url, (response) => {
      if ([301, 302, 307, 308].includes(response.statusCode)) {
        response.resume();
        download(new URL(response.headers.location, url), destination).then(resolve, reject);
        return;
      }
      if (response.statusCode !== 200) {
        response.resume();
        reject(new Error(`Runtime download failed with HTTP ${response.statusCode}`));
        return;
      }
      pipeline(response, createWriteStream(destination)).then(resolve, reject);
    });
    request.on('error', reject);
  });
}

function sha256(filePath) {
  return createHash('sha256').update(readFileSync(filePath)).digest('hex');
}

function run(command, commandArgs, options = {}) {
  const { env = {}, ...rest } = options;
  const result = spawnSync(command, commandArgs, {
    cwd: repoRoot,
    stdio: 'inherit',
    ...rest,
    env: { ...process.env, LC_ALL: 'C', LANG: 'C', ...env },
  });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed with exit code ${result.status}`);
}

let upstreamTestsPassed = false;

function canonicalJSON(value) {
  if (Array.isArray(value)) return value.map(canonicalJSON);
  if (value && typeof value === 'object') return Object.fromEntries(Object.keys(value).sort().map(key => [key, canonicalJSON(value[key])]));
  return value;
}

function normalizeReleaseAPIMetadata(data) {
  const service = name => data.services.find(item => item.name === name);
  const im = service('im');
  const folder = im?.resources?.files?.methods?.folder;
  const chatCreate = im?.resources?.chats?.methods?.create;
  const mail = service('mail')?.resources?.['user_mailbox.threads']?.methods?.list?.parameters;
  const mindnotes = service('mindnotes')?.resources?.nodes?.methods?.list;
  const sameTokens = (actual, expected) => Array.isArray(actual) && [...actual].sort().join(',') === [...expected].sort().join(',');
  if (folder?.id !== 'files.folder' || !sameTokens(chatCreate?.accessTokens, ['tenant', 'user']) || !sameTokens(mindnotes?.accessTokens, ['tenant', 'user']) || mail?.folder_id?.description !== '文件夹 id，支持INBOX、SENT、SPAM、ARCHIVED、SCHEDULED、TRASH、DRAFT以及自定义文件夹ID。与 label_id 必须且只能传一个；两个都不传或两个都传都会报错' || mail?.label_id?.description !== '标签id，支持IMPORTANT、OTHER、FLAGGED以及自定义标签ID。与 folder_id 必须且只能传一个；两个都不传或两个都传都会报错') throw new Error('Official Feishu API metadata differs from the reviewed 1.0.93 release delta');
  delete im.resources.files.methods.folder;
  chatCreate.accessTokens = ['tenant'];
  mindnotes.accessTokens = ['user'];
  mail.folder_id.description = '文件夹 id，支持INBOX、SENT、SPAM、ARCHIVED、SCHEDULED、TRASH、DRAFT以及自定义文件夹ID';
  mail.label_id.description = '标签id，支持IMPORTANT、OTHER、FLAGGED以及自定义标签ID';
  return canonicalJSON(data);
}

function normalizeWindowsDevelopmentAPIMetadata(data) {
  const service = name => data.services.find(item => item.name === name);
  const im = service('im');
  const folder = im?.resources?.files?.methods?.folder;
  const chatCreate = im?.resources?.chats?.methods?.create;
  const mail = service('mail')?.resources?.['user_mailbox.threads']?.methods?.list?.parameters;
  const mindnotes = service('mindnotes')?.resources?.nodes?.methods?.list;
  const includesTokens = (actual, required) => Array.isArray(actual) && required.every(token => actual.includes(token));
  if (folder?.id !== 'files.folder' || !includesTokens(chatCreate?.accessTokens, ['tenant', 'user']) || !includesTokens(mindnotes?.accessTokens, ['tenant', 'user']) || typeof mail?.folder_id?.description !== 'string' || typeof mail?.label_id?.description !== 'string') {
    throw new Error('Official Feishu API metadata is incompatible with the Windows development runtime');
  }
  delete im.resources.files.methods.folder;
  chatCreate.accessTokens = ['tenant'];
  mindnotes.accessTokens = ['user'];
  mail.folder_id.description = '文件夹 id，支持INBOX、SENT、SPAM、ARCHIVED、SCHEDULED、TRASH、DRAFT以及自定义文件夹ID';
  mail.label_id.description = '标签id，支持IMPORTANT、OTHER、FLAGGED以及自定义标签ID';
  return canonicalJSON(data);
}

async function stageAPIMetadata(cache, sourceRoot, target) {
  const item = larkCliManifest.apiMetadata;
  if (windowsDevelopmentRuntime) {
    const metadata = path.join(cache, `development-windows-${target}-api-meta.json`);
    const envelopePath = `${metadata}.download`;
    rmSync(envelopePath, { force: true });
    await download(item.url, envelopePath);
    let envelope;
    try {
      envelope = JSON.parse(readFileSync(envelopePath, 'utf8'));
    } finally {
      rmSync(envelopePath, { force: true });
    }
    if (envelope?.msg !== 'succeeded' || !Array.isArray(envelope?.data?.services) || envelope.data.services.length !== item.serviceCount) throw new Error('Official Feishu API metadata response is invalid');
    writeFileSync(metadata, `${JSON.stringify(normalizeWindowsDevelopmentAPIMetadata(envelope.data), null, 2)}\n`);
    const destination = path.join(sourceRoot, item.path);
    mkdirSync(path.dirname(destination), { recursive: true });
    cpSync(metadata, destination);
    return;
  }
  const metadata = path.join(cache, item.archive);
  if (!existsSync(metadata) || sha256(metadata) !== item.sha256) {
    const envelopePath = `${metadata}.download`;
    rmSync(envelopePath, { force: true });
    await download(item.url, envelopePath);
    let envelope;
    try {
      envelope = JSON.parse(readFileSync(envelopePath, 'utf8'));
    } finally {
      rmSync(envelopePath, { force: true });
    }
    if (envelope?.msg !== 'succeeded' || !Array.isArray(envelope?.data?.services) || envelope.data.services.length !== item.serviceCount) throw new Error('Official Feishu API metadata response is invalid');
    writeFileSync(metadata, `${JSON.stringify(normalizeReleaseAPIMetadata(envelope.data), null, 2)}\n`);
  }
  if (sha256(metadata) !== item.sha256) throw new Error('Official Feishu API metadata checksum mismatch');
  const destination = path.join(sourceRoot, item.path);
  mkdirSync(path.dirname(destination), { recursive: true });
  cpSync(metadata, destination);
}

async function stageLarkCLI(target) {
	const item = larkCliManifest.artifacts[target];
	if (!item) throw new Error(`Unsupported lark-cli target: ${target}`);
	const cache = path.join(repoRoot, 'dist', 'cache', 'lark-cli');
	const archive = path.join(cache, larkCliManifest.source.archive);
	mkdirSync(cache, { recursive: true });
	if (!existsSync(archive) || sha256(archive) !== larkCliManifest.source.sha256) {
		rmSync(archive, { force: true });
		await download(larkCliManifest.source.url, archive);
	}
	if (sha256(archive) !== larkCliManifest.source.sha256) throw new Error(`lark-cli source checksum mismatch: ${larkCliManifest.source.archive}`);

	const unpack = path.join(cache, `source-${target}`);
	rmSync(unpack, { recursive: true, force: true });
	mkdirSync(unpack, { recursive: true });
	run('tar', ['-xzf', archive, '-C', unpack]);
	const sourceRoot = path.join(unpack, larkCliManifest.source.root);
	await stageAPIMetadata(cache, sourceRoot, target);
	const sourceRelative = path.relative(repoRoot, sourceRoot).split(path.sep).join('/');
	if (!safeRelative(sourceRelative)) throw new Error('Invalid controlled lark-cli source directory');
	const patchPath = path.join(repoRoot, larkCliManifest.patch.path);
	// Apply relative to the extracted source itself. Preview builds live below the
	// checkout's dist directory, so allowing Git to discover the parent .git
	// directory would redirect new patch files into the outer worktree.
	const patchEnvironment = { GIT_CEILING_DIRECTORIES: repoRoot };
	const applyArgs = ['apply', '--unsafe-paths', '--unidiff-zero', patchPath];
	run('git', [...applyArgs.slice(0, 1), '--check', ...applyArgs.slice(1)], { cwd: sourceRoot, env: patchEnvironment });
	run('git', applyArgs, { cwd: sourceRoot, env: patchEnvironment });
	run('gofmt', ['-w', 'cmd/auth/auth.go', 'cmd/auth/logout.go', 'cmd/auth/logout_ksfassistant_test.go', 'cmd/auth/scopes.go', 'cmd/config/init.go', 'cmd/config/init_interactive.go', 'cmd/config/init_ksfassistant_test.go', 'cmd/config/bind_test.go', 'internal/keychain/keychain.go'], { cwd: sourceRoot });
	if (!upstreamTestsPassed) {
		const authTestPattern = windowsDevelopmentRuntime ? '^TestAuthLogoutRunPurgeLocalProfile' : '.';
		const configTestPattern = windowsDevelopmentRuntime ? '^TestConfigInitJSONRegistrationUserIsOptional$' : '.';
		if (windowsDevelopmentRuntime) {
			run('go', ['test', './cmd/auth', '-run', authTestPattern], { cwd: sourceRoot });
			run('go', ['test', './cmd/config', '-run', configTestPattern], { cwd: sourceRoot });
			run('go', ['test', './internal/auth', './internal/keychain'], { cwd: sourceRoot });
		} else {
			run('go', ['test', './cmd/auth', './cmd/config', './internal/auth', './internal/keychain'], { cwd: sourceRoot });
		}
		upstreamTestsPassed = true;
	}
	const outputDir = path.join(repoRoot, 'dist', 'runtime', 'lark-cli', target);
	rmSync(outputDir, { recursive: true, force: true });
	mkdirSync(outputDir, { recursive: true });
	const output = path.join(outputDir, item.executable);
	const [goos, archName] = target.split('-');
	const goarch = archName === 'x64' ? 'amd64' : 'arm64';
	const ldflags = `-s -w -X github.com/larksuite/cli/internal/build.Version=${larkCliManifest.version} -X github.com/larksuite/cli/internal/build.Date=2026-09-01`;
	run('go', ['build', '-buildvcs=false', '-trimpath', '-ldflags', ldflags, '-o', output, '.'], { cwd: sourceRoot, env: { CGO_ENABLED: '0', GOOS: goos, GOARCH: goarch } });
	if (updateControlledHashes) item.executableSha256 = sha256(output);
	else if (!windowsDevelopmentRuntime && sha256(output) !== item.executableSha256) throw new Error(`Controlled lark-cli executable checksum mismatch: ${target}`);
	if (!target.startsWith('windows-')) chmodSync(output, 0o755);
  rmSync(unpack, { recursive: true, force: true });
}

async function stageSkills() {
  const cache = path.join(repoRoot, 'dist', 'cache', 'lark-cli');
  mkdirSync(cache, { recursive: true });
  const archive = path.join(cache, skillsManifest.source.archive);
  if (!existsSync(archive) || sha256(archive) !== skillsManifest.source.sha256) {
    await download(skillsManifest.source.url, archive);
  }
  if (sha256(archive) !== skillsManifest.source.sha256) throw new Error('Skills source checksum mismatch');
  const unpack = mkdtempSync(path.join(cache, 'skills-unpack-'));
  const output = path.join(repoRoot, 'dist', 'runtime', 'lark-skills');
  mkdirSync(path.dirname(output), { recursive: true });
  const stage = mkdtempSync(`${output}-stage-`);
  try {
    run('tar', ['-xzf', archive, '-C', unpack, `${skillsManifest.source.root}/skills`, `${skillsManifest.source.root}/LICENSE`]);
    const sourceRoot = path.join(unpack, skillsManifest.source.root);
    const license = path.join(sourceRoot, 'LICENSE');
    if (sha256(license) !== skillsManifest.licenseSha256) throw new Error('License checksum mismatch');
    cpSync(license, path.join(stage, 'LICENSE'));
    for (const skill of skillsManifest.skills) {
      for (const [name, digest] of Object.entries(skill.files)) {
        const source = path.join(sourceRoot, 'skills', skill.name, name);
        if (!lstatSync(source).isFile() || sha256(source) !== digest) throw new Error(`Skill checksum mismatch: ${skill.name}/${name}`);
        const destination = path.join(stage, 'skills', skill.name, name);
        mkdirSync(path.dirname(destination), { recursive: true });
        cpSync(source, destination);
      }
    }
    const execution = JSON.parse(readFileSync(path.join(repoRoot, 'Core/internal/usercommand/execution-manifest.json'), 'utf8'));
    if (execution.version !== larkCliManifest.version) throw new Error('Reviewed execution manifest must match the managed lark-cli version');
    const adapted = adaptSkills({ root: path.join(stage, 'skills'), upstream: skillsManifest, upstreamBytes: readFileSync(path.join(repoRoot, 'runtime/lark-skills.json')), descriptors: execution.descriptors });
    adapted.manifest.version = larkCliManifest.version;
    writeFileSync(path.join(stage, 'manifest.json'), `${JSON.stringify(adapted.manifest, null, 2)}\n`);
    writeFileSync(path.join(stage, 'adaptation-report.json'), adapted.bytes);
    rmSync(output, { recursive: true, force: true });
    renameSync(stage, output);
  } finally {
    rmSync(unpack, { recursive: true, force: true });
    rmSync(stage, { recursive: true, force: true });
  }
}

assertLayout();
if (verifyOnly) {
	console.log(`Feishu runtime manifest is valid: lark-cli ${larkCliManifest.version}`);
  process.exit(0);
}
if (!['windows', 'darwin'].includes(platform) || arches.length === 0) {
  throw new Error('Usage: prepare-feishu-runtime.mjs --platform windows|darwin --arch x64 [--arch arm64]');
}
if (windowsDevelopmentRuntime && platform !== 'windows') throw new Error('Windows development runtime is only available for --platform windows');
if (windowsDevelopmentRuntime && updateControlledHashes) throw new Error('Windows development runtime cannot update controlled release hashes');
for (const arch of [...new Set(arches)]) {
	const target = `${platform}-${arch}`;
	await stageLarkCLI(target);
}
if (windowsDevelopmentRuntime) {
	console.log(`Staged Windows development lark-cli ${larkCliManifest.version}; release manifests and macOS runtimes were not changed.`);
	process.exit(0);
}
await stageSkills();
for (const [target, artifact] of Object.entries(larkCliManifest.artifacts)) {
  const binary = path.join(repoRoot, 'dist/runtime/task', target, `ksf-assistant-task${target.startsWith('windows-') ? '.exe' : ''}`);
  if (existsSync(binary)) artifact.taskExecutableSha256 = sha256(binary);
}
if (updateControlledHashes) writeFileSync(path.join(repoRoot, 'runtime', 'lark-cli-runtime.json'), `${JSON.stringify(larkCliManifest, null, 2)}\n`);
writeFileSync(path.join(repoRoot, 'dist', 'runtime', 'lark-cli-runtime.json'), `${JSON.stringify(larkCliManifest, null, 2)}\n`);
console.log(`Staged pinned lark-cli ${larkCliManifest.version} and ${skillsManifest.skills.length} KSFAssistant-adapted official Skills (no standalone ksfas).`);
