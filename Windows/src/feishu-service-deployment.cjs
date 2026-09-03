'use strict';

const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

function assertChild(root, target) {
  const relative = path.relative(path.resolve(root), path.resolve(target));
  if (!relative || relative.startsWith('..') || path.isAbsolute(relative)) {
    throw new Error(`飞书服务部署路径越界: ${target}`);
  }
}

function healthyRelease(releaseRoot) {
  const serviceRoot = path.join(releaseRoot, 'service');
  const node = path.join(releaseRoot, 'runtime', 'node.exe');
  const client = path.join(serviceRoot, 'scripts', 'bridge-client.js');
  const bridge = path.join(serviceRoot, 'bot-bridge.js');
  const larkCli = path.join(serviceRoot, 'node_modules', '@larksuite', 'cli', 'scripts', 'run.js');
  const larkSDK = path.join(serviceRoot, 'node_modules', '@larksuiteoapi', 'node-sdk', 'package.json');
  if (![node, client, bridge, larkCli, larkSDK].every((item) => fs.existsSync(item) && fs.statSync(item).isFile())) return false;
  for (const script of [client, bridge]) {
    const result = spawnSync(node, ['--check', script], { cwd: serviceRoot, windowsHide: true, encoding: 'utf8' });
    if (result.status !== 0) return false;
  }
  return true;
}

function releaseVersion(releaseRoot) {
  try {
    return JSON.parse(fs.readFileSync(path.join(releaseRoot, 'service', 'usage-bar-service.json'), 'utf8')).productVersion || '';
  } catch {
    return '';
  }
}

function renameWithRetry(source, destination) {
  let lastError;
  for (let attempt = 0; attempt < 10; attempt += 1) {
    try {
      fs.renameSync(source, destination);
      return;
    } catch (error) {
      lastError = error;
      if (!['EPERM', 'EACCES'].includes(error.code)) throw error;
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 50 * (attempt + 1));
    }
  }
  throw lastError;
}

function deployFeishuService({ sourceServiceRoot, sourceNode, dataRoot, productVersion }) {
  const deploymentRoot = path.join(dataRoot, 'services', 'feishu-bridge');
  const current = path.join(deploymentRoot, 'current');
  const previous = path.join(deploymentRoot, 'previous');
  const staging = path.join(deploymentRoot, `staging-${process.pid}`);
  for (const candidate of [current, previous, staging]) assertChild(deploymentRoot, candidate);
  fs.mkdirSync(deploymentRoot, { recursive: true });

  if (releaseVersion(current) === productVersion && healthyRelease(current)) {
    return runtimeAt(current, false);
  }
  fs.rmSync(staging, { recursive: true, force: true });
  fs.mkdirSync(path.join(staging, 'runtime'), { recursive: true });
  fs.cpSync(sourceServiceRoot, path.join(staging, 'service'), { recursive: true });
  fs.copyFileSync(sourceNode, path.join(staging, 'runtime', 'node.exe'));
  if (!healthyRelease(staging)) {
    fs.rmSync(staging, { recursive: true, force: true });
    throw new Error('飞书桥 staging 健康检查失败，未替换当前版本');
  }

  fs.rmSync(previous, { recursive: true, force: true });
  if (fs.existsSync(current)) renameWithRetry(current, previous);
  try {
    renameWithRetry(staging, current);
    if (!healthyRelease(current)) throw new Error('飞书桥 current 健康检查失败');
  } catch (error) {
    fs.rmSync(current, { recursive: true, force: true });
    if (fs.existsSync(previous)) renameWithRetry(previous, current);
    throw error;
  }
  return runtimeAt(current, true);
}

function runtimeAt(releaseRoot, changed) {
  return {
    changed,
    releaseRoot,
    serviceRoot: path.join(releaseRoot, 'service'),
    node: path.join(releaseRoot, 'runtime', 'node.exe'),
  };
}

function removeLegacyWindowsService(runtime, spawn = spawnSync) {
  const installer = path.join(runtime.serviceRoot, 'windows', 'Uninstall-FeishuBridge.ps1');
  const result = spawn('powershell.exe', [
    '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass',
    '-File', installer,
  ], { cwd: runtime.serviceRoot, windowsHide: true, encoding: 'utf8', timeout: 30_000 });
  if (result.error || result.status !== 0) {
    const detail = String(result.stderr || result.stdout || result.error?.message || '').replace(/\s+/g, ' ').trim();
    throw new Error(`旧飞书桥后台入口停用失败${detail ? `: ${detail}` : ''}`);
  }
}

module.exports = { deployFeishuService, healthyRelease, removeLegacyWindowsService };
