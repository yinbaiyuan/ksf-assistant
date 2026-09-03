const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { assertPrivateMode } = require('./helpers/platform-private');
const {
  QUEUE_STATE_SCHEMA_VERSION,
  createQueueStore,
} = require('../lib/queue-worker-core');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-queue-core-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('queue store shares state, complete-line, result, and recent-result behavior', (t) => {
  const dir = temporaryDirectory(t);
  const queuePath = path.join(dir, 'queue.jsonl');
  const resultsPath = path.join(dir, 'results.jsonl');
  const statePath = path.join(dir, 'state.json');
  const store = createQueueStore({
    queuePath,
    resultsPath,
    statePath,
    wake: { enabled: true, host: '127.0.0.1', configuredPort: 0, actualPort: null },
  });

  assert.deepEqual(store.readState(), store.defaultState());
  const updated = store.updateState({
    lastProcessedAt: '2026-09-01T00:00:00.000Z',
    wake: { actualPort: 54321 },
  });
  assert.equal(updated.lastProcessedAt, '2026-09-01T00:00:00.000Z');
  assert.equal(updated.wake.enabled, true);
  assert.equal(updated.wake.actualPort, 54321);

  fs.writeFileSync(queuePath, '{"id":"one"}\n{"id":"partial"}');
  assert.deepEqual(store.readCompleteLines(), ['{"id":"one"}']);

  store.appendResult({ id: 'one', status: 'completed' });
  store.appendResult({ id: 'two', status: 'failed' });
  assert.deepEqual(store.results().map((item) => item.id), ['one', 'two']);
  assert.deepEqual(store.recentResults(1).map((item) => item.id), ['two']);
});

test('queue store recovers from invalid state without losing configured wake defaults', (t) => {
  const dir = temporaryDirectory(t);
  const statePath = path.join(dir, 'state.json');
  fs.writeFileSync(statePath, 'not-json\n');
  const store = createQueueStore({
    queuePath: path.join(dir, 'queue.jsonl'),
    resultsPath: path.join(dir, 'results.jsonl'),
    statePath,
    wake: { enabled: false, host: '127.0.0.1', configuredPort: 0, actualPort: null },
  });
  assert.deepEqual(store.readState(), store.defaultState());
});

test('queue state writes atomically with private permissions and bounded dedupe retention', (t) => {
  const dir = temporaryDirectory(t);
  const statePath = path.join(dir, 'state.json');
  const store = createQueueStore({
    queuePath: path.join(dir, 'queue.jsonl'),
    resultsPath: path.join(dir, 'results.jsonl'),
    statePath,
    maxProcessedIds: 100,
    warnBytes: 1024,
  });
  const processedIds = Object.fromEntries(Array.from({ length: 130 }, (_, index) => [`id-${index}`, 'completed']));
  store.writeState({ ...store.defaultState(), processedIds });
  const state = store.readState();
  assert.equal(state.schemaVersion, QUEUE_STATE_SCHEMA_VERSION);
  assert.equal(Object.keys(state.processedIds).length, 100);
  assert.equal(state.processedIds['id-0'], undefined);
  assert.equal(state.processedIds['id-129'], 'completed');
  assertPrivateMode(statePath, 0o600);
  assert.equal(fs.readdirSync(dir).some((name) => name.includes('.tmp-')), false);
  assert.deepEqual(store.maintenanceStatus().warnings, ['processed_id_retention_full']);
});
