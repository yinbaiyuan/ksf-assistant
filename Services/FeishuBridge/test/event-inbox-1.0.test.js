const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { assertNoPublicPermissionBits } = require('./helpers/platform-private');
const {
  APPROVAL_EVENT_KEYS,
  FIXED_EVENT_KEYS,
  createEventInbox,
  sanitizeEvent,
  validateConfiguredEventKeys,
} = require('../lib/event-inbox');
const { EVENT_WATCHES, resolvedOperation } = require('../lib/event-subscriptions');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-events-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('lark-cli 1.0.92 event catalog freezes all 23 non-Approval keys and rejects future or Approval keys', () => {
  assert.equal(FIXED_EVENT_KEYS.length, 23);
  assert.equal(new Set(FIXED_EVENT_KEYS).size, 23);
  assert.equal(APPROVAL_EVENT_KEYS.length, 2);
  for (const key of APPROVAL_EVENT_KEYS) assert.equal(FIXED_EVENT_KEYS.includes(key), false);
  assert.deepEqual(validateConfiguredEventKeys(['im.message.receive_v1', 'board.whiteboard.updated_v1']), ['im.message.receive_v1', 'board.whiteboard.updated_v1']);
  assert.throws(() => validateConfiguredEventKeys(['approval.instance.status_changed_v4']), /unsupported_event_keys/);
  assert.throws(() => validateConfiguredEventKeys(['future.event_v9']), /unsupported_event_keys/);
});

test('private event inbox deduplicates by event ID, rolls daily files, and stores no transcript body', (t) => {
  const dir = temporaryDirectory(t);
  const inbox = createEventInbox({ dir });
  const raw = {
    header: { event_id: 'evt_private' },
    event: {
      recording_id: 'recording_private',
      open_id: 'ou_private',
      transcript: '这段逐字稿绝对不能落盘',
      create_time: '2026-09-02T10:00:00+08:00',
    },
  };
  assert.equal(inbox.receive('vc.recording.recording_transcript_generated_v1', raw, '2026-09-02T02:00:01.000Z').status, 'stored');
  assert.equal(inbox.receive('vc.recording.recording_transcript_generated_v1', raw, '2026-09-02T02:00:02.000Z').status, 'duplicate');
  const files = fs.readdirSync(dir).sort();
  assert.deepEqual(files, ['events-2026-09-02.jsonl', 'state.json']);
  const serialized = files.map((name) => fs.readFileSync(path.join(dir, name), 'utf8')).join('\n');
  assert.equal(serialized.includes('这段逐字稿绝对不能落盘'), false);
  assert.equal(serialized.includes('evt_private'), false);
  assert.equal(serialized.includes('ou_private'), false);
  assert.equal(inbox.status().received, 1);
  assert.equal(inbox.status().duplicates, 1);
  assert.equal(inbox.recent(10)[0].transcriptContentStored, false);
  assertNoPublicPermissionBits(dir);
  for (const name of files) assertNoPublicPermissionBits(path.join(dir, name));
});

test('sanitized event records contain only bounded metadata and fingerprints', () => {
  const record = sanitizeEvent('im.chat.updated_v1', {
    header: { event_id: 'evt_one' },
    event: { chat_id: 'oc_secret', open_id: 'ou_secret', name: '秘密群名', content: '秘密正文' },
  }, '2026-09-02T00:00:00.000Z');
  assert.deepEqual(Object.keys(record).sort(), [
    'actorFingerprint', 'contentFingerprint', 'contentLength', 'eventFingerprint', 'eventKey',
    'occurredAt', 'receivedAt', 'resourceFingerprint', 'schemaVersion', 'transcriptContentStored', 'transcriptEvent',
  ].sort());
  assert.equal(JSON.stringify(record).includes('秘密'), false);
});

test('typed event watch catalog exposes only reviewed subscriptions and Task cannot be unsubscribed', () => {
  assert.deepEqual(Object.keys(EVENT_WATCHES).sort(), ['meeting', 'minutes', 'note', 'recording', 'task', 'whiteboard']);
  assert.equal(EVENT_WATCHES.task.remove, null);
  assert.deepEqual(resolvedOperation(EVENT_WATCHES.whiteboard, 'add', 'board_private'), {
    method: 'POST',
    path: '/open-apis/board/v1/whiteboards/board_private/subscribe',
  });
  assert.throws(() => resolvedOperation(EVENT_WATCHES.task, 'remove'), /cannot_be_removed/);
});

test('production code preserves one official SDK connection and never starts lark-cli event consume', () => {
  const root = path.join(__dirname, '..');
  const bridge = fs.readFileSync(path.join(root, 'bot-bridge.js'), 'utf8');
  assert.equal((bridge.match(/createOfficialEventAdapter\(/g) || []).length, 1);
  assert.doesNotMatch(bridge, /['"]event['"]\s*,\s*['"]consume['"]/);
});
