const fs = require('node:fs');
const path = require('node:path');
const { chmodPrivate, privatePermissionsSatisfied } = require('./platform-runtime');
const {
  acquireFileLock,
  fingerprintIdentifier,
  redactSensitiveText,
  writeSecureJson,
} = require('./bridge-client-core');
const { collectPages, normalizeDirectoryName } = require('./contact-directory');

const GROUP_DIRECTORY_SCHEMA_VERSION = 1;
const DEFAULT_GROUP_REFRESH_MS = 30 * 60 * 1000;
const DEFAULT_GROUP_MAX_AGE_MS = 2 * 60 * 60 * 1000;
const DEFAULT_GROUP_EVENT_DEBOUNCE_MS = 5 * 1000;
const GROUP_DIRECTORY_INVALIDATION_EVENT_KEYS = new Set([
  'im.chat.disbanded_v1',
  'im.chat.member.bot.added_v1',
  'im.chat.member.bot.deleted_v1',
  'im.chat.updated_v1',
]);

function normalizeGroupName(value) {
  return normalizeDirectoryName(value);
}

function groupDirectoryEventRequiresRefresh(eventKey) {
  return GROUP_DIRECTORY_INVALIDATION_EVENT_KEYS.has(String(eventKey || ''));
}

function assertSecureRegularFile(filePath) {
  const stat = fs.lstatSync(filePath);
  if (stat.isSymbolicLink() || !stat.isFile()) throw new Error('group_directory_cache_not_regular');
  if (!privatePermissionsSatisfied(stat)) throw new Error('group_directory_cache_insecure_permissions');
}

function readJson(filePath, fallback) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch (error) {
    if (error.code === 'ENOENT') return fallback;
    throw error;
  }
}

function validateGroupDirectoryCache(cache) {
  if (!cache || typeof cache !== 'object') throw new Error('group_directory_cache_invalid');
  if (cache.schemaVersion !== GROUP_DIRECTORY_SCHEMA_VERSION) {
    throw new Error('group_directory_cache_schema_unsupported');
  }
  if (!Array.isArray(cache.groups)) throw new Error('group_directory_cache_groups_invalid');
  for (const group of cache.groups) {
    if (!group || typeof group !== 'object' || !group.chatId || !group.displayName) {
      throw new Error('group_directory_cache_group_invalid');
    }
  }
  return cache;
}

function loadGroupDirectoryCache(cachePath) {
  assertSecureRegularFile(cachePath);
  return validateGroupDirectoryCache(readJson(cachePath));
}

function safeDescription(value) {
  return String(value || '').replace(/\s+/gu, ' ').trim().slice(0, 160);
}

function groupIsActive(rawGroup) {
  const status = String(rawGroup.chat_status || rawGroup.chatStatus || '').toLowerCase();
  return !['disbanded', 'dissolved', 'deleted'].includes(status);
}

function reduceGroup(rawGroup) {
  const chatId = String(rawGroup.chat_id || rawGroup.chatId || '').trim();
  const displayName = String(rawGroup.name || rawGroup.displayName || '').trim();
  if (!chatId || !displayName || !groupIsActive(rawGroup)) return null;
  return {
    chatId,
    displayName,
    description: safeDescription(rawGroup.description),
    external: Boolean(rawGroup.external),
    active: true,
  };
}

async function fetchVisibleGroups(lark, { pageSize = 100 } = {}) {
  const rows = await collectPages(
    (pageToken) => lark.larkImListChats({ pageSize, pageToken }),
    ['items', 'chats'],
  );
  const groups = new Map();
  for (const row of rows) {
    const group = reduceGroup(row);
    if (group) groups.set(group.chatId, group);
  }
  return {
    groups: [...groups.values()].sort((left, right) => left.displayName.localeCompare(right.displayName, 'zh-CN')),
    sourceRows: rows.length,
  };
}

function cacheAgeMs(cache, now = Date.now()) {
  const generated = Date.parse(cache?.generatedAt || '');
  return Number.isFinite(generated) ? Math.max(0, now - generated) : Number.POSITIVE_INFINITY;
}

function groupDirectoryStatus(cachePath, {
  enabled = false,
  maxAgeMs = DEFAULT_GROUP_MAX_AGE_MS,
  statePath = `${cachePath}.state.json`,
  now = Date.now(),
} = {}) {
  let cache;
  let cacheError;
  let secure = false;
  try {
    cache = loadGroupDirectoryCache(cachePath);
    secure = true;
  } catch (error) {
    if (error.code !== 'ENOENT') cacheError = String(error.message || error);
  }
  let state = {};
  try {
    state = readJson(statePath, {});
  } catch (error) {
    state = { lastError: String(error.message || error) };
  }
  const ageMs = cache ? cacheAgeMs(cache, now) : undefined;
  return {
    enabled,
    exists: Boolean(cache),
    secure,
    complete: Boolean(cache?.complete),
    generatedAt: cache?.generatedAt,
    ageMs,
    stale: !cache || ageMs > maxAgeMs,
    groupCount: cache?.groups?.length || 0,
    lastAttemptAt: state.lastAttemptAt,
    lastSuccessAt: state.lastSuccessAt,
    hasLastError: Boolean(state.lastError || cacheError),
    lastError: redactSensitiveText(state.lastError || cacheError || ''),
  };
}

function candidatePublicView(group) {
  return {
    displayName: group.displayName,
    description: group.description || '',
    external: Boolean(group.external),
    targetFingerprint: fingerprintIdentifier(group.chatId),
  };
}

function exactGroupCandidates(cache, name) {
  const normalized = normalizeGroupName(name);
  if (!normalized) return [];
  return cache.groups.filter((group) => group.active !== false
    && normalizeGroupName(group.displayName) === normalized);
}

function searchGroups(cache, query, { limit = 20 } = {}) {
  const normalized = normalizeGroupName(query);
  if (!normalized) return [];
  return cache.groups
    .filter((group) => group.active !== false && normalizeGroupName(group.displayName).includes(normalized))
    .slice(0, Math.max(1, limit))
    .map(candidatePublicView);
}

function resolveGroupName(cache, name, config = {}) {
  const normalized = normalizeGroupName(name);
  if (!normalized) return { status: 'not_found', candidates: [] };
  const binding = config.groupNameBindings?.[normalized];
  if (binding) {
    const bound = cache.groups.find((group) => group.chatId === binding.id && group.active !== false);
    if (!bound || normalizeGroupName(bound.displayName) !== normalized) {
      return {
        status: 'binding_stale',
        bindingFingerprint: fingerprintIdentifier(binding.id),
        candidates: exactGroupCandidates(cache, name).map(candidatePublicView),
      };
    }
    return {
      status: 'resolved',
      source: 'binding',
      target: { type: 'chat_id', id: bound.chatId },
      candidate: candidatePublicView(bound),
    };
  }

  const exact = exactGroupCandidates(cache, name);
  if (exact.length === 1) {
    return {
      status: 'resolved',
      source: 'unique_exact_group_name',
      target: { type: 'chat_id', id: exact[0].chatId },
      candidate: candidatePublicView(exact[0]),
    };
  }
  if (exact.length > 1) {
    return { status: 'ambiguous', candidates: exact.map(candidatePublicView) };
  }
  return { status: 'not_found', candidates: searchGroups(cache, name, { limit: 5 }) };
}

function bindGroupName(cache, config, name, candidateFingerprint, now = new Date()) {
  const normalized = normalizeGroupName(name);
  if (!normalized) throw new Error('group_binding_name_invalid');
  const selected = exactGroupCandidates(cache, name)
    .find((group) => fingerprintIdentifier(group.chatId) === candidateFingerprint);
  if (!selected) throw new Error('group_binding_candidate_not_found');
  const next = {
    ...config,
    groupNameBindings: { ...(config.groupNameBindings || {}) },
  };
  next.groupNameBindings[normalized] = {
    type: 'chat_id',
    id: selected.chatId,
    name: selected.displayName,
    boundAt: now.toISOString(),
  };
  return { config: next, binding: candidatePublicView(selected) };
}

function createGroupDirectoryService({
  lark,
  cachePath,
  statePath = `${cachePath}.state.json`,
  enabled = false,
  refreshMs = DEFAULT_GROUP_REFRESH_MS,
  maxAgeMs = DEFAULT_GROUP_MAX_AGE_MS,
  pageSize = 100,
  now = () => new Date(),
} = {}) {
  if (!cachePath) throw new Error('group directory cache path is required');
  let refreshTimer = null;
  let eventRefreshTimer = null;
  let syncInFlight = null;
  let reportRefreshResult = () => {};

  function status() {
    return groupDirectoryStatus(cachePath, {
      enabled,
      maxAgeMs,
      statePath,
      now: now().getTime(),
    });
  }

  async function sync(reason = 'manual') {
    if (!enabled) throw new Error('group_directory_sync_disabled');
    if (!lark) throw new Error('group_directory_lark_client_missing');
    if (syncInFlight) return syncInFlight;
    syncInFlight = (async () => {
      fs.mkdirSync(path.dirname(cachePath), { recursive: true, mode: 0o700 });
      chmodPrivate(path.dirname(cachePath), 0o700);
      const release = await acquireFileLock(`${cachePath}.sync.lock`, { timeoutMs: 30000, staleMs: 10 * 60 * 1000 });
      const startedAt = now().toISOString();
      try {
        const result = await fetchVisibleGroups(lark, { pageSize });
        const generatedAt = now().toISOString();
        writeSecureJson(cachePath, {
          schemaVersion: GROUP_DIRECTORY_SCHEMA_VERSION,
          source: 'feishu-im-v1-bot-member-chats',
          complete: true,
          generatedAt,
          groups: result.groups,
        });
        writeSecureJson(statePath, {
          schemaVersion: 1,
          lastAttemptAt: startedAt,
          lastSuccessAt: generatedAt,
          reason,
          groupCount: result.groups.length,
          sourceRows: result.sourceRows,
        });
        return {
          status: 'synced',
          generatedAt,
          groupCount: result.groups.length,
          sourceRows: result.sourceRows,
        };
      } catch (error) {
        const previous = readJson(statePath, {});
        writeSecureJson(statePath, {
          ...previous,
          schemaVersion: 1,
          lastAttemptAt: startedAt,
          reason,
          lastError: redactSensitiveText(String(error.message || error)),
        });
        throw error;
      } finally {
        release();
      }
    })();
    try {
      return await syncInFlight;
    } finally {
      syncInFlight = null;
    }
  }

  async function ensureFresh(reason = 'resolve_group_name') {
    const current = status();
    if (!current.stale) return loadGroupDirectoryCache(cachePath);
    await sync(reason);
    return loadGroupDirectoryCache(cachePath);
  }

  function runRefresh(reason, onResult = reportRefreshResult) {
    return sync(reason).then(
      (result) => onResult(null, result),
      (error) => onResult(error),
    );
  }

  function start(onResult = () => {}) {
    if (!enabled || refreshTimer) return false;
    reportRefreshResult = onResult;
    void runRefresh('startup');
    refreshTimer = setInterval(
      () => { void runRefresh('scheduled'); },
      Math.max(60 * 1000, refreshMs),
    );
    refreshTimer.unref?.();
    return true;
  }

  function scheduleRefresh(reason = 'event', delayMs = DEFAULT_GROUP_EVENT_DEBOUNCE_MS) {
    if (!enabled || eventRefreshTimer) return false;
    eventRefreshTimer = setTimeout(() => {
      eventRefreshTimer = null;
      void runRefresh(reason);
    }, Math.max(0, delayMs));
    eventRefreshTimer.unref?.();
    return true;
  }

  function stop() {
    if (refreshTimer) clearInterval(refreshTimer);
    if (eventRefreshTimer) clearTimeout(eventRefreshTimer);
    refreshTimer = null;
    eventRefreshTimer = null;
    reportRefreshResult = () => {};
  }

  return {
    bind(name, candidateFingerprint, config) {
      return bindGroupName(loadGroupDirectoryCache(cachePath), config, name, candidateFingerprint, now());
    },
    ensureFresh,
    load() { return loadGroupDirectoryCache(cachePath); },
    resolve(name, config) { return resolveGroupName(loadGroupDirectoryCache(cachePath), name, config); },
    scheduleRefresh,
    search(query, options) { return searchGroups(loadGroupDirectoryCache(cachePath), query, options); },
    start,
    status,
    stop,
    sync,
  };
}

module.exports = {
  DEFAULT_GROUP_EVENT_DEBOUNCE_MS,
  DEFAULT_GROUP_MAX_AGE_MS,
  DEFAULT_GROUP_REFRESH_MS,
  GROUP_DIRECTORY_SCHEMA_VERSION,
  GROUP_DIRECTORY_INVALIDATION_EVENT_KEYS,
  bindGroupName,
  cacheAgeMs,
  candidatePublicView,
  createGroupDirectoryService,
  exactGroupCandidates,
  fetchVisibleGroups,
  groupDirectoryStatus,
  groupDirectoryEventRequiresRefresh,
  loadGroupDirectoryCache,
  normalizeGroupName,
  resolveGroupName,
  searchGroups,
};
