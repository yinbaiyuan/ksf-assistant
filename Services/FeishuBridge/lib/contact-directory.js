const fs = require('node:fs');
const path = require('node:path');
const { chmodPrivate, privatePermissionsSatisfied } = require('./platform-runtime');
const {
  acquireFileLock,
  fingerprintIdentifier,
  redactSensitiveText,
  writeSecureJson,
} = require('./bridge-client-core');

const DIRECTORY_SCHEMA_VERSION = 1;
const DEFAULT_REFRESH_MS = 6 * 60 * 60 * 1000;
const DEFAULT_MAX_AGE_MS = 24 * 60 * 60 * 1000;
const ROOT_DEPARTMENT_ID = '0';

function normalizeDirectoryName(value) {
  return String(value || '')
    .normalize('NFKC')
    .trim()
    .replace(/\s+/gu, ' ')
    .toLocaleLowerCase();
}

function assertSecureRegularFile(filePath) {
  const stat = fs.lstatSync(filePath);
  if (stat.isSymbolicLink() || !stat.isFile()) throw new Error('directory_cache_not_regular');
  if (!privatePermissionsSatisfied(stat)) throw new Error('directory_cache_insecure_permissions');
}

function readJson(filePath, fallback) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch (error) {
    if (error.code === 'ENOENT') return fallback;
    throw error;
  }
}

function validateDirectoryCache(cache) {
  if (!cache || typeof cache !== 'object') throw new Error('directory_cache_invalid');
  if (cache.schemaVersion !== DIRECTORY_SCHEMA_VERSION) throw new Error('directory_cache_schema_unsupported');
  if (!Array.isArray(cache.users)) throw new Error('directory_cache_users_invalid');
  for (const user of cache.users) {
    if (!user || typeof user !== 'object' || !user.openId || !user.displayName) {
      throw new Error('directory_cache_user_invalid');
    }
  }
  return cache;
}

function loadDirectoryCache(cachePath) {
  assertSecureRegularFile(cachePath);
  return validateDirectoryCache(readJson(cachePath));
}

function itemsFromResponse(response, candidateKeys) {
  const data = response?.data?.data || response?.data || response || {};
  for (const key of candidateKeys) {
    if (Array.isArray(data[key])) return data[key];
  }
  return [];
}

function paginationFromResponse(response) {
  const data = response?.data?.data || response?.data || response || {};
  return {
    hasMore: Boolean(data.has_more ?? data.hasMore),
    pageToken: String(data.page_token || data.pageToken || ''),
  };
}

async function collectPages(fetchPage, candidateKeys, { maxPages = 1000 } = {}) {
  const items = [];
  const seenTokens = new Set();
  let pageToken = '';
  for (let page = 0; page < maxPages; page += 1) {
    const response = await fetchPage(pageToken);
    items.push(...itemsFromResponse(response, candidateKeys));
    const pagination = paginationFromResponse(response);
    if (!pagination.hasMore) return items;
    if (!pagination.pageToken || seenTokens.has(pagination.pageToken)) {
      throw new Error('directory_pagination_token_invalid');
    }
    seenTokens.add(pagination.pageToken);
    pageToken = pagination.pageToken;
  }
  throw new Error('directory_pagination_limit_exceeded');
}

function departmentIdentifier(department) {
  return String(department?.open_department_id || department?.department_id || '');
}

function departmentParentIdentifier(department) {
  return String(department?.parent_department_id || department?.parentDepartmentId || ROOT_DEPARTMENT_ID);
}

function departmentPath(departmentId, departments) {
  if (!departmentId || departmentId === ROOT_DEPARTMENT_ID) return '';
  const names = [];
  const visited = new Set();
  let current = departmentId;
  while (current && current !== ROOT_DEPARTMENT_ID && !visited.has(current) && names.length < 32) {
    visited.add(current);
    const department = departments.get(current);
    if (!department) break;
    const name = String(department.name || '').trim();
    if (name) names.unshift(name);
    current = departmentParentIdentifier(department);
  }
  return names.join(' / ');
}

function userIsActive(user) {
  const status = user?.status || {};
  if (status.is_activated === false) return false;
  if (status.is_frozen || status.is_resigned || status.is_unjoin || status.is_exited) return false;
  return true;
}

function uniqueNonEmpty(values) {
  return [...new Set(values.map((value) => String(value || '').trim()).filter(Boolean))];
}

function mergeUser(existing, rawUser, departments) {
  const openId = String(rawUser.open_id || rawUser.openId || '').trim();
  if (!openId) return existing;
  const names = uniqueNonEmpty([
    ...(existing?.names || []),
    rawUser.name,
    rawUser.en_name,
    rawUser.enName,
  ]);
  if (!names.length) return existing;
  const rawDepartmentIds = Array.isArray(rawUser.department_ids)
    ? rawUser.department_ids
    : Array.isArray(rawUser.departmentIds) ? rawUser.departmentIds : [];
  const departmentPaths = uniqueNonEmpty([
    ...(existing?.departmentPaths || []),
    ...rawDepartmentIds.map((id) => departmentPath(String(id), departments)),
  ]);
  return {
    openId,
    displayName: String(rawUser.name || existing?.displayName || names[0]).trim(),
    names,
    departmentPaths,
    active: userIsActive(rawUser) && existing?.active !== false,
  };
}

async function fetchVisibleDirectory(lark, { pageSize = 50 } = {}) {
  const rawDepartments = await collectPages(
    (pageToken) => lark.larkContactListDepartments({
      departmentId: ROOT_DEPARTMENT_ID,
      fetchChild: true,
      pageSize,
      pageToken,
    }),
    ['items', 'departments'],
  );
  const departments = new Map();
  for (const department of rawDepartments) {
    const id = departmentIdentifier(department);
    if (id) departments.set(id, department);
  }

  const departmentIds = [ROOT_DEPARTMENT_ID, ...departments.keys()];
  const users = new Map();
  let sourceRows = 0;
  for (const departmentId of departmentIds) {
    const rows = await collectPages(
      (pageToken) => lark.larkContactListUsers({ departmentId, pageSize, pageToken }),
      ['items', 'users'],
    );
    sourceRows += rows.length;
    for (const rawUser of rows) {
      const openId = String(rawUser.open_id || rawUser.openId || '').trim();
      if (!openId) continue;
      const merged = mergeUser(users.get(openId), rawUser, departments);
      if (merged) users.set(openId, merged);
    }
  }

  const activeUsers = [...users.values()]
    .filter((user) => user.active)
    .sort((left, right) => left.displayName.localeCompare(right.displayName, 'zh-CN'));
  return {
    users: activeUsers,
    departmentCount: departments.size,
    sourceRows,
    skippedInactiveCount: users.size - activeUsers.length,
  };
}

function cacheAgeMs(cache, now = Date.now()) {
  const generated = Date.parse(cache?.generatedAt || '');
  return Number.isFinite(generated) ? Math.max(0, now - generated) : Number.POSITIVE_INFINITY;
}

function directoryStatus(cachePath, {
  enabled = false,
  maxAgeMs = DEFAULT_MAX_AGE_MS,
  statePath = `${cachePath}.state.json`,
  now = Date.now(),
} = {}) {
  let cache;
  let cacheError;
  let secure = false;
  try {
    cache = loadDirectoryCache(cachePath);
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
    userCount: cache?.users?.length || 0,
    departmentCount: cache?.departmentCount || 0,
    lastAttemptAt: state.lastAttemptAt,
    lastSuccessAt: state.lastSuccessAt,
    hasLastError: Boolean(state.lastError || cacheError),
    lastError: redactSensitiveText(state.lastError || cacheError || ''),
  };
}

function candidatePublicView(user) {
  return {
    displayName: user.displayName,
    departmentPaths: user.departmentPaths || [],
    targetFingerprint: fingerprintIdentifier(user.openId),
  };
}

function exactDirectoryCandidates(cache, name) {
  const normalized = normalizeDirectoryName(name);
  if (!normalized) return [];
  return cache.users.filter((user) => user.active !== false && (user.names || [user.displayName])
    .some((candidate) => normalizeDirectoryName(candidate) === normalized));
}

function searchDirectory(cache, query, { limit = 20 } = {}) {
  const normalized = normalizeDirectoryName(query);
  if (!normalized) return [];
  return cache.users
    .map((user) => {
      const normalizedNames = (user.names || [user.displayName]).map(normalizeDirectoryName);
      const exact = normalizedNames.includes(normalized);
      const partial = normalizedNames.some((name) => name.includes(normalized));
      return { user, exact, partial };
    })
    .filter((item) => item.user.active !== false && (item.exact || item.partial))
    .sort((left, right) => Number(right.exact) - Number(left.exact)
      || left.user.displayName.localeCompare(right.user.displayName, 'zh-CN'))
    .slice(0, Math.max(1, Math.min(100, Number(limit) || 20)))
    .map(({ user, exact }) => ({ ...candidatePublicView(user), exact }));
}

function resolveDirectoryName(cache, name, config = {}) {
  const normalized = normalizeDirectoryName(name);
  if (!normalized) return { status: 'invalid_name', candidates: [] };
  const binding = config.nameBindings?.[normalized];
  if (binding?.id) {
    const bound = cache.users.find((user) => user.openId === binding.id && user.active !== false);
    if (!bound) {
      return {
        status: 'binding_stale',
        bindingFingerprint: fingerprintIdentifier(binding.id),
        candidates: exactDirectoryCandidates(cache, name).map(candidatePublicView),
      };
    }
    return {
      status: 'resolved',
      source: 'binding',
      target: { type: 'open_id', id: bound.openId },
      candidate: candidatePublicView(bound),
    };
  }
  const candidates = exactDirectoryCandidates(cache, name);
  if (candidates.length === 1) {
    return {
      status: 'resolved',
      source: 'unique_exact_name',
      target: { type: 'open_id', id: candidates[0].openId },
      candidate: candidatePublicView(candidates[0]),
    };
  }
  if (candidates.length > 1) {
    return { status: 'ambiguous', candidates: candidates.map(candidatePublicView) };
  }
  return { status: 'not_found', candidates: searchDirectory(cache, name, { limit: 10 }) };
}

function bindDirectoryName(cache, config, name, candidateFingerprint, now = new Date()) {
  const normalized = normalizeDirectoryName(name);
  if (!normalized) throw new Error('directory_binding_name_invalid');
  const candidates = exactDirectoryCandidates(cache, name);
  const selected = candidates.find((user) => fingerprintIdentifier(user.openId) === candidateFingerprint);
  if (!selected) throw new Error('directory_binding_candidate_not_found');
  const next = {
    ...config,
    nameBindings: { ...(config.nameBindings || {}) },
  };
  next.nameBindings[normalized] = {
    name: String(name).trim(),
    type: 'open_id',
    id: selected.openId,
    boundAt: now.toISOString(),
  };
  return { config: next, binding: candidatePublicView(selected) };
}

function createDirectoryService({
  lark,
  cachePath,
  statePath = `${cachePath}.state.json`,
  enabled = false,
  refreshMs = DEFAULT_REFRESH_MS,
  maxAgeMs = DEFAULT_MAX_AGE_MS,
  pageSize = 50,
  minimumUserCount = 1,
  now = () => new Date(),
} = {}) {
  if (!cachePath) throw new Error('directory cache path is required');
  let refreshTimer = null;
  let syncInFlight = null;

  function status() {
    return directoryStatus(cachePath, {
      enabled,
      maxAgeMs,
      statePath,
      now: now().getTime(),
    });
  }

  async function sync(reason = 'manual') {
    if (!enabled) throw new Error('directory_sync_disabled');
    if (!lark) throw new Error('directory_lark_client_missing');
    if (syncInFlight) return syncInFlight;
    syncInFlight = (async () => {
      fs.mkdirSync(path.dirname(cachePath), { recursive: true, mode: 0o700 });
      chmodPrivate(path.dirname(cachePath), 0o700);
      const release = await acquireFileLock(`${cachePath}.sync.lock`, { timeoutMs: 30000, staleMs: 10 * 60 * 1000 });
      const startedAt = now().toISOString();
      try {
        const result = await fetchVisibleDirectory(lark, { pageSize });
        if (result.users.length < minimumUserCount) throw new Error('directory_sync_result_too_small');
        const generatedAt = now().toISOString();
        const cache = {
          schemaVersion: DIRECTORY_SCHEMA_VERSION,
          source: 'feishu-contact-v3-bot',
          complete: true,
          generatedAt,
          departmentCount: result.departmentCount,
          users: result.users,
        };
        writeSecureJson(cachePath, cache);
        writeSecureJson(statePath, {
          schemaVersion: 1,
          lastAttemptAt: startedAt,
          lastSuccessAt: generatedAt,
          reason,
          userCount: result.users.length,
          departmentCount: result.departmentCount,
          sourceRows: result.sourceRows,
          skippedInactiveCount: result.skippedInactiveCount,
        });
        return {
          status: 'synced',
          generatedAt,
          userCount: result.users.length,
          departmentCount: result.departmentCount,
          sourceRows: result.sourceRows,
          skippedInactiveCount: result.skippedInactiveCount,
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

  async function ensureFresh(reason = 'resolve_name') {
    const current = status();
    if (!current.stale) return loadDirectoryCache(cachePath);
    await sync(reason);
    return loadDirectoryCache(cachePath);
  }

  function start(onResult = () => {}) {
    if (!enabled || refreshTimer) return;
    const run = () => sync('scheduled').then(
      (result) => onResult(null, result),
      (error) => onResult(error),
    );
    run();
    refreshTimer = setInterval(run, Math.max(60 * 1000, refreshMs));
    refreshTimer.unref?.();
  }

  function stop() {
    if (refreshTimer) clearInterval(refreshTimer);
    refreshTimer = null;
  }

  return {
    bind(name, candidateFingerprint, config) {
      return bindDirectoryName(loadDirectoryCache(cachePath), config, name, candidateFingerprint, now());
    },
    ensureFresh,
    load() { return loadDirectoryCache(cachePath); },
    resolve(name, config) { return resolveDirectoryName(loadDirectoryCache(cachePath), name, config); },
    search(query, options) { return searchDirectory(loadDirectoryCache(cachePath), query, options); },
    start,
    status,
    stop,
    sync,
  };
}

module.exports = {
  DEFAULT_MAX_AGE_MS,
  DEFAULT_REFRESH_MS,
  DIRECTORY_SCHEMA_VERSION,
  bindDirectoryName,
  cacheAgeMs,
  candidatePublicView,
  collectPages,
  createDirectoryService,
  directoryStatus,
  exactDirectoryCandidates,
  fetchVisibleDirectory,
  loadDirectoryCache,
  normalizeDirectoryName,
  resolveDirectoryName,
  searchDirectory,
};
