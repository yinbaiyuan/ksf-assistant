'use strict';

const { readSettings, remapProjectPins, writeSettingsAtomic } = require('./identity-migration.cjs');

const DEFAULTS = Object.freeze({
  ksfRoot: '',
  selectedFeishuTargetAlias: '',
  pinnedProjectIds: [],
  launchAtLogin: false,
  selectedPricingPlanId: 'openai:gpt-6-astra',
  customPricingPlans: [],
});
const BUILTIN_PRICING_IDS = new Set([
  'openai:gpt-6-astra',
  'openai:gpt-5.6-sol',
  'openai:gpt-5.6-terra',
  'openai:gpt-5.6-luna',
  'deepseek:deepseek-v4-flash:off-peak',
  'deepseek:deepseek-v4-flash:peak',
  'deepseek:deepseek-v4-pro:off-peak',
  'deepseek:deepseek-v4-pro:peak',
]);

class ConfigStore {
  #diskValue;

  constructor(filePath) {
    this.filePath = filePath;
    this.value = this.#load();
  }

  get() {
    return structuredClone(this.value);
  }

  update(patch) {
    const allowed = Object.fromEntries(Object.keys(DEFAULTS)
      .filter((key) => patch && Object.prototype.hasOwnProperty.call(patch, key))
      .map((key) => [key, patch[key]]));
    const next = sanitize(remapProjectPins({ ...this.value, ...allowed }));
    const previousPlans = Array.isArray(this.#diskValue.customPricingPlans) ? this.#diskValue.customPricingPlans : [];
    const diskValue = {
      ...this.#diskValue,
      ...next,
      customPricingPlans: next.customPricingPlans.map((plan) => ({
        ...previousPlans.find((previous) => previous && previous.id === plan.id),
        ...plan,
      })),
    };
    writeSettingsAtomic(this.filePath, diskValue, { overwrite: true });
    this.#diskValue = diskValue;
    this.value = next;
    return this.get();
  }

  #load() {
    this.#diskValue = readSettings(this.filePath) ?? {};
    return sanitize(remapProjectPins(this.#diskValue));
  }
}

function sanitize(value) {
  const string = (candidate) => typeof candidate === 'string' && !/[\u0000-\u001f\u007f]/.test(candidate) ? candidate.trim() : '';
  const rate = (candidate) => Number.isSafeInteger(candidate) && candidate >= 0 && candidate <= 1_000_000_000 ? candidate : null;
  const customPricingPlans = [];
  const seen = new Set();
  for (const candidate of Array.isArray(value.customPricingPlans) ? value.customPricingPlans : []) {
    if (customPricingPlans.length >= 20 || !candidate || typeof candidate !== 'object') break;
    const id = string(candidate.id);
    const provider = string(candidate.provider);
    const model = string(candidate.model);
    const variant = string(candidate.variant);
    const regular = rate(candidate.regularInputMicroUsdPerMillion);
    const cached = rate(candidate.cachedInputMicroUsdPerMillion);
    const output = rate(candidate.outputMicroUsdPerMillion);
    if (!/^custom:[A-Za-z0-9._:-]{1,72}$/.test(id) || seen.has(id) || !provider || !model || provider.length > 60 || model.length > 60 || variant.length > 40 || regular == null || cached == null || output == null) continue;
    seen.add(id);
    customPricingPlans.push({
      id, provider, model, variant, displayName: '',
      regularInputMicroUsdPerMillion: regular,
      cachedInputMicroUsdPerMillion: cached,
      outputMicroUsdPerMillion: output,
      builtIn: false,
    });
  }
  const requestedPricingPlanId = string(value.selectedPricingPlanId);
  const selectedPricingPlanId = BUILTIN_PRICING_IDS.has(requestedPricingPlanId) || seen.has(requestedPricingPlanId)
    ? requestedPricingPlanId
    : DEFAULTS.selectedPricingPlanId;
  return {
    ksfRoot: string(value.ksfRoot),
    selectedFeishuTargetAlias: string(value.selectedFeishuTargetAlias),
    pinnedProjectIds: [...new Set(Array.isArray(value.pinnedProjectIds) ? value.pinnedProjectIds.map(string).filter(Boolean) : [])],
    launchAtLogin: value.launchAtLogin === true,
    selectedPricingPlanId,
    customPricingPlans,
  };
}

module.exports = { ConfigStore, sanitize };
