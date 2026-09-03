'use strict';

const assert = require('node:assert/strict');
const test = require('node:test');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { ConfigStore, sanitize } = require('../src/config-store.cjs');

test('first launch keeps KSF optional instead of guessing a directory', (t) => {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'codexassistant-settings-'));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  assert.equal(new ConfigStore(path.join(root, 'settings.json')).get().ksfRoot, '');
});

test('settings keep only bounded platform-neutral fields', () => {
  assert.deepEqual(sanitize({
    ksfRoot: ' C:\\KSF ',
    feishuBridgeRoot: 'C:\\legacy-bridge-that-must-be-ignored',
    selectedFeishuTargetAlias: '测试用户',
    pinnedProjectIds: ['a', 'a', '', 'b'],
    launchAtLogin: true,
    selectedPricingPlanId: 'custom:team',
    customPricingPlans: [{ id: 'custom:team', provider: '团队', model: '模型', regularInputMicroUsdPerMillion: 1, cachedInputMicroUsdPerMillion: 2, outputMicroUsdPerMillion: 3 }],
    secret: 'must-not-persist',
  }), {
    ksfRoot: 'C:\\KSF',
    selectedFeishuTargetAlias: '测试用户',
    pinnedProjectIds: ['a', 'b'],
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
  assert.equal(value.selectedPricingPlanId, 'openai:gpt-5.6-sol');
  assert.equal(value.customPricingPlans.length, 20);
  assert.equal(value.customPricingPlans[0].builtIn, false);
});

test('deleted selected custom pricing plan falls back to the default', () => {
  assert.equal(sanitize({ selectedPricingPlanId: 'custom:deleted', customPricingPlans: [] }).selectedPricingPlanId, 'openai:gpt-5.6-sol');
});

test('settings reject control characters', () => {
  const value = sanitize({ ksfRoot: 'C:\\safe\u0000bad', pinnedProjectIds: ['ok', 'bad\nvalue'] });
  assert.equal(value.ksfRoot, '');
  assert.deepEqual(value.pinnedProjectIds, ['ok']);
});
