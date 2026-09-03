const crypto = require('node:crypto');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { chmodPrivate, defaultDataRoot } = require('./platform-runtime');

const PROTOCOL = 'codex-feishu-task-link-v1';
const SCHEMA_VERSION = 2;
const DEFAULT_LEASE_MS = 24 * 60 * 60 * 1000;
const INPUT_CAPTURE_MS = 2 * 60 * 1000;
const LINK_STATES = new Set(['active', 'released', 'expired']);
const TURN_STATES = new Set([
  'idle', 'running', 'waiting_input', 'desktop_action_required', 'queued',
  'plan_ready', 'completed', 'failed', 'interrupted',
]);
const TURN_OWNERS = new Set(['desktop', 'bridge', 'none']);
const ACTION_REQUIRED = new Set(['none', 'feishu', 'desktop']);
const COLLABORATION_MODES = new Set(['default', 'plan']);
const LEASE_PROTECTED_TURN_STATES = new Set([
  'running', 'waiting_input', 'desktop_action_required', 'queued', 'plan_ready',
]);

function defaultTaskLinkPath(home = os.homedir()) {
  return path.join(defaultDataRoot({ homeDir: home }), 'task-links-v1.json');
}

function assertSafePath(filePath) {
  const absolute = path.resolve(filePath);
  const expectedRoot = defaultDataRoot({ homeDir: os.homedir() });
  if (absolute !== path.join(expectedRoot, 'task-links-v1.json')) {
    throw new Error('task link store must use the configured Feishu Bridge private path');
  }
  for (const candidate of [expectedRoot, absolute]) {
    if (!fs.existsSync(candidate)) continue;
    const stat = fs.lstatSync(candidate);
    if (stat.isSymbolicLink()) throw new Error(`unsafe symbolic link: ${candidate}`);
  }
  return absolute;
}

function emptyStore() {
  return {
    protocol: PROTOCOL,
    schemaVersion: SCHEMA_VERSION,
    updatedAt: new Date(0).toISOString(),
    links: [],
  };
}

function legacyTurnProjection(state) {
  switch (state) {
    case 'waiting_current_turn':
      return { linkState: 'active', turnState: 'running', turnOwner: 'desktop', actionRequired: 'none' };
    case 'running':
      return { linkState: 'active', turnState: 'running', turnOwner: 'bridge', actionRequired: 'none' };
    case 'waiting_input':
      return { linkState: 'active', turnState: 'waiting_input', turnOwner: 'bridge', actionRequired: 'feishu' };
    case 'queued':
      return { linkState: 'active', turnState: 'queued', turnOwner: 'desktop', actionRequired: 'none' };
    case 'completed':
      return { linkState: 'active', turnState: 'completed', turnOwner: 'none', actionRequired: 'none' };
    case 'failed':
      return { linkState: 'active', turnState: 'failed', turnOwner: 'none', actionRequired: 'none' };
    case 'released':
      return { linkState: 'released', turnState: 'idle', turnOwner: 'none', actionRequired: 'none' };
    case 'expired':
      return { linkState: 'expired', turnState: 'idle', turnOwner: 'none', actionRequired: 'none' };
    default:
      return { linkState: 'active', turnState: 'idle', turnOwner: 'none', actionRequired: 'none' };
  }
}

function normalizeInputCapture(value) {
  if (!value || typeof value !== 'object') return null;
  const chatId = String(value.chatId || '').trim();
  const operatorId = String(value.operatorId || '').trim();
  const messageId = String(value.messageId || '').trim();
  const expiresAt = String(value.expiresAt || '').trim();
  if (!chatId || !operatorId || !messageId || !Number.isFinite(Date.parse(expiresAt))) return null;
  return { chatId, operatorId, messageId, expiresAt };
}

function normalizeStoredLink(link) {
  const legacy = legacyTurnProjection(link.state);
  const turnState = TURN_STATES.has(link.turnState) ? link.turnState : legacy.turnState;
  return {
    ...link,
    linkState: LINK_STATES.has(link.linkState) ? link.linkState : legacy.linkState,
    turnState,
    turnOwner: TURN_OWNERS.has(link.turnOwner) ? link.turnOwner : legacy.turnOwner,
    actionRequired: ACTION_REQUIRED.has(link.actionRequired) ? link.actionRequired : legacy.actionRequired,
    activeTurnId: String(link.activeTurnId || ''),
    pendingMessageId: String(link.pendingMessageId || ''),
    pendingCleanupDir: String(link.pendingCleanupDir || ''),
    stagedCleanupDirs: Array.isArray(link.stagedCleanupDirs) ? link.stagedCleanupDirs.map(String) : [],
    lastDeliveredTurnId: String(link.lastDeliveredTurnId || ''),
    cardRevision: Number.isInteger(link.cardRevision) ? link.cardRevision : 0,
    messageIds: Array.isArray(link.messageIds) ? link.messageIds : [],
    inputCapture: normalizeInputCapture(link.inputCapture),
    nextTurnMode: COLLABORATION_MODES.has(link.nextTurnMode) ? link.nextTurnMode : 'default',
    activeTurnMode: COLLABORATION_MODES.has(link.activeTurnMode) ? link.activeTurnMode : '',
    pendingPlanTurnId: turnState === 'plan_ready' ? String(link.pendingPlanTurnId || '') : '',
    pendingPlanRevision: turnState === 'plan_ready' ? String(link.pendingPlanRevision || '') : '',
  };
}

function normalizeStore(parsed) {
  if (parsed?.protocol !== PROTOCOL || !Array.isArray(parsed.links)) {
    throw new Error('unsupported or damaged task link store');
  }
  const migrating = parsed.schemaVersion !== SCHEMA_VERSION;
  return {
    ...parsed,
    protocol: PROTOCOL,
    schemaVersion: SCHEMA_VERSION,
    links: parsed.links.map((link) => {
      const normalized = normalizeStoredLink(link);
      if (!migrating || normalized.linkState !== 'active') return normalized;
      const anchor = Date.parse(normalized.lastInteractionAt || normalized.updatedAt || normalized.createdAt || '');
      return {
        ...normalized,
        expiresAt: new Date((Number.isFinite(anchor) ? anchor : Date.now()) + DEFAULT_LEASE_MS).toISOString(),
      };
    }),
  };
}

function readRawStore(filePath = defaultTaskLinkPath()) {
  const safePath = assertSafePath(filePath);
  if (!fs.existsSync(safePath)) return emptyStore();
  const stat = fs.lstatSync(safePath);
  if (!stat.isFile() || stat.isSymbolicLink()
    || (process.platform !== 'win32' && (stat.mode & 0o077) !== 0)) {
    throw new Error('unsafe task link store permissions');
  }
  return JSON.parse(fs.readFileSync(safePath, 'utf8'));
}

function readStore(filePath = defaultTaskLinkPath()) {
  return normalizeStore(readRawStore(filePath));
}

function writeStore(store, filePath = defaultTaskLinkPath()) {
  const safePath = assertSafePath(filePath);
  const directory = path.dirname(safePath);
  fs.mkdirSync(directory, { recursive: true, mode: 0o700 });
  chmodPrivate(directory, 0o700);
  const temp = path.join(directory, `.task-links-${process.pid}-${crypto.randomBytes(5).toString('hex')}.tmp`);
  const value = {
    ...normalizeStore(store),
    protocol: PROTOCOL,
    schemaVersion: SCHEMA_VERSION,
    updatedAt: new Date().toISOString(),
  };
  const fd = fs.openSync(temp, 'wx', 0o600);
  try {
    fs.writeFileSync(fd, `${JSON.stringify(value, null, 2)}\n`, 'utf8');
    fs.fsyncSync(fd);
  } finally {
    fs.closeSync(fd);
  }
  fs.renameSync(temp, safePath);
  chmodPrivate(safePath, 0o600);
  return value;
}

function withStoreLock(filePath, operation) {
  const safePath = assertSafePath(filePath);
  const directory = path.dirname(safePath);
  fs.mkdirSync(directory, { recursive: true, mode: 0o700 });
  chmodPrivate(directory, 0o700);
  const lockPath = `${safePath}.lock`;
  let fd;
  const deadline = Date.now() + 5000;
  while (fd === undefined) {
    try {
      fd = fs.openSync(lockPath, 'wx', 0o600);
    } catch (error) {
      if (error.code !== 'EEXIST') throw error;
      const stat = fs.lstatSync(lockPath);
      if (stat.isSymbolicLink()) throw new Error('unsafe task link store lock');
      if (Date.now() - stat.mtimeMs > 30000) { fs.unlinkSync(lockPath); continue; }
      if (Date.now() >= deadline) throw new Error('task link store lock timeout');
      Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, 20);
    }
  }
  try { return operation(); } finally { fs.closeSync(fd); try { fs.unlinkSync(lockPath); } catch {} }
}

function migrateStore(filePath = defaultTaskLinkPath()) {
  return withStoreLock(filePath, () => {
    const raw = readRawStore(filePath);
    const migrated = normalizeStore(raw);
    if (raw.schemaVersion !== SCHEMA_VERSION
      || raw.links.some((link) => !link.linkState || !link.turnState || !link.turnOwner)) {
      return writeStore(migrated, filePath);
    }
    return migrated;
  });
}

function leaseProtected(link) {
  return link.linkState === 'active' && LEASE_PROTECTED_TURN_STATES.has(link.turnState);
}

function effectiveLinkState(link, now = Date.now()) {
  if (link.linkState === 'released' || link.linkState === 'expired') return link.linkState;
  if (leaseProtected(link)) return 'active';
  return Date.parse(link.expiresAt) <= now ? 'expired' : 'active';
}

function legacyState(link, now = Date.now()) {
  const linkState = effectiveLinkState(link, now);
  if (linkState !== 'active') return linkState;
  if (link.turnOwner === 'desktop' && link.turnState === 'running') return 'waiting_current_turn';
  if (link.turnState === 'idle') return 'connected';
  return link.turnState;
}

function controlsFor(link, now = Date.now()) {
  const active = effectiveLinkState(link, now) === 'active';
  const hasActiveTurn = Boolean(String(link.activeTurnId || '').trim());
  return {
    canSend: active && ['idle', 'plan_ready', 'completed', 'failed', 'interrupted'].includes(link.turnState),
    canSteer: active && link.turnState === 'running' && link.actionRequired === 'none' && hasActiveTurn,
    canInterrupt: active
      && ['running', 'waiting_input', 'desktop_action_required', 'queued'].includes(link.turnState)
      && (link.turnState !== 'running' || hasActiveTurn),
    canAnswer: active && link.turnState === 'waiting_input' && link.actionRequired === 'feishu',
    canRelease: active,
    canSetMode: active && link.turnState !== 'plan_ready',
    canImplementPlan: active
      && link.turnState === 'plan_ready'
      && Boolean(String(link.pendingPlanTurnId || '').trim())
      && /^[a-f0-9]{20}$/.test(String(link.pendingPlanRevision || '')),
    acceptsAttachments: active && link.actionRequired !== 'desktop',
  };
}

function inputCaptureActive(link, now = Date.now()) {
  const at = now instanceof Date ? now.getTime() : Number(now);
  return effectiveLinkState(link, at) === 'active'
    && Boolean(link.inputCapture)
    && Date.parse(link.inputCapture.expiresAt) > at;
}

function inputCaptureView(link, now = Date.now()) {
  const at = now instanceof Date ? now.getTime() : Number(now);
  const active = inputCaptureActive(link, at);
  return {
    active,
    remainingSeconds: active
      ? Math.max(0, Math.floor((Date.parse(link.inputCapture.expiresAt) - at) / 1000))
      : 0,
  };
}

function operatorMatches(link, openId) {
  return link?.target?.type === 'open_id'
    && Boolean(openId)
    && link.target.id === openId;
}

function publicLink(link, now = Date.now()) {
  const linkState = effectiveLinkState(link, now);
  return {
    taskKey: link.taskKey,
    title: link.title,
    projectName: link.projectName || '',
    targetAlias: link.targetAlias || '',
    linkState,
    turnState: link.turnState,
    turnOwner: link.turnOwner,
    actionRequired: link.actionRequired,
    controls: controlsFor(link, now),
    state: legacyState(link, now),
    createdAt: link.createdAt,
    updatedAt: link.updatedAt,
    expiresAt: link.expiresAt,
    remainingSeconds: linkState === 'active'
      ? Math.max(0, Math.floor((Date.parse(link.expiresAt) - now) / 1000)) : 0,
    nextTurnMode: link.nextTurnMode,
    activeTurnMode: link.activeTurnMode,
    hasPendingPlanImplementation: link.turnState === 'plan_ready'
      && Boolean(link.pendingPlanTurnId && link.pendingPlanRevision),
    planImplementationRevision: link.turnState === 'plan_ready'
      ? String(link.pendingPlanRevision || '') : '',
    hasPendingMessage: Boolean(link.pendingMessageId),
    detailAvailable: Boolean(link.detailSummary || link.progress?.detail),
    phase: String(link.progress?.phase || ''),
    detailSummary: String(link.detailSummary || link.progress?.detail || ''),
  };
}

function expiryState(link, now = Date.now()) {
  const normalized = normalizeStoredLink(link);
  const state = effectiveLinkState(normalized, now);
  return state === 'active' ? legacyState(normalized, now) : state;
}

function normalizeLink(input, now = new Date()) {
  const threadId = String(input.threadId || '').trim();
  const cwd = path.resolve(String(input.cwd || '').trim());
  const title = String(input.title || '').trim().slice(0, 160);
  const targetAlias = String(input.targetAlias || '').trim();
  if (!/^[0-9a-f-]{20,}$/i.test(threadId)) throw new Error('invalid Codex thread id');
  if (!path.isAbsolute(cwd) || !fs.existsSync(cwd) || !fs.statSync(cwd).isDirectory()) throw new Error('invalid task working directory');
  if (!title || !targetAlias) throw new Error('task title and target alias are required');
  const createdAt = now.toISOString();
  const initial = input.turnState
    ? {
        turnState: input.turnState,
        turnOwner: input.turnOwner || 'none',
        actionRequired: input.actionRequired || 'none',
      }
    : legacyTurnProjection(input.state);
  return normalizeStoredLink({
    id: `LINK-${crypto.randomBytes(8).toString('hex').toUpperCase()}`,
    taskKey: crypto.createHash('sha256').update(threadId).digest('hex').slice(0, 20),
    threadId,
    cwd,
    title,
    projectName: String(input.projectName || '').trim().slice(0, 120),
    targetAlias,
    target: input.target,
    rootMessageId: '',
    messageIds: [],
    linkState: 'active',
    ...initial,
    createdAt,
    updatedAt: createdAt,
    lastInteractionAt: createdAt,
    expiresAt: new Date(now.getTime() + DEFAULT_LEASE_MS).toISOString(),
    activeTurnId: String(input.activeTurnId || ''),
    pendingMessageId: '',
    inputCapture: null,
    pendingCleanupDir: '',
    stagedCleanupDirs: [],
    lastDeliveredTurnId: '',
    nextTurnMode: COLLABORATION_MODES.has(input.nextTurnMode) ? input.nextTurnMode : 'default',
    activeTurnMode: COLLABORATION_MODES.has(input.activeTurnMode) ? input.activeTurnMode : '',
    pendingPlanTurnId: '',
    pendingPlanRevision: '',
    progress: input.progress || {
      phase: initial.turnState === 'running' ? '运行中' : '已连接',
      detail: initial.turnState === 'running' ? '正在读取当前 Codex 轮次。' : '等待飞书指令',
      changedFiles: 0,
      testStatus: '未运行',
    },
  });
}

function upsertLink(input, filePath = defaultTaskLinkPath()) {
  return withStoreLock(filePath, () => {
    const store = readStore(filePath);
    const existing = store.links.find((link) => link.threadId === input.threadId
      && effectiveLinkState(link) === 'active');
    const now = new Date();
    const link = existing
      ? normalizeStoredLink({
          ...existing,
          title: String(input.title || existing.title).trim().slice(0, 160),
          projectName: String(input.projectName ?? existing.projectName ?? '').trim().slice(0, 120),
          targetAlias: input.targetAlias || existing.targetAlias,
          target: input.target || existing.target,
          cwd: input.cwd || existing.cwd,
          progress: input.progress || existing.progress,
          updatedAt: now.toISOString(),
          lastInteractionAt: now.toISOString(),
          expiresAt: new Date(now.getTime() + DEFAULT_LEASE_MS).toISOString(),
        })
      : normalizeLink(input, now);
    const links = store.links.filter((item) => item.id !== link.id);
    links.push(link);
    writeStore({ ...store, links }, filePath);
    return link;
  });
}

function validatePatch(patch) {
  if (patch.linkState && !LINK_STATES.has(patch.linkState)) throw new Error('invalid task link state');
  if (patch.turnState && !TURN_STATES.has(patch.turnState)) throw new Error('invalid task turn state');
  if (patch.turnOwner && !TURN_OWNERS.has(patch.turnOwner)) throw new Error('invalid task turn owner');
  if (patch.actionRequired && !ACTION_REQUIRED.has(patch.actionRequired)) throw new Error('invalid task action requirement');
  if (patch.nextTurnMode && !COLLABORATION_MODES.has(patch.nextTurnMode)) throw new Error('invalid task collaboration mode');
  if (patch.activeTurnMode && !COLLABORATION_MODES.has(patch.activeTurnMode)) throw new Error('invalid active task collaboration mode');
}

function captureContext(input) {
  const chatId = String(input?.chatId || '').trim();
  const operatorId = String(input?.operatorId || '').trim();
  const messageId = String(input?.messageId || '').trim();
  if (!chatId || !operatorId) throw new Error('capture chat and operator are required');
  return { chatId, operatorId, messageId };
}

function activeLinkIndexByTaskKey(store, taskKey, now) {
  const matches = store.links
    .map((link, index) => ({ link, index }))
    .filter(({ link }) => link.taskKey === taskKey && effectiveLinkState(link, now) === 'active')
    .sort((left, right) => Date.parse(right.link.updatedAt) - Date.parse(left.link.updatedAt));
  return matches[0]?.index ?? -1;
}

function clearExpiredCapturesInStore(store, now) {
  const expired = [];
  store.links = store.links.map((link) => {
    if (!link.inputCapture || Date.parse(link.inputCapture.expiresAt) > now) return link;
    const cleared = normalizeStoredLink({ ...link, inputCapture: null, updatedAt: new Date(now).toISOString() });
    expired.push(cleared);
    return cleared;
  });
  return expired;
}

function armInputCapture(taskKey, input, filePath = defaultTaskLinkPath(), now = new Date()) {
  return withStoreLock(filePath, () => {
    const store = readStore(filePath);
    const at = now instanceof Date ? now.getTime() : Number(now);
    const timestamp = new Date(at);
    const context = captureContext(input);
    if (!context.messageId) throw new Error('capture card message is required');
    clearExpiredCapturesInStore(store, at);
    const index = activeLinkIndexByTaskKey(store, String(taskKey || ''), at);
    if (index < 0) throw new Error('task link is not active');
    const current = store.links[index];
    if (!operatorMatches(current, context.operatorId)) throw new Error('capture operator does not match task link');
    if (![current.rootMessageId, ...(current.messageIds || [])].includes(context.messageId)) {
      throw new Error('capture card message does not match task link');
    }
    const controls = controlsFor(current, at);
    if (!(controls.canSend || controls.canSteer || controls.canAnswer) || !controls.acceptsAttachments) {
      throw new Error('task link is not accepting input');
    }

    const replacedLinks = [];
    store.links = store.links.map((link, linkIndex) => {
      if (linkIndex === index || !inputCaptureActive(link, at)) return link;
      if (link.inputCapture.chatId !== context.chatId
        || link.inputCapture.operatorId !== context.operatorId) return link;
      const cleared = normalizeStoredLink({ ...link, inputCapture: null, updatedAt: timestamp.toISOString() });
      replacedLinks.push(cleared);
      return cleared;
    });
    store.links[index] = normalizeStoredLink({
      ...store.links[index],
      inputCapture: {
        chatId: context.chatId,
        operatorId: context.operatorId,
        messageId: context.messageId,
        expiresAt: new Date(at + INPUT_CAPTURE_MS).toISOString(),
      },
      updatedAt: timestamp.toISOString(),
      lastInteractionAt: timestamp.toISOString(),
      expiresAt: new Date(at + DEFAULT_LEASE_MS).toISOString(),
    });
    writeStore(store, filePath);
    return { link: store.links[index], replacedLinks };
  });
}

function cancelInputCapture(taskKey, input, filePath = defaultTaskLinkPath(), now = new Date()) {
  return withStoreLock(filePath, () => {
    const store = readStore(filePath);
    const at = now instanceof Date ? now.getTime() : Number(now);
    const context = captureContext(input);
    const expired = clearExpiredCapturesInStore(store, at);
    const index = activeLinkIndexByTaskKey(store, String(taskKey || ''), at);
    if (index < 0) {
      if (expired.length) writeStore(store, filePath);
      return { canceled: false, link: null };
    }
    const current = store.links[index];
    const capture = current.inputCapture;
    if (!capture || capture.chatId !== context.chatId || capture.operatorId !== context.operatorId) {
      if (expired.length) writeStore(store, filePath);
      return { canceled: false, link: current };
    }
    const cleared = normalizeStoredLink({
      ...current, inputCapture: null, updatedAt: new Date(at).toISOString(),
    });
    store.links[index] = cleared;
    writeStore(store, filePath);
    return { canceled: true, link: cleared };
  });
}

function consumeInputCapture(input, filePath = defaultTaskLinkPath(), now = new Date()) {
  return withStoreLock(filePath, () => {
    const store = readStore(filePath);
    const at = now instanceof Date ? now.getTime() : Number(now);
    const context = captureContext(input);
    const expired = clearExpiredCapturesInStore(store, at);
    const matches = store.links
      .map((link, index) => ({ link, index }))
      .filter(({ link }) => inputCaptureActive(link, at)
        && link.inputCapture.chatId === context.chatId
        && link.inputCapture.operatorId === context.operatorId);
    if (matches.length > 1) {
      const ambiguousIds = new Set(matches.map(({ link }) => link.id));
      store.links = store.links.map((link) => (
        ambiguousIds.has(link.id)
          ? normalizeStoredLink({ ...link, inputCapture: null, updatedAt: new Date(at).toISOString() })
          : link
      ));
      writeStore(store, filePath);
      throw new Error('ambiguous task input capture');
    }
    if (!matches.length) {
      if (expired.length) writeStore(store, filePath);
      return null;
    }
    const { index } = matches[0];
    const consumed = normalizeStoredLink({
      ...store.links[index],
      inputCapture: null,
      updatedAt: new Date(at).toISOString(),
      lastInteractionAt: new Date(at).toISOString(),
      expiresAt: new Date(at + DEFAULT_LEASE_MS).toISOString(),
    });
    store.links[index] = consumed;
    writeStore(store, filePath);
    return consumed;
  });
}

function expireInputCaptures(filePath = defaultTaskLinkPath(), now = new Date()) {
  return withStoreLock(filePath, () => {
    const store = readStore(filePath);
    const at = now instanceof Date ? now.getTime() : Number(now);
    const expired = clearExpiredCapturesInStore(store, at);
    if (expired.length) writeStore(store, filePath);
    return expired;
  });
}

function updateLink(id, patch, filePath = defaultTaskLinkPath(), { renew = false, terminalAt = null } = {}) {
  return withStoreLock(filePath, () => {
    const store = readStore(filePath);
    const index = store.links.findIndex((link) => link.id === id);
    if (index < 0) throw new Error('task link not found');
    validatePatch(patch);
    const previous = store.links[index];
    const now = terminalAt ? new Date(terminalAt) : new Date();
    const inactive = previous.linkState === 'released' || previous.linkState === 'expired';
    const safePatch = inactive
      ? Object.fromEntries(Object.entries(patch).filter(([key]) => [
          'pendingMessageId', 'pendingCleanupDir', 'activeTurnId', 'lastDeliveredTurnId',
          'stagedCleanupDirs', 'inputCapture',
        ].includes(key)))
      : patch;
    const shouldAnchorTerminal = terminalAt && safePatch.turnState
      && !LEASE_PROTECTED_TURN_STATES.has(safePatch.turnState);
    store.links[index] = normalizeStoredLink({
      ...previous,
      ...safePatch,
      updatedAt: now.toISOString(),
      ...((renew || shouldAnchorTerminal) && !inactive ? {
        lastInteractionAt: renew ? now.toISOString() : previous.lastInteractionAt,
        expiresAt: new Date(Math.max(
          now.getTime(),
          Date.parse(previous.lastInteractionAt || previous.createdAt || now.toISOString()),
        ) + DEFAULT_LEASE_MS).toISOString(),
      } : {}),
    });
    writeStore(store, filePath);
    return store.links[index];
  });
}

function resolveReply(message, filePath = defaultTaskLinkPath()) {
  const ids = new Set([message?.message_id, message?.root_id, message?.parent_id].filter(Boolean));
  return readStore(filePath).links.find((link) => (
    [link.rootMessageId, ...(link.messageIds || [])].some((id) => ids.has(id))
  ));
}

function findLinkByTaskKey(taskKey, filePath = defaultTaskLinkPath()) {
  const matches = readStore(filePath).links.filter((link) => link.taskKey === taskKey);
  return matches.sort((left, right) => {
    const activeDelta = Number(effectiveLinkState(right) === 'active') - Number(effectiveLinkState(left) === 'active');
    return activeDelta || Date.parse(right.updatedAt) - Date.parse(left.updatedAt);
  })[0];
}

function listPublic(filePath = defaultTaskLinkPath()) {
  const byTaskKey = new Map();
  for (const link of readStore(filePath).links) {
    const existing = byTaskKey.get(link.taskKey);
    const prefer = !existing
      || effectiveLinkState(link) === 'active'
      || (effectiveLinkState(existing) !== 'active' && Date.parse(link.updatedAt) > Date.parse(existing.updatedAt));
    if (prefer) byTaskKey.set(link.taskKey, link);
  }
  return [...byTaskKey.values()].map((link) => publicLink(link));
}

module.exports = {
  DEFAULT_LEASE_MS,
  INPUT_CAPTURE_MS,
  PROTOCOL,
  SCHEMA_VERSION,
  armInputCapture,
  cancelInputCapture,
  controlsFor,
  consumeInputCapture,
  defaultTaskLinkPath,
  effectiveLinkState,
  expiryState,
  expireInputCaptures,
  findLinkByTaskKey,
  legacyState,
  listPublic,
  migrateStore,
  normalizeLink,
  operatorMatches,
  inputCaptureView,
  publicLink,
  readStore,
  resolveReply,
  updateLink,
  upsertLink,
  writeStore,
};
