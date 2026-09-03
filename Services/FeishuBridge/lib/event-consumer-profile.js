const fs = require('node:fs');
const path = require('node:path');
const { atomicWritePrivateJson } = require('./queue-worker-core');

const EVENT_CONSUMER_PROFILE_SCHEMA_VERSION = 1;
const DEFAULT_EVENT_CONSUMER_PROFILE = 'primary';
const EVENT_CONSUMER_PROFILES = Object.freeze({
  primary: Object.freeze({
    inboundConnection: true,
    inboundScope: 'messages_and_cards',
    localCapabilitiesPreserved: true,
  }),
  'manual-only': Object.freeze({
    inboundConnection: false,
    inboundScope: 'none',
    localCapabilitiesPreserved: true,
  }),
});

function defaultEventConsumerProfilePath(dataRoot) {
  return path.join(dataRoot, 'event-consumer-profile.json');
}

function normalizeEventConsumerProfile(value) {
  const profile = String(value || '').trim();
  if (!Object.hasOwn(EVENT_CONSUMER_PROFILES, profile)) {
    throw new Error(`unsupported event consumer profile: ${profile || '-'}`);
  }
  return profile;
}

function assertRegularOrMissing(filePath) {
  try {
    const stat = fs.lstatSync(filePath);
    if (stat.isSymbolicLink()) throw new Error(`refusing symbolic link: ${filePath}`);
    if (!stat.isFile()) throw new Error(`expected regular file: ${filePath}`);
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
  }
}

function readEventConsumerProfile(filePath) {
  assertRegularOrMissing(filePath);
  try {
    const loaded = JSON.parse(fs.readFileSync(filePath, 'utf8'));
    if (Number(loaded.schemaVersion) !== EVENT_CONSUMER_PROFILE_SCHEMA_VERSION) {
      throw new Error(`unsupported event consumer profile schema: ${loaded.schemaVersion ?? '-'}`);
    }
    return {
      schemaVersion: EVENT_CONSUMER_PROFILE_SCHEMA_VERSION,
      profile: normalizeEventConsumerProfile(loaded.profile),
      updatedAt: String(loaded.updatedAt || ''),
      source: 'file',
    };
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    return {
      schemaVersion: EVENT_CONSUMER_PROFILE_SCHEMA_VERSION,
      profile: DEFAULT_EVENT_CONSUMER_PROFILE,
      updatedAt: '',
      source: 'default',
    };
  }
}

function inspectEventConsumerProfile(filePath) {
  try {
    return { valid: true, ...readEventConsumerProfile(filePath) };
  } catch (error) {
    return {
      valid: false,
      schemaVersion: EVENT_CONSUMER_PROFILE_SCHEMA_VERSION,
      profile: 'invalid',
      updatedAt: '',
      source: 'file',
      error: String(error.message || error).replace(/\s+/g, ' ').trim().slice(0, 240),
    };
  }
}

function writeEventConsumerProfile(filePath, value, now = new Date()) {
  const profile = normalizeEventConsumerProfile(value);
  const record = {
    schemaVersion: EVENT_CONSUMER_PROFILE_SCHEMA_VERSION,
    profile,
    updatedAt: now.toISOString(),
  };
  assertRegularOrMissing(filePath);
  atomicWritePrivateJson(filePath, record);
  return { ...record, source: 'file' };
}

function eventConsumerProfileDefinition(profile) {
  return {
    profile: normalizeEventConsumerProfile(profile),
    ...EVENT_CONSUMER_PROFILES[profile],
  };
}

function eventConsumerShouldConnect(profile, masterEnabled = true) {
  return Boolean(masterEnabled && eventConsumerProfileDefinition(profile).inboundConnection);
}

function publicEventConsumerProfileCatalog() {
  return Object.keys(EVENT_CONSUMER_PROFILES).map(eventConsumerProfileDefinition);
}

module.exports = {
  DEFAULT_EVENT_CONSUMER_PROFILE,
  EVENT_CONSUMER_PROFILES,
  EVENT_CONSUMER_PROFILE_SCHEMA_VERSION,
  defaultEventConsumerProfilePath,
  eventConsumerProfileDefinition,
  eventConsumerShouldConnect,
  inspectEventConsumerProfile,
  normalizeEventConsumerProfile,
  publicEventConsumerProfileCatalog,
  readEventConsumerProfile,
  writeEventConsumerProfile,
};
