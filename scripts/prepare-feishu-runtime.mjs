import { createHash } from 'node:crypto';
import { createWriteStream, existsSync, mkdirSync, readFileSync, rmSync, cpSync, chmodSync } from 'node:fs';
import { get } from 'node:https';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const larkCliManifest = JSON.parse(readFileSync(path.join(repoRoot, 'runtime', 'lark-cli-runtime.json'), 'utf8'));
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
	for (const key of ['windows-x64', 'windows-arm64', 'darwin-x64', 'darwin-arm64']) {
		const cli = larkCliManifest.artifacts?.[key];
    if (!cli?.archive || !/^[a-f0-9]{64}$/.test(cli.sha256) || !cli.executable) {
      throw new Error(`Invalid lark-cli runtime manifest entry: ${key}`);
    }
  }
}

async function download(url, destination) {
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
  if (!target.startsWith('windows-')) chmodSync(output, 0o755);
  rmSync(unpack, { recursive: true, force: true });
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
