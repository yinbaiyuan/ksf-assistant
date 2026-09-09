'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { ConfigStore, sanitize } = require('../src/config-store.cjs');

test('first launch keeps KSF optional instead of guessing a directory', (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ksfassistant-settings-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  assert.equal(new ConfigStore(path.join(root, 'settings.json')).get().ksfRoot, '');
});

test('settings keep only bounded platform-neutral fields', () => {
  assert.deepEqual(sanitize({
    ksfRoot: ' C:\\KSF ',
    feishuBridgeRoot: 'C:\\legacy-bridge-that-must-be-ignored',
    selectedFeishuTargetAlias: '测试用户',
    pinnedProjectIds: ['a', 'a', '', 'b'],
    pinnedWorkspaceIds: ['workspace://a', 'workspace://a', '', 'workspace://b'],
    launchAtLogin: true,
    selectedPricingPlanId: 'custom:team',
    customPricingPlans: [{ id: 'custom:team', provider: '团队', model: '模型', regularInputMicroUsdPerMillion: 1, cachedInputMicroUsdPerMillion: 2, outputMicroUsdPerMillion: 3 }],
    secret: 'must-not-persist',
  }), {
    ksfRoot: 'C:\\KSF',
    selectedFeishuTargetAlias: '测试用户',
    pinnedProjectIds: ['a', 'b'],
    pinnedWorkspaceIds: ['workspace://a', 'workspace://b'],
    launchAtLogin: true,
    selectedPricingPlanId: 'custom:team',
    customPricingPlans: [{ id: 'custom:team', provider: '团队', model: '模型', variant: '', displayName: '', regularInputMicroUsdPerMillion: 1, cachedInputMicroUsdPerMillion: 2, outputMicroUsdPerMillion: 3, builtIn: false }],
  });
});

test('pricing settings fall back safely and keep at most twenty custom plans', () => {
  const customPricingPlans = Array.from({ length: 24 }, (_, index) => ({
    id: `custom:${index}`,
    provider: 'P',
    model: `M${index}`,
    regularInputMicroUsdPerMillion: index,
    cachedInputMicroUsdPerMillion: index,
    outputMicroUsdPerMillion: index,
  }));
  const value = sanitize({ selectedPricingPlanId: 'bad\nplan', customPricingPlans });
  assert.equal(value.selectedPricingPlanId, 'openai:gpt-6-astra');
  assert.equal(value.customPricingPlans.length, 20);
  assert.equal(value.customPricingPlans[0].builtIn, false);
});

test('deleted selected custom pricing plan falls back to the default', () => {
  assert.equal(sanitize({ selectedPricingPlanId: 'custom:deleted', customPricingPlans: [] }).selectedPricingPlanId, 'openai:gpt-6-astra');
});

test('Astra is the fresh default and both new and existing selections persist', (t) => {
  const filePath = settingsFixture(t);
  const store = new ConfigStore(filePath);
  assert.equal(store.get().selectedPricingPlanId, 'openai:gpt-6-astra');
  for (const id of ['openai:gpt-5.6-sol', 'openai:gpt-6-astra']) {
    store.update({ selectedPricingPlanId: id });
    assert.equal(new ConfigStore(filePath).get().selectedPricingPlanId, id);
  }
});

test('settings reject control characters', () => {
  const value = sanitize({ ksfRoot: 'C:\\safe\u0000bad', pinnedProjectIds: ['ok', 'bad\nvalue'], pinnedWorkspaceIds: ['workspace://ok', 'bad\nvalue'] });
  assert.equal(value.ksfRoot, '');
  assert.deepEqual(value.pinnedProjectIds, ['ok']);
  assert.deepEqual(value.pinnedWorkspaceIds, ['workspace://ok']);
});

function settingsFixture(t, value) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'ksfassistant-config-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const filePath = path.join(root, 'settings.json');
  if (value !== undefined) fs.writeFileSync(filePath, JSON.stringify(value));
  return filePath;
}

test('disk-only fields survive updates but never cross the renderer boundary', (t) => {
  const filePath = settingsFixture(t, {
    ksfRoot: 'C:\\KSF',
    future: { version: 7, nested: ['unchanged'] },
    feishuBridgeRoot: 'C:\\shared-bridge',
    customPricingPlans: [{ id: 'custom:team', provider: 'P', model: 'M', regularInputMicroUsdPerMillion: 1, cachedInputMicroUsdPerMillion: 2, outputMicroUsdPerMillion: 3, futureRate: { version: 2 } }],
  });
  const store = new ConfigStore(filePath);
  const before = store.get();
  assert.equal(before.future, undefined);
  assert.equal(before.customPricingPlans[0].futureRate, undefined);
  const updated = store.update({
    launchAtLogin: true,
    future: 'renderer-overwrite',
    injected: true,
    feishuBridgeRoot: 'renderer-overwrite',
    customPricingPlans: [{ ...before.customPricingPlans[0], model: 'Updated', futureRate: 'renderer-overwrite', injected: true }],
  });
  const disk = JSON.parse(fs.readFileSync(filePath, 'utf8'));
  assert.deepEqual(disk.future, { version: 7, nested: ['unchanged'] });
  assert.equal(disk.feishuBridgeRoot, 'C:\\shared-bridge');
  assert.deepEqual(disk.customPricingPlans[0].futureRate, { version: 2 });
  assert.equal(disk.customPricingPlans[0].injected, undefined);
  assert.equal(disk.injected, undefined);
  assert.equal(updated.future, undefined);
  assert.equal(updated.customPricingPlans[0].futureRate, undefined);
  assert.equal(updated.customPricingPlans[0].model, 'Updated');
  assert.deepEqual(new ConfigStore(filePath).get(), updated);
  store.update({ selectedFeishuTargetAlias: 'target' });
  assert.deepEqual(JSON.parse(fs.readFileSync(filePath, 'utf8')).future, disk.future);
});

for (const invalid of ['{broken', 'null', '[]', '"text"', '42']) {
  test(`invalid existing settings abort instead of silently defaulting: ${invalid}`, (t) => {
    const filePath = settingsFixture(t);
    fs.writeFileSync(filePath, invalid);
    assert.throws(() => new ConfigStore(filePath), /settings|configuration|配置/i);
    assert.equal(fs.readFileSync(filePath, 'utf8'), invalid);
  });
}

test('settings read errors abort instead of silently defaulting', (t) => {
  const filePath = settingsFixture(t, {});
  const read = fs.readFileSync;
  t.mock.method(fs, 'readFileSync', (target, ...args) => {
    if (target === filePath) throw Object.assign(new Error('permission denied'), { code: 'EACCES' });
    return read(target, ...args);
  });
  assert.throws(() => new ConfigStore(filePath), /settings|configuration|配置/i);
});

test('failed atomic update retains disk and in-memory settings and cleans temporary files', (t) => {
  const filePath = settingsFixture(t, { ksfRoot: 'original', future: { retained: true } });
  const store = new ConfigStore(filePath);
  const originalDisk = fs.readFileSync(filePath, 'utf8');
  const originalValue = store.get();
  t.mock.method(fs, 'renameSync', () => { throw Object.assign(new Error('rename denied'), { code: 'EPERM' }); });
  assert.throws(() => store.update({ ksfRoot: 'replacement' }), /rename denied/);
  assert.equal(fs.readFileSync(filePath, 'utf8'), originalDisk);
  assert.deepEqual(store.get(), originalValue);
  assert.deepEqual(fs.readdirSync(path.dirname(filePath)), ['settings.json']);
});

test('new renderer patch fields never become disk-only fields', (t) => {
  const filePath = settingsFixture(t);
  const store = new ConfigStore(filePath);
  store.update(JSON.parse('{"launchAtLogin":true,"unknown":{"value":1},"__proto__":{"polluted":true}}'));
  const disk = JSON.parse(fs.readFileSync(filePath, 'utf8'));
  assert.deepEqual(disk, store.get());
  assert.equal(disk.unknown, undefined);
  assert.equal(Object.hasOwn(disk, '__proto__'), false);
});

test('existing settings pins remap in memory without rewriting the destination on startup', (t) => {
  const filePath = settingsFixture(t, { pinnedProjectIds: ['10项目/Codex Usage Bar/项目记忆卡.md', 'project:Codex Usage Bar'], future: { retained: true } });
  const original = fs.readFileSync(filePath, 'utf8');
  const store = new ConfigStore(filePath);
  assert.deepEqual(store.get().pinnedProjectIds, ['10项目/KSFAssistant/项目记忆卡.md', 'project:Codex Usage Bar']);
  assert.equal(fs.readFileSync(filePath, 'utf8'), original);
  store.update({ launchAtLogin: true });
  assert.deepEqual(JSON.parse(fs.readFileSync(filePath, 'utf8')).pinnedProjectIds, store.get().pinnedProjectIds);
});

test('a settings file disappearing after metadata read is an explicit read failure', (t) => {
  const filePath = settingsFixture(t, {});
  const read = fs.readFileSync;
  t.mock.method(fs, 'readFileSync', (target, ...args) => {
    if (target === filePath) throw Object.assign(new Error('disappeared'), { code: 'ENOENT' });
    return read(target, ...args);
  });
  assert.throws(() => new ConfigStore(filePath), /ENOENT/);
});

test('a directory at the settings destination is not treated as first launch', (t) => {
  const filePath = settingsFixture(t);
  fs.mkdirSync(filePath);
  assert.throws(() => new ConfigStore(filePath), /设置/);
});

test('atomic update publishes complete settings only after the temporary write', (t) => {
  const filePath = settingsFixture(t, { ksfRoot: 'original', future: { retained: true } });
  const original = fs.readFileSync(filePath, 'utf8');
  const rename = fs.renameSync;
  let published = false;
  t.mock.method(fs, 'renameSync', (temporary, destination) => {
    assert.equal(destination, filePath);
    assert.equal(fs.readFileSync(destination, 'utf8'), original);
    const staged = JSON.parse(fs.readFileSync(temporary, 'utf8'));
    assert.equal(staged.ksfRoot, 'updated');
    assert.deepEqual(staged.future, { retained: true });
    published = true;
    return rename(temporary, destination);
  });
  new ConfigStore(filePath).update({ ksfRoot: 'updated' });
  assert.equal(published, true);
  assert.deepEqual(fs.readdirSync(path.dirname(filePath)), ['settings.json']);
});
