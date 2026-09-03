const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');

const {
  install,
  status,
  uninstall,
} = require('../scripts/install-codex-skill');
const {
  isBridgeRoot,
  resolveProjectRoot,
} = require('../.agents/skills/feishu-bridge/scripts/bridge');

const projectRoot = path.resolve(__dirname, '..');
const skillRoot = path.join(projectRoot, '.agents', 'skills', 'feishu-bridge');

function skillFiles(root) {
  const output = [];
  const walk = (directory) => {
    for (const entry of fs.readdirSync(directory, { withFileTypes: true })) {
      const fullPath = path.join(directory, entry.name);
      if (entry.isDirectory()) walk(fullPath);
      else output.push(fullPath);
    }
  };
  walk(root);
  return output;
}

test('repository skill is discoverable, portable, and free of private paths', () => {
  const skill = fs.readFileSync(path.join(skillRoot, 'SKILL.md'), 'utf8').replace(/\r\n/g, '\n');
  assert.match(skill, /^---\nname: feishu-bridge\ndescription:/);
  assert.match(skill, /scripts\/bridge\.js/);
  assert.equal(fs.existsSync(path.join(skillRoot, 'agents', 'openai.yaml')), true);
  assert.equal(fs.existsSync(path.join(skillRoot, 'installation.json')), false);
  for (const filePath of skillFiles(skillRoot)) {
    const content = fs.readFileSync(filePath, 'utf8');
    assert.doesNotMatch(content, /\/Users\/lawis|Documents\/KSF|ksf-feishu-bridge/);
  }
});

test('skill wrapper resolves both repository and installed skill locations', () => {
  assert.equal(isBridgeRoot(projectRoot), true);
  assert.equal(resolveProjectRoot({ env: {}, skillRoot }), projectRoot);

  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-skill-wrapper-'));
  fs.writeFileSync(path.join(temporary, 'installation.json'), `${JSON.stringify({ projectRoot })}\n`);
  assert.equal(resolveProjectRoot({ env: {}, skillRoot: temporary }), projectRoot);
});

test('user-scope installer updates only managed skill directories', () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-skill-install-'));
  const target = path.join(temporary, '.agents', 'skills', 'feishu-bridge');
  const installed = install(target);
  assert.equal(installed.status, 'installed');
  assert.equal(status(target).managed, true);
  assert.equal(status(target).projectAvailable, true);

  const updated = install(target);
  assert.equal(updated.status, 'installed');
  const removed = uninstall(target);
  assert.equal(removed.status, 'uninstalled');
  assert.equal(fs.existsSync(target), false);
});

test('user-scope installer refuses unmanaged targets', () => {
  const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-skill-unmanaged-'));
  const target = path.join(temporary, 'feishu-bridge');
  fs.mkdirSync(target, { recursive: true });
  fs.writeFileSync(path.join(target, 'SKILL.md'), 'unmanaged\n');
  assert.throws(() => install(target), /refusing to replace unmanaged/);
  assert.throws(() => uninstall(target), /refusing to remove unmanaged/);
});
