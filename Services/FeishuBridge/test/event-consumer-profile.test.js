const assert = require('node:assert/strict');
const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const {
  defaultEventConsumerProfilePath,
  eventConsumerShouldConnect,
  inspectEventConsumerProfile,
  publicEventConsumerProfileCatalog,
  readEventConsumerProfile,
  writeEventConsumerProfile,
} = require('../lib/event-consumer-profile');
const { buildRuntime } = require('../lib/bridge-client-core');
const { assertPrivateMode } = require('./helpers/platform-private');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-event-profile-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('missing event profile defaults to primary without creating a file', (t) => {
  const dir = temporaryDirectory(t);
  const filePath = defaultEventConsumerProfilePath(dir);
  assert.deepEqual(readEventConsumerProfile(filePath), {
    schemaVersion: 1,
    profile: 'primary',
    updatedAt: '',
    source: 'default',
  });
  assert.equal(fs.existsSync(filePath), false);
});

test('event profile writes securely and preserves only reviewed profiles', (t) => {
  const dir = temporaryDirectory(t);
  const filePath = defaultEventConsumerProfilePath(dir);
  const written = writeEventConsumerProfile(filePath, 'manual-only', new Date('2026-09-03T08:00:00.000Z'));
  assert.equal(written.profile, 'manual-only');
  assert.equal(readEventConsumerProfile(filePath).profile, 'manual-only');
  assertPrivateMode(filePath, 0o600);
  assert.throws(() => writeEventConsumerProfile(filePath, 'cards-only'), /unsupported event consumer profile/);
});

test('invalid profile files fail closed and can be inspected without throwing', (t) => {
  const dir = temporaryDirectory(t);
  const filePath = defaultEventConsumerProfilePath(dir);
  fs.writeFileSync(filePath, '{"schemaVersion":1,"profile":"cards-only"}\n');
  assert.throws(() => readEventConsumerProfile(filePath), /unsupported event consumer profile/);
  const inspected = inspectEventConsumerProfile(filePath);
  assert.equal(inspected.valid, false);
  assert.equal(inspected.profile, 'invalid');
});

test('only primary opens inbound while both profiles preserve local capabilities', () => {
  assert.equal(eventConsumerShouldConnect('primary', true), true);
  assert.equal(eventConsumerShouldConnect('primary', false), false);
  assert.equal(eventConsumerShouldConnect('manual-only', true), false);
  assert.deepEqual(publicEventConsumerProfileCatalog(), [
    {
      profile: 'primary',
      inboundConnection: true,
      inboundScope: 'messages_and_cards',
      localCapabilitiesPreserved: true,
    },
    {
      profile: 'manual-only',
      inboundConnection: false,
      inboundScope: 'none',
      localCapabilitiesPreserved: true,
    },
  ]);
});

test('runtime stores the profile in the platform private data root', (t) => {
  const dir = temporaryDirectory(t);
  const runtime = buildRuntime(dir, { FEISHU_BRIDGE_DATA_DIR: dir }, { platform: process.platform });
  assert.equal(runtime.eventConsumerProfilePath, path.join(dir, 'event-consumer-profile.json'));
  assert.equal(runtime.privatePathBoundary.secure, true);
});

test('unified client exposes catalog and securely changes the local profile', (t) => {
  const dir = temporaryDirectory(t);
  const script = path.join(__dirname, '..', 'scripts', 'bridge-client.js');
  const env = {
    ...process.env,
    FEISHU_BRIDGE_DATA_DIR: dir,
    FEISHU_BRIDGE_LOG_DIR: dir,
  };
  const catalog = JSON.parse(execFileSync(process.execPath, [script, 'profile', 'catalog'], {
    cwd: path.join(__dirname, '..'),
    env,
    encoding: 'utf8',
  }));
  assert.equal(catalog.sharedAppRule, 'exactly_one_primary');
  assert.deepEqual(catalog.profiles.map((item) => item.profile), ['primary', 'manual-only']);

  const updated = JSON.parse(execFileSync(
    process.execPath,
    [script, 'profile', 'set', 'manual-only'],
    { cwd: path.join(__dirname, '..'), env, encoding: 'utf8' },
  ));
  assert.equal(updated.previousProfile, 'primary');
  assert.equal(updated.profile, 'manual-only');
  assert.equal(readEventConsumerProfile(defaultEventConsumerProfilePath(dir)).profile, 'manual-only');
});
