#!/usr/bin/env node

const fs = require('node:fs');
const path = require('node:path');
const { spawnSync } = require('node:child_process');

function isBridgeRoot(candidate) {
  if (!candidate) return false;
  try {
    const packageJson = JSON.parse(fs.readFileSync(path.join(candidate, 'package.json'), 'utf8'));
    return packageJson.name === 'feishu-bot-bridge'
      && fs.statSync(path.join(candidate, 'scripts', 'bridge-client.js')).isFile();
  } catch {
    return false;
  }
}

function readInstallation(skillRoot) {
  try {
    return JSON.parse(fs.readFileSync(path.join(skillRoot, 'installation.json'), 'utf8'));
  } catch {
    return {};
  }
}

function resolveProjectRoot({
  env = process.env,
  skillRoot = path.resolve(__dirname, '..'),
} = {}) {
  const candidates = [
    env.FEISHU_BRIDGE_PROJECT_ROOT && path.resolve(env.FEISHU_BRIDGE_PROJECT_ROOT),
    path.resolve(skillRoot, '..', '..', '..'),
    readInstallation(skillRoot).projectRoot,
  ];
  return candidates.find(isBridgeRoot) || '';
}

function run(argv = process.argv.slice(2)) {
  const root = resolveProjectRoot();
  if (!root) throw new Error('bridge_project_not_found: set FEISHU_BRIDGE_PROJECT_ROOT or reinstall the skill from the bridge repository');
  if (argv[0] === '--project-root') {
    process.stdout.write(`${root}\n`);
    return 0;
  }
  const result = spawnSync(process.execPath, [path.join(root, 'scripts', 'bridge-client.js'), ...argv], {
    cwd: root,
    env: { ...process.env, FEISHU_BRIDGE_PROJECT_ROOT: root },
    stdio: 'inherit',
  });
  if (result.error) throw result.error;
  return result.status === null ? 1 : result.status;
}

if (require.main === module) {
  try {
    process.exitCode = run();
  } catch (error) {
    process.stderr.write(`${JSON.stringify({ status: 'error', error: error.message }, null, 2)}\n`);
    process.exitCode = 1;
  }
}

module.exports = { isBridgeRoot, readInstallation, resolveProjectRoot, run };
