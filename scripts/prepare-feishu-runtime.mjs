import { createHash } from 'node:crypto';
import { createWriteStream, existsSync, mkdirSync, readFileSync, rmSync, cpSync, chmodSync, writeFileSync } from 'node:fs';
import { get } from 'node:https';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { pipeline } from 'node:stream/promises';
import { fileURLToPath } from 'node:url';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const manifest = JSON.parse(readFileSync(path.join(repoRoot, 'runtime', 'node-runtime.json'), 'utf8'));
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
    const item = manifest.artifacts?.[key];
    if (!item?.archive || !/^[a-f0-9]{64}$/.test(item.sha256) || !item.executable) {
      throw new Error(`Invalid Node runtime manifest entry: ${key}`);
    }
  }
  for (const relative of ['package.json', 'package-lock.json', 'bot-bridge.js', 'scripts/bridge-client.js']) {
    if (!existsSync(path.join(repoRoot, 'Services', 'FeishuBridge', relative))) {
      throw new Error(`Feishu Bridge runtime file is missing: ${relative}`);
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
        reject(new Error(`Node runtime download failed with HTTP ${response.statusCode}`));
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
  const result = spawnSync(command, commandArgs, { cwd: repoRoot, stdio: 'inherit', ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed with exit code ${result.status}`);
}

function stageService() {
  const source = path.join(repoRoot, 'Services', 'FeishuBridge');
  const target = path.join(repoRoot, 'dist', 'services', 'feishu-bridge');
  rmSync(target, { recursive: true, force: true });
  mkdirSync(target, { recursive: true });
  for (const entry of ['bot-bridge.js', 'package.json', 'package-lock.json', 'lib', 'scripts', 'windows', 'launchd']) {
    const from = path.join(source, entry);
    if (existsSync(from)) cpSync(from, path.join(target, entry), { recursive: true });
  }
  const npmArgs = ['ci', '--omit=dev', '--ignore-scripts', '--no-audit', '--no-fund'];
  if (process.platform === 'win32') {
    run(process.env.ComSpec || 'cmd.exe', ['/d', '/s', '/c', 'npm.cmd', ...npmArgs], { cwd: target });
  } else {
    run('npm', npmArgs, { cwd: target });
  }
  writeFileSync(path.join(target, 'usage-bar-service.json'), `${JSON.stringify({
    productVersion: '0.9.0-internal.1',
    serviceVersion: JSON.parse(readFileSync(path.join(source, 'package.json'), 'utf8')).version,
    nodeVersion: manifest.version,
  }, null, 2)}\n`);
}

async function stageRuntime(target) {
  const item = manifest.artifacts[target];
  if (!item) throw new Error(`Unsupported runtime target: ${target}`);
  const cache = path.join(repoRoot, 'dist', 'cache', 'node');
  const archive = path.join(cache, item.archive);
  mkdirSync(cache, { recursive: true });
  if (!existsSync(archive) || sha256(archive) !== item.sha256) {
    rmSync(archive, { force: true });
    await download(`${manifest.baseUrl}/${item.archive}`, archive);
  }
  if (sha256(archive) !== item.sha256) throw new Error(`Node runtime checksum mismatch: ${item.archive}`);

  const unpack = path.join(cache, `unpack-${target}`);
  rmSync(unpack, { recursive: true, force: true });
  mkdirSync(unpack, { recursive: true });
  if (item.archive.endsWith('.zip')) {
    const quote = (value) => `'${String(value).replaceAll("'", "''")}'`;
    run('powershell.exe', ['-NoLogo', '-NoProfile', '-NonInteractive', '-Command',
      `Expand-Archive -LiteralPath ${quote(archive)} -DestinationPath ${quote(unpack)} -Force`]);
  } else {
    run('tar', ['-xzf', archive, '-C', unpack]);
  }
  const extractedRoot = path.join(unpack, item.archive.replace(/\.(?:zip|tar\.gz)$/, ''));
  const source = path.join(extractedRoot, item.executable);
  if (!existsSync(source)) throw new Error(`Node executable missing after extraction: ${target}`);
  const outputDir = path.join(repoRoot, 'dist', 'runtime', 'node', target);
  rmSync(outputDir, { recursive: true, force: true });
  mkdirSync(outputDir, { recursive: true });
  const output = path.join(outputDir, target.startsWith('windows-') ? 'node.exe' : 'node');
  cpSync(source, output);
  if (!target.startsWith('windows-')) chmodSync(output, 0o755);
  rmSync(unpack, { recursive: true, force: true });
}

assertLayout();
if (verifyOnly) {
  console.log(`Feishu runtime manifest is valid: Node ${manifest.version}`);
  process.exit(0);
}
if (!['windows', 'darwin'].includes(platform) || arches.length === 0) {
  throw new Error('Usage: prepare-feishu-runtime.mjs --platform windows|darwin --arch x64 [--arch arm64]');
}
stageService();
for (const arch of [...new Set(arches)]) await stageRuntime(`${platform}-${arch}`);
