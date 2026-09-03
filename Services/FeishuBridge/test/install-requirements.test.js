const assert = require('node:assert/strict');
const test = require('node:test');

const { requirements } = require('../scripts/install-requirements');

test('installation requirements export the fixed permission and event catalogs', () => {
  const all = JSON.parse(requirements([]));
  assert.equal(all.requiredScopes.bot.length, 17);
  assert.equal(all.requiredScopes.user.length, 119);
  assert.equal(all.fixedEventKeys.length, 23);
  assert.equal(all.fixedEventKeys.some((key) => key.startsWith('approval.')), false);
  assert.equal(all.runtime.bridgeVersion, '1.0.0');
});

test('installation requirements support shell-safe list formats', () => {
  const user = requirements(['scopes', '--identity', 'user', '--format', 'comma']).trim().split(',');
  const events = requirements(['events', '--format', 'lines']).trim().split('\n');
  assert.equal(user.length, 119);
  assert.equal(events.length, 23);
  assert.ok(user.includes('docx:document:readonly'));
  assert.ok(events.includes('card.action.trigger'));
});

test('installation requirements accept format flags without an explicit subject', () => {
  const all = JSON.parse(requirements(['--format', 'json']));
  assert.equal(all.requiredScopes.bot.length, 17);
});
