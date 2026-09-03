#!/usr/bin/env node

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const crypto = require('node:crypto');

const MANAGED_BY = 'feishu-bot-bridge';
const SKILL_NAME = 'feishu-bridge';
const projectRoot = path.resolve(__dirname, '..');
const sourceRoot = path.join(projectRoot, '.agents', 'skills', SKILL_NAME);

function parseArgs(argv) {
  const action = argv[0] || 'install';
  const options = { action, dryRun: false, target: '' };
  for (let index = 1; index < argv.length; index += 1) {
    const token = argv[index];
    if (token === '--dry-run') {
      options.dryRun = true;
      continue;
    }
    if (token === '--target') {
      const value = argv[index + 1];
      if (!value) throw new Error('missing value for --target');
      options.target = path.resolve(value);
      index += 1;
      continue;
    }
    throw new Error(`unknown argument: ${token}`);
  }
  if (!['install', 'check', 'uninstall'].includes(action)) {
    throw new Error('action must be install, check, or uninstall');
  }
  return options;
}

function defaultTarget(homeDir = os.homedir()) {
  return path.join(homeDir, '.agents', 'skills', SKILL_NAME);
}

function markerPath(target) {
  return path.join(target, '.feishu-bridge-skill-install.json');
}

function readJson(filePath) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw error;
  }
}

function validateSource() {
  const skillPath = path.join(sourceRoot, 'SKILL.md');
  if (!fs.existsSync(skillPath)) throw new Error(`skill source is missing: ${skillPath}`);
}

function managedInstall(target) {
  const marker = readJson(markerPath(target));
  return Boolean(marker && marker.managedBy === MANAGED_BY && marker.skillName === SKILL_NAME);
}

function status(target) {
  const skillPath = path.join(target, 'SKILL.md');
  const installation = readJson(path.join(target, 'installation.json'));
  return {
    status: fs.existsSync(skillPath) ? 'installed' : 'not_installed',
    target,
    managed: managedInstall(target),
    projectRoot: installation?.projectRoot,
    projectAvailable: Boolean(installation?.projectRoot && fs.existsSync(path.join(installation.projectRoot, 'scripts', 'bridge-client.js'))),
  };
}

function install(target, { dryRun = false } = {}) {
  validateSource();
  if (fs.existsSync(target) && !managedInstall(target)) {
    throw new Error(`refusing to replace unmanaged skill directory: ${target}`);
  }
  if (dryRun) return { status: 'dry_run', action: 'install', target, projectRoot };

  fs.mkdirSync(path.dirname(target), { recursive: true });
  const nonce = `${process.pid}-${crypto.randomBytes(4).toString('hex')}`;
  const temporary = `${target}.tmp-${nonce}`;
  const backup = `${target}.backup-${nonce}`;
  fs.cpSync(sourceRoot, temporary, { recursive: true, errorOnExist: true, force: false });
  fs.writeFileSync(path.join(temporary, 'installation.json'), `${JSON.stringify({
    schemaVersion: 1,
    projectRoot,
  }, null, 2)}\n`, { mode: 0o600 });
  fs.writeFileSync(path.join(temporary, '.feishu-bridge-skill-install.json'), `${JSON.stringify({
    schemaVersion: 1,
    managedBy: MANAGED_BY,
    skillName: SKILL_NAME,
    projectRoot,
  }, null, 2)}\n`, { mode: 0o600 });

  let previousMoved = false;
  try {
    if (fs.existsSync(target)) {
      fs.renameSync(target, backup);
      previousMoved = true;
    }
    fs.renameSync(temporary, target);
    if (previousMoved) fs.rmSync(backup, { recursive: true, force: true });
  } catch (error) {
    if (fs.existsSync(temporary)) fs.rmSync(temporary, { recursive: true, force: true });
    if (previousMoved && !fs.existsSync(target) && fs.existsSync(backup)) fs.renameSync(backup, target);
    throw error;
  }
  return { status: 'installed', target, projectRoot, restartCodexIfNotVisible: true };
}

function uninstall(target, { dryRun = false } = {}) {
  if (!fs.existsSync(target)) return { status: 'not_installed', target };
  if (!managedInstall(target)) throw new Error(`refusing to remove unmanaged skill directory: ${target}`);
  if (dryRun) return { status: 'dry_run', action: 'uninstall', target };
  fs.rmSync(target, { recursive: true, force: true });
  return { status: 'uninstalled', target, projectPreserved: true };
}

function run(argv = process.argv.slice(2), homeDir = os.homedir()) {
  const options = parseArgs(argv);
  const target = options.target || defaultTarget(homeDir);
  if (options.action === 'check') return status(target);
  if (options.action === 'uninstall') return uninstall(target, options);
  return install(target, options);
}

if (require.main === module) {
  try {
    process.stdout.write(`${JSON.stringify(run(), null, 2)}\n`);
  } catch (error) {
    process.stderr.write(`${JSON.stringify({ status: 'error', error: error.message }, null, 2)}\n`);
    process.exitCode = 1;
  }
}

module.exports = {
  defaultTarget,
  install,
  managedInstall,
  parseArgs,
  run,
  status,
  uninstall,
};
