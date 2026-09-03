const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const { chmodPrivate } = require('./platform-runtime');

const QUEUE_STATE_SCHEMA_VERSION = 2;

function appendJsonl(filePath, record) {
  fs.appendFileSync(filePath, `${JSON.stringify(record)}\n`, 'utf8');
}

function fileSize(filePath) {
  try {
    const stat = fs.lstatSync(filePath);
    return stat.isFile() && !stat.isSymbolicLink() ? stat.size : 0;
  } catch (error) {
    if (error.code === 'ENOENT') return 0;
    throw error;
  }
}

function atomicWritePrivateJson(filePath, value) {
  const directory = path.dirname(filePath);
  fs.mkdirSync(directory, { recursive: true, mode: 0o700 });
  const temporary = `${filePath}.tmp-${process.pid}-${crypto.randomBytes(4).toString('hex')}`;
  const fd = fs.openSync(temporary, 'wx', 0o600);
  try {
    fs.writeFileSync(fd, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
    fs.fsyncSync(fd);
  } finally {
    fs.closeSync(fd);
  }
  fs.renameSync(temporary, filePath);
  chmodPrivate(filePath, 0o600);
}

function pruneProcessedIds(processedIds, maximum) {
  const entries = Object.entries(processedIds || {});
  if (entries.length <= maximum) return Object.fromEntries(entries);
  return Object.fromEntries(entries.slice(-maximum));
}

function createQueueStore({
  queuePath,
  resultsPath,
  statePath,
  wake = {},
  maxProcessedIds = 5000,
  warnBytes = 100 * 1024 * 1024,
}) {
  const processedIdLimit = Math.max(100, Number(maxProcessedIds) || 5000);
  const sizeWarningThreshold = Math.max(1024, Number(warnBytes) || 100 * 1024 * 1024);

  function defaultState() {
    return {
      schemaVersion: QUEUE_STATE_SCHEMA_VERSION,
      processedLineCount: 0,
      processedIds: {},
      lastProcessedAt: '',
      lastError: '',
      wake: {
        enabled: Boolean(wake.enabled),
        host: wake.host || '127.0.0.1',
        configuredPort: Number(wake.configuredPort) || 0,
        actualPort: wake.actualPort ?? null,
      },
    };
  }

  function readState() {
    if (!fs.existsSync(statePath)) return defaultState();
    try {
      const loaded = JSON.parse(fs.readFileSync(statePath, 'utf8'));
      return {
        ...defaultState(),
        ...loaded,
        schemaVersion: QUEUE_STATE_SCHEMA_VERSION,
        processedIds: pruneProcessedIds(loaded.processedIds, processedIdLimit),
        wake: { ...defaultState().wake, ...(loaded.wake || {}) },
      };
    } catch {
      return defaultState();
    }
  }

  function writeState(state) {
    atomicWritePrivateJson(statePath, {
      ...defaultState(),
      ...state,
      schemaVersion: QUEUE_STATE_SCHEMA_VERSION,
      processedIds: pruneProcessedIds(state.processedIds, processedIdLimit),
      wake: { ...defaultState().wake, ...(state.wake || {}) },
    });
  }

  function updateState(patch) {
    const state = readState();
    const next = {
      ...state,
      ...patch,
      wake: { ...(state.wake || {}), ...(patch.wake || {}) },
    };
    writeState(next);
    return next;
  }

  function readCompleteLines() {
    if (!fs.existsSync(queuePath)) return [];
    const content = fs.readFileSync(queuePath, 'utf8');
    const lines = content.split('\n');
    if (content && !content.endsWith('\n')) lines.pop();
    return lines.filter((line) => line.trim());
  }

  function results() {
    if (!fs.existsSync(resultsPath)) return [];
    return fs.readFileSync(resultsPath, 'utf8')
      .split('\n')
      .filter((line) => line.trim())
      .map((line) => {
        try {
          return JSON.parse(line);
        } catch {
          return { _invalid: true, error: 'invalid_json_line' };
        }
      });
  }

  function maintenanceStatus() {
    const files = {
      queueBytes: fileSize(queuePath),
      resultsBytes: fileSize(resultsPath),
      stateBytes: fileSize(statePath),
    };
    const warnings = [];
    if (files.queueBytes >= sizeWarningThreshold) warnings.push('queue_file_large');
    if (files.resultsBytes >= sizeWarningThreshold) warnings.push('results_file_large');
    const state = readState();
    if (Object.keys(state.processedIds || {}).length >= processedIdLimit) {
      warnings.push('processed_id_retention_full');
    }
    return {
      schemaVersion: QUEUE_STATE_SCHEMA_VERSION,
      processedIdLimit,
      warnBytes: sizeWarningThreshold,
      ...files,
      warnings,
    };
  }

  return {
    appendResult(record) { appendJsonl(resultsPath, record); },
    defaultState,
    readCompleteLines,
    readState,
    maintenanceStatus,
    recentResults(limit = 10) { return results().slice(-limit).reverse(); },
    results,
    updateState,
    writeState,
  };
}

module.exports = {
  QUEUE_STATE_SCHEMA_VERSION,
  atomicWritePrivateJson,
  createQueueStore,
  pruneProcessedIds,
};
