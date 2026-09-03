const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const { atomicWritePrivateJson } = require('./queue-worker-core');
const { chmodPrivate, privatePermissionsSatisfied } = require('./platform-runtime');

// lark-cli 1.0.92 currently exposes 25 keys. The two Approval keys are excluded;
// the remaining 23 keys are frozen here and never auto-expand with dependency upgrades.
const FIXED_EVENT_KEYS = Object.freeze([
  'application.bot.menu_v6',
  'board.whiteboard.updated_v1',
  'card.action.trigger',
  'im.chat.disbanded_v1',
  'im.chat.member.bot.added_v1',
  'im.chat.member.bot.deleted_v1',
  'im.chat.member.user.added_v1',
  'im.chat.member.user.deleted_v1',
  'im.chat.member.user.withdrawn_v1',
  'im.chat.updated_v1',
  'im.message.message_read_v1',
  'im.message.reaction.created_v1',
  'im.message.reaction.deleted_v1',
  'im.message.receive_v1',
  'minutes.minute.generated_v1',
  'task.task.update_user_access_v2',
  'vc.meeting.participant_meeting_ended_v1',
  'vc.meeting.participant_meeting_joined_v1',
  'vc.meeting.participant_meeting_started_v1',
  'vc.note.generated_v1',
  'vc.recording.recording_ended_v1',
  'vc.recording.recording_started_v1',
  'vc.recording.recording_transcript_generated_v1',
]);

const APPROVAL_EVENT_KEYS = Object.freeze([
  'approval.instance.status_changed_v4',
  'approval.task.status_changed_v4',
]);

const EVENT_KEY_SET = new Set(FIXED_EVENT_KEYS);
const TRANSCRIPT_EVENT_KEYS = new Set(['vc.recording.recording_transcript_generated_v1']);

function fingerprint(value) {
  if (value === undefined || value === null || value === '') return '';
  return `sha256:${crypto.createHash('sha256').update(String(value)).digest('hex').slice(0, 20)}`;
}

function firstValue(value, keys) {
  if (!value || typeof value !== 'object') return undefined;
  for (const key of keys) {
    if (value[key] !== undefined && value[key] !== null && value[key] !== '') return value[key];
  }
  for (const child of Object.values(value)) {
    if (!child || typeof child !== 'object') continue;
    const found = firstValue(child, keys);
    if (found !== undefined) return found;
  }
  return undefined;
}

function textMetrics(value) {
  if (!value || typeof value !== 'object') return { length: 0, fingerprint: '' };
  const candidates = [];
  const walk = (item, key = '') => {
    if (typeof item === 'string' && /(?:text|content|transcript|word|sentence)/i.test(key)) candidates.push(item);
    else if (Array.isArray(item)) item.forEach((child) => walk(child, key));
    else if (item && typeof item === 'object') Object.entries(item).forEach(([childKey, child]) => walk(child, childKey));
  };
  walk(value);
  const joined = candidates.join('\n');
  return { length: joined.length, fingerprint: fingerprint(joined) };
}

function sanitizeEvent(eventKey, raw, receivedAt = new Date().toISOString()) {
  if (!EVENT_KEY_SET.has(eventKey)) throw new Error('event_key_not_in_fixed_catalog');
  const eventId = firstValue(raw, ['event_id', 'eventId', 'uuid', 'message_id']);
  if (!eventId) throw new Error('event_id_missing');
  const actor = firstValue(raw, ['operator_id', 'open_id', 'user_id', 'sender_id', 'participant_id']);
  const resource = firstValue(raw, [
    'chat_id', 'message_id', 'task_guid', 'whiteboard_id', 'whiteboard_token',
    'meeting_id', 'note_id', 'minute_token', 'recording_id', 'app_id',
  ]);
  const occurredAt = firstValue(raw, ['create_time', 'event_time', 'timestamp', 'update_time']) || receivedAt;
  const metrics = textMetrics(raw);
  return {
    schemaVersion: 1,
    eventKey,
    eventFingerprint: fingerprint(eventId),
    actorFingerprint: fingerprint(actor),
    resourceFingerprint: fingerprint(resource),
    occurredAt: String(occurredAt),
    receivedAt,
    contentLength: metrics.length,
    contentFingerprint: metrics.fingerprint,
    transcriptContentStored: false,
    transcriptEvent: TRANSCRIPT_EVENT_KEYS.has(eventKey),
  };
}

function ensurePrivateDirectory(dir) {
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const stat = fs.lstatSync(dir);
  if (!stat.isDirectory() || stat.isSymbolicLink()) throw new Error('unsafe_event_inbox_directory');
  chmodPrivate(dir, 0o700);
}

function readJson(pathValue, fallback) {
  try {
    const stat = fs.lstatSync(pathValue);
    if (!stat.isFile() || stat.isSymbolicLink() || !privatePermissionsSatisfied(stat)) {
      throw new Error('unsafe_event_state_file');
    }
    return JSON.parse(fs.readFileSync(pathValue, 'utf8'));
  } catch (error) {
    if (error.code === 'ENOENT') return fallback;
    throw error;
  }
}

function dailyPath(dir, iso) {
  const date = /^\d{4}-\d{2}-\d{2}/.test(String(iso)) ? String(iso).slice(0, 10) : new Date().toISOString().slice(0, 10);
  return path.join(dir, `events-${date}.jsonl`);
}

function appendPrivateJsonl(filePath, value) {
  const descriptor = fs.openSync(filePath, fs.constants.O_CREAT | fs.constants.O_APPEND | fs.constants.O_WRONLY, 0o600);
  try {
    const line = `${JSON.stringify(value)}\n`;
    fs.writeSync(descriptor, line, null, 'utf8');
    fs.fsyncSync(descriptor);
  } finally {
    fs.closeSync(descriptor);
  }
  chmodPrivate(filePath, 0o600);
}

function readCompleteJsonl(filePath) {
  try {
    const stat = fs.lstatSync(filePath);
    if (!stat.isFile() || stat.isSymbolicLink() || !privatePermissionsSatisfied(stat)) {
      throw new Error('unsafe_event_inbox_file');
    }
    const text = fs.readFileSync(filePath, 'utf8');
    const complete = text.endsWith('\n') ? text : text.slice(0, Math.max(0, text.lastIndexOf('\n') + 1));
    return complete.split('\n').filter(Boolean).flatMap((line) => {
      try { return [JSON.parse(line)]; } catch { return []; }
    });
  } catch (error) {
    if (error.code === 'ENOENT') return [];
    throw error;
  }
}

function inspectEventInbox(dir, { enabled = true } = {}) {
  if (!enabled) return { enabled: false, exists: fs.existsSync(dir), secure: true };
  try {
    const stat = fs.lstatSync(dir);
    if (!stat.isDirectory() || stat.isSymbolicLink()) {
      return { enabled: true, exists: true, secure: false, error: 'unsafe_event_inbox_directory' };
    }
    const names = fs.readdirSync(dir).filter((name) => /^events-\d{4}-\d{2}-\d{2}\.jsonl$/.test(name));
    let bytes = 0;
    let filesSecure = true;
    for (const name of names) {
      const item = fs.lstatSync(path.join(dir, name));
      bytes += item.isFile() ? item.size : 0;
      if (!item.isFile() || item.isSymbolicLink() || !privatePermissionsSatisfied(item)) filesSecure = false;
    }
    const statePath = path.join(dir, 'state.json');
    let stateSecure = true;
    let state = {};
    if (fs.existsSync(statePath)) {
      const stateStat = fs.lstatSync(statePath);
      stateSecure = stateStat.isFile() && !stateStat.isSymbolicLink()
        && privatePermissionsSatisfied(stateStat);
      if (stateSecure) state = JSON.parse(fs.readFileSync(statePath, 'utf8'));
    }
    return {
      enabled: true,
      exists: true,
      secure: privatePermissionsSatisfied(stat) && filesSecure && stateSecure,
      fixedEventCount: FIXED_EVENT_KEYS.length,
      files: names.length,
      bytes,
      received: Number(state.received || 0),
      duplicates: Number(state.duplicates || 0),
      lastReceivedAt: state.lastReceivedAt || '',
      lastEventKey: state.lastEventKey || '',
      lastError: state.lastError || '',
    };
  } catch (error) {
    if (error.code === 'ENOENT') {
      return { enabled: true, exists: false, secure: false, fixedEventCount: FIXED_EVENT_KEYS.length };
    }
    return { enabled: true, exists: true, secure: false, error: String(error.message || error) };
  }
}

function createEventInbox({ dir, maxDedupeIds = 10000 } = {}) {
  if (!dir) throw new Error('event inbox directory is required');
  ensurePrivateDirectory(dir);
  const statePath = path.join(dir, 'state.json');

  function readState() {
    return readJson(statePath, { schemaVersion: 1, processed: {}, received: 0, duplicates: 0 });
  }

  function writeState(state) {
    const entries = Object.entries(state.processed || {});
    const processed = Object.fromEntries(entries.slice(Math.max(0, entries.length - maxDedupeIds)));
    atomicWritePrivateJson(statePath, { ...state, schemaVersion: 1, processed });
  }

  function receive(eventKey, raw, receivedAt = new Date().toISOString()) {
    const record = sanitizeEvent(eventKey, raw, receivedAt);
    const state = readState();
    if (state.processed?.[record.eventFingerprint]) {
      writeState({ ...state, duplicates: Number(state.duplicates || 0) + 1, lastDuplicateAt: receivedAt });
      return { status: 'duplicate', record };
    }
    appendPrivateJsonl(dailyPath(dir, receivedAt), record);
    writeState({
      ...state,
      processed: { ...(state.processed || {}), [record.eventFingerprint]: receivedAt },
      received: Number(state.received || 0) + 1,
      lastReceivedAt: receivedAt,
      lastEventKey: eventKey,
      lastError: '',
    });
    return { status: 'stored', record };
  }

  function files() {
    return fs.readdirSync(dir)
      .filter((name) => /^events-\d{4}-\d{2}-\d{2}\.jsonl$/.test(name))
      .sort();
  }

  function recent(limit = 20) {
    const all = files().slice(-7).flatMap((name) => readCompleteJsonl(path.join(dir, name)));
    return all.slice(Math.max(0, all.length - Math.max(1, Math.min(100, Number(limit) || 20)))).reverse();
  }

  function get(eventFingerprint) {
    return files().slice().reverse().flatMap((name) => readCompleteJsonl(path.join(dir, name)))
      .find((record) => record.eventFingerprint === eventFingerprint) || null;
  }

  function status() {
    const state = readState();
    const names = files();
    const bytes = names.reduce((total, name) => total + fs.statSync(path.join(dir, name)).size, 0);
    return {
      enabled: true,
      secure: privatePermissionsSatisfied(fs.statSync(dir)),
      fixedEventCount: FIXED_EVENT_KEYS.length,
      files: names.length,
      bytes,
      received: Number(state.received || 0),
      duplicates: Number(state.duplicates || 0),
      lastReceivedAt: state.lastReceivedAt || '',
      lastEventKey: state.lastEventKey || '',
      lastError: state.lastError || '',
    };
  }

  return { get, receive, recent, status };
}

function validateConfiguredEventKeys(keys) {
  const values = [...new Set(keys || [])];
  const rejected = values.filter((key) => !EVENT_KEY_SET.has(key));
  if (rejected.length) throw new Error(`unsupported_event_keys:${rejected.join(',')}`);
  return values;
}

module.exports = {
  APPROVAL_EVENT_KEYS,
  FIXED_EVENT_KEYS,
  TRANSCRIPT_EVENT_KEYS,
  createEventInbox,
  fingerprint,
  inspectEventInbox,
  readCompleteJsonl,
  sanitizeEvent,
  validateConfiguredEventKeys,
};
