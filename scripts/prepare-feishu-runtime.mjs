import { createHash } from 'node:crypto';
import { createWriteStream, existsSync, mkdirSync, readFileSync, rmSync, cpSync, chmodSync, writeFileSync, lstatSync, mkdtempSync, renameSync } from 'node:fs';
import { get } from 'node:https';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const larkCliManifest = JSON.parse(readFileSync(path.join(repoRoot, 'runtime', 'lark-cli-runtime.json'), 'utf8'));
const skillsManifest = JSON.parse(readFileSync(path.join(repoRoot, 'runtime', 'lark-skills.json'), 'utf8'));
const integration = JSON.parse(readFileSync(path.join(repoRoot, 'runtime', 'lark-skills-integration.json'), 'utf8'));
const args = process.argv.slice(2);
const verifyOnly = args.includes('--verify');
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
	if (larkCliManifest.version !== '1.0.93' || skillsManifest.version !== larkCliManifest.version || skillsManifest.source.tag !== `v${larkCliManifest.version}` || skillsManifest.license !== 'MIT' || !/^[a-f0-9]{64}$/.test(skillsManifest.source.sha256)) throw new Error('CLI/Skills version or license mismatch');
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
    if (!cli?.archive || !/^[a-f0-9]{64}$/.test(cli.sha256) || !cli.executable) {
      throw new Error(`Invalid lark-cli runtime manifest entry: ${key}`);
    }
  }
}

function safeRelative(name) {
  return !!name && !name.includes('\\') && !name.includes(':') && !name.startsWith('/') && name.split('/').every(part => part && part !== '.' && part !== '..');
}

async function download(url, destination) {
  const parsed = new URL(url);
  if (parsed.protocol !== 'https:' || !['github.com', 'codeload.github.com', 'release-assets.githubusercontent.com', 'objects.githubusercontent.com'].includes(parsed.hostname)) throw new Error('Non-official download host refused');
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

async function stageLarkCLI(target) {
  const item = larkCliManifest.artifacts[target];
  if (!item) throw new Error(`Unsupported lark-cli target: ${target}`);
  const cache = path.join(repoRoot, 'dist', 'cache', 'lark-cli');
  const archive = path.join(cache, item.archive);
  mkdirSync(cache, { recursive: true });
  if (!existsSync(archive) || sha256(archive) !== item.sha256) {
    rmSync(archive, { force: true });
    await download(`${larkCliManifest.baseUrl}/${item.archive}`, archive);
  }
  if (sha256(archive) !== item.sha256) throw new Error(`lark-cli checksum mismatch: ${item.archive}`);

  const unpack = path.join(cache, `unpack-${target}`);
  rmSync(unpack, { recursive: true, force: true });
  mkdirSync(unpack, { recursive: true });
  if (item.archive.endsWith('.zip')) {
		if (process.platform === 'win32') {
			const quote = (value) => `'${String(value).replaceAll("'", "''")}'`;
			run('powershell.exe', ['-NoLogo', '-NoProfile', '-NonInteractive', '-Command',
				`Expand-Archive -LiteralPath ${quote(archive)} -DestinationPath ${quote(unpack)} -Force`]);
		} else {
			run('unzip', ['-q', '-o', archive, '-d', unpack]);
		}
  } else {
    run('tar', ['-xzf', archive, '-C', unpack]);
  }
  const candidates = [path.join(unpack, item.executable), path.join(unpack, 'bin', item.executable)];
  const source = candidates.find((candidate) => existsSync(candidate));
  if (!source) throw new Error(`lark-cli executable missing after extraction: ${target}`);
  const outputDir = path.join(repoRoot, 'dist', 'runtime', 'lark-cli', target);
  rmSync(outputDir, { recursive: true, force: true });
  mkdirSync(outputDir, { recursive: true });
  const output = path.join(outputDir, item.executable);
  cpSync(source, output);
  if (item.executableSha256 && item.executableSha256 !== sha256(output)) throw new Error('Extracted CLI executable checksum mismatch');
  item.executableSha256 = sha256(output);
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
    const bundled = structuredClone(skillsManifest);
    const files = {};
    if (integration.schemaVersion !== 1 || integration.name !== 'ksfas' || integration.license !== 'MIT') throw new Error('Invalid integration Skill');
    for (const [name, content] of Object.entries(integration.files)) {
      if (!safeRelative(name) || typeof content !== 'string') throw new Error('Invalid integration resource');
      const destination = path.join(stage, 'skills', integration.name, name);
      mkdirSync(path.dirname(destination), { recursive: true });
      writeFileSync(destination, content);
      files[name] = sha256(destination);
    }
    bundled.skills.push({ name: integration.name, files });
    writeFileSync(path.join(stage, 'manifest.json'), `${JSON.stringify(bundled, null, 2)}\n`);
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
for (const arch of [...new Set(arches)]) {
	const target = `${platform}-${arch}`;
	await stageLarkCLI(target);
}
await stageSkills();
for (const [target, artifact] of Object.entries(larkCliManifest.artifacts)) {
  const binary = path.join(repoRoot, 'dist/runtime/task', target, `ksf-assistant-task${target.startsWith('windows-') ? '.exe' : ''}`);
  if (existsSync(binary)) artifact.taskExecutableSha256 = sha256(binary);
}
writeFileSync(path.join(repoRoot, 'dist', 'runtime', 'lark-cli-runtime.json'), `${JSON.stringify(larkCliManifest, null, 2)}\n`);
console.log(`Staged pinned lark-cli ${larkCliManifest.version} and ${skillsManifest.skills.length} official Skills plus ksfas.`);
