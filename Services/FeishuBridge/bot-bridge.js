const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { spawn, spawnSync } = require('node:child_process');
const readline = require('node:readline');
const { createLarkCliRunner } = require('./lib/lark-cli-runner');
const { replyIdempotencyKey } = require('./lib/reply-policy');
const { createDirectoryService } = require('./lib/contact-directory');
const {
  createGroupDirectoryService,
  groupDirectoryEventRequiresRefresh,
} = require('./lib/group-directory');
const { atomicWritePrivateJson, createQueueStore } = require('./lib/queue-worker-core');
const {
  auditFingerprint,
  auditRequestContent,
  auditStructuredDescriptor,
  auditTargetDescriptor,
  redactAuditText,
} = require('./lib/audit-policy');
const { cleanupStagedMedia } = require('./lib/media-staging');
const {
  defaultClientConfigPath,
  documentIdentityForTarget,
  loadClientConfig,
  saveTestAssetBinding,
  validateMessageRequest,
} = require('./lib/bridge-client-core');
const { validateActionRequest } = require('./lib/action-registry');
const {
  operatorMatches: defaultConversationOperatorMatches,
  readStore: readDefaultConversationStore,
  resolveConversation: resolveDefaultConversation,
  updateConversation: updateDefaultConversation,
  upsertConversation: upsertDefaultConversation,
} = require('./lib/default-conversation-store');
const { actionExecutionSummary, executeActionRequest } = require('./lib/work-actions');
const { capability, selectCapabilityResultIdentifier } = require('./lib/capability-registry');
const { RUNTIME_MANIFEST } = require('./lib/runtime-manifest');
const {
  SUPPORTED_INBOUND_MESSAGE_TYPES,
  cleanupInboundAssets,
  inboundAssetsPrompt,
  inboundMessageDetails,
  stageInboundMessage,
} = require('./lib/inbound-media');
const {
  bridgeCardAction,
  normalizeCardAction,
  normalizeEventKey,
} = require('./lib/inbound-events');
const {
  TASK_LINK_CARD_REVISION,
  fitProgressCardToRequestBudget,
  formatElapsedDuration,
  progressCard,
} = require('./lib/progress-card');
const { deliverTaskLinkCard } = require('./lib/task-link-card-delivery');
const { loadOfficialCredentials } = require('./lib/lark-cli-credentials');
const {
  OFFICIAL_EVENT_TRANSPORT,
  createOfficialEventAdapter,
} = require('./lib/official-event-adapter');
const {
  FIXED_EVENT_KEYS,
  createEventInbox,
  validateConfiguredEventKeys,
} = require('./lib/event-inbox');
const {
  eventConsumerShouldConnect,
  readEventConsumerProfile,
} = require('./lib/event-consumer-profile');
const {
  effectiveLinkState: taskLinkEffectiveState,
  findLinkByTaskKey: findTaskLinkByKey,
  migrateStore: migrateTaskLinkStore,
  operatorMatches: taskLinkOperatorMatches,
  publicLink: publicTaskLink,
  readStore: readTaskLinkStore,
  resolveReply: resolveTaskLinkReply,
  updateLink: updateTaskLink,
  upsertLink: upsertTaskLink,
} = require('./lib/task-link-store');
const {
  KSFProjectPromotionClient,
  parseProjectContinuationIntent,
  projectContinuationPrompt,
  promotedTaskTitle,
} = require('./lib/ksf-project-promotion');
const {
  changedFilePathsFromEvent,
  finalTextFromTurn,
  latestTurn,
  projectDesktopTaskSnapshot,
  publicProgressFromEvent,
  publicProgressText,
  publicTurnState,
  recoveredRunningTurnOwner,
  safeQuestionSummary,
  terminalTaskLinkDetail,
  taskLinkCollaborationMode,
  taskLinkFollowupProjection,
  taskLinkProgressForTurn,
  taskLinkSnapshotRequiresSync,
  taskInput,
  turnsFromResult,
  validateAuthoritativeThread,
} = require('./lib/codex-task-control');
const { CodexDesktopTaskController } = require('./lib/codex-desktop-ipc');
const { CodexDesktopTurnJournal } = require('./lib/codex-desktop-turn-journal');
const {
  assertPrivatePathBoundary,
  defaultCodexBin,
  chmodPrivate,
  defaultDataRoot,
  defaultDesktopIPCPath,
  defaultLarkCliBin,
  defaultLogDir,
  spawnSleepInhibitor,
} = require('./lib/platform-runtime');

const homeDir = process.env.HOME || os.homedir();
const dataRoot = defaultDataRoot({ env: process.env, homeDir });
const logDir = process.env.FEISHU_BRIDGE_LOG_DIR || defaultLogDir(__dirname, { env: process.env, homeDir });
const kmsRoot = process.env.KMS_ROOT
  || (homeDir ? path.join(homeDir, 'Documents', 'KMS') : path.join(__dirname, 'KMS'));
const auditDir = process.env.FEISHU_AUDIT_DIR
  || path.join(logDir, 'audit');
const codexBin = defaultCodexBin({ env: process.env });
const codexTimeoutMs = Number(process.env.CODEX_TIMEOUT_MS || 10 * 60 * 1000);
const codexTaskTimeoutMs = Number(process.env.CODEX_TASK_TIMEOUT_MS || 30 * 60 * 1000);
const codexBypassApprovals = parseBool(process.env.CODEX_BYPASS_APPROVALS || 'true');
const codexAppServerRequestTimeoutMs = Number(process.env.CODEX_APP_SERVER_REQUEST_TIMEOUT_MS || 60 * 1000);
const codexTransportPreference = process.env.CODEX_TRANSPORT || 'auto';
const codexAutoStartDaemon = parseBool(process.env.CODEX_AUTO_START_DAEMON || 'true');
const codexClientName = process.env.CODEX_CLIENT_NAME || 'codex_vscode';
const codexClientTitle = process.env.CODEX_CLIENT_TITLE || 'Codex';
const defaultSessionName = process.env.CODEX_FEISHU_DEFAULT_SESSION || 'feishu-default-kms';
const defaultThreadTitle = process.env.CODEX_FEISHU_DEFAULT_THREAD_TITLE || '飞书默认对话';
const codexHome = process.env.CODEX_HOME || (homeDir ? path.join(homeDir, '.codex') : '');
const codexDesktopIPCPath = defaultDesktopIPCPath({ env: process.env, homeDir });
const codexDesktopRequestTimeoutMs = Number(process.env.CODEX_DESKTOP_REQUEST_TIMEOUT_MS || 20 * 1000);
const messageLogPath = path.join(logDir, 'messages.jsonl');
const taskLogPath = path.join(logDir, 'tasks.jsonl');
const sessionsPath = path.join(logDir, 'sessions.json');
const taskRunsDir = path.join(logDir, 'task-runs');
const outboxPath = resolveProjectPath(process.env.FEISHU_OUTBOX_PATH, path.join(logDir, 'outbox.jsonl'));
const outboxResultsPath = path.join(logDir, 'outbox-results.jsonl');
const outboxStatePath = path.join(logDir, 'outbox-state.json');
const docboxPath = resolveProjectPath(process.env.FEISHU_DOCBOX_PATH, path.join(logDir, 'docbox.jsonl'));
const docboxResultsPath = path.join(logDir, 'docbox-results.jsonl');
const docboxStatePath = path.join(logDir, 'docbox-state.json');
const actionboxPath = resolveProjectPath(process.env.FEISHU_ACTIONBOX_PATH, path.join(logDir, 'actionbox.jsonl'));
const actionboxResultsPath = path.join(logDir, 'actionbox-results.jsonl');
const actionboxStatePath = path.join(logDir, 'actionbox-state.json');
const eventConsumerStatePath = path.join(logDir, 'event-consumer-state.json');
const eventConsumerProfilePath = path.join(dataRoot, 'event-consumer-profile.json');
const outboundEnabled = parseBool(process.env.FEISHU_OUTBOUND_ENABLED || 'false');
const outboxPollMs = Math.max(500, parseNumber(process.env.FEISHU_OUTBOX_POLL_MS, 3000));
const outboundDryRun = parseBool(process.env.FEISHU_OUTBOUND_DRY_RUN || 'false');
const outboundWakeEnabled = parseBool(process.env.FEISHU_OUTBOUND_WAKE_ENABLED || 'false');
const outboundWakeHost = process.env.FEISHU_OUTBOUND_WAKE_HOST || '127.0.0.1';
const outboundWakePort = parseNumber(process.env.FEISHU_OUTBOUND_WAKE_PORT, 0);
const docboxEnabled = parseBool(process.env.FEISHU_DOCBOX_ENABLED || 'false');
const docboxPollMs = Math.max(500, parseNumber(process.env.FEISHU_DOCBOX_POLL_MS, 3000));
const docboxDryRun = parseBool(process.env.FEISHU_DOCBOX_DRY_RUN || 'true');
const docboxWakeEnabled = parseBool(process.env.FEISHU_DOCBOX_WAKE_ENABLED || 'false');
const docboxWakeHost = process.env.FEISHU_DOCBOX_WAKE_HOST || '127.0.0.1';
const docboxWakePort = parseNumber(process.env.FEISHU_DOCBOX_WAKE_PORT, 0);
const docboxAllowedSources = parseCsv(process.env.FEISHU_DOCBOX_ALLOWED_SOURCES || 'local,codex');
const actionboxEnabled = parseBool(process.env.FEISHU_ACTIONBOX_ENABLED || 'false');
const actionboxPollMs = Math.max(500, parseNumber(process.env.FEISHU_ACTIONBOX_POLL_MS, 3000));
const actionboxDryRun = parseBool(process.env.FEISHU_ACTIONBOX_DRY_RUN || 'true');
const actionboxAllowedSources = parseCsv(process.env.FEISHU_ACTIONBOX_ALLOWED_SOURCES || 'local,codex');
const actionboxTimeoutMs = Math.max(10000, parseNumber(process.env.FEISHU_ACTIONBOX_TIMEOUT_MS, 120000));
const actionboxWakeEnabled = parseBool(process.env.FEISHU_ACTIONBOX_WAKE_ENABLED || 'false');
const actionboxWakeHost = process.env.FEISHU_ACTIONBOX_WAKE_HOST || '127.0.0.1';
const actionboxWakePort = parseNumber(process.env.FEISHU_ACTIONBOX_WAKE_PORT, 0);
const queueMaxProcessedIds = Math.max(100, parseNumber(process.env.FEISHU_QUEUE_MAX_PROCESSED_IDS, 5000));
const queueWarnBytes = Math.max(1024, parseNumber(process.env.FEISHU_QUEUE_WARN_BYTES, 100 * 1024 * 1024));
const directoryEnabled = parseBool(process.env.FEISHU_DIRECTORY_ENABLED || 'false');
const directoryCachePath = resolveProjectPath(
  process.env.FEISHU_DIRECTORY_CACHE_PATH,
  path.join(dataRoot, 'directory.json'),
);
const directoryStatePath = resolveProjectPath(
  process.env.FEISHU_DIRECTORY_STATE_PATH,
  path.join(dataRoot, 'directory-state.json'),
);
const directoryRefreshMs = Math.max(60 * 1000, parseNumber(
  process.env.FEISHU_DIRECTORY_REFRESH_MS,
  6 * 60 * 60 * 1000,
));
const directoryMaxAgeMs = Math.max(60 * 1000, parseNumber(
  process.env.FEISHU_DIRECTORY_MAX_AGE_MS,
  24 * 60 * 60 * 1000,
));
const directoryPageSize = Math.max(1, Math.min(50, parseNumber(process.env.FEISHU_DIRECTORY_PAGE_SIZE, 50)));
const directoryMinimumUserCount = Math.max(1, parseNumber(process.env.FEISHU_DIRECTORY_MIN_USER_COUNT, 1));
const groupDirectoryEnabled = parseBool(process.env.FEISHU_GROUP_DIRECTORY_ENABLED || 'false');
const groupDirectoryCachePath = resolveProjectPath(
  process.env.FEISHU_GROUP_DIRECTORY_CACHE_PATH,
  path.join(dataRoot, 'group-directory.json'),
);
const groupDirectoryStatePath = resolveProjectPath(
  process.env.FEISHU_GROUP_DIRECTORY_STATE_PATH,
  path.join(dataRoot, 'group-directory-state.json'),
);
const groupDirectoryRefreshMs = Math.max(60 * 1000, parseNumber(
  process.env.FEISHU_GROUP_DIRECTORY_REFRESH_MS,
  30 * 60 * 1000,
));
const groupDirectoryMaxAgeMs = Math.max(60 * 1000, parseNumber(
  process.env.FEISHU_GROUP_DIRECTORY_MAX_AGE_MS,
  2 * 60 * 60 * 1000,
));
const groupDirectoryPageSize = Math.max(1, Math.min(100, parseNumber(
  process.env.FEISHU_GROUP_DIRECTORY_PAGE_SIZE,
  100,
)));
const directAllowedOpenIds = parseCsv(process.env.FEISHU_DIRECT_ALLOWED_OPEN_IDS);
const groupEnabled = parseBool(process.env.FEISHU_GROUP_ENABLED || 'false');
const groupAllowedChatIds = parseCsv(process.env.FEISHU_GROUP_ALLOWED_CHAT_IDS);
const groupAllowedOpenIds = parseCsv(process.env.FEISHU_GROUP_ALLOWED_OPEN_IDS);
const larkCliBin = defaultLarkCliBin(__dirname, { env: process.env });
const larkCliProfile = process.env.LARK_CLI_PROFILE || '';
const larkCliAs = process.env.LARK_CLI_AS || 'bot';
const mediaStagingRuntime = {
  mediaStagingDir: process.platform === 'win32'
    ? path.join(dataRoot, 'private-cache', 'outbox-assets')
    : path.join(__dirname, 'node_modules', '.cache', 'feishu-bridge', 'outbox-assets'),
};
const eventConsumerEnabled = parseBool(process.env.FEISHU_EVENT_CONSUMER_ENABLED || 'true');
const eventConsumerProfilePollMs = Math.max(250, parseNumber(
  process.env.FEISHU_EVENT_PROFILE_POLL_MS,
  1000,
));
const eventTransport = process.env.FEISHU_EVENT_TRANSPORT || OFFICIAL_EVENT_TRANSPORT;
const eventConsumerKeys = validateConfiguredEventKeys([...parseCsv(
  process.env.FEISHU_EVENT_KEYS || FIXED_EVENT_KEYS.join(','),
)].map((key) => normalizeEventKey(key)).filter(Boolean));
const eventInboxEnabled = parseBool(process.env.FEISHU_EVENT_INBOX_ENABLED || 'true');
const eventInboxDir = resolveProjectPath(
  process.env.FEISHU_EVENT_INBOX_DIR,
  path.join(dataRoot, 'events'),
);
assertPrivatePathBoundary(dataRoot, {
  logDir,
  outboxPath,
  docboxPath,
  actionboxPath,
  directoryCachePath,
  directoryStatePath,
  groupDirectoryCachePath,
  groupDirectoryStatePath,
  eventInboxDir,
  eventConsumerProfilePath,
  mediaStagingDir: mediaStagingRuntime.mediaStagingDir,
}, process.platform);
const inboundMaxBytes = Math.max(1024, parseNumber(process.env.FEISHU_INBOUND_MAX_BYTES, 25 * 1024 * 1024));
const instanceLockPath = path.join(logDir, 'bridge.pid');
const taskLinkInputRequests = new Map();
const taskLinkExecutions = new Set();
const taskLinkQueuedContexts = new Map();
const taskLinkCardRevisionAttempts = new Set();
const taskLinkLatestInputs = new Map();
const taskLinkFinalResults = new Map();
const taskLinkCardPushes = new Map();
const taskLinkJournalWatchers = new Map();
const taskLinkRefreshes = new Set();
const taskLinkFollowupTransitions = new Map();
const cardFollowupExecutions = new Set();
const defaultConversationChains = new Map();
let taskLinkCaffeinate = null;

const codexDesktopTaskController = new CodexDesktopTaskController({
  socketPath: codexDesktopIPCPath,
  requestTimeoutMs: codexDesktopRequestTimeoutMs,
  platform: process.platform,
  projectRoot: __dirname,
});
const codexDesktopTurnJournal = new CodexDesktopTurnJournal({ codexHome });
const projectPromotionClient = new KSFProjectPromotionClient({ env: process.env });
let taskLinkLeaseTimer = null;
const lark = createLarkCliRunner({
  bin: larkCliBin,
  cwd: __dirname,
  env: process.env,
  profile: larkCliProfile,
  as: larkCliAs,
  privateDir: process.platform === 'win32' ? path.join(dataRoot, 'private-cache', 'action-payloads') : '',
  mediaRoot: mediaStagingRuntime.mediaStagingDir,
});
const directoryLark = createLarkCliRunner({
  bin: larkCliBin,
  cwd: __dirname,
  env: process.env,
  profile: larkCliProfile,
  as: 'bot',
  privateDir: process.platform === 'win32' ? path.join(dataRoot, 'private-cache', 'action-payloads') : '',
  mediaRoot: mediaStagingRuntime.mediaStagingDir,
});
const userLark = createLarkCliRunner({
  bin: larkCliBin,
  cwd: __dirname,
  env: process.env,
  profile: larkCliProfile,
  as: 'user',
  privateDir: process.platform === 'win32' ? path.join(dataRoot, 'private-cache', 'action-payloads') : '',
  mediaRoot: mediaStagingRuntime.mediaStagingDir,
});
const directoryService = createDirectoryService({
  lark: directoryLark,
  cachePath: directoryCachePath,
  statePath: directoryStatePath,
  enabled: directoryEnabled,
  refreshMs: directoryRefreshMs,
  maxAgeMs: directoryMaxAgeMs,
  pageSize: directoryPageSize,
  minimumUserCount: directoryMinimumUserCount,
});
const groupDirectoryService = createGroupDirectoryService({
  lark: directoryLark,
  cachePath: groupDirectoryCachePath,
  statePath: groupDirectoryStatePath,
  enabled: groupDirectoryEnabled,
  refreshMs: groupDirectoryRefreshMs,
  maxAgeMs: groupDirectoryMaxAgeMs,
  pageSize: groupDirectoryPageSize,
});
const eventInbox = eventInboxEnabled ? createEventInbox({ dir: eventInboxDir }) : null;
const outboxStore = createQueueStore({
  queuePath: outboxPath,
  resultsPath: outboxResultsPath,
  statePath: outboxStatePath,
  wake: {
    enabled: outboundWakeEnabled,
    host: outboundWakeHost,
    configuredPort: outboundWakePort,
    actualPort: null,
  },
  maxProcessedIds: queueMaxProcessedIds,
  warnBytes: queueWarnBytes,
});
const docboxStore = createQueueStore({
  queuePath: docboxPath,
  resultsPath: docboxResultsPath,
  statePath: docboxStatePath,
  wake: {
    enabled: docboxWakeEnabled,
    host: docboxWakeHost,
    configuredPort: docboxWakePort,
    actualPort: null,
  },
  maxProcessedIds: queueMaxProcessedIds,
  warnBytes: queueWarnBytes,
});
const actionboxStore = createQueueStore({
  queuePath: actionboxPath,
  resultsPath: actionboxResultsPath,
  statePath: actionboxStatePath,
  wake: {
    enabled: actionboxWakeEnabled,
    host: actionboxWakeHost,
    configuredPort: actionboxWakePort,
    actualPort: null,
  },
  maxProcessedIds: queueMaxProcessedIds,
  warnBytes: queueWarnBytes,
});

fs.mkdirSync(logDir, { recursive: true });
fs.mkdirSync(taskRunsDir, { recursive: true });
fs.mkdirSync(auditDir, { recursive: true });

let instanceLockFd = null;

function isProcessAlive(pid) {
  if (!pid || pid === process.pid) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

function acquireInstanceLock() {
  if (instanceLockFd !== null) return;
  try {
    const existing = JSON.parse(fs.readFileSync(instanceLockPath, 'utf8'));
    if (isProcessAlive(existing.pid)) {
      throw new Error(`another feishu bridge instance is already running: pid ${existing.pid}`);
    }
    fs.unlinkSync(instanceLockPath);
  } catch (error) {
    if (error.code !== 'ENOENT' && !String(error.message || '').startsWith('another feishu bridge')) {
      try {
        fs.unlinkSync(instanceLockPath);
      } catch (unlinkError) {
        if (unlinkError.code !== 'ENOENT') throw unlinkError;
      }
    } else if (String(error.message || '').startsWith('another feishu bridge')) {
      throw error;
    }
  }

  instanceLockFd = fs.openSync(instanceLockPath, 'wx');
  fs.writeFileSync(instanceLockFd, `${JSON.stringify({
    pid: process.pid,
    startedAt: new Date().toISOString(),
  })}\n`, 'utf8');
}

function releaseInstanceLock() {
  if (instanceLockFd === null) return;
  try {
    fs.closeSync(instanceLockFd);
  } catch {
    // Ignore cleanup errors during shutdown.
  }
  instanceLockFd = null;
  try {
    const existing = JSON.parse(fs.readFileSync(instanceLockPath, 'utf8'));
    if (existing.pid === process.pid) fs.unlinkSync(instanceLockPath);
  } catch {
    // Ignore stale or already removed lock files.
  }
}

const chatCache = new Map();
const seenMessageIds = new Map();
const taskMessageIndex = new Map();
const messageDedupeTtlMs = 2 * 60 * 60 * 1000;
let officialEventAdapter = null;
let eventConsumerProfileTimer = null;
let eventConsumerProfileReconcilePromise = null;
let activeEventConsumerProfile = '';
let lastEventConsumerProfileSignature = '';

function readEventConsumerState() {
  try {
    return JSON.parse(fs.readFileSync(eventConsumerStatePath, 'utf8'));
  } catch {
    return {
      schemaVersion: 3,
      transport: eventTransport,
      status: eventConsumerEnabled ? 'unknown' : 'disabled',
      updatedAt: '',
      events: {},
    };
  }
}

function updateEventTransportState(patch) {
  const current = readEventConsumerState();
  atomicWritePrivateJson(eventConsumerStatePath, {
    ...current,
    ...patch,
    schemaVersion: 3,
    transport: eventTransport,
    updatedAt: new Date().toISOString(),
    events: current.events || {},
  });
}

function updateEventConsumerState(eventKey, patch) {
  const current = readEventConsumerState();
  atomicWritePrivateJson(eventConsumerStatePath, {
    ...current,
    schemaVersion: 3,
    transport: eventTransport,
    updatedAt: new Date().toISOString(),
    events: {
      ...(current.events || {}),
      [eventKey]: {
        ...(current.events?.[eventKey] || {}),
        ...patch,
        updatedAt: new Date().toISOString(),
      },
    },
  });
}

function eventConsumerRunningCount() {
  const state = readEventConsumerState();
  return eventConsumerKeys.filter((key) => state.events?.[key]?.status === 'running').length;
}

function parseCsv(value) {
  return new Set(String(value || '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean));
}

function parseBool(value) {
  return /^(1|true|yes|on)$/i.test(String(value || '').trim());
}

function parseNumber(value, fallback) {
  const number = Number(value);
  return Number.isFinite(number) ? number : fallback;
}

function resolveProjectPath(value, fallback) {
  if (!value) return fallback;
  return path.isAbsolute(value) ? value : path.join(__dirname, value);
}

function appendJsonl(filePath, record) {
  fs.appendFileSync(filePath, `${JSON.stringify(record)}\n`, 'utf8');
}

function appendMessageLog(record) {
  appendJsonl(messageLogPath, record);
}

function appendTaskLog(record) {
  appendJsonl(taskLogPath, record);
  indexTaskLogRecord(record);
}

function logIgnored(context, route, reason) {
  appendMessageLog({
    direction: 'ignored',
    at: new Date().toISOString(),
    chatId: context.message.chat_id,
    messageId: context.message.message_id,
    route,
    reason,
  });
}

function logOutbound(context, route, text) {
  appendMessageLog({
    direction: 'outbound',
    sentAt: new Date().toISOString(),
    chatId: context.message.chat_id,
    replyTo: context.message.message_id,
    route,
    text,
  });
}

function logAccepted(context, route) {
  appendMessageLog({
    direction: 'accepted',
    at: new Date().toISOString(),
    chatId: context.message.chat_id,
    messageId: context.message.message_id,
    route,
  });
}

function logError(context, error, extra = {}) {
  appendMessageLog({
    direction: 'error',
    at: new Date().toISOString(),
    chatId: context.message.chat_id,
    replyTo: context.message.message_id,
    error: error.stack || error.message || String(error),
    ...extra,
  });
}

function countLogLines(filePath = messageLogPath) {
  if (!fs.existsSync(filePath)) return 0;
  return fs.readFileSync(filePath, 'utf8').split('\n').filter(Boolean).length;
}

function readJsonl(filePath) {
  if (!fs.existsSync(filePath)) return [];
  return fs.readFileSync(filePath, 'utf8')
    .split('\n')
    .filter(Boolean)
    .map((line) => {
      try {
        return JSON.parse(line);
      } catch {
        return null;
      }
    })
    .filter(Boolean);
}

function rememberMessage(messageId) {
  if (!messageId) return false;
  const now = Date.now();
  for (const [id, at] of seenMessageIds) {
    if (now - at > messageDedupeTtlMs) seenMessageIds.delete(id);
  }
  if (seenMessageIds.has(messageId)) return false;
  seenMessageIds.set(messageId, now);
  return true;
}

function hydrateSeenMessageIds() {
  const now = Date.now();
  for (const item of readJsonl(messageLogPath)) {
    if (item.direction !== 'inbound' || !item.messageId) continue;
    const at = Date.parse(item.receivedAt || item.at || '');
    if (!Number.isFinite(at) || now - at > messageDedupeTtlMs) continue;
    seenMessageIds.set(item.messageId, at);
  }
}

function indexTaskLogRecord(record) {
  if (!record?.id || !record.messageRefs) return;
  for (const ids of Object.values(record.messageRefs)) {
    for (const messageId of ids || []) {
      if (messageId) taskMessageIndex.set(messageId, record.id);
    }
  }
}

function hydrateTaskMessageIndex() {
  taskMessageIndex.clear();
  for (const item of readJsonl(taskLogPath)) indexTaskLogRecord(item);
}

function mergeTaskEvent(previous, event) {
  const merged = { ...(previous || {}), ...event };
  if (previous?.messageRefs || event.messageRefs) {
    merged.messageRefs = { ...(previous?.messageRefs || {}) };
    for (const [kind, ids] of Object.entries(event.messageRefs || {})) {
      merged.messageRefs[kind] = Array.from(new Set([
        ...(merged.messageRefs[kind] || []),
        ...(ids || []),
      ]));
    }
  }
  return merged;
}

function readSessions() {
  if (!fs.existsSync(sessionsPath)) return {};
  try {
    return JSON.parse(fs.readFileSync(sessionsPath, 'utf8'));
  } catch {
    return {};
  }
}

function writeSessions(sessions) {
  fs.writeFileSync(sessionsPath, `${JSON.stringify(sessions, null, 2)}\n`, 'utf8');
}

function parseText(content) {
  try {
    return JSON.parse(content || '{}').text || '';
  } catch {
    return content || '';
  }
}

function normalizeCommand(text) {
  return String(text || '')
    .replace(/@\S+\s*/g, '')
    .replace(/^[-:：，,]\s*/, '')
    .replace(/\s+/g, ' ')
    .trim();
}

function senderText(sender) {
  const id = sender?.sender_id || {};
  return [
    `open_id: ${id.open_id || '-'}`,
    `union_id: ${id.union_id || '-'}`,
    `user_id: ${id.user_id || '-'}`,
  ].join('\n');
}

function senderOpenId(sender) {
  return sender?.sender_id?.open_id || '';
}

function isAllowedBySet(set, value) {
  return set.size === 0 || set.has('*') || set.has(value);
}

async function fetchChat(chatId) {
  if (chatCache.has(chatId)) return chatCache.get(chatId);
  const resp = await lark.larkApi('GET', `/open-apis/im/v1/chats/${chatId}`);
  const chat = lark.larkApiData(resp) || {};
  chatCache.set(chatId, chat);
  return chat;
}

function chatKind(chat) {
  if (chat.chat_mode === 'p2p') return 'direct';
  if (chat.chat_mode === 'group' || chat.chat_mode === 'topic') return 'group';
  return 'unknown';
}

async function authorizeMessage(message, sender) {
  const chat = await fetchChat(message.chat_id);
  const kind = chatKind(chat);
  const openId = senderOpenId(sender);

  if (kind === 'direct') {
    return {
      allowed: directAllowedOpenIds.has(openId),
      kind,
      chat,
      reason: directAllowedOpenIds.has(openId)
        ? 'direct_allowed'
        : 'direct_sender_not_allowed',
    };
  }

  if (kind === 'group') {
    if (!groupEnabled) {
      return {
        allowed: false,
        kind,
        chat,
        reason: 'group_disabled',
      };
    }
    const allowedGroup = isAllowedBySet(groupAllowedChatIds, message.chat_id);
    const allowedSender = isAllowedBySet(groupAllowedOpenIds, openId);
    return {
      allowed: allowedGroup && allowedSender,
      kind,
      chat,
      reason: allowedGroup && allowedSender
        ? 'group_allowed'
        : !allowedGroup
          ? 'group_chat_not_allowed'
          : 'group_sender_not_allowed',
    };
  }

  return {
    allowed: false,
    kind,
    chat,
    reason: 'unknown_chat_kind',
  };
}

function todayKey(date = new Date()) {
  const formatter = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  });
  return formatter.format(date);
}

function timeText(date = new Date()) {
  return new Intl.DateTimeFormat('zh-CN', {
    timeZone: 'Asia/Shanghai',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(date);
}

function auditPathFor(date = new Date()) {
  return path.join(auditDir, `${todayKey(date)}.md`);
}

function ensureAuditFile(date = new Date()) {
  const filePath = auditPathFor(date);
  if (!fs.existsSync(filePath)) {
    fs.writeFileSync(filePath, [
      '---',
      'type: log',
      'status: active',
      'domain: feishu-codex-bridge',
      'project: feishu-codex-bridge',
      'summary: 飞书桥接 Codex 的当日消息、任务和执行结果审计记录。',
      `created: ${todayKey(date)}`,
      `updated: ${todayKey(date)}`,
      '---',
      '',
      `# 飞书-Codex 审计日志 ${todayKey(date)}`,
      '',
    ].join('\n'), 'utf8');
  }
  return filePath;
}

function appendAudit(section, lines, date = new Date()) {
  const filePath = ensureAuditFile(date);
  const content = [
    `## ${section} ${timeText(date)}`,
    '',
    ...lines,
    '',
  ].join('\n');
  fs.appendFileSync(filePath, content, 'utf8');
  return filePath;
}

function clip(text, limit = 3500) {
  const value = String(text || '');
  if (value.length <= limit) return value;
  return `${value.slice(0, limit - 80)}\n\n[内容过长，已截断；完整内容见本地日志。]`;
}

function splitMessage(text, limit = 3200) {
  const value = String(text || '');
  if (value.length <= limit) return [value];
  const parts = [];
  let rest = value;
  while (rest.length > limit) {
    let cut = rest.lastIndexOf('\n\n', limit);
    if (cut < limit * 0.5) cut = rest.lastIndexOf('\n', limit);
    if (cut < limit * 0.5) cut = rest.lastIndexOf('。', limit);
    if (cut < limit * 0.5) cut = limit;
    parts.push(rest.slice(0, cut).trim());
    rest = rest.slice(cut).trim();
  }
  if (rest) parts.push(rest);
  return parts.filter(Boolean);
}

function safeOneLine(text, limit = 160) {
  return clip(String(text || '').replace(/\s+/g, ' ').trim(), limit);
}

function withTimeout(promise, timeoutMs, label) {
  let timer;
  const timeout = new Promise((resolve) => {
    timer = setTimeout(() => resolve(`timeout: ${label}`), timeoutMs);
  });
  return Promise.race([
    promise.finally(() => clearTimeout(timer)),
    timeout,
  ]);
}

function ensureLarkCliReady() {
  return lark.ensureReady();
}

async function replyText(messageId, chatId, text, { phase = 'immediate' } = {}) {
  const parts = splitMessage(text);
  const first = parts.shift() || '';
  const sentIds = [];
  const firstIdempotencyKey = replyIdempotencyKey(messageId, phase, 0);
  try {
    const { messageId: sentId } = await lark.larkImReply({
      messageId,
      text: first,
      idempotencyKey: firstIdempotencyKey,
    });
    if (sentId) sentIds.push(sentId);
  } catch {
    const id = await sendChatText(chatId, first, `${firstIdempotencyKey}-fallback`);
    if (id) sentIds.push(id);
  }
  for (const [index, part] of parts.entries()) {
    const id = await sendChatText(
      chatId,
      part,
      replyIdempotencyKey(messageId, phase, index + 1),
    );
    if (id) sentIds.push(id);
  }
  return sentIds;
}

async function replyCard(messageId, card, { phase = 'card' } = {}) {
  const { messageId: sentId } = await lark.larkImReplyCard({
    messageId,
    card,
    idempotencyKey: replyIdempotencyKey(messageId, phase, 0),
  });
  return sentId || '';
}

async function updateProgressCard(context, state) {
  if (!context?.progressMessageId) return false;
  const fitted = fitProgressCardToRequestBudget(state);
  if (!fitted.card) return false;
  try {
    await lark.larkImPatchCard({
      messageId: context.progressMessageId,
      card: fitted.card,
      timeoutMs: 30000,
    });
    return fitted.complete;
  } catch (error) {
    appendMessageLog({
      direction: 'progress_card_update_failed',
      at: new Date().toISOString(),
      messageId: context.progressMessageId,
      error: safeOneLine(error.message || String(error), 500),
    });
    return false;
  }
}

async function deliverProgressResult(context, state, text, { phase = 'final' } = {}) {
  const cardUpdated = await updateProgressCard(context, state);
  if (cardUpdated) {
    return { mode: 'card', messageIds: [context.progressMessageId] };
  }
  const messageIds = await replyText(
    context.message.message_id,
    context.message.chat_id,
    text,
    { phase },
  );
  return { mode: 'text_fallback', messageIds };
}

function cleanupContextInbound(context) {
  if (!context?.inbound?.cleanupDir || context.inboundCleaned) return;
  context.inboundCleaned = true;
  try {
    cleanupInboundAssets(__dirname, context.inbound.cleanupDir);
  } catch (error) {
    appendMessageLog({
      direction: 'inbound_asset_cleanup_failed',
      at: new Date().toISOString(),
      error: safeOneLine(error.message || String(error), 500),
    });
  }
}

async function sendChatText(chatId, text, uuid = `codex-msg-${Date.now()}`) {
  const { messageId } = await lark.larkImSend({
    target: { type: 'chat_id', id: chatId },
    text,
    idempotencyKey: uuid,
  });
  return messageId;
}

async function sendTextByTarget(target, text, uuid = `codex-outbox-${Date.now()}`) {
  const type = target?.type;
  if (type !== 'chat_id' && type !== 'open_id') {
    throw new Error(`unsupported target type: ${type || '-'}`);
  }
  const { messageId } = await lark.larkImSend({
    target,
    text,
    idempotencyKey: uuid,
  });
  return messageId;
}

async function sendMessageByTarget(target, request, uuid = `codex-outbox-${Date.now()}`) {
  const type = target?.type;
  if (type !== 'chat_id' && type !== 'open_id') {
    throw new Error(`unsupported target type: ${type || '-'}`);
  }
  const { messageId } = await lark.larkImSend({
    target,
    format: request.type,
    content: request.text,
    filePath: request.filePath,
    idempotencyKey: uuid,
  });
  return messageId;
}

async function getChatInfo(chatId) {
  const chat = await fetchChat(chatId);
  return [
    `群名：${chat.name || '-'}`,
    `chat_id：${chat.chat_id || chatId}`,
    `群类型：${chat.chat_mode || '-'} / ${chat.chat_type || '-'}`,
    `成员数：${chat.member_count ?? '-'}`,
    `描述：${chat.description || '-'}`,
  ].join('\n');
}

async function getMemberList(chatId) {
  const resp = await lark.larkApi('GET', `/open-apis/im/v1/chats/${chatId}/members`, {
    params: { page_size: 50, member_id_type: 'open_id' },
  });
  const data = userLark.larkApiData(resp) || {};
  const members = data.items || [];
  if (!members.length) return '未读取到成员，可能是权限或群安全设置限制。';
  return [
    `成员数：${data.member_total ?? members.length}`,
    ...members.map((member, index) => `${index + 1}. ${member.name || '-'} (${member.member_id})`),
  ].join('\n');
}

function getCodexVersion() {
  const result = spawnSync(codexBin, ['--version'], {
    encoding: 'utf8',
    timeout: 5000,
  });
  if (result.error) return `error: ${result.error.message}`;
  return (result.stdout || result.stderr || '').trim() || `exit ${result.status}`;
}

function codexSandboxMode() {
  return codexBypassApprovals ? 'danger-full-access' : 'workspace-write';
}

function codexSandboxPolicy() {
  return codexBypassApprovals
    ? { type: 'dangerFullAccess' }
    : { type: 'workspaceWrite', writableRoots: [kmsRoot], networkAccess: true };
}

function bridgeDeveloperInstructions() {
  return [
    '你是通过飞书桥接到本机 Codex app-server 的协作 AI。',
    '默认使用中文，先结论后展开，回答要直接、克制、可执行。',
    '遵守当前工作区的 AGENTS.md 和 .agents 规则。',
    '不要直接调用飞书发消息工具；最终回复由桥接程序回传飞书。',
    '如需读取或写入飞书文档，优先使用 lark-cli，并保持 bot/app 身份边界。',
    '飞书写入、移动、改权限属于高影响操作，除非用户本轮明确要求并给出目标，否则只给方案和待确认清单。',
  ].join('\n');
}

function startCodexDaemon() {
  const result = spawnSync(codexBin, ['app-server', 'daemon', 'start'], {
    cwd: kmsRoot,
    encoding: 'utf8',
    timeout: 30 * 1000,
    env: process.env,
  });
  appendMessageLog({
    direction: 'codex_daemon_start',
    at: new Date().toISOString(),
    ok: result.status === 0,
    status: result.status,
    stdout: safeOneLine(result.stdout || '', 1000),
    stderr: safeOneLine(result.stderr || result.error?.message || '', 1000),
  });
  return result.status === 0;
}

class CodexAppServer {
  constructor() {
    this.proc = null;
    this.rl = null;
    this.ready = null;
    this.nextId = 1;
    this.pending = new Map();
    this.activeTurns = new Map();
    this.loadedThreadIds = new Set();
    this.stderrTail = '';
    this.transport = 'not-started';
  }

  async ensureStarted() {
    if (this.ready) return this.ready;
    this.ready = this.startProcess().catch((error) => {
      this.ready = null;
      this.teardown(error, this.proc);
      throw error;
    });
    return this.ready;
  }

  async startProcess() {
    const modes = codexTransportPreference === 'proxy'
      ? ['proxy']
      : codexTransportPreference === 'app-server'
        ? ['app-server']
        : ['proxy', 'app-server'];
    let lastError;
    let triedDaemonStart = false;
    for (const mode of modes) {
      try {
        await this.startProcessWithMode(mode);
        this.transport = mode;
        appendMessageLog({
          direction: 'codex_transport_started',
          at: new Date().toISOString(),
          transport: mode,
          clientName: codexClientName,
        });
        return;
      } catch (error) {
        lastError = error;
        appendMessageLog({
          direction: 'codex_transport_failed',
          at: new Date().toISOString(),
          transport: mode,
          error: error.message || String(error),
        });
        this.teardown(error, this.proc);
        if (mode === 'proxy' && codexAutoStartDaemon && !triedDaemonStart) {
          triedDaemonStart = true;
          if (startCodexDaemon()) {
            try {
              await this.startProcessWithMode(mode);
              this.transport = mode;
              appendMessageLog({
                direction: 'codex_transport_started',
                at: new Date().toISOString(),
                transport: mode,
                clientName: codexClientName,
                afterDaemonStart: true,
              });
              return;
            } catch (retryError) {
              lastError = retryError;
              appendMessageLog({
                direction: 'codex_transport_failed',
                at: new Date().toISOString(),
                transport: mode,
                afterDaemonStart: true,
                error: retryError.message || String(retryError),
              });
              this.teardown(retryError, this.proc);
            }
          }
        }
      }
    }
    throw lastError || new Error('codex app-server could not start');
  }

  async startProcessWithMode(mode) {
    const args = mode === 'proxy' ? ['app-server', 'proxy'] : ['app-server'];
    const proc = spawn(codexBin, args, {
      cwd: kmsRoot,
      stdio: ['pipe', 'pipe', 'pipe'],
      env: process.env,
    });
    this.proc = proc;

    proc.on('exit', (code, signal) => {
      if (this.proc !== proc) return;
      const error = new Error(`codex app-server ${mode} exited with ${signal || code}`);
      if (this.transport === mode) this.ready = null;
      this.teardown(error, proc);
    });
    proc.on('error', (error) => {
      if (this.proc !== proc) return;
      if (this.transport === mode) this.ready = null;
      this.teardown(error, proc);
    });
    proc.stderr.on('data', (chunk) => {
      if (this.proc !== proc) return;
      this.stderrTail = `${this.stderrTail}${chunk.toString()}`.slice(-8000);
    });
    const rl = readline.createInterface({ input: proc.stdout });
    this.rl = rl;
    rl.on('line', (line) => {
      if (this.proc !== proc) return;
      this.handleLine(line);
    });

    await this.requestRaw('initialize', {
      clientInfo: {
        name: codexClientName,
        title: codexClientTitle,
        version: RUNTIME_MANIFEST.capabilityVersion,
      },
      capabilities: {
        experimentalApi: true,
      },
    }, codexAppServerRequestTimeoutMs);
    this.notify('initialized', {});
  }

  teardown(error, proc = this.proc) {
    if (proc && this.proc && this.proc !== proc) return;
    if (this.rl) {
      this.rl.removeAllListeners();
      this.rl.close();
      this.rl = null;
    }
    if (this.proc) {
      const currentProc = this.proc;
      currentProc.removeAllListeners();
      if (currentProc.exitCode === null && !currentProc.killed) {
        currentProc.kill();
      }
      this.proc = null;
    }
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error || new Error('codex app-server stopped'));
    }
    this.pending.clear();
    for (const turn of this.activeTurns.values()) {
      clearTimeout(turn.timer);
      if (turn.logStream) turn.logStream.end();
      turn.reject(error || new Error('codex app-server stopped'));
    }
    this.activeTurns.clear();
    this.loadedThreadIds.clear();
  }

  async close() {
    const loadedThreadIds = [...this.loadedThreadIds];
    for (const threadId of loadedThreadIds) {
      try {
        await this.requestRaw('thread/unsubscribe', { threadId }, 5000);
      } catch (error) {
        appendMessageLog({
          direction: 'codex_thread_unsubscribe_failed',
          at: new Date().toISOString(),
          threadId,
          error: safeOneLine(error.message || String(error), 300),
        });
      }
    }
    this.ready = null;
    this.transport = 'not-started';
    this.teardown(new Error('codex app-server turn runner closed'));
  }

  handleLine(line) {
    if (!line.trim()) return;
    let msg;
    try {
      msg = JSON.parse(line);
    } catch {
      return;
    }

    if (msg.id !== undefined && msg.method) {
      this.handleServerRequest(msg);
      return;
    }

    if (msg.id !== undefined) {
      const pending = this.pending.get(msg.id);
      if (!pending) return;
      this.pending.delete(msg.id);
      clearTimeout(pending.timer);
      if (msg.error) {
        pending.reject(this.rpcError(msg.error));
      } else {
        pending.resolve(msg.result);
      }
      return;
    }

    if (msg.method) this.handleNotification(msg);
  }

  handleServerRequest(msg) {
    if (msg.method === 'item/tool/requestUserInput') {
      const params = msg.params || {};
      const active = [...this.activeTurns.values()];
      const turn = active.find((candidate) => this.notificationBelongsToTurn({ params }, candidate))
        || (active.length === 1 ? active[0] : null);
      if (turn?.onInputRequest) {
        if ((params.questions || []).some((question) => question.isSecret)) {
          turn.onInputRequest(params.questions || [], { desktopRequired: true, reason: 'sensitive_input' });
          this.send({ id: msg.id, error: { code: -32602, message: 'Sensitive user input is not relayed through Feishu' } });
          return;
        }
        taskLinkInputRequests.set(turn.threadId, {
          owner: 'app-server',
          requestId: msg.id,
          questions: params.questions || [],
        });
        turn.onInputRequest(params.questions || [], { desktopRequired: false });
        return;
      }
    }
    if (msg.method === 'mcpServer/elicitation/request') {
      const params = msg.params || {};
      const active = [...this.activeTurns.values()];
      const turn = active.find((candidate) => this.notificationBelongsToTurn({ params }, candidate))
        || (active.length === 1 ? active[0] : null);
      if (turn?.onInputRequest) {
        turn.onInputRequest([], { desktopRequired: true, reason: 'mcp_elicitation' });
      }
      this.send({ id: msg.id, error: { code: -32602, message: 'MCP elicitation must be completed in Codex Desktop' } });
      return;
    }
    this.send({
      id: msg.id,
      error: {
        code: -32601,
        message: `Unsupported app-server request: ${msg.method}`,
      },
    });
  }

  handleNotification(msg) {
    for (const turn of this.activeTurns.values()) {
      if (!this.notificationBelongsToTurn(msg, turn)) continue;
      turn.rawEvents.push(msg);
      if (turn.logStream) turn.logStream.write(`${JSON.stringify(msg)}\n`);
      if (turn.onProgress) turn.onProgress(msg);

      const item = msg.params?.item;
      if (msg.method === 'item/completed' && item?.type === 'agentMessage' && item.text) {
        if (!item.phase || item.phase === 'final_answer') {
          turn.finalText = item.text;
        }
      }

      if (msg.method === 'item/agentMessage/delta' && typeof msg.params?.delta === 'string') {
        turn.deltaText += msg.params.delta;
      }

      if (msg.method === 'error') {
        turn.lastError = msg.params?.error?.message || msg.params?.message || JSON.stringify(msg.params || {});
      }

      if (msg.method === 'turn/completed') {
        this.completeTurn(turn.id, msg);
      }
    }
  }

  notificationBelongsToTurn(msg, turn) {
    const params = msg.params || {};
    const threadId = params.threadId || params.thread?.id || params.item?.threadId;
    const turnId = params.turnId || params.turn?.id || params.item?.turnId;
    if (threadId && threadId !== turn.threadId) return false;
    if (turnId && turn.turnId && turnId !== turn.turnId) return false;
    return Boolean(threadId || turnId || msg.method === 'error');
  }

  completeTurn(activeTurnId, msg) {
    const turn = this.activeTurns.get(activeTurnId);
    if (!turn) return;
    this.activeTurns.delete(activeTurnId);
    taskLinkInputRequests.delete(turn.threadId);
    clearTimeout(turn.timer);
    if (turn.logStream) turn.logStream.end();
    const status = msg.params?.turn?.status || msg.params?.status || 'completed';
    const durationMs = Date.now() - turn.startedAt;
    if (['failed', 'cancelled', 'canceled', 'interrupted'].includes(status) || turn.lastError) {
      const error = new Error(turn.lastError || `codex turn finished with status ${status}`);
      error.durationMs = durationMs;
      error.events = turn.rawEvents;
      turn.reject(error);
      return;
    }
    turn.resolve({
      threadId: turn.threadId,
      turnId: turn.turnId,
      finalText: turn.finalText || turn.deltaText.trim(),
      stdout: turn.rawEvents.map((event) => JSON.stringify(event)).join('\n'),
      stderr: this.stderrTail,
      durationMs,
    });
  }

  rpcError(error) {
    const err = new Error(error?.message || String(error));
    err.code = error?.code;
    err.data = error?.data;
    return err;
  }

  async request(method, params = {}, timeoutMs = codexAppServerRequestTimeoutMs) {
    await this.ensureStarted();
    return this.requestRaw(method, params, timeoutMs);
  }

  requestRaw(method, params = {}, timeoutMs = codexAppServerRequestTimeoutMs) {
    const id = this.nextId++;
    const payload = { method, id, params };
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`codex app-server request timed out: ${method}`));
      }, timeoutMs);
      this.pending.set(id, { resolve, reject, timer, method });
      this.send(payload);
    });
  }

  notify(method, params = {}) {
    this.send({ method, params });
  }

  send(payload) {
    if (!this.proc?.stdin?.writable) {
      throw new Error('codex app-server is not writable');
    }
    this.proc.stdin.write(`${JSON.stringify(payload)}\n`);
  }

  async accountStatus() {
    try {
      const result = await this.request('account/read', { refreshToken: false }, 10 * 1000);
      const account = result?.account;
      return account ? `${account.type}${account.planType ? `/${account.planType}` : ''}` : 'not_signed_in';
    } catch (error) {
      return `error: ${error.message || String(error)}`;
    }
  }

  async collaborationModes() {
    const result = await this.request('collaborationMode/list', {}, 10 * 1000);
    return (Array.isArray(result?.data) ? result.data : [])
      .map((item) => String(item?.mode || ''))
      .filter((mode) => ['default', 'plan'].includes(mode));
  }

  async setThreadName(threadId, name) {
    if (!threadId || !name) return false;
    try {
      await this.request('thread/name/set', { threadId, name }, 10 * 1000);
      return true;
    } catch (error) {
      appendMessageLog({
        direction: 'thread_name_failed',
        at: new Date().toISOString(),
        threadId,
        name,
        error: error.message || String(error),
      });
      return false;
    }
  }

  async startOrResumeThread(sessionId, cwd = kmsRoot, { taskLink = false } = {}) {
    if (sessionId) {
      const result = await this.request('thread/resume', { threadId: sessionId });
      const thread = result?.thread;
      if (thread?.id) this.loadedThreadIds.add(thread.id);
      return thread;
    }
    const result = await this.request('thread/start', {
      cwd,
      approvalPolicy: 'never',
      sandbox: taskLink ? 'danger-full-access' : codexSandboxMode(),
      personality: 'pragmatic',
      developerInstructions: bridgeDeveloperInstructions(),
      serviceName: codexClientName,
      threadSource: 'user',
      ephemeral: false,
    });
    const thread = result?.thread;
    if (thread?.id) this.loadedThreadIds.add(thread.id);
    return thread;
  }

  async runDesktopTaskLinkTurn({
    input, sessionId, logPath, timeoutMs, cwd, collaborationMode,
    onProgress, onInputRequest, onTurnStarted,
  }) {
    const before = projectDesktopTaskSnapshot(
      await this.readThreadSnapshot(sessionId),
      codexDesktopTurnJournal.snapshot(sessionId),
    );
    const beforeTurnId = before.turnId || '';
    if (logPath && fs.existsSync(logPath)) chmodPrivate(logPath, 0o600);
    const logStream = logPath ? fs.createWriteStream(logPath, { flags: 'a', mode: 0o600 }) : null;
    const startedAt = Date.now();
    let turnId = '';
    let announcedTurn = false;
    let desktopInputReported = false;
    let reportedInputRequestId = '';
    let waitingStartedAt = 0;
    let waitingDurationMs = 0;
    let lastJournalProgress = '';
    const seenItems = new Set();

    try {
      const submitted = await codexDesktopTaskController.startTurn({
        threadId: sessionId,
        cwd,
        input,
        collaborationMode,
      });
      turnId = submitted.turnId || '';
      if (turnId && onTurnStarted) {
        announcedTurn = true;
        onTurnStarted({ threadId: sessionId, turnId });
      }

      while (true) {
        const now = Date.now();
        const currentWaitingMs = waitingStartedAt ? now - waitingStartedAt : 0;
        if (now - startedAt - waitingDurationMs - currentWaitingMs >= timeoutMs) break;
        await new Promise((resolve) => setTimeout(resolve, 500));
        const snapshot = projectDesktopTaskSnapshot(
          await this.readThreadSnapshot(sessionId),
          codexDesktopTurnJournal.snapshot(sessionId),
        );
        if (logStream) {
          logStream.write(`${JSON.stringify({
            method: 'desktop/thread/read',
            params: {
              threadId: sessionId,
              turn: snapshot.turn,
              publicState: snapshot.publicState,
              journalTurn: snapshot.journalTurn,
            },
          })}\n`);
        }
        const candidateTurnId = snapshot.turnId || '';
        const isSubmittedTurn = Boolean(candidateTurnId)
          && (turnId ? candidateTurnId === turnId : candidateTurnId !== beforeTurnId);
        if (!isSubmittedTurn) continue;
        turnId = candidateTurnId;
        if (!announcedTurn && onTurnStarted) {
          announcedTurn = true;
          onTurnStarted({ threadId: sessionId, turnId });
        }

        const journalTurn = snapshot.journalTurn;
        if (journalTurn?.turnId === turnId
          && journalTurn.lastMessagePhase === 'commentary'
          && journalTurn.lastMessage) {
          const fingerprint = `${journalTurn.updatedAt || ''}:${journalTurn.lastMessage}`;
          if (fingerprint !== lastJournalProgress && onProgress) {
            lastJournalProgress = fingerprint;
            onProgress({
              method: 'item/completed',
              params: {
                threadId: sessionId,
                turnId,
                item: {
                  type: 'agentMessage',
                  phase: 'commentary',
                  text: journalTurn.lastMessage,
                },
              },
            });
          }
        }

        const snapshotItems = snapshot.turn?.id === turnId ? snapshot.turn.items || [] : [];
        for (const [index, item] of snapshotItems.entries()) {
          const itemKey = item?.id || `${index}:${item?.type || 'item'}`;
          if (seenItems.has(itemKey)) continue;
          seenItems.add(itemKey);
          if (onProgress) {
            onProgress({
              method: 'item/completed',
              params: { threadId: sessionId, turnId, item },
            });
          }
        }

        const pendingInput = journalTurn?.turnId === turnId
          ? trackDesktopTaskLinkInput(sessionId, journalTurn)
          : null;
        if (pendingInput) {
          if (!waitingStartedAt) waitingStartedAt = Date.now();
          const desktopRequired = pendingInput.questions.some((question) => question.isSecret);
          if (reportedInputRequestId !== pendingInput.requestId && onInputRequest) {
            reportedInputRequestId = pendingInput.requestId;
            onInputRequest(pendingInput.questions, {
              desktopRequired,
              preserveTurn: true,
              reason: desktopRequired ? 'sensitive_input' : 'desktop_user_input',
            });
          }
          continue;
        }
        if (waitingStartedAt) {
          waitingDurationMs += Date.now() - waitingStartedAt;
          waitingStartedAt = 0;
        }
        if (reportedInputRequestId) {
          const tracked = taskLinkInputRequests.get(sessionId);
          if (tracked?.owner === 'desktop' && tracked.requestId === reportedInputRequestId) {
            taskLinkInputRequests.delete(sessionId);
          }
          reportedInputRequestId = '';
        }

        if (snapshot.publicState.turnState === 'desktop_action_required') {
          if (!desktopInputReported && onInputRequest) {
            desktopInputReported = true;
            onInputRequest([], {
              desktopRequired: true,
              preserveTurn: true,
              reason: 'desktop_owned_input',
            });
          }
          continue;
        }
        if (['running', 'waiting_input'].includes(snapshot.publicState.turnState)) continue;
        if (snapshot.publicState.turnState === 'failed') {
          throw new Error('Codex Desktop task turn failed');
        }
        if (snapshot.publicState.turnState === 'interrupted') {
          return {
            status: 'interrupted',
            threadId: sessionId,
            turnId,
            finalText: '',
            stdout: '',
            stderr: '',
            durationMs: Date.now() - startedAt,
          };
        }
        if (snapshot.publicState.turnState === 'completed') {
          return {
            status: 'completed',
            threadId: sessionId,
            turnId,
            finalText: snapshot.finalText || finalTextFromTurn(snapshot.turn),
            stdout: '',
            stderr: '',
            durationMs: Date.now() - startedAt,
          };
        }
      }
      if (turnId) {
        await codexDesktopTaskController.interrupt({ threadId: sessionId, turnId }).catch(() => {});
      }
      throw new Error(`codex desktop turn timed out after ${timeoutMs}ms`);
    } finally {
      const pending = taskLinkInputRequests.get(sessionId);
      if (pending?.owner === 'desktop' && (!turnId || pending.turnId === turnId)) {
        taskLinkInputRequests.delete(sessionId);
      }
      if (logStream) logStream.end();
    }
  }

  async runTurn({
    prompt, input, sessionId, logPath, timeoutMs, cwd = kmsRoot, collaborationMode = null,
    onProgress, onInputRequest, onTurnStarted, taskLink = false,
  }) {
    if (taskLink && sessionId) {
      return this.runDesktopTaskLinkTurn({
        input: input || [{ type: 'text', text: prompt }],
        sessionId,
        logPath,
        timeoutMs,
        cwd,
        collaborationMode,
        onProgress,
        onInputRequest,
        onTurnStarted,
      });
    }
    await this.ensureStarted();
    const thread = await this.startOrResumeThread(sessionId, cwd, { taskLink });
    if (!thread?.id) throw new Error('codex app-server did not return a thread id');

    if (logPath && fs.existsSync(logPath)) chmodPrivate(logPath, 0o600);
    const logStream = logPath ? fs.createWriteStream(logPath, { flags: 'a', mode: 0o600 }) : null;
    let turnStarted;
    try {
      turnStarted = await this.request('turn/start', {
        threadId: thread.id,
        input: input || [{ type: 'text', text: prompt }],
        cwd,
        approvalPolicy: 'never',
        sandboxPolicy: taskLink ? { type: 'dangerFullAccess' } : codexSandboxPolicy(),
        summary: 'auto',
      }, codexAppServerRequestTimeoutMs);
    } catch (error) {
      if (logStream) logStream.end();
      throw error;
    }
    const turnId = turnStarted?.turn?.id || `${thread.id}:${Date.now()}`;
    const activeTurnId = `${thread.id}:${turnId}`;
    if (onTurnStarted) onTurnStarted({ threadId: thread.id, turnId });

    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.activeTurns.delete(activeTurnId);
        if (logStream) logStream.end();
        this.request('turn/interrupt', { threadId: thread.id, turnId }, 5000).catch(() => {});
        reject(new Error(`codex turn timed out after ${timeoutMs}ms`));
      }, timeoutMs);
      this.activeTurns.set(activeTurnId, {
        id: activeTurnId,
        threadId: thread.id,
        turnId,
        startedAt: Date.now(),
        timer,
        resolve,
        reject,
        logStream,
        rawEvents: [],
        finalText: '',
        deltaText: '',
        lastError: '',
        onProgress,
        onInputRequest,
      });
    });
  }

  answerUserInput(threadId, answers) {
    const pending = taskLinkInputRequests.get(threadId);
    if (!pending) return false;
    taskLinkInputRequests.delete(threadId);
    this.send({ id: pending.requestId, result: { answers } });
    return true;
  }

  async readThreadSnapshot(threadId) {
    const result = await this.request('thread/read', { threadId, includeTurns: true }, 10000);
    const thread = validateAuthoritativeThread(result);
    let turns = Array.isArray(thread.turns) ? thread.turns : [];
    if (!turns.length || !Array.isArray(latestTurn(thread, turns)?.items)) {
      const listed = await this.request('thread/turns/list', {
        threadId,
        limit: 10,
        itemsView: 'full',
      }, 10000).catch(() => null);
      turns = turnsFromResult(listed);
    }
    const turn = latestTurn(thread, turns);
    return { thread, turn, publicState: publicTurnState(thread, turn) };
  }

  async steer(threadId, turnId, input) {
    const result = await this.request('turn/steer', {
      threadId,
      expectedTurnId: turnId,
      input,
    }, 10000);
    return result?.turnId || result?.turn?.id || turnId;
  }

  async interrupt(threadId, turnId) {
    await this.request('turn/interrupt', { threadId, turnId }, 10000);
    return true;
  }
}

const codexAppServer = new CodexAppServer();

async function readTaskLinkSnapshot(threadId) {
  const snapshot = await codexAppServer.readThreadSnapshot(threadId);
  return projectDesktopTaskSnapshot(snapshot, codexDesktopTurnJournal.snapshot(threadId));
}

function latestTasks() {
  const byId = new Map();
  for (const event of readJsonl(taskLogPath)) {
    if (!event.id) continue;
    byId.set(event.id, mergeTaskEvent(byId.get(event.id), event));
  }
  return Array.from(byId.values()).sort((a, b) => String(a.createdAt || a.at || '').localeCompare(String(b.createdAt || b.at || '')));
}

function recentTaskRefs(limit = 10) {
  return latestTasks().slice(-limit).reverse();
}

function taskDisplayText(task) {
  if (!task) return '';
  if (task.instruction) return task.instruction;
  const description = String(task.description || '');
  const matches = [...description.matchAll(/追加要求：\s*\n([\s\S]*?)(?=\n\n(?:原任务描述|追加要求|继续任务：)|$)/g)];
  const last = matches.at(-1)?.[1]?.trim();
  return last || description;
}

function recentMessages(limit = 10) {
  return readJsonl(messageLogPath)
    .filter((item) => item.direction === 'inbound' || item.direction === 'codex_reply')
    .slice(-limit);
}

function defaultOutboxState() {
  return outboxStore.defaultState();
}

function readOutboxState() {
  return outboxStore.readState();
}

function writeOutboxState(state) {
  outboxStore.writeState(state);
}

function updateOutboxState(patch) {
  return outboxStore.updateState(patch);
}

function readCompleteOutboxLines() {
  return outboxStore.readCompleteLines();
}

function appendOutboxResult(result) {
  outboxStore.appendResult(result);
}

function outboxResults() {
  return outboxStore.results();
}

function recentOutboxResults(limit = 10) {
  return outboxStore.recentResults(limit);
}

function extractTrace(text) {
  const match = String(text || '').match(/\[TRACE:([^\]\s]+)\]/);
  return match ? { code: match[1] } : null;
}

function parseKeyValueFields(text) {
  const fields = {};
  for (const line of String(text || '').split('\n')) {
    const match = line.match(/^\s*([^:：\s][^:：]{0,40})\s*[:：]\s*(.+?)\s*$/);
    if (!match) continue;
    fields[match[1].trim()] = match[2].trim();
  }
  return Object.keys(fields).length ? { fields } : null;
}

function validateOutboxRequest(request) {
  return validateMessageRequest(request);
}

function outboxAuditLines(request, result) {
  const target = result.target || request?.target || {};
  return [
    `- outbox_id：${result.id || request?.id || '-'}`,
    `- status：${result.status}`,
    `- source：${request?.source || result.source || '-'}`,
    `- message_type：${request?.type || '-'}`,
    `- target：${auditTargetDescriptor(target)}`,
    `- reason：${redactAuditText(request?.reason || '-')}`,
    `- trace：${safeOneLine(redactAuditText(JSON.stringify(result.trace || request?.trace || {})), 700)}`,
    `- message_ids：${(result.messageIds || []).map(auditFingerprint).join(', ') || '-'}`,
    `- content：${auditRequestContent(request)}`,
    result.error ? `- error：${safeOneLine(redactAuditText(result.error), 700)}` : '',
  ].filter(Boolean);
}

function recordOutboxResult(request, result) {
  const record = {
    ...result,
    id: result.id || request?.id || '',
    target: result.target || request?.target,
    source: result.source || request?.source,
    trace: result.trace || request?.trace,
  };
  appendOutboxResult(record);
  appendMessageLog({
    direction: 'outbound_outbox_result',
    at: new Date().toISOString(),
    outboxId: record.id,
    status: record.status,
    target: auditTargetDescriptor(record.target),
    source: record.source,
    trace: redactAuditText(JSON.stringify(record.trace || {})),
    content: auditRequestContent(request),
    messageIds: (record.messageIds || []).map(auditFingerprint),
    error: redactAuditText(record.error || ''),
  });
  appendAudit('出站消息', outboxAuditLines(request, record));
  cleanupStagedMedia(mediaStagingRuntime, request?.filePath);
  return record;
}

async function processOutboxRequest(request) {
  const now = new Date().toISOString();
  const validationError = validateOutboxRequest(request);
  if (validationError) {
    return recordOutboxResult(request, {
      id: request?.id || '',
      status: 'invalid',
      error: validationError,
      failedAt: now,
    });
  }

  const parts = request.type === 'text' ? splitMessage(request.text) : [request.text || request.filePath];
  if (outboundDryRun || request.dryRun === true) {
    return recordOutboxResult(request, {
      id: request.id,
      status: 'dry_run',
      target: request.target,
      messageIds: [],
      dryRun: true,
      partCount: parts.length,
      sentAt: now,
    });
  }

  const messageIds = [];
  const errors = [];
  for (const [index, part] of parts.entries()) {
    try {
      const uuid = `${request.id}-${index + 1}`;
      const messageId = await sendMessageByTarget(request.target, {
        ...request,
        text: request.type === 'text' ? part : request.text,
      }, uuid);
      if (messageId) messageIds.push(messageId);
    } catch (error) {
      errors.push(error.message || String(error));
    }
  }

  if (errors.length === 0) {
    return recordOutboxResult(request, {
      id: request.id,
      status: 'sent',
      target: request.target,
      messageIds,
      sentAt: new Date().toISOString(),
    });
  }

  return recordOutboxResult(request, {
    id: request.id,
    status: messageIds.length ? 'partial_sent' : 'failed',
    target: request.target,
    messageIds,
    error: errors.join('; '),
    failedAt: new Date().toISOString(),
  });
}

let outboxProcessing = false;
let outboxPendingWake = false;
let outboxPollTimer = null;
let wakeServer = null;
let wakeActualPort = null;

async function processOutboxNow(trigger = 'manual') {
  if (!outboundEnabled) return;
  if (outboxProcessing) {
    outboxPendingWake = true;
    return;
  }
  outboxProcessing = true;
  try {
    do {
      outboxPendingWake = false;
      const state = readOutboxState();
      const lines = readCompleteOutboxLines();
      let processedLineCount = Number(state.processedLineCount || 0);
      const processedIds = { ...(state.processedIds || {}) };
      if (processedLineCount > lines.length) processedLineCount = 0;

      for (let index = processedLineCount; index < lines.length; index += 1) {
        const line = lines[index];
        let request;
        try {
          request = JSON.parse(line);
        } catch {
          const result = recordOutboxResult(null, {
            id: `invalid-line-${index + 1}`,
            status: 'invalid',
            error: 'invalid_json',
            failedAt: new Date().toISOString(),
          });
          processedIds[result.id] = result.status;
          continue;
        }

        if (request?.id && processedIds[request.id]) {
          recordOutboxResult(request, {
            id: request.id,
            status: 'duplicate',
            error: 'duplicate_id',
            target: request.target,
            failedAt: new Date().toISOString(),
          });
          continue;
        }

        const result = await processOutboxRequest(request);
        if (result.id) processedIds[result.id] = result.status;
      }

      updateOutboxState({
        processedLineCount: lines.length,
        processedIds,
        lastProcessedAt: new Date().toISOString(),
        lastError: '',
        lastTrigger: trigger,
        wake: {
          enabled: outboundWakeEnabled,
          host: outboundWakeHost,
          configuredPort: outboundWakePort,
          actualPort: wakeActualPort,
        },
      });
    } while (outboxPendingWake);
  } catch (error) {
    updateOutboxState({
      lastProcessedAt: new Date().toISOString(),
      lastError: error.stack || error.message || String(error),
    });
    appendMessageLog({
      direction: 'outbox_error',
      at: new Date().toISOString(),
      trigger,
      error: error.stack || error.message || String(error),
    });
  } finally {
    outboxProcessing = false;
  }
}

function startOutboxWorker() {
  if (!outboundEnabled) return;
  fs.mkdirSync(path.dirname(outboxPath), { recursive: true });
  updateOutboxState({
    wake: {
      enabled: outboundWakeEnabled,
      host: outboundWakeHost,
      configuredPort: outboundWakePort,
      actualPort: wakeActualPort,
    },
  });
  outboxPollTimer = setInterval(() => {
    processOutboxNow('poll').catch((error) => {
      console.error('Outbox polling failed:', error);
    });
  }, outboxPollMs);
  processOutboxNow('startup').catch((error) => {
    console.error('Outbox startup scan failed:', error);
  });
}

function startWakeServer() {
  if (!outboundEnabled || !outboundWakeEnabled) return;
  if (outboundWakeHost !== '127.0.0.1') {
    throw new Error('FEISHU_OUTBOUND_WAKE_HOST must be 127.0.0.1');
  }
  wakeServer = http.createServer((req, res) => {
    req.resume();
    if (req.method !== 'POST' || req.url !== '/internal/outbox/wake') {
      res.writeHead(404, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ ok: false, error: 'not_found' }));
      return;
    }
    processOutboxNow('wake').catch((error) => {
      console.error('Outbox wake failed:', error);
    });
    res.writeHead(202, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ ok: true, status: 'wake_accepted' }));
  });
  wakeServer.listen(outboundWakePort, outboundWakeHost, () => {
    wakeActualPort = wakeServer.address().port;
    console.log(`Outbox wake server: http://${outboundWakeHost}:${wakeActualPort}/internal/outbox/wake`);
    updateOutboxState({
      wake: {
        enabled: true,
        host: outboundWakeHost,
        configuredPort: outboundWakePort,
        actualPort: wakeActualPort,
      },
    });
  });
}

function outboxStatusLines() {
  const state = readOutboxState();
  const maintenance = outboxStore.maintenanceStatus();
  return [
    `outbound_enabled: ${outboundEnabled}`,
    `outbox_path: ${outboxPath}`,
    `outbox_results_path: ${outboxResultsPath}`,
    `outbox_poll_ms: ${outboxPollMs}`,
    'outbound_target_policy: any_explicit_id',
    `outbound_dry_run: ${outboundDryRun}`,
    `outbound_wake_enabled: ${outboundWakeEnabled}`,
    `outbound_wake_host: ${outboundWakeHost}`,
    `outbound_wake_port: ${wakeActualPort ?? state.wake?.actualPort ?? '-'}`,
    `outbox_processed_lines: ${state.processedLineCount || 0}`,
    `outbox_processed_ids: ${Object.keys(state.processedIds || {}).length}`,
    `outbox_last_processed_at: ${state.lastProcessedAt || '-'}`,
    `outbox_last_error: ${state.lastError ? safeOneLine(state.lastError, 240) : '-'}`,
    `outbox_queue_bytes: ${maintenance.queueBytes}`,
    `outbox_results_bytes: ${maintenance.resultsBytes}`,
    `outbox_maintenance_warnings: ${maintenance.warnings.join(',') || '-'}`,
  ];
}

function commandRecentOutbox() {
  const items = recentOutboxResults(10);
  if (!items.length) return '暂无出站记录。';
  return items.map((item, index) => [
    `${index + 1}. ${item.id || '-'}`,
    `   status: ${item.status || '-'}`,
    `   target: ${item.target?.type || '-'}:${item.target?.id || '-'}`,
    `   source: ${item.source || '-'}`,
    `   at: ${item.sentAt || item.failedAt || item.at || '-'}`,
    item.error ? `   error: ${safeOneLine(item.error, 180)}` : '',
  ].filter(Boolean).join('\n')).join('\n\n');
}

function commandOutboxDetail(outboxId) {
  const items = outboxResults().filter((item) => item.id === outboxId);
  if (!items.length) return `未找到出站请求：${outboxId}`;
  const item = items.at(-1);
  return [
    `id: ${item.id || '-'}`,
    `status: ${item.status || '-'}`,
    `target: ${item.target?.type || '-'}:${item.target?.id || '-'}`,
    `source: ${item.source || '-'}`,
    `message_ids: ${(item.messageIds || []).join(', ') || '-'}`,
    `trace: ${JSON.stringify(item.trace || {})}`,
    `sent_at: ${item.sentAt || '-'}`,
    `failed_at: ${item.failedAt || '-'}`,
    `error: ${item.error || '-'}`,
  ].join('\n');
}

function defaultDocboxState() {
  return docboxStore.defaultState();
}

function readDocboxState() {
  return docboxStore.readState();
}

function writeDocboxState(state) {
  docboxStore.writeState(state);
}

function updateDocboxState(patch) {
  return docboxStore.updateState(patch);
}

function readCompleteDocboxLines() {
  return docboxStore.readCompleteLines();
}

function appendDocboxResult(result) {
  docboxStore.appendResult(result);
}

function docboxResults() {
  return docboxStore.results();
}

function recentDocboxResults(limit = 10) {
  return docboxStore.recentResults(limit);
}

function validateDocboxRequest(request) {
  if (!request || typeof request !== 'object') return 'request_not_object';
  if (request.dryRun !== undefined && typeof request.dryRun !== 'boolean') return 'invalid_dry_run';
  if (!request.id || typeof request.id !== 'string') return 'missing_id';
  if (request.type !== 'document_task') return 'unsupported_type';
  if (request.action !== 'create_document' && request.action !== 'update_document') return 'unsupported_action';
  if (request.identity !== documentIdentityForTarget(request.target)) return 'unsupported_identity';
  if (request.versionPolicy && request.versionPolicy !== 'official_before_update') return 'unsupported_version_policy';
  if (!request.source || typeof request.source !== 'string') return 'missing_source';
  if (!request.instruction || typeof request.instruction !== 'string') return 'missing_instruction';
  if (!request.content || typeof request.content !== 'object') return 'missing_content';
  if (request.content.format !== 'markdown' && request.content.format !== 'text') return 'unsupported_content_format';
  if (typeof request.content.text !== 'string' || !request.content.text.trim()) return 'missing_content_text';

  if (request.target !== undefined) {
    if (!request.target || typeof request.target !== 'object') return 'invalid_target';
    const allowedKinds = new Set(['url', 'docx_token', 'wiki_url', 'wiki_token', 'folder_token']);
    if (!allowedKinds.has(request.target.kind)) return 'unsupported_target_kind';
    if (!request.target.value || typeof request.target.value !== 'string') return 'missing_target_value';
  }
  if (request.action === 'update_document' && !request.target) return 'missing_target';
  if (request.updateMode !== undefined) {
    const allowedUpdateModes = new Set(['append', 'overwrite', 'str_replace']);
    if (!allowedUpdateModes.has(request.updateMode)) return 'unsupported_update_mode';
  }
  return '';
}

function isDocboxSourceAllowed(source) {
  return docboxAllowedSources.has('*') || docboxAllowedSources.has(String(source || '').trim());
}

function docboxVersionPolicy(request) {
  if (request?.action !== 'update_document') return undefined;
  return request.versionPolicy || 'official_before_update';
}

function defaultDocboxVersion(request, extra = {}) {
  const policy = docboxVersionPolicy(request);
  if (!policy) return undefined;
  return {
    policy,
    created: false,
    ...extra,
  };
}

function parseDocxTokenFromUrl(value) {
  const match = String(value || '').match(/\/docx\/([A-Za-z0-9]+)/);
  return match?.[1] || '';
}

function parseWikiTokenFromUrl(value) {
  const match = String(value || '').match(/\/wiki\/([A-Za-z0-9]+)/);
  return match?.[1] || '';
}

function findFirstKey(value, keys) {
  if (!value || typeof value !== 'object') return undefined;
  for (const key of keys) {
    if (value[key] !== undefined && value[key] !== null && value[key] !== '') return value[key];
  }
  for (const child of Object.values(value)) {
    const found = findFirstKey(child, keys);
    if (found !== undefined && found !== null && found !== '') return found;
  }
  return undefined;
}

function larkDocumentTextLength(fetchResult) {
  const text = findFirstKey(fetchResult, ['markdown', 'content', 'text', 'plain_text']);
  if (typeof text === 'string') return text.length;
  return JSON.stringify(fetchResult || {}).length;
}

function docboxLarkForRequest(request) {
  return request.identity === 'bot' ? directoryLark : userLark;
}

async function resolveWikiDocxToken(documentLark, wikiToken) {
  const resp = await documentLark.larkApi('GET', '/open-apis/wiki/v2/spaces/get_node', {
    params: { token: wikiToken, obj_type: 'wiki' },
  });
  const data = documentLark.larkApiData(resp) || {};
  const node = data.node || data;
  const objType = node.obj_type || node.objType || '';
  const objToken = node.obj_token || node.objToken || '';
  if (objType !== 'docx' || !objToken) {
    throw new Error(`wiki node is not a docx document: ${objType || '-'}`);
  }
  return {
    token: objToken,
    objType,
    url: node.url || '',
    wikiToken,
    title: node.title || '',
  };
}

async function resolveDocboxDocumentTarget(documentLark, target) {
  const kind = target?.kind;
  const value = String(target?.value || '').trim();
  if (!value) throw new Error('missing_target_value');

  if (kind === 'docx_token') {
    return {
      token: value,
      objType: 'docx',
      url: '',
      title: '',
    };
  }

  if (kind === 'wiki_token') {
    return resolveWikiDocxToken(documentLark, value);
  }

  if (kind === 'url' || kind === 'wiki_url') {
    const docxToken = parseDocxTokenFromUrl(value);
    if (docxToken) {
      return {
        token: docxToken,
        objType: 'docx',
        url: value,
        title: '',
      };
    }
    const wikiToken = parseWikiTokenFromUrl(value);
    if (wikiToken) {
      const resolved = await resolveWikiDocxToken(documentLark, wikiToken);
      return {
        ...resolved,
        url: value,
      };
    }
  }

  throw new Error(`unsupported_document_target: ${kind || '-'}`);
}

function docboxUpdateMode(request) {
  return request.updateMode || 'append';
}

function docboxDocFormat(request) {
  return request.content.format === 'markdown' ? 'markdown' : 'xml';
}

function docboxVersionFromResponse(documentLark, request, resp, versionName) {
  const data = documentLark.larkApiData(resp) || {};
  const versionId = data.version_id
    || data.version
    || data.version?.version_id
    || data.version?.id
    || data.id
    || data.versionId
    || '';
  return {
    ...defaultDocboxVersion(request, { required: true }),
    created: Boolean(versionId),
    versionId,
    versionName: data.name || data.version?.name || versionName,
    createdAt: data.created_time || data.version?.created_time || new Date().toISOString(),
  };
}

function docboxAuditLines(request, result) {
  const target = result.target || request?.target || {};
  return [
    `- docbox_id：${result.id || request?.id || '-'}`,
    `- status：${result.status}`,
    `- action：${result.action || request?.action || '-'}`,
    `- identity：${result.identity || request?.identity || '-'}`,
    `- source：${request?.source || result.source || '-'}`,
    `- target：${auditTargetDescriptor(target)}`,
    `- reason：${redactAuditText(request?.reason || '-')}`,
    `- content：${auditRequestContent(request)}`,
    result.document ? `- document：${safeOneLine(redactAuditText(JSON.stringify(result.document)), 700)}` : '',
    result.beforeRevisionId ? `- before_revision_id：${result.beforeRevisionId}` : '',
    result.afterRevisionId ? `- after_revision_id：${result.afterRevisionId}` : '',
    result.version ? `- version：${safeOneLine(redactAuditText(JSON.stringify(result.version)), 700)}` : '',
    `- codex_task_id：${result.codexTaskId || '-'}`,
    `- run_log：${result.runLogPath || '-'}`,
    `- trace：${safeOneLine(redactAuditText(JSON.stringify(result.trace || request?.trace || {})), 700)}`,
    result.error ? `- error：${safeOneLine(redactAuditText(result.error), 700)}` : '',
    result.summary ? `- summary：${safeOneLine(redactAuditText(result.summary), 900)}` : '',
  ].filter(Boolean);
}

function recordDocboxResult(request, result) {
  const record = {
    ...result,
    id: result.id || request?.id || '',
    action: result.action || request?.action,
    identity: result.identity || request?.identity,
    source: result.source || request?.source,
    target: result.target || request?.target,
    document: result.document,
    beforeRevisionId: result.beforeRevisionId,
    afterRevisionId: result.afterRevisionId,
    version: result.version || defaultDocboxVersion(request),
    trace: result.trace || request?.trace,
  };
  appendDocboxResult(record);
  appendMessageLog({
    direction: 'docbox_result',
    at: new Date().toISOString(),
    docboxId: record.id,
    status: record.status,
    action: record.action,
    identity: record.identity,
    source: record.source,
    target: auditTargetDescriptor(record.target),
    document: record.document ? redactAuditText(JSON.stringify(record.document)) : undefined,
    beforeRevisionId: record.beforeRevisionId,
    afterRevisionId: record.afterRevisionId,
    version: record.version,
    codexTaskId: record.codexTaskId,
    threadId: record.threadId,
    runLogPath: record.runLogPath,
    trace: redactAuditText(JSON.stringify(record.trace || {})),
    error: record.error,
    summary: record.summary,
  });
  appendAudit('文档任务', docboxAuditLines(request, record));
  return record;
}

async function processDocboxCreateTask(request) {
  const accepted = recordDocboxResult(request, {
    id: request.id,
    status: 'accepted',
    action: request.action,
    target: request.target,
    contentLength: request.content.text.length,
    contentFormat: request.content.format,
    acceptedAt: new Date().toISOString(),
    summary: '已接受固定 lark-cli 文档创建任务，等待创建与复读完成。',
  });
  const startedAt = Date.now();
  const content = request.content.text;
  const heading = request.content.format === 'markdown'
    ? content.match(/^\s*#\s+([^\n]{1,200})\s*(?:\n|$)/)
    : null;
  const title = String(request.title || heading?.[1] || `${request.id} 飞书文档`).trim().slice(0, 200);
  const body = heading
    ? content.slice(heading[0].length).replace(/^\s*\n/, '')
    : content;
  const documentLark = docboxLarkForRequest(request);
  try {
    const createResp = await documentLark.larkDocCreate({
      content: body,
      docFormat: docboxDocFormat(request),
      title,
      parentToken: request.target?.value,
      timeoutMs: 90 * 1000,
    });
    const data = documentLark.larkApiData(createResp) || {};
    const token = findFirstKey(data.document || data, [
      'document_id', 'document_token', 'obj_token', 'token',
    ]);
    const url = findFirstKey(data.document || data, ['url', 'document_url']);
    if (!token) throw new Error('document_create_response_missing_token');
    const fetched = await documentLark.larkDocFetch(token, { timeoutMs: 60 * 1000 });
    const revisionId = findFirstKey(fetched, ['document_revision_id', 'revision_id', 'revisionId', 'revision']);
    return recordDocboxResult(request, {
      id: request.id,
      status: 'completed',
      action: request.action,
      target: request.target,
      document: { title, url, token },
      afterRevisionId: revisionId,
      contentLength: content.length,
      durationMs: Date.now() - startedAt,
      summary: '已通过固定 lark-cli 创建新版 Docx，并完成复读验证。',
      completedAt: new Date().toISOString(),
      acceptedStatus: accepted.status,
    });
  } catch (error) {
    return recordDocboxResult(request, {
      id: request.id,
      status: 'failed',
      action: request.action,
      target: request.target,
      durationMs: Date.now() - startedAt,
      error: `document_create_failed: ${error.message || String(error)}`,
      summary: '固定 lark-cli 文档创建或复读失败；未自动重试，也未生成新请求 ID。',
      failedAt: new Date().toISOString(),
      acceptedStatus: accepted.status,
    });
  }
}

async function processDocboxUpdateTask(request) {
  const accepted = recordDocboxResult(request, {
    id: request.id,
    status: 'accepted',
    action: request.action,
    target: request.target,
    contentLength: request.content.text.length,
    contentFormat: request.content.format,
    version: defaultDocboxVersion(request, { required: true }),
    acceptedAt: new Date().toISOString(),
    summary: '已接受文档更新任务，将先创建飞书官方文档版本。',
  });

  const startedAt = Date.now();
  let document;
  let beforeFetch;
  let beforeRevisionId;
  let version;
  const documentLark = docboxLarkForRequest(request);
  try {
    document = await resolveDocboxDocumentTarget(documentLark, request.target);
    beforeFetch = await documentLark.larkDocFetch(document.token, { timeoutMs: 60 * 1000 });
    beforeRevisionId = findFirstKey(beforeFetch, ['document_revision_id', 'revision_id', 'revisionId', 'revision']);
  } catch (error) {
    return recordDocboxResult(request, {
      id: request.id,
      status: 'failed',
      action: request.action,
      target: request.target,
      document,
      version: defaultDocboxVersion(request, { required: true, created: false }),
      beforeRevisionId,
      error: `document_inspect_failed: ${error.message || String(error)}`,
      summary: '目标文档解析或读取失败，未创建版本，未更新正文。',
      failedAt: new Date().toISOString(),
      acceptedStatus: accepted.status,
    });
  }

  const versionName = request.versionName || `${request.id} 更新前版本`;
  try {
    const versionResp = await documentLark.larkDocCreateVersion(document.token, {
      name: versionName,
      objType: 'docx',
      timeoutMs: 60 * 1000,
    });
    version = docboxVersionFromResponse(documentLark, request, versionResp, versionName);
    if (!version.created) {
      throw new Error('empty_version_id');
    }
  } catch (error) {
    return recordDocboxResult(request, {
      id: request.id,
      status: 'failed',
      action: request.action,
      target: request.target,
      document: {
        url: document.url,
        token: document.token,
        title: document.title,
      },
      beforeRevisionId,
      version: defaultDocboxVersion(request, {
        required: true,
        created: false,
        versionName,
      }),
      error: `document_version_create_failed: ${error.message || String(error)}`,
      summary: '飞书官方文档版本创建失败，已停止，未更新正文。',
      failedAt: new Date().toISOString(),
      acceptedStatus: accepted.status,
    });
  }

  try {
    const updateResp = await documentLark.larkDocUpdate(document.token, {
      content: request.content.text,
      docFormat: docboxDocFormat(request),
      command: docboxUpdateMode(request),
      newTitle: request.newTitle,
      selectionByTitle: request.selection?.byTitle,
      selectionWithEllipsis: request.selection?.withEllipsis,
      timeoutMs: 90 * 1000,
    });
    const afterFetch = await documentLark.larkDocFetch(document.token, { timeoutMs: 60 * 1000 });
    const afterRevisionId = findFirstKey(afterFetch, ['document_revision_id', 'revision_id', 'revisionId', 'revision']);
    return recordDocboxResult(request, {
      id: request.id,
      status: 'completed',
      action: request.action,
      target: request.target,
      document: {
        url: document.url,
        token: document.token,
        title: document.title,
        objType: document.objType,
      },
      beforeRevisionId,
      afterRevisionId,
      version,
      updateMode: docboxUpdateMode(request),
      contentLength: request.content.text.length,
      beforeLength: larkDocumentTextLength(beforeFetch),
      afterLength: larkDocumentTextLength(afterFetch),
      updateResultFingerprint: auditFingerprint(JSON.stringify(updateResp || {})),
      durationMs: Date.now() - startedAt,
      summary: `已创建官方版本，并以 ${docboxUpdateMode(request)} 模式更新文档。`,
      completedAt: new Date().toISOString(),
      acceptedStatus: accepted.status,
    });
  } catch (error) {
    return recordDocboxResult(request, {
      id: request.id,
      status: 'failed',
      action: request.action,
      target: request.target,
      document: {
        url: document.url,
        token: document.token,
        title: document.title,
        objType: document.objType,
      },
      beforeRevisionId,
      version,
      updateMode: docboxUpdateMode(request),
      error: `document_update_failed_after_version_created: ${error.message || String(error)}`,
      summary: '官方版本已创建，但正文更新失败。目标文档未确认完成更新。',
      failedAt: new Date().toISOString(),
      acceptedStatus: accepted.status,
    });
  }
}

async function processDocboxTask(request) {
  if (docboxDryRun || request.dryRun === true) {
    return recordDocboxResult(request, {
      id: request.id,
      status: 'dry_run',
      action: request.action,
      target: request.target,
      dryRun: true,
      version: defaultDocboxVersion(request, { dryRun: true }),
      contentLength: request.content.text.length,
      contentFormat: request.content.format,
      summary: request.dryRun === true
        ? '请求级 dry-run，未执行文档写入。'
        : 'FEISHU_DOCBOX_DRY_RUN=true，未执行文档写入。',
      completedAt: new Date().toISOString(),
    });
  }

  if (request.action === 'create_document') return processDocboxCreateTask(request);
  return processDocboxUpdateTask(request);
}

async function processDocboxRequest(request) {
  const now = new Date().toISOString();
  const validationError = validateDocboxRequest(request);
  if (validationError) {
    return recordDocboxResult(request, {
      id: request?.id || '',
      status: 'invalid',
      error: validationError,
      failedAt: now,
    });
  }

  if (!isDocboxSourceAllowed(request.source)) {
    return recordDocboxResult(request, {
      id: request.id,
      status: 'denied',
      error: 'source_not_allowed',
      failedAt: now,
    });
  }

  try {
    return await processDocboxTask(request);
  } catch (error) {
    return recordDocboxResult(request, {
      id: request.id,
      status: 'failed',
      action: request.action,
      target: request.target,
      error: error.stack || error.message || String(error),
      failedAt: new Date().toISOString(),
    });
  }
}

let docboxProcessing = false;
let docboxPendingWake = false;
let docboxPollTimer = null;
let docboxWakeServer = null;
let docboxWakeActualPort = null;

async function processDocboxNow(trigger = 'manual') {
  if (!docboxEnabled) return;
  if (docboxProcessing) {
    docboxPendingWake = true;
    return;
  }
  docboxProcessing = true;
  try {
    do {
      docboxPendingWake = false;
      const state = readDocboxState();
      const lines = readCompleteDocboxLines();
      let processedLineCount = Number(state.processedLineCount || 0);
      const processedIds = { ...(state.processedIds || {}) };
      if (processedLineCount > lines.length) processedLineCount = 0;

      for (let index = processedLineCount; index < lines.length; index += 1) {
        const line = lines[index];
        let request;
        try {
          request = JSON.parse(line);
        } catch {
          const result = recordDocboxResult(null, {
            id: `invalid-line-${index + 1}`,
            status: 'invalid',
            error: 'invalid_json',
            failedAt: new Date().toISOString(),
          });
          processedIds[result.id] = result.status;
          continue;
        }

        if (request?.id && processedIds[request.id]) {
          recordDocboxResult(request, {
            id: request.id,
            status: 'duplicate',
            error: 'duplicate_id',
            failedAt: new Date().toISOString(),
          });
          continue;
        }

        const result = await processDocboxRequest(request);
        if (result.id) processedIds[result.id] = result.status;
      }

      updateDocboxState({
        processedLineCount: lines.length,
        processedIds,
        lastProcessedAt: new Date().toISOString(),
        lastError: '',
        lastTrigger: trigger,
        wake: {
          enabled: docboxWakeEnabled,
          host: docboxWakeHost,
          configuredPort: docboxWakePort,
          actualPort: docboxWakeActualPort,
        },
      });
    } while (docboxPendingWake);
  } catch (error) {
    updateDocboxState({
      lastProcessedAt: new Date().toISOString(),
      lastError: error.stack || error.message || String(error),
    });
    appendMessageLog({
      direction: 'docbox_error',
      at: new Date().toISOString(),
      trigger,
      error: error.stack || error.message || String(error),
    });
  } finally {
    docboxProcessing = false;
  }
}

function startDocboxWorker() {
  if (!docboxEnabled) return;
  fs.mkdirSync(path.dirname(docboxPath), { recursive: true });
  updateDocboxState({
    wake: {
      enabled: docboxWakeEnabled,
      host: docboxWakeHost,
      configuredPort: docboxWakePort,
      actualPort: docboxWakeActualPort,
    },
  });
  docboxPollTimer = setInterval(() => {
    processDocboxNow('poll').catch((error) => {
      console.error('Docbox polling failed:', error);
    });
  }, docboxPollMs);
  processDocboxNow('startup').catch((error) => {
    console.error('Docbox startup scan failed:', error);
  });
}

function startDocboxWakeServer() {
  if (!docboxEnabled || !docboxWakeEnabled) return;
  if (docboxWakeHost !== '127.0.0.1') {
    throw new Error('FEISHU_DOCBOX_WAKE_HOST must be 127.0.0.1');
  }
  docboxWakeServer = http.createServer((req, res) => {
    req.resume();
    if (req.method !== 'POST' || req.url !== '/internal/docbox/wake') {
      res.writeHead(404, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ ok: false, error: 'not_found' }));
      return;
    }
    processDocboxNow('wake').catch((error) => {
      console.error('Docbox wake failed:', error);
    });
    res.writeHead(202, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ ok: true, status: 'wake_accepted' }));
  });
  docboxWakeServer.listen(docboxWakePort, docboxWakeHost, () => {
    docboxWakeActualPort = docboxWakeServer.address().port;
    console.log(`Docbox wake server: http://${docboxWakeHost}:${docboxWakeActualPort}/internal/docbox/wake`);
    updateDocboxState({
      wake: {
        enabled: true,
        host: docboxWakeHost,
        configuredPort: docboxWakePort,
        actualPort: docboxWakeActualPort,
      },
    });
  });
}

function actionboxAuditLines(request, result) {
  const content = request?.domain === 'drive' && request?.action === 'add_comment'
    ? auditRequestContent({ text: request?.input?.comment || '' })
    : auditStructuredDescriptor(request?.input || {});
  return [
    `- actionbox_id：${result.id || request?.id || '-'}`,
    `- status：${result.status || '-'}`,
    `- action：${request?.domain || '-'}\.${request?.action || '-'}`,
    request?.capabilityId ? `- capability：${request.capabilityId}` : '',
    `- identity：${request?.identity || '-'}`,
    `- source：${request?.source || '-'}`,
    `- target：${auditTargetDescriptor(request?.target || {})}`,
    `- content：${content}`,
    `- block：${auditFingerprint(request?.input?.blockId) || '-'}`,
    `- result：${result.resultFingerprint || '-'}`,
    result.error ? `- error：${safeOneLine(redactAuditText(result.error), 700)}` : '',
  ].filter(Boolean);
}

function recordActionboxResult(request, result) {
  const content = request?.domain === 'drive' && request?.action === 'add_comment'
    ? auditRequestContent({ text: request?.input?.comment || '' })
    : auditStructuredDescriptor(request?.input || {});
  const record = {
    ...result,
    id: result.id || request?.id || '',
    domain: request?.domain,
    action: request?.action,
    capabilityId: request?.capabilityId,
    identity: request?.identity,
    source: request?.source,
    target: request?.target,
    trace: request?.trace,
  };
  actionboxStore.appendResult(record);
  appendMessageLog({
    direction: 'actionbox_result',
    at: new Date().toISOString(),
    actionboxId: record.id,
    status: record.status,
    action: `${record.domain || '-'}\.${record.action || '-'}`,
    capabilityId: record.capabilityId,
    identity: record.identity,
    source: record.source,
    target: auditTargetDescriptor(record.target || {}),
    content,
    resultFingerprint: record.resultFingerprint,
    error: redactAuditText(record.error || ''),
  });
  appendAudit('飞书动作', actionboxAuditLines(request, record));
  return record;
}

function isActionboxSourceAllowed(source) {
  return actionboxAllowedSources.has('*') || actionboxAllowedSources.has(String(source || '').trim());
}

async function processActionboxRequest(request) {
  const validationError = validateActionRequest(request);
  if (validationError) {
    return recordActionboxResult(request, {
      status: 'invalid',
      error: validationError,
      failedAt: new Date().toISOString(),
    });
  }
  if (!isActionboxSourceAllowed(request.source)) {
    return recordActionboxResult(request, {
      status: 'denied',
      error: 'source_not_allowed',
      failedAt: new Date().toISOString(),
    });
  }
  if (actionboxDryRun || request.dryRun === true) {
    return recordActionboxResult(request, {
      status: 'dry_run',
      dryRun: true,
      completedAt: new Date().toISOString(),
    });
  }
  try {
    const actionLark = request.identity === 'bot' ? lark : userLark;
    const execution = await executeActionRequest(actionLark, request, { timeoutMs: actionboxTimeoutMs });
    const summary = actionExecutionSummary(execution);
    let savedAsset;
    let assetSaveError = '';
    if (request.type === 'feishu_capability' && request.saveAs) {
      const definition = capability(request.capabilityId);
      const match = selectCapabilityResultIdentifier(definition, {
        input: request.input,
        response: execution?.response,
        remoteResponse: execution?.remote?.response,
      });
      if (!match) {
        assetSaveError = 'registered_result_identifier_not_found';
      } else {
        try {
          savedAsset = await saveTestAssetBinding(
            process.env.FEISHU_CLIENT_CONFIG_PATH || defaultClientConfigPath(),
            {
              alias: request.saveAs,
              kind: match.kind,
              value: match.value,
              capabilityId: request.capabilityId,
            },
          );
        } catch (error) {
          assetSaveError = safeOneLine(redactAuditText(error.message || String(error)), 300);
        }
      }
    }
    return recordActionboxResult(request, {
      status: 'completed',
      resultFingerprint: auditFingerprint(summary.identifier || JSON.stringify(execution.response || {})),
      savedAsset,
      assetSaveError: assetSaveError || undefined,
      preflightPerformed: summary.preflightPerformed,
      verificationPerformed: summary.verificationPerformed,
      revisionBefore: summary.revisionBefore,
      revisionAfter: summary.revisionAfter,
      completedAt: new Date().toISOString(),
    });
  } catch (error) {
    return recordActionboxResult(request, {
      status: 'failed',
      error: error.stack || error.message || String(error),
      failedAt: new Date().toISOString(),
    });
  }
}

let actionboxProcessing = false;
let actionboxPendingWake = false;
let actionboxPollTimer = null;
let actionboxWakeServer = null;
let actionboxWakeActualPort = null;

async function processActionboxNow(trigger = 'manual') {
  if (!actionboxEnabled) return;
  if (actionboxProcessing) {
    actionboxPendingWake = true;
    return;
  }
  actionboxProcessing = true;
  try {
    do {
      actionboxPendingWake = false;
      const state = actionboxStore.readState();
      const lines = actionboxStore.readCompleteLines();
      let processedLineCount = Number(state.processedLineCount || 0);
      const processedIds = { ...(state.processedIds || {}) };
      if (processedLineCount > lines.length) processedLineCount = 0;
      for (let index = processedLineCount; index < lines.length; index += 1) {
        let request;
        try {
          request = JSON.parse(lines[index]);
        } catch {
          const invalid = recordActionboxResult(null, {
            id: `invalid-line-${index + 1}`,
            status: 'invalid',
            error: 'invalid_json',
            failedAt: new Date().toISOString(),
          });
          processedIds[invalid.id] = invalid.status;
          continue;
        }
        if (request?.id && processedIds[request.id]) {
          recordActionboxResult(request, {
            id: request.id,
            status: 'duplicate',
            error: 'duplicate_id',
            failedAt: new Date().toISOString(),
          });
          continue;
        }
        const result = await processActionboxRequest(request);
        if (result.id) processedIds[result.id] = result.status;
      }
      actionboxStore.updateState({
        processedLineCount: lines.length,
        processedIds,
        lastProcessedAt: new Date().toISOString(),
        lastError: '',
        lastTrigger: trigger,
        wake: {
          enabled: actionboxWakeEnabled,
          host: actionboxWakeHost,
          configuredPort: actionboxWakePort,
          actualPort: actionboxWakeActualPort,
        },
      });
    } while (actionboxPendingWake);
  } catch (error) {
    actionboxStore.updateState({
      lastProcessedAt: new Date().toISOString(),
      lastError: error.stack || error.message || String(error),
    });
    appendMessageLog({
      direction: 'actionbox_error',
      at: new Date().toISOString(),
      trigger,
      error: redactAuditText(error.stack || error.message || String(error)),
    });
  } finally {
    actionboxProcessing = false;
  }
}

function startActionboxWorker() {
  if (!actionboxEnabled) return;
  fs.mkdirSync(path.dirname(actionboxPath), { recursive: true });
  actionboxStore.updateState({
    wake: {
      enabled: actionboxWakeEnabled,
      host: actionboxWakeHost,
      configuredPort: actionboxWakePort,
      actualPort: actionboxWakeActualPort,
    },
  });
  actionboxPollTimer = setInterval(() => {
    processActionboxNow('poll').catch((error) => console.error('Actionbox polling failed:', error));
  }, actionboxPollMs);
  processActionboxNow('startup').catch((error) => console.error('Actionbox startup scan failed:', error));
}

function startActionboxWakeServer() {
  if (!actionboxEnabled || !actionboxWakeEnabled) return;
  if (actionboxWakeHost !== '127.0.0.1') {
    throw new Error('FEISHU_ACTIONBOX_WAKE_HOST must be 127.0.0.1');
  }
  actionboxWakeServer = http.createServer((req, res) => {
    req.resume();
    if (req.method !== 'POST' || req.url !== '/internal/actionbox/wake') {
      res.writeHead(404, { 'content-type': 'application/json' });
      res.end(JSON.stringify({ ok: false, error: 'not_found' }));
      return;
    }
    processActionboxNow('wake').catch((error) => console.error('Actionbox wake failed:', error));
    res.writeHead(202, { 'content-type': 'application/json' });
    res.end(JSON.stringify({ ok: true, status: 'wake_accepted' }));
  });
  actionboxWakeServer.listen(actionboxWakePort, actionboxWakeHost, () => {
    actionboxWakeActualPort = actionboxWakeServer.address().port;
    console.log(`Actionbox wake server: http://${actionboxWakeHost}:${actionboxWakeActualPort}/internal/actionbox/wake`);
    actionboxStore.updateState({
      wake: {
        enabled: true,
        host: actionboxWakeHost,
        configuredPort: actionboxWakePort,
        actualPort: actionboxWakeActualPort,
      },
    });
  });
}

function docboxStatusLines() {
  const state = readDocboxState();
  const maintenance = docboxStore.maintenanceStatus();
  return [
    `docbox_enabled: ${docboxEnabled}`,
    `docbox_path: ${docboxPath}`,
    `docbox_results_path: ${docboxResultsPath}`,
    `docbox_poll_ms: ${docboxPollMs}`,
    `docbox_allowed_sources: ${docboxAllowedSources.size}`,
    `docbox_dry_run: ${docboxDryRun}`,
    `docbox_wake_enabled: ${docboxWakeEnabled}`,
    `docbox_wake_host: ${docboxWakeHost}`,
    `docbox_wake_port: ${docboxWakeActualPort ?? state.wake?.actualPort ?? '-'}`,
    `docbox_processed_lines: ${state.processedLineCount || 0}`,
    `docbox_processed_ids: ${Object.keys(state.processedIds || {}).length}`,
    `docbox_last_processed_at: ${state.lastProcessedAt || '-'}`,
    `docbox_last_error: ${state.lastError ? safeOneLine(state.lastError, 240) : '-'}`,
    `docbox_queue_bytes: ${maintenance.queueBytes}`,
    `docbox_results_bytes: ${maintenance.resultsBytes}`,
    `docbox_maintenance_warnings: ${maintenance.warnings.join(',') || '-'}`,
  ];
}

function commandRecentDocbox() {
  const items = recentDocboxResults(10);
  if (!items.length) return '暂无文档任务记录。';
  return items.map((item, index) => [
    `${index + 1}. ${item.id || '-'}`,
    `   status: ${item.status || '-'}`,
    `   action: ${item.action || '-'}`,
    `   target: ${item.target?.kind || '-'}:${safeOneLine(item.target?.value || '-', 120)}`,
    `   source: ${item.source || '-'}`,
    `   at: ${item.completedAt || item.acceptedAt || item.failedAt || item.at || '-'}`,
    item.error ? `   error: ${safeOneLine(item.error, 180)}` : '',
  ].filter(Boolean).join('\n')).join('\n\n');
}

function commandDocboxDetail(docboxId) {
  const items = docboxResults().filter((item) => item.id === docboxId);
  if (!items.length) return `未找到文档任务：${docboxId}`;
  return items.map((item) => [
    `id: ${item.id || '-'}`,
    `status: ${item.status || '-'}`,
    `action: ${item.action || '-'}`,
    `target: ${item.target?.kind || '-'}:${item.target?.value || '-'}`,
    `document: ${item.document ? JSON.stringify(item.document) : '-'}`,
    `before_revision_id: ${item.beforeRevisionId || '-'}`,
    `after_revision_id: ${item.afterRevisionId || '-'}`,
    `source: ${item.source || '-'}`,
    `version: ${JSON.stringify(item.version || {})}`,
    `codex_task_id: ${item.codexTaskId || '-'}`,
    `thread_id: ${item.threadId || '-'}`,
    `run_log: ${item.runLogPath || '-'}`,
    `trace: ${JSON.stringify(item.trace || {})}`,
    `summary: ${safeOneLine(item.summary || '-', 1600)}`,
    `error: ${item.error || '-'}`,
  ].join('\n')).join('\n\n---\n\n');
}

async function statusText() {
  const defaultConversations = readDefaultConversationStore().conversations;
  const activeDefaultConversations = defaultConversations.filter((item) => item.state === 'active');
  const accountStatus = await withTimeout(codexAppServer.accountStatus(), 3000, 'codex account status');
  const directory = directoryService.status();
  const groupDirectory = groupDirectoryService.status();
  return [
    'bridge: online',
    `bridge_version: ${RUNTIME_MANIFEST.bridgeVersion}`,
    `capability_version: ${RUNTIME_MANIFEST.capabilityVersion}`,
    `stability_baseline_version: ${RUNTIME_MANIFEST.stabilityBaselineVersion}`,
    `queue_state_schema_version: ${RUNTIME_MANIFEST.queueStateSchemaVersion}`,
    `lark_cli_bin: ${larkCliBin}`,
    `lark_cli_profile: ${larkCliProfile || 'default'}`,
    `lark_cli_as: ${larkCliAs}`,
    `event_consumer_enabled: ${eventConsumerEnabled}`,
    `event_transport: ${eventTransport}`,
    `event_consumer_running: ${eventConsumerRunningCount()}/${eventConsumerKeys.length}`,
    `event_consumer_keys: ${eventConsumerKeys.join(',') || '-'}`,
    `codex: ${getCodexVersion()}`,
    `codex_bin: ${codexBin}`,
    `codex_transport_preference: ${codexTransportPreference}`,
    `codex_transport_active: ${codexAppServer.transport}`,
    `codex_auto_start_daemon: ${codexAutoStartDaemon}`,
    `codex_client: ${codexClientName}`,
    `codex_account: ${accountStatus}`,
    `codex_sandbox: ${codexSandboxMode()}`,
    `kms_root: ${kmsRoot}`,
    'default_conversation_mode: one_thread_per_root_message',
    `default_conversation_count: ${activeDefaultConversations.length}`,
    `legacy_default_session_key: ${defaultSessionName}`,
    `default_thread_title: ${defaultThreadTitle}`,
    `direct_allowed_open_ids: ${directAllowedOpenIds.size}`,
    `group_enabled: ${groupEnabled}`,
    `group_allowed_chat_ids: ${groupAllowedChatIds.size || 'all'}`,
    `group_allowed_open_ids: ${groupAllowedOpenIds.size || 'all'}`,
    `directory_enabled: ${directory.enabled}`,
    `directory_cached_users: ${directory.userCount}`,
    `directory_generated_at: ${directory.generatedAt || '-'}`,
    `directory_stale: ${directory.stale}`,
    `directory_last_error: ${directory.hasLastError ? safeOneLine(directory.lastError, 240) : '-'}`,
    `group_directory_enabled: ${groupDirectory.enabled}`,
    `group_directory_cached_groups: ${groupDirectory.groupCount}`,
    `group_directory_generated_at: ${groupDirectory.generatedAt || '-'}`,
    `group_directory_stale: ${groupDirectory.stale}`,
    `group_directory_last_error: ${groupDirectory.hasLastError ? safeOneLine(groupDirectory.lastError, 240) : '-'}`,
    `log_path: ${messageLogPath}`,
    `task_log_path: ${taskLogPath}`,
    `audit_dir: ${auditDir}`,
    `message_records: ${countLogLines(messageLogPath)}`,
    `task_events: ${countLogLines(taskLogPath)}`,
    `task_count: ${latestTasks().length}`,
    `task_message_refs: ${taskMessageIndex.size}`,
    ...outboxStatusLines(),
    ...docboxStatusLines(),
    `uptime_sec: ${Math.floor(process.uptime())}`,
  ].join('\n');
}

function helpText(context = {}) {
  if (context.authz?.kind === 'group') {
    return [
      `Codex bridge ${RUNTIME_MANIFEST.capabilityVersion} - 群聊模式`,
      '',
      '群聊路由规则：',
      '- @机器人 help：显示本菜单',
      '- @机器人 cmd <命令>：走本地机器人命令',
      '- @机器人 task <任务描述>：暂不响应',
      '- @机器人 <其他内容>：暂不响应',
      '',
      '群聊基础命令：',
      '- @机器人 cmd ping',
      '- @机器人 cmd id',
      '- @机器人 cmd whoami',
      '- @机器人 cmd 群信息',
      '- @机器人 cmd 状态',
      '',
      '回复说明：',
      '- 群聊暂不支持回复任务消息续跑',
      '',
      '群聊权限：',
      `- group_enabled: ${groupEnabled}`,
      `- group_allowed_chat_ids: ${groupAllowedChatIds.size || 'all'}`,
      `- group_allowed_open_ids: ${groupAllowedOpenIds.size || 'all'}`,
    ].join('\n');
  }

  return [
    `Codex bridge ${RUNTIME_MANIFEST.capabilityVersion} - 单聊模式`,
    '',
    '单聊路由规则：',
    '- help：显示本菜单',
    '- cmd <命令>：走本地机器人命令',
    '- task <任务描述>：创建独立 Codex 后台任务',
    '- 我需要继续完成<项目名>：把当前卡片对应的对话升级为既有 KSF 项目任务',
    '- 其他内容：每条根消息创建独立 Codex 对话',
    '',
    'cmd 可用命令：',
    '- cmd ping',
    '- cmd help',
    '- cmd id',
    '- cmd whoami',
    '- cmd 群信息',
    '- cmd 成员列表',
    '- cmd 状态',
    '- cmd 最近消息',
    '- cmd 最近任务',
    '- cmd 最近出站',
    '- cmd 出站 <OUT-id>',
    '- cmd 最近文档',
    '- cmd 文档 <DOC-id>',
    '- cmd 任务 <task_id>',
    '- task任务 / task 任务',
    '- task继续1 <追加要求>',
    '- cmd 审计 今天',
    '- cmd echo <文本>',
    '',
    '回复说明：',
    '- 卡片内追问或引用回复卡片，只继续该卡片对应的 Codex 对话',
    '- 回复某条任务相关的机器人消息，会自动在该任务上下文里继续执行',
    '- 等价于 task继续 <该任务> <你的回复内容>',
    '- 项目升级会精确匹配既有项目并验真归属；不会自动创建新项目',
    '',
    '权限策略：',
    '- 单聊：只响应授权用户',
    '- 群聊：默认关闭；开启后响应被 @ 的群消息',
    '',
    '群聊里请 @ 机器人后发送命令。',
  ].join('\n');
}

function buildCodexPrompt(prompt, context, mode) {
  return inboundAssetsPrompt(prompt, context?.inbound);
}

async function runCodex({ prompt, sessionId, logPath, timeoutMs, cwd, onTurnStarted }) {
  const turnRunner = new CodexAppServer();
  try {
    return await turnRunner.runTurn({
      prompt,
      sessionId,
      logPath,
      timeoutMs,
      cwd,
      onTurnStarted,
    });
  } finally {
    await turnRunner.close();
  }
}

function defaultConversationMessageIds(context, extraIds = []) {
  return [...new Set([
    context?.message?.message_id,
    context?.message?.root_id,
    context?.message?.parent_id,
    context?.progressMessageId,
    ...extraIds,
  ].map((value) => String(value || '').trim()).filter(Boolean))];
}

function defaultConversationLockKey(conversation, context) {
  return conversation?.rootMessageId
    || context?.progressMessageId
    || context?.message?.message_id
    || `chat:${context?.message?.chat_id || 'unknown'}`;
}

function withDefaultConversationLock(key, operation) {
  const previous = defaultConversationChains.get(key) || Promise.resolve();
  const pending = previous.then(operation, operation);
  const settled = pending.then(() => undefined, () => undefined);
  defaultConversationChains.set(key, settled);
  settled.then(
    () => {
      if (defaultConversationChains.get(key) === settled) defaultConversationChains.delete(key);
    },
    () => {},
  );
  return pending;
}

function bindDefaultConversation(context, {
  threadId,
  turnId = '',
  cwd = '',
  title = defaultThreadTitle,
  messageIds = [],
} = {}) {
  const current = context.defaultConversation || null;
  if (current?.threadId && current.threadId !== threadId) {
    throw new Error('飞书卡片绑定的 Codex 对话发生变化，已停止续接。');
  }
  const rootMessageId = current?.rootMessageId
    || context.progressMessageId
    || context.message?.message_id
    || '';
  const conversation = upsertDefaultConversation({
    threadId,
    cwd: current?.cwd || cwd || kmsRoot,
    title: current?.title || title,
    chatId: current?.chatId || context.message?.chat_id,
    operatorId: current?.operatorId || senderOpenId(context.sender),
    rootMessageId,
    messageIds: defaultConversationMessageIds(context, [
      ...(current?.messageIds || []),
      ...messageIds,
      rootMessageId,
    ]),
    lastTurnId: turnId || current?.lastTurnId || '',
  });
  context.defaultConversation = conversation;
  context.progressMessageId = conversation.rootMessageId;
  return conversation;
}

function extendDefaultConversation(context, patch = {}) {
  if (!context.defaultConversation?.id) return null;
  const conversation = updateDefaultConversation(context.defaultConversation.id, {
    ...patch,
    messageIds: defaultConversationMessageIds(context, [
      ...(context.defaultConversation.messageIds || []),
      ...(patch.messageIds || []),
    ]),
  });
  context.defaultConversation = conversation;
  return conversation;
}

async function answerWithDefaultCodexUnlocked(prompt, context, {
  auditInput = prompt,
  route = 'default_chat',
  conversation = context.defaultConversation || null,
} = {}) {
  if (conversation && conversation.state !== 'active') {
    throw new Error('该飞书对话已经升级或关闭，不能继续作为默认对话使用。');
  }
  if (conversation && !defaultConversationOperatorMatches(conversation, {
    chatId: context.message?.chat_id,
    operatorId: senderOpenId(context.sender),
  })) {
    throw new Error('该飞书对话与当前操作者不匹配。');
  }
  const startedAt = new Date();
  const codexPrompt = buildCodexPrompt(prompt, context, 'default_chat');
  const shouldNameThread = !conversation?.threadId;
  const result = await runCodex({
    prompt: codexPrompt,
    sessionId: conversation?.threadId,
    cwd: conversation?.cwd || kmsRoot,
    timeoutMs: codexTimeoutMs,
    onTurnStarted: ({ threadId, turnId }) => {
      bindDefaultConversation(context, {
        threadId,
        turnId,
        cwd: conversation?.cwd || kmsRoot,
      });
    },
  });

  if (result.threadId) {
    if (shouldNameThread) {
      await codexAppServer.setThreadName(result.threadId, defaultThreadTitle);
    }
    bindDefaultConversation(context, {
      threadId: result.threadId,
      turnId: result.turnId,
      cwd: conversation?.cwd || kmsRoot,
    });
  }

  appendMessageLog({
    direction: 'codex_reply',
    at: new Date().toISOString(),
    chatId: context.message.chat_id,
    replyTo: context.message.message_id,
    route,
    threadId: result.threadId,
    durationMs: result.durationMs,
    text: result.finalText,
  });
  appendAudit(route === 'project_promotion' ? '默认对话升级项目' : '普通消息', [
    `- 来源：${context.authz?.kind || '-'} ${context.message.chat_id}`,
    `- 输入：${safeOneLine(auditInput, 500)}`,
    `- Codex Thread：${result.threadId || '-'}`,
    `- 状态：已回复，用时 ${Math.round(result.durationMs / 1000)} 秒`,
    `- 回复摘要：${safeOneLine(result.finalText, 500)}`,
  ], startedAt);

  context.lastCodexDurationMs = result.durationMs;
  context.lastCodexThreadId = result.threadId || '';
  context.lastCodexTurnId = result.turnId || '';
  return result.finalText || 'Codex 没有返回文本。';
}

function answerWithDefaultCodex(prompt, context, options = {}) {
  const conversation = options.conversation || context.defaultConversation || null;
  const key = defaultConversationLockKey(conversation, context);
  return withDefaultConversationLock(
    key,
    () => answerWithDefaultCodexUnlocked(prompt, context, { ...options, conversation }),
  );
}

function taskLinkTargetAlias(openId) {
  try {
    const config = loadClientConfig(defaultClientConfigPath());
    const match = Object.entries(config.messageTargets || {}).find(([, target]) => (
      target?.type === 'open_id' && target?.id === openId
    ));
    if (match?.[0]) return match[0];
  } catch (error) {
    appendMessageLog({
      direction: 'project_promotion_target_alias_failed',
      at: new Date().toISOString(),
      error: safeOneLine(error.message || String(error), 300),
    });
  }
  return '当前授权单聊';
}

function retireLegacyDefaultSession() {
  const sessions = readSessions();
  const legacy = sessions[defaultSessionName];
  if (!legacy) return false;
  delete sessions[defaultSessionName];
  writeSessions(sessions);
  appendMessageLog({
    direction: 'legacy_default_session_retired',
    at: new Date().toISOString(),
    sessionName: defaultSessionName,
    oldThreadId: legacy.threadId || '',
    reason: 'default_conversations_are_bound_per_root_message',
  });
  return true;
}

function projectPromotionInput(route) {
  return String(route?.payload?.originalText || route?.payload?.projectQuery || '').trim();
}

async function promoteVerifiedDefaultConversation({
  context,
  project,
  result,
  originalInput,
  source,
}) {
  const threadId = String(context.lastCodexThreadId || '').trim();
  if (!threadId) throw new Error('Codex 没有返回可绑定的任务标识。');
  const snapshot = await readTaskLinkSnapshot(threadId);
  const openId = senderOpenId(context.sender);
  if (!openId) throw new Error('当前飞书用户缺少可验证的 open_id。');
  const title = promotedTaskTitle(project.name);

  const activeExisting = readTaskLinkStore().links.find((item) => (
    item.threadId === threadId && taskLinkEffectiveState(item) === 'active'
  ));
  if (activeExisting && !taskLinkOperatorMatches(activeExisting, openId)) {
    throw new Error('这个 Codex 任务已经连接给其他飞书用户，无法自动升级。');
  }
  if (activeExisting?.projectName && activeExisting.projectName !== project.name) {
    throw new Error('这个 Codex 任务已经绑定其他项目，不能静默切换项目。');
  }
  await codexAppServer.setThreadName(threadId, title);

  const durationSeconds = Number.isFinite(context.lastCodexDurationMs)
    ? Math.max(0, Math.round(context.lastCodexDurationMs / 1000))
    : snapshot.turnDurationSeconds;
  const progress = {
    phase: '完成',
    detail: '项目上下文已加载，可以从飞书继续任务。',
    changedFiles: 0,
    testStatus: '未运行',
    startedAt: snapshot.turnStartedAt || '',
    durationSeconds: Number.isInteger(durationSeconds) ? durationSeconds : undefined,
  };
  let link = activeExisting
    ? updateTaskLink(activeExisting.id, {
        title,
        projectName: project.name,
        targetAlias: taskLinkTargetAlias(openId),
        target: { type: 'open_id', id: openId },
        cwd: snapshot.thread.cwd,
        rootMessageId: context.progressMessageId || activeExisting.rootMessageId,
        messageIds: [...new Set([
          ...(activeExisting.messageIds || []),
          context.progressMessageId || '',
        ].filter(Boolean))],
        turnState: 'completed',
        turnOwner: 'none',
        actionRequired: 'none',
        activeTurnId: '',
        lastDeliveredTurnId: snapshot.turnId || context.lastCodexTurnId || '',
        detailSummary: safeOneLine(result, 900),
        progress,
      }, undefined, { renew: true, terminalAt: new Date().toISOString() })
    : upsertTaskLink({
        threadId,
        cwd: snapshot.thread.cwd,
        title,
        projectName: project.name,
        targetAlias: taskLinkTargetAlias(openId),
        target: { type: 'open_id', id: openId },
        turnState: 'completed',
        turnOwner: 'none',
        actionRequired: 'none',
        progress,
      });

  if (!activeExisting) {
    link = updateTaskLink(link.id, {
      rootMessageId: context.progressMessageId || '',
      messageIds: [context.progressMessageId].filter(Boolean),
      lastDeliveredTurnId: snapshot.turnId || context.lastCodexTurnId || '',
      detailSummary: safeOneLine(result, 900),
    }, undefined, { renew: true, terminalAt: new Date().toISOString() });
  }
  taskLinkLatestInputs.set(link.id, originalInput);
  rememberTaskLinkFinalResult(link, result);

  let cardUpdated = await patchTaskLinkCard(
    link,
    'completed',
    result,
    link.progress,
    originalInput,
  );
  if (!cardUpdated) {
    const card = fittedTaskLinkCard(link, 'completed', result, {
      latestInput: originalInput,
      openIds: [openId],
    });
    const messageId = await replyCard(
      context.message.message_id,
      card,
      { phase: `project-promoted:${link.taskKey}` },
    );
    link = updateTaskLink(link.id, {
      rootMessageId: messageId,
      messageIds: [...new Set([...(link.messageIds || []), messageId].filter(Boolean))],
    });
    context.progressMessageId = messageId;
    cardUpdated = Boolean(messageId);
  }
  if (!cardUpdated) {
    if (!activeExisting) updateTaskLink(link.id, { linkState: 'released' });
    throw new Error('项目已识别，但飞书任务卡创建失败；默认对话仍保留，可直接重试。');
  }
  if (context.defaultConversation?.id) {
    extendDefaultConversation(context, {
      state: 'promoted',
      messageIds: [link.rootMessageId, ...(link.messageIds || [])],
    });
  }

  refreshTaskLinkWakeAssertion();
  appendMessageLog({
    direction: 'project_promotion_completed',
    at: new Date().toISOString(),
    chatId: context.message.chat_id,
    replyTo: context.message.message_id,
    taskKey: link.taskKey,
    projectName: project.name,
    threadId,
    source,
  });
  return { result, link };
}

async function detectVerifiedProject(threadId) {
  try {
    return await projectPromotionClient.currentProject(threadId);
  } catch (error) {
    appendMessageLog({
      direction: 'project_projection_detection_failed',
      at: new Date().toISOString(),
      threadId,
      error: safeOneLine(error.message || String(error), 500),
    });
    return null;
  }
}

async function promoteDefaultConversationFromProjection(context, result, originalInput) {
  const threadId = String(context.lastCodexThreadId || '').trim();
  if (!threadId || context.defaultConversation?.state !== 'active') return null;
  const resolved = await detectVerifiedProject(threadId);
  if (!resolved?.project) return null;
  try {
    return await promoteVerifiedDefaultConversation({
      context,
      project: resolved.project,
      result,
      originalInput,
      source: 'verified_projection',
    });
  } catch (error) {
    appendMessageLog({
      direction: 'project_projection_promotion_failed',
      at: new Date().toISOString(),
      threadId,
      projectName: resolved.project.name,
      error: safeOneLine(error.message || String(error), 500),
    });
    return null;
  }
}

async function promoteDefaultConversationUnlocked(
  route,
  context,
  conversation = context.defaultConversation || null,
) {
  const originalInput = projectPromotionInput(route);
  const resolved = await projectPromotionClient.resolveIntent(originalInput);
  if (!resolved?.project) throw new Error('无法识别要继续的项目。');

  const result = await answerWithDefaultCodexUnlocked(
    projectContinuationPrompt(resolved.project, originalInput),
    context,
    { auditInput: originalInput, route: 'project_promotion', conversation },
  );
  const threadId = String(context.lastCodexThreadId || '').trim();
  if (!threadId) throw new Error('Codex 没有返回可绑定的任务标识。');
  await projectPromotionClient.verifyBinding(threadId, resolved.project.id);
  await promoteVerifiedDefaultConversation({
    context,
    project: resolved.project,
    result,
    originalInput,
    source: 'explicit_intent',
  });
  return result;
}

function promoteDefaultConversation(route, context, conversation = context.defaultConversation || null) {
  const key = defaultConversationLockKey(conversation, context);
  return withDefaultConversationLock(
    key,
    () => promoteDefaultConversationUnlocked(route, context, conversation),
  );
}

function makeTaskId() {
  const now = new Date();
  const stamp = new Intl.DateTimeFormat('en-CA', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  }).format(now).replace(/[^\d]/g, '');
  return `TASK-${stamp}-${Math.random().toString(36).slice(2, 6).toUpperCase()}`;
}

function createTask(description, context) {
  const task = createTaskRecord(description, context);
  context.pendingTaskReply = {
    taskId: task.id,
    kind: 'createdReplyIds',
  };
  runTaskInBackground(task, context);
  return [
    '任务已创建，开始执行。',
    `任务ID：${task.id}`,
    '状态：running',
    '',
    `描述：${task.description}`,
    '',
    `可用 task任务 查看最近任务。`,
    `可用 cmd 任务 ${task.id} 查询状态。`,
  ].join('\n');
}

function createTaskRecord(description, context, extra = {}) {
  const task = {
    id: makeTaskId(),
    status: 'running',
    description,
    instruction: extra.instruction || description,
    createdAt: new Date().toISOString(),
    source: {
      chatId: context.message.chat_id,
      messageId: context.message.message_id,
      senderId: context.sender?.sender_id || null,
      chatKind: context.authz?.kind || null,
    },
    ...extra,
  };
  appendTaskLog(task);
  appendAudit('任务创建', [
    `- 任务ID：${task.id}`,
    task.parentTaskId ? `- 父任务ID：${task.parentTaskId}` : '',
    `- 来源：${task.source.chatKind || '-'} ${task.source.chatId}`,
    `- 输入：${safeOneLine(taskDisplayText(task), 500)}`,
    '- 状态：running',
  ].filter(Boolean));
  return task;
}

async function runTaskInBackground(task, context) {
  const runLogPath = path.join(taskRunsDir, `${task.id}.log`);
  try {
    fs.writeFileSync(runLogPath, [
      `task_id: ${task.id}`,
      `created_at: ${task.createdAt}`,
      task.parentTaskId ? `parent_task_id: ${task.parentTaskId}` : '',
      task.resumeThreadId ? `resume_thread_id: ${task.resumeThreadId}` : '',
      `description: ${taskDisplayText(task)}`,
      '',
    ].filter(Boolean).join('\n'), { encoding: 'utf8', mode: 0o600 });

    const result = await runCodex({
      prompt: buildCodexPrompt(task.description, context, 'task'),
      sessionId: task.resumeThreadId,
      timeoutMs: codexTaskTimeoutMs,
      logPath: runLogPath,
    });
    if (result.threadId && !task.resumeThreadId) {
      await codexAppServer.setThreadName(result.threadId, safeOneLine(task.description, 80));
    }
    const completed = {
      ...task,
      status: 'completed',
      completedAt: new Date().toISOString(),
      threadId: result.threadId,
      durationMs: result.durationMs,
      runLogPath,
      result: result.finalText,
    };
    appendTaskLog(completed);
    appendAudit('任务完成', [
      `- 任务ID：${task.id}`,
      `- Codex session：${result.threadId || '-'}`,
      `- 状态：completed，用时 ${Math.round(result.durationMs / 1000)} 秒`,
      `- 结果摘要：${safeOneLine(result.finalText, 700)}`,
      `- 日志：${runLogPath}`,
    ]);
    const delivery = await deliverProgressResult(context, {
      status: 'completed',
      taskId: task.id,
      detail: [
        `本次任务耗时：${formatElapsedDuration(Math.round(result.durationMs / 1000))}`,
        result.finalText || '任务已完成。',
        `完整日志：${runLogPath}`,
      ].join('\n\n'),
    }, [
      `任务完成：${task.id}`,
      '',
      result.finalText,
      '',
      `完整日志：${runLogPath}`,
    ].join('\n'), { phase: `task-completed:${task.id}` });
    registerTaskMessageRefs(task.id, 'completedReplyIds', delivery.messageIds);
  } catch (error) {
    const failed = {
      ...task,
      status: 'failed',
      completedAt: new Date().toISOString(),
      runLogPath,
      error: error.stack || error.message || String(error),
    };
    appendTaskLog(failed);
    appendAudit('任务失败', [
      `- 任务ID：${task.id}`,
      '- 状态：failed',
      `- 错误：${safeOneLine(error.message || String(error), 700)}`,
      `- 日志：${runLogPath}`,
    ]);
    const delivery = await deliverProgressResult(context, {
      status: 'failed',
      taskId: task.id,
      detail: [
        error.message || String(error),
        `日志：${runLogPath}`,
      ].join('\n\n'),
    }, [
      `任务失败：${task.id}`,
      '',
      error.message || String(error),
      '',
      `日志：${runLogPath}`,
    ].join('\n'), { phase: `task-failed:${task.id}` });
    registerTaskMessageRefs(task.id, 'failedReplyIds', delivery.messageIds);
  } finally {
    cleanupContextInbound(context);
  }
}

async function commandRecentMessages() {
  const items = recentMessages(10);
  if (!items.length) return '暂无消息记录。';
  return items.map((item) => [
    `${item.receivedAt || item.at || item.sentAt || '-'} [${item.direction}]`,
    `chat_id: ${item.chatId || '-'}`,
    `message_id: ${item.messageId || item.replyTo || '-'}`,
    `text: ${safeOneLine(item.command || item.text, 240)}`,
  ].join('\n')).join('\n\n');
}

function commandRecentTasks() {
  const tasks = recentTaskRefs(10);
  if (!tasks.length) return '暂无任务记录。';
  return tasks.map((task, index) => [
    `#${index + 1} ${task.id} [${task.status}]`,
      task.parentTaskId ? `parent: ${task.parentTaskId}` : '',
      safeOneLine(taskDisplayText(task), 240),
      `created_at: ${task.createdAt || '-'}`,
    `completed_at: ${task.completedAt || '-'}`,
    `thread_id: ${task.threadId || '-'}`,
  ].filter(Boolean).join('\n')).join('\n\n');
}

function resolveTaskRef(ref) {
  const value = String(ref || '').trim();
  const indexMatch = value.match(/^#?(\d+)$/);
  if (indexMatch) {
    return recentTaskRefs(10)[Number(indexMatch[1]) - 1] || null;
  }
  return latestTasks().find((item) => item.id === value) || null;
}

function taskMessageRefFromReply(message) {
  for (const messageId of [message.parent_id, message.root_id]) {
    if (!messageId) continue;
    const taskId = taskMessageIndex.get(messageId);
    if (taskId) return { task: resolveTaskRef(taskId), messageId };
  }
  return null;
}

function registerTaskMessageRefs(taskId, kind, messageIds) {
  const ids = (messageIds || []).filter(Boolean);
  if (!taskId || !ids.length) return;
  appendTaskLog({
    id: taskId,
    at: new Date().toISOString(),
    messageRefs: {
      [kind]: ids,
    },
  });
}

function registerPendingTaskReply(context, messageIds) {
  if (!context.pendingTaskReply) return;
  registerTaskMessageRefs(
    context.pendingTaskReply.taskId,
    context.pendingTaskReply.kind,
    messageIds,
  );
  delete context.pendingTaskReply;
}

function commandTaskDetail(taskRef) {
  const task = resolveTaskRef(taskRef);
  if (!task) return `未找到任务：${taskRef}`;
  return [
    `${task.id} [${task.status}]`,
    '',
    `描述：${taskDisplayText(task)}`,
    task.parentTaskId ? `parent_task_id: ${task.parentTaskId}` : '',
    `created_at: ${task.createdAt || '-'}`,
    `completed_at: ${task.completedAt || '-'}`,
    `thread_id: ${task.threadId || '-'}`,
    task.resumeThreadId ? `resume_thread_id: ${task.resumeThreadId}` : '',
    `run_log: ${task.runLogPath || '-'}`,
    '',
    task.result ? `结果摘要：${safeOneLine(task.result, 1200)}` : '',
    task.error ? `错误：${safeOneLine(task.error, 1200)}` : '',
  ].filter(Boolean).join('\n');
}

function continueTask(taskRef, instruction, context) {
  const parent = resolveTaskRef(taskRef);
  if (!parent) return `未找到任务：${taskRef}`;
  if (!parent.threadId) {
    return [
      `任务 ${parent.id} 还不能继续。`,
      `当前状态：${parent.status || '-'}`,
      '原因：没有可恢复的 Codex thread_id。请等任务完成后再继续，或查看任务日志定位失败原因。',
    ].join('\n');
  }

  const cleanInstruction = String(instruction || '').trim();
  const task = createTaskRecord(cleanInstruction, context, {
    instruction: cleanInstruction,
    parentTaskId: parent.id,
    resumeThreadId: parent.threadId,
    parentDescription: taskDisplayText(parent),
  });
  context.pendingTaskReply = {
    taskId: task.id,
    kind: 'continuedReplyIds',
  };
  runTaskInBackground(task, context);
  return [
    '续跑任务已创建，开始执行。',
    `任务ID：${task.id}`,
    `父任务ID：${parent.id}`,
    '状态：running',
    '',
    `追加要求：${instruction}`,
    '',
    `可用 task任务 查看最近任务。`,
    `可用 cmd 任务 ${task.id} 查询状态。`,
  ].join('\n');
}

function commandAuditToday() {
  const filePath = ensureAuditFile(new Date());
  const content = fs.readFileSync(filePath, 'utf8');
  const tail = content.split('\n').slice(-80).join('\n').trim();
  return [
    `今日审计页：${filePath}`,
    '',
    tail || '今日暂未写入审计内容。',
  ].join('\n');
}

async function handleCommand(command, context) {
  if (!command || /^(help|帮助|菜单)$/i.test(command)) return helpText(context);
  if (context.authz?.kind === 'group') return handleGroupCommand(command, context);

  if (/^ping$/i.test(command)) return 'pong';
  if (/^(status|状态)$/i.test(command)) return statusText();
  if (/^(whoami|我是谁)$/i.test(command)) return senderText(context.sender);

  if (/^(id|ids)$/i.test(command)) {
    return [
      `chat_id: ${context.message.chat_id}`,
      `message_id: ${context.message.message_id}`,
      `message_type: ${context.message.message_type}`,
      '',
      senderText(context.sender),
    ].join('\n');
  }

  if (/^(群信息|chat info)$/i.test(command)) {
    return getChatInfo(context.message.chat_id);
  }

  if (/^(成员列表|群成员|members)$/i.test(command)) {
    return getMemberList(context.message.chat_id);
  }

  if (/^(最近消息|messages|recent messages)$/i.test(command)) {
    return commandRecentMessages();
  }

  if (/^(最近任务|任务列表|tasks|task list)$/i.test(command)) {
    return commandRecentTasks();
  }

  if (/^(最近出站|出站列表|outbox|recent outbox)$/i.test(command)) {
    return commandRecentOutbox();
  }

  const outboxDetailMatch = command.match(/^(?:出站|outbox)\s+(\S+)$/i);
  if (outboxDetailMatch) return commandOutboxDetail(outboxDetailMatch[1]);

  if (/^(最近文档|文档列表|docbox|recent docbox)$/i.test(command)) {
    return commandRecentDocbox();
  }

  const docboxDetailMatch = command.match(/^(?:文档|docbox)\s+(\S+)$/i);
  if (docboxDetailMatch) return commandDocboxDetail(docboxDetailMatch[1]);

  const taskDetailMatch = command.match(/^(?:任务|task)\s+(\S+)$/i);
  if (taskDetailMatch) return commandTaskDetail(taskDetailMatch[1]);

  if (/^(审计\s*今天|今日审计|audit today)$/i.test(command)) {
    return commandAuditToday();
  }

  const echoMatch = command.match(/^echo\s+(.+)$/i);
  if (echoMatch) return echoMatch[1];

  return [
    `已收到：${command}`,
    '',
    '我现在只处理机器人交互命令。发送 help 查看可用命令。',
  ].join('\n');
}

async function handleGroupCommand(command, context) {
  if (/^ping$/i.test(command)) return 'pong';
  if (/^(status|状态)$/i.test(command)) return statusText();
  if (/^(whoami|我是谁)$/i.test(command)) return senderText(context.sender);

  if (/^(id|ids)$/i.test(command)) {
    return [
      `chat_id: ${context.message.chat_id}`,
      `message_id: ${context.message.message_id}`,
      `message_type: ${context.message.message_type}`,
      '',
      senderText(context.sender),
    ].join('\n');
  }

  if (/^(群信息|chat info)$/i.test(command)) {
    return getChatInfo(context.message.chat_id);
  }

  return null;
}

function parseRoute(command) {
  if (/^help$/i.test(command)) {
    return { kind: 'help', payload: '' };
  }

  const projectContinuation = parseProjectContinuationIntent(command);
  if (projectContinuation) {
    return { kind: 'project_promotion', payload: projectContinuation };
  }

  const taskContinueMatch = command.match(/^task继续\s*(#?\S+)\s+([\s\S]+)$/i);
  if (taskContinueMatch) {
    return {
      kind: 'task_continue',
      payload: {
        taskRef: taskContinueMatch[1],
        instruction: taskContinueMatch[2].trim(),
      },
    };
  }

  if (/^task\s*任务$/i.test(command)) {
    return { kind: 'task_list', payload: '' };
  }

  const cmdMatch = command.match(/^cmd(.*)$/i);
  if (cmdMatch) {
    return { kind: 'cmd', payload: cmdMatch[1].trim() };
  }

  const taskMatch = command.match(/^task(.*)$/i);
  if (taskMatch) {
    const description = taskMatch[1].trim();
    return description
      ? { kind: 'task', payload: description }
      : { kind: 'task_validation', payload: '' };
  }

  return { kind: 'default_chat', payload: command };
}

async function handleImmediateRoute(route, context) {
  if (route.kind === 'help') return helpText(context);
  if (route.kind === 'cmd') return handleCommand(route.payload, context);
  if (route.kind === 'task_list') return commandRecentTasks();
  if (route.kind === 'task_continue') {
    return continueTask(route.payload.taskRef, route.payload.instruction, context);
  }
  if (route.kind === 'task_validation') return '请补充任务描述：task <任务描述>';
  throw new Error(`Unsupported immediate route: ${route.kind}`);
}

async function sendProcessingReply(route, context) {
  const isProjectPromotion = route.kind === 'project_promotion';
  const cardState = {
    status: 'processing',
    detail: route.kind === 'task'
      ? '正在创建后台任务。'
      : isProjectPromotion
        ? '正在识别项目，并把当前对话升级为项目任务。'
        : '请求已经进入本机 Codex。',
    latestInput: isProjectPromotion ? projectPromotionInput(route)
      : route.kind === 'default_chat' ? route.payload : '',
    hideActions: Boolean(context.reuseProgressCard),
    schemaVersion: ['default_chat', 'project_promotion'].includes(route.kind) ? '2.0' : undefined,
  };
  try {
    if (context.reuseProgressCard && context.progressMessageId
      && await updateProgressCard(context, cardState)) {
      logOutbound(context, route.kind, 'Codex 正在处理（原卡更新）');
      return;
    }
    context.progressMessageId = await replyCard(
      context.message.message_id,
      progressCard(cardState),
      { phase: 'processing-card' },
    );
    if (context.defaultConversation?.id && context.progressMessageId) {
      extendDefaultConversation(context, {
        rootMessageId: context.progressMessageId,
        messageIds: [context.progressMessageId],
      });
    }
    logOutbound(context, route.kind, 'Codex 正在处理（状态卡）');
  } catch (error) {
    await replyText(
      context.message.message_id,
      context.message.chat_id,
      '等待Codex响应中',
      { phase: 'processing' },
    );
    logOutbound(context, route.kind, '等待Codex响应中');
  }
}

function taskLinkProgressFromEvent(event) {
  return publicProgressFromEvent(event);
}

function taskLinkCardPushState(linkId) {
  if (!taskLinkCardPushes.has(linkId)) {
    taskLinkCardPushes.set(linkId, {
      timer: null,
      pending: null,
      inFlight: false,
      lastFingerprint: '',
      writeChain: Promise.resolve(),
    });
  }
  return taskLinkCardPushes.get(linkId);
}

function cancelTaskLinkCardPush(linkId) {
  const state = taskLinkCardPushes.get(linkId);
  if (!state) return;
  if (state.timer) clearTimeout(state.timer);
  state.timer = null;
  state.pending = null;
}

async function patchTaskLinkCard(
  link,
  status,
  detail,
  progress = link.progress,
  latestInput = '',
  { questions = [], requireComplete = false, cardToken = '' } = {},
) {
  return enqueueTaskLinkCardUpdate(link, () => {
    const resolvedLatestInput = taskLinkLatestInputs.get(link.id) || latestInput || '';
    const resolvedDetail = taskLinkDisplayDetail(link, status, detail);
    return fitProgressCardToRequestBudget({
      status,
      title: link.title,
    detail: resolvedDetail,
    taskLink: publicTaskLink(link),
    progress,
      latestInput: resolvedLatestInput,
      questions,
    });
  }, { requireComplete, cardToken });
}

async function patchPreparedTaskLinkCard(link, card, { cardToken = '' } = {}) {
  return enqueueTaskLinkCardUpdate(
    link,
    () => ({ card, complete: true }),
    { cardToken },
  );
}

async function enqueueTaskLinkCardUpdate(
  link,
  buildCard,
  { requireComplete = false, cardToken = '' } = {},
) {
  if (!link.rootMessageId && !cardToken) return false;
  const pushState = taskLinkCardPushState(link.id);
  const operation = pushState.writeChain.then(async () => {
    const fitted = await buildCard();
    const card = fitted.card;
    if (!card) return false;
    const fingerprint = JSON.stringify(card);
    if (pushState.lastFingerprint === fingerprint) return requireComplete ? fitted.complete : true;
    const delivery = await deliverTaskLinkCard({
      messageId: link.rootMessageId,
      token: cardToken,
      card,
      patchByMessage: ({ messageId, card: nextCard }) => lark.larkImPatchCard({
        messageId,
        card: nextCard,
        timeoutMs: 30000,
      }),
      updateByToken: ({ token, card: nextCard }) => lark.larkCardUpdateByToken({
        token,
        card: nextCard,
        timeoutMs: 30000,
      }),
    });
    if (!delivery.ok) {
      const errors = [
        delivery.patchError
          ? `message patch: ${delivery.patchError.message || String(delivery.patchError)}` : '',
        delivery.tokenError
          ? `token update: ${delivery.tokenError.message || String(delivery.tokenError)}` : '',
      ].filter(Boolean).join('; ');
      appendMessageLog({
        direction: 'task_link_card_update_failed',
        at: new Date().toISOString(),
        taskKey: link.taskKey,
        error: safeOneLine(errors || 'task link card update failed', 300),
      });
      return false;
    }
    pushState.lastFingerprint = fingerprint;
    updateTaskLink(link.id, { cardRevision: TASK_LINK_CARD_REVISION });
    return requireComplete ? fitted.complete : true;
  });
  pushState.writeChain = operation.then(() => undefined, () => undefined);
  return operation;
}

function rememberTaskLinkFinalResult(link, finalText) {
  const text = String(finalText || '').trim();
  if (link?.id && text) taskLinkFinalResults.set(link.id, text);
  return text;
}

function taskLinkDisplayDetail(link, status, fallback = '') {
  const detail = String(fallback || link?.progress?.detail || link?.detailSummary || '').trim();
  if (String(status || link?.turnState || '') !== 'completed') return detail;
  const cached = String(taskLinkFinalResults.get(link?.id) || '').trim();
  if (cached) return cached;
  let journalTurn = null;
  try { journalTurn = codexDesktopTurnJournal.snapshot(link.threadId); } catch {}
  const resolved = terminalTaskLinkDetail(link, journalTurn, detail);
  return rememberTaskLinkFinalResult(link, resolved) || detail;
}

function fittedTaskLinkCard(link, status, detail, {
  questions = [], latestInput = '', openIds = [],
} = {}) {
  const fitted = fitProgressCardToRequestBudget({
    status,
    title: link.title,
    detail: taskLinkDisplayDetail(link, status, detail),
    taskLink: publicTaskLink(link),
    progress: link.progress,
    latestInput: taskLinkLatestInputs.get(link.id) || latestInput || '',
    questions,
    openIds,
  });
  return fitted.card || progressCard({
    status: 'failed',
    title: link.title,
    detail: '卡片内容无法安全适配，请在 Codex Desktop 查看完整结果。',
    openIds,
  });
}

function scheduleTaskLinkCardPatch(link, status, detail, progress = link.progress, latestInput = '') {
  if (!link?.rootMessageId) return;
  const state = taskLinkCardPushState(link.id);
  state.pending = { link, status, detail, progress, latestInput };
  if (state.timer || state.inFlight) return;
  state.timer = setTimeout(async () => {
    state.timer = null;
    const pending = state.pending;
    state.pending = null;
    if (!pending) return;
    state.inFlight = true;
    try {
      await patchTaskLinkCard(
        pending.link,
        pending.status,
        pending.detail,
        pending.progress,
        pending.latestInput,
      );
    } finally {
      state.inFlight = false;
      if (state.pending) scheduleTaskLinkCardPatch(
        state.pending.link,
        state.pending.status,
        state.pending.detail,
        state.pending.progress,
        state.pending.latestInput,
      );
    }
  }, 900);
}

function handleTaskLinkJournalChange(linkId, journalTurn) {
  let link;
  try {
    link = readTaskLinkStore().links.find((item) => item.id === linkId);
  } catch {
    return;
  }
  if (!link || taskLinkEffectiveState(link) !== 'active') return;
  if (taskLinkExecutions.has(link.threadId)) return;
  const pendingInput = trackDesktopTaskLinkInput(link.threadId, journalTurn);
  if (pendingInput) {
    taskLinkFinalResults.delete(link.id);
    const desktopRequired = pendingInput.questions.some((question) => question.isSecret);
    const observedTurnOwner = recoveredRunningTurnOwner(link, {
      turnState: desktopRequired ? 'desktop_action_required' : 'waiting_input',
      turnOwner: 'desktop',
      actionRequired: desktopRequired ? 'desktop' : 'feishu',
    }, journalTurn.turnId);
    const waiting = updateTaskLink(link.id, {
      turnState: desktopRequired ? 'desktop_action_required' : 'waiting_input',
      turnOwner: observedTurnOwner,
      actionRequired: desktopRequired ? 'desktop' : 'feishu',
      activeTurnId: journalTurn.turnId || link.activeTurnId,
      progress: {
        ...link.progress,
        phase: desktopRequired ? '需要桌面操作' : '等待输入',
        detail: desktopRequired
          ? '该输入可能包含敏感信息，请在 Codex Desktop 中继续。'
          : 'Codex 等待你的选择。',
      },
    }, undefined, { renew: !desktopRequired });
    cancelTaskLinkCardPush(waiting.id);
    patchTaskLinkCard(
      waiting,
      waiting.turnState,
      desktopRequired
        ? waiting.progress.detail
        : safeQuestionSummary(pendingInput.questions) || waiting.progress.detail,
      waiting.progress,
      '',
      { questions: desktopRequired ? [] : pendingInput.questions },
    ).catch(() => {});
    return;
  }
  if (taskLinkInputRequests.get(link.threadId)?.owner === 'desktop') {
    taskLinkInputRequests.delete(link.threadId);
  }
  if (!journalTurn || journalTurn.state !== 'running') {
    refreshTaskLinks(link.id).catch(() => {});
    return;
  }
  taskLinkFinalResults.delete(link.id);
  const commentary = journalTurn.lastMessagePhase === 'commentary'
    ? publicProgressText(journalTurn.lastMessage) : '';
  const planText = publicProgressText(journalTurn.planText, 3000);
  const observedMode = String(journalTurn.collaborationMode?.mode || '');
  const sameTurn = String(link.activeTurnId || '') === String(journalTurn.turnId || '');
  const observedTurnOwner = recoveredRunningTurnOwner(link, {
    turnState: 'running', turnOwner: 'desktop', actionRequired: 'none',
  }, journalTurn.turnId);
  const userMessage = String(journalTurn.lastUserMessage || '').trim();
  const inputChanged = Boolean(userMessage && taskLinkLatestInputs.get(link.id) !== userMessage);
  if (userMessage) taskLinkLatestInputs.set(link.id, userMessage);
  const detail = commentary || (link.turnState === 'running'
    ? link.progress?.detail : 'Codex 已开始处理。');
  const unchanged = link.turnState === 'running'
    && link.turnOwner === observedTurnOwner
    && String(link.activeTurnId || '') === String(journalTurn.turnId || '')
    && !inputChanged
    && (!commentary || publicProgressText(link.detailSummary) === commentary)
    && (!planText || publicProgressText(link.progress?.plan, 3000) === planText)
    && (!observedMode || observedMode === link.activeTurnMode);
  if (unchanged) return;
  const observed = updateTaskLink(link.id, {
    turnState: 'running',
    turnOwner: observedTurnOwner,
    actionRequired: 'none',
    activeTurnId: journalTurn.turnId || link.activeTurnId,
    activeTurnMode: observedMode || (sameTurn ? link.activeTurnMode : ''),
    detailSummary: commentary || link.detailSummary,
    progress: {
      ...taskLinkProgressForTurn(link.progress, journalTurn.turnId),
      phase: commentary ? '当前进展' : '运行中',
      detail,
      plan: planText || (sameTurn ? link.progress?.plan : ''),
      startedAt: sameTurn ? link.progress?.startedAt : journalTurn.startedAt || '',
      durationSeconds: sameTurn ? link.progress?.durationSeconds : undefined,
    },
  });
  scheduleTaskLinkCardPatch(observed, 'running', detail, observed.progress);
}

function syncTaskLinkJournalWatchers(links) {
  const activeIds = new Set();
  for (const link of links || []) {
    if (taskLinkEffectiveState(link) !== 'active') continue;
    activeIds.add(link.id);
    if (taskLinkJournalWatchers.has(link.id)) continue;
    const stop = codexDesktopTurnJournal.watch(link.threadId, (journalTurn) => {
      handleTaskLinkJournalChange(link.id, journalTurn);
    });
    if (stop) taskLinkJournalWatchers.set(link.id, stop);
  }
  for (const [linkId, stop] of taskLinkJournalWatchers.entries()) {
    if (activeIds.has(linkId)) continue;
    stop();
    taskLinkJournalWatchers.delete(linkId);
    cancelTaskLinkCardPush(linkId);
    taskLinkLatestInputs.delete(linkId);
  }
}

function trackDesktopTaskLinkInput(threadId, journalTurn) {
  const request = journalTurn?.pendingInput;
  if (!request?.requestId || !Array.isArray(request.questions) || !request.questions.length) return null;
  const existing = taskLinkInputRequests.get(threadId);
  if (existing?.owner === 'desktop' && existing.requestId === request.requestId) return existing;
  const pending = {
    owner: 'desktop',
    requestId: request.requestId,
    turnId: journalTurn.turnId || '',
    questions: request.questions,
    submitting: false,
    answered: false,
  };
  taskLinkInputRequests.set(threadId, pending);
  return pending;
}

function taskLinkAnswers(threadId, text) {
  const pending = taskLinkInputRequests.get(threadId);
  if (!pending || pending.submitting || pending.answered) return null;
  const lines = String(text || '').split('\n').map((line) => line.trim()).filter(Boolean);
  const answers = {};
  for (const [index, question] of pending.questions.entries()) {
    let value = lines.find((line) => line.startsWith(`${question.id}:`) || line.startsWith(`${question.id}：`));
    value = value ? value.replace(new RegExp(`^${question.id}[:：]\\s*`), '') : (pending.questions.length === 1 ? text : lines[index]);
    if (!value) return null;
    answers[question.id] = { answers: String(value).split(/[,，]/).map((item) => item.trim()).filter(Boolean) };
  }
  return answers;
}

async function answerTaskLinkUserInput(threadId, answers) {
  const pending = taskLinkInputRequests.get(threadId);
  if (!pending || pending.submitting || pending.answered) return false;
  if (pending.owner !== 'desktop') return codexAppServer.answerUserInput(threadId, answers);
  pending.submitting = true;
  try {
    await codexDesktopTaskController.submitUserInput({
      threadId,
      turnId: pending.turnId,
      questions: pending.questions,
      response: { answers },
    });
    pending.submitting = false;
    pending.answered = true;
    return true;
  } catch (error) {
    pending.submitting = false;
    throw error;
  }
}

async function answerTaskLinkFromText(threadId, text) {
  const answers = taskLinkAnswers(threadId, text);
  return answers ? answerTaskLinkUserInput(threadId, answers) : false;
}

async function executeTaskLink(link, command, context) {
  const fresh = readTaskLinkStore().links.find((item) => item.id === link.id) || link;
  const latestInput = String(context.taskLinkLatestInput || command || '').trim();
  if (latestInput) taskLinkLatestInputs.set(fresh.id, latestInput);
  if (taskLinkEffectiveState(fresh) !== 'active') {
    cleanupContextInbound(context);
    await replyText(context.message.message_id, context.message.chat_id, '该任务连接已失效，请回到 CodexAssistant 重新连接。', { phase: `task-link-inactive:${fresh.taskKey}` });
    return;
  }
  if (await answerTaskLinkFromText(link.threadId, command)) {
    const resumed = updateTaskLink(link.id, {
      turnState: 'running',
      turnOwner: ['bridge', 'desktop'].includes(fresh.turnOwner) ? fresh.turnOwner : 'bridge',
      actionRequired: 'none',
      pendingMessageId: '',
    }, undefined, { renew: true });
    await patchTaskLinkCard(resumed, 'processing', '已收到你的选择，Codex 继续执行。', resumed.progress, latestInput);
    cleanupContextInbound(context);
    return;
  }

  let snapshot;
  try {
    snapshot = await readTaskLinkSnapshot(fresh.threadId);
  } catch (error) {
    cleanupContextInbound(context);
    throw error;
  }
  trackDesktopTaskLinkInput(fresh.threadId, snapshot.journalTurn);
  if (await answerTaskLinkFromText(fresh.threadId, command)) {
    const resumed = updateTaskLink(link.id, {
      turnState: 'running',
      turnOwner: ['bridge', 'desktop'].includes(fresh.turnOwner) ? fresh.turnOwner : 'bridge',
      actionRequired: 'none',
      pendingMessageId: '',
    }, undefined, { renew: true });
    await patchTaskLinkCard(resumed, 'processing', '已收到你的选择，Codex 继续执行。', resumed.progress, latestInput);
    cleanupContextInbound(context);
    return;
  }
  if (snapshot.publicState.turnState === 'waiting_input') {
    const pending = taskLinkInputRequests.get(fresh.threadId);
    const waiting = updateTaskLink(fresh.id, {
      turnState: 'waiting_input',
      turnOwner: ['bridge', 'desktop'].includes(fresh.turnOwner) ? fresh.turnOwner : 'desktop',
      actionRequired: 'feishu',
      activeTurnId: snapshot.turnId || fresh.activeTurnId,
      progress: { ...fresh.progress, phase: '等待输入', detail: 'Codex 等待你的选择。' },
    }, undefined, { renew: true });
    await patchTaskLinkCard(
      waiting,
      'waiting_input',
      safeQuestionSummary(pending?.questions) || '请通过卡片选项或按问题编号回复。',
      waiting.progress,
      '',
      { questions: pending?.questions || [] },
    );
    cleanupContextInbound(context);
    return;
  }
  const activeTurn = snapshot.publicState.turnState === 'running'
    || snapshot.publicState.turnState === 'waiting_input'
    || snapshot.publicState.turnState === 'desktop_action_required';
  if (activeTurn || taskLinkExecutions.has(fresh.threadId)) {
    if (snapshot.publicState.actionRequired === 'desktop') {
      const blocked = updateTaskLink(fresh.id, {
        ...snapshot.publicState,
        activeTurnId: snapshot.turnId || fresh.activeTurnId,
        progress: { ...fresh.progress, phase: '需要桌面操作', detail: '当前轮正在等待 Codex Desktop 中的输入或批准。' },
      }, undefined, { renew: true });
      const cardUpdated = await patchTaskLinkCard(
        blocked, 'desktop_action_required', blocked.progress.detail, blocked.progress, latestInput,
      );
      if (!cardUpdated) {
        await replyText(context.message.message_id, context.message.chat_id, '当前轮正在等待 Codex Desktop 操作。你仍可在飞书停止本轮或解除连接。', { phase: `task-link-desktop-action:${fresh.taskKey}` });
      }
      cleanupContextInbound(context);
      return;
    }
    const input = taskInput(command, context.inbound);
    try {
      const turnId = await codexDesktopTaskController.steer({
        threadId: fresh.threadId,
        cwd: fresh.cwd,
        input,
        turnId: snapshot.turnId || fresh.activeTurnId,
      });
      const dirs = context.inbound?.cleanupDir
        ? [...new Set([...(fresh.stagedCleanupDirs || []), context.inbound.cleanupDir])] : (fresh.stagedCleanupDirs || []);
      const steered = updateTaskLink(fresh.id, {
        turnState: 'running',
        turnOwner: taskLinkExecutions.has(fresh.threadId) ? 'bridge' : 'desktop',
        actionRequired: 'none',
        activeTurnId: turnId,
        stagedCleanupDirs: dirs,
        progress: { ...fresh.progress, phase: '已修正当前轮', detail: '飞书中的补充要求已送入当前 Codex 轮次。' },
      }, undefined, { renew: true });
      if (context.inbound?.cleanupDir) context.inboundCleaned = true;
      const cardUpdated = await patchTaskLinkCard(
        steered, 'processing', steered.progress.detail, steered.progress, latestInput,
      );
      if (!cardUpdated) {
        await replyText(context.message.message_id, context.message.chat_id, '已将这条要求送入当前 Codex 轮次。', { phase: `task-link-steered:${fresh.taskKey}` });
      }
      return;
    } catch (error) {
      if (!/active.*turn.*not.*steer|not.*steerable|cannot.*steer|turn.*changed/i.test(error.message || '')) {
        cleanupContextInbound(context);
        throw error;
      }
      if (fresh.pendingMessageId) {
        cleanupContextInbound(context);
        const cardUpdated = await patchTaskLinkCard(
          fresh, 'queued', '已有一条消息等待下一轮，请先等待。', fresh.progress, latestInput,
        );
        if (!cardUpdated) {
          await replyText(context.message.message_id, context.message.chat_id, '已有一条消息等待下一轮，请先等待。', { phase: `task-link-busy:${fresh.taskKey}` });
        }
        return;
      }
      taskLinkQueuedContexts.set(fresh.threadId, { command, context });
      const queued = updateTaskLink(fresh.id, {
        turnState: 'queued',
        turnOwner: taskLinkExecutions.has(fresh.threadId) ? 'bridge' : 'desktop',
        pendingMessageId: context.message.message_id,
        pendingCleanupDir: context.inbound?.cleanupDir || '',
        progress: { ...fresh.progress, phase: '等待下一轮', detail: '当前轮暂时不可修正，这条消息将在本轮结束后执行。' },
      }, undefined, { renew: true });
      if (context.inbound?.cleanupDir) context.inboundCleaned = true;
      const cardUpdated = await patchTaskLinkCard(
        queued, 'queued', queued.progress.detail, queued.progress, latestInput,
      );
      if (!cardUpdated) {
        await replyText(context.message.message_id, context.message.chat_id, '当前轮暂时不可修正；消息已保留，将在本轮结束后执行。', { phase: `task-link-queued:${fresh.taskKey}` });
      }
      return;
    }
  }

  const requiresModeOverride = fresh.nextTurnMode === 'plan' || fresh.activeTurnMode === 'plan';
  const collaborationMode = requiresModeOverride
    ? taskLinkCollaborationMode(fresh, snapshot) : null;
  taskLinkExecutions.add(link.threadId);
  taskLinkQueuedContexts.delete(link.threadId);
  const turnStartedAt = new Date().toISOString();
  let current = updateTaskLink(link.id, {
    turnState: 'running', turnOwner: 'bridge', actionRequired: 'none',
    activeTurnMode: fresh.nextTurnMode || 'default',
    pendingMessageId: '', pendingCleanupDir: '',
    progress: {
      phase: '分析', detail: 'Codex 已开始处理。', changedFiles: 0, testStatus: '未运行',
      changedFilesTurnId: '', plan: '', startedAt: turnStartedAt, durationSeconds: undefined,
    },
  }, undefined, { renew: true });
  taskLinkFinalResults.delete(link.id);
  await patchTaskLinkCard(current, 'processing', '请求已进入原 Codex 任务。', current.progress, latestInput);
  let lastActivityAt = 0;
  let hasNarrativeProgress = false;
  const changedFilePaths = new Set();
  try {
    const result = await codexAppServer.runTurn({
      input: taskInput(command, context.inbound), sessionId: current.threadId, cwd: current.cwd,
      timeoutMs: codexTaskTimeoutMs, taskLink: true, collaborationMode,
      onTurnStarted: ({ turnId }) => {
        current = updateTaskLink(current.id, {
          activeTurnId: turnId,
          progress: taskLinkProgressForTurn(current.progress, turnId),
        });
      },
      onProgress: (event) => {
        const progress = taskLinkProgressFromEvent(event);
        for (const filePath of changedFilePathsFromEvent(event)) changedFilePaths.add(filePath);
        const changedFiles = changedFilePaths.size;
        if (!progress && changedFiles === Number(current.progress?.changedFiles || 0)) return;
        if (progress?.kind === 'activity') {
          if (hasNarrativeProgress || Date.now() - lastActivityAt < 2000) return;
          lastActivityAt = Date.now();
        }
        if (['commentary', 'plan'].includes(progress?.kind)) hasNarrativeProgress = true;
        const nextProgress = progress
          ? {
              ...current.progress,
              phase: progress.phase,
              detail: progress.detail,
              changedFiles,
              ...(progress.plan ? { plan: progress.plan } : {}),
            }
          : { ...current.progress, changedFiles };
        if (progress
          && progress.phase === current.progress?.phase
          && progress.detail === current.progress?.detail
          && (!progress.plan || progress.plan === current.progress?.plan)
          && changedFiles === Number(current.progress?.changedFiles || 0)) return;
        current = updateTaskLink(current.id, {
          detailSummary: progress ? publicProgressText(progress.detail) : current.detailSummary,
          progress: nextProgress,
        });
        scheduleTaskLinkCardPatch(
          current,
          'running',
          current.progress.detail,
          current.progress,
          latestInput,
        );
      },
      onInputRequest: (questions, request = {}) => {
        const desktopRequired = request.desktopRequired || questions.some((question) => question.isSecret);
        const safeQuestions = safeQuestionSummary(questions);
        current = updateTaskLink(current.id, {
          turnState: desktopRequired ? 'desktop_action_required' : 'waiting_input',
          turnOwner: 'bridge',
          actionRequired: desktopRequired ? 'desktop' : 'feishu',
          progress: {
            ...current.progress,
            phase: desktopRequired ? '需要桌面操作' : '等待输入',
            detail: desktopRequired ? '该输入可能包含敏感信息，请在 Codex Desktop 中继续。' : 'Codex 等待你的选择。',
          },
        }, undefined, { renew: !desktopRequired });
        cancelTaskLinkCardPush(current.id);
        if (current.rootMessageId) patchTaskLinkCard(
          current,
          desktopRequired ? 'desktop_action_required' : 'waiting_input',
          safeQuestions || '该请求需要在 Codex Desktop 中处理。',
          current.progress,
          latestInput,
          { questions: desktopRequired ? [] : questions },
        ).catch(() => {});
        if (desktopRequired && current.activeTurnId && !request.preserveTurn) {
          codexDesktopTaskController.interrupt({
            threadId: current.threadId,
            turnId: current.activeTurnId,
          }).catch(() => {});
        }
      },
    });
    if (result.status === 'interrupted') {
      cancelTaskLinkCardPush(current.id);
      current = updateTaskLink(current.id, {
        turnState: 'interrupted', turnOwner: 'none', actionRequired: 'none', activeTurnId: '',
        detailSummary: '当前 Codex 轮次已停止，任务连接继续有效。',
        progress: {
          ...current.progress, phase: '已停止', detail: '当前 Codex 轮次已停止，任务连接继续有效。',
          durationSeconds: Math.max(0, Math.round(Number(result.durationMs || 0) / 1000)),
        },
      }, undefined, { terminalAt: new Date().toISOString() });
      if (taskLinkEffectiveState(current) === 'active') {
        await patchTaskLinkCard(current, 'interrupted', current.progress.detail, current.progress, latestInput);
      }
      return;
    }
    cancelTaskLinkCardPush(current.id);
    current = updateTaskLink(current.id, {
      turnState: 'completed', turnOwner: 'none', actionRequired: 'none',
      activeTurnId: '', lastDeliveredTurnId: result.turnId,
      detailSummary: safeOneLine(result.finalText, 900),
      progress: {
        ...current.progress, phase: '完成', detail: '任务本轮已完成。',
        durationSeconds: Math.max(0, Math.round(Number(result.durationMs || 0) / 1000)),
      },
    }, undefined, { terminalAt: new Date().toISOString() });
    if (taskLinkEffectiveState(current) === 'active') {
      const finalText = result.finalText || '任务已完成。';
      rememberTaskLinkFinalResult(current, finalText);
      const cardUpdated = await patchTaskLinkCard(
        current,
        'completed',
        finalText,
        current.progress,
        latestInput,
        { requireComplete: true },
      );
      if (!cardUpdated) {
        const ids = await replyText(
          context.message.message_id,
          context.message.chat_id,
          finalText,
          { phase: `task-link-result:${current.taskKey}:${result.turnId}` },
        );
        updateTaskLink(current.id, { messageIds: [...new Set([...(current.messageIds || []), ...ids])] });
      }
    }
  } catch (error) {
    cancelTaskLinkCardPush(current.id);
    const requiresDesktop = current.actionRequired === 'desktop';
    const interrupted = /interrupt|cancel/i.test(error.message || '');
    const fallbackStartedAt = Date.parse(current.progress?.startedAt || '');
    const terminalDurationSeconds = Number.isFinite(error.durationMs)
      ? Math.max(0, Math.round(error.durationMs / 1000))
      : Number.isFinite(fallbackStartedAt)
        ? Math.max(0, Math.round((Date.now() - fallbackStartedAt) / 1000))
        : undefined;
    current = updateTaskLink(current.id, requiresDesktop ? {
      turnState: 'desktop_action_required', turnOwner: 'desktop', actionRequired: 'desktop', activeTurnId: '',
      detailSummary: '该输入需要在 Codex Desktop 中继续。',
      progress: { ...current.progress, phase: '需要桌面操作', detail: '该输入需要在 Codex Desktop 中继续。' },
    } : {
      turnState: interrupted ? 'interrupted' : 'failed',
      turnOwner: 'none', actionRequired: 'none', activeTurnId: '',
      detailSummary: safeOneLine(error.message, 300),
      progress: {
        ...current.progress,
        phase: interrupted ? '已停止' : '失败',
        detail: interrupted ? '当前 Codex 轮次已停止，任务连接继续有效。' : safeOneLine(error.message, 300),
        durationSeconds: terminalDurationSeconds,
      },
    }, undefined, { terminalAt: new Date().toISOString() });
    if (taskLinkEffectiveState(current) === 'active') {
      const cardUpdated = await patchTaskLinkCard(
        current, current.turnState, current.progress.detail, current.progress, latestInput,
      );
      if (!requiresDesktop && !interrupted && !cardUpdated) await sendFailureReply(context, error, 'task_link');
    }
  } finally {
    taskLinkExecutions.delete(link.threadId);
    cleanupContextInbound(context);
    cleanupTaskLinkAssetDirectories(current);
    refreshTaskLinkWakeAssertion();
  }
}

function cleanupTaskLinkAssetDirectories(link) {
  const directories = [...new Set([
    ...(link?.stagedCleanupDirs || []),
    link?.pendingCleanupDir || '',
  ].filter(Boolean))];
  for (const directory of directories) {
    try { cleanupInboundAssets(__dirname, directory); } catch (error) {
      appendMessageLog({ direction: 'task_link_asset_cleanup_failed', at: new Date().toISOString(), taskKey: link?.taskKey, error: safeOneLine(error.message, 240) });
    }
  }
  if (link?.id && directories.length) {
    try { updateTaskLink(link.id, { stagedCleanupDirs: [], pendingCleanupDir: '' }); } catch {}
  }
}

function refreshTaskLinkWakeAssertion() {
  let active = false;
  let links = [];
  try {
    const store = readTaskLinkStore();
    links = store.links;
    for (const link of store.links) {
      if (taskLinkEffectiveState(link) === 'expired' && link.linkState !== 'expired') {
        const expired = updateTaskLink(link.id, { linkState: 'expired', pendingMessageId: '' });
        cleanupTaskLinkAssetDirectories(expired);
      }
      if (taskLinkEffectiveState(link) === 'active') active = true;
    }
  } catch (error) {
    appendMessageLog({ direction: 'task_link_store_failed', at: new Date().toISOString(), error: safeOneLine(error.message, 300) });
  }
  syncTaskLinkJournalWatchers(links);
  if (active && !taskLinkCaffeinate) {
    const inhibitor = spawnSleepInhibitor({ projectRoot: __dirname, mode: 'active-task', env: process.env });
    taskLinkCaffeinate = inhibitor;
    inhibitor?.once('error', (error) => {
      if (taskLinkCaffeinate === inhibitor) taskLinkCaffeinate = null;
      appendMessageLog({
        direction: 'task_link_sleep_inhibitor_failed',
        at: new Date().toISOString(),
        error: safeOneLine(error.message, 240),
      });
    });
    inhibitor?.once('exit', () => {
      if (taskLinkCaffeinate === inhibitor) taskLinkCaffeinate = null;
    });
  }
  if (!active && taskLinkCaffeinate) { taskLinkCaffeinate.kill(); taskLinkCaffeinate = null; }
}

async function processQueuedTaskLinks() {
  let links;
  try { links = readTaskLinkStore().links; } catch { return; }
  for (const link of links) {
    if (link.turnState !== 'queued' || !link.pendingMessageId || taskLinkExecutions.has(link.threadId)) continue;
    try {
      const snapshot = await readTaskLinkSnapshot(link.threadId);
      if (['running', 'waiting_input', 'desktop_action_required'].includes(snapshot.publicState.turnState)) continue;
    } catch { continue; }
    const queued = taskLinkQueuedContexts.get(link.threadId);
    if (queued) {
      taskLinkQueuedContexts.delete(link.threadId);
      await executeTaskLink(link, queued.command, queued.context);
      continue;
    }
    const inbound = [...readJsonl(messageLogPath)].reverse().find((item) => item.direction === 'inbound' && item.messageId === link.pendingMessageId);
    if (!inbound?.text || !inbound.chatId) {
      const failed = updateTaskLink(link.id, {
        turnState: 'failed', turnOwner: 'none', actionRequired: 'none',
        pendingMessageId: '', progress: { ...link.progress, phase: '排队内容已失效', detail: '桥重启后无法恢复这条排队消息，请重新回复任务卡。' },
      });
      cleanupTaskLinkAssetDirectories(failed);
      continue;
    }
    await executeTaskLink(link, inbound.text, { message: { message_id: inbound.messageId, chat_id: inbound.chatId } });
  }
}

async function refreshTaskLinks(onlyLinkId = '') {
  let links;
  try { links = readTaskLinkStore().links; } catch { return; }
  for (const link of links) {
    if (onlyLinkId && link.id !== onlyLinkId) continue;
    if (taskLinkEffectiveState(link) !== 'active'
      || taskLinkExecutions.has(link.threadId)
      || taskLinkFollowupTransitions.has(link.threadId)) continue;
    if (taskLinkRefreshes.has(link.id)) continue;
    taskLinkRefreshes.add(link.id);
    try {
      const snapshot = await readTaskLinkSnapshot(link.threadId);
      const pendingInput = trackDesktopTaskLinkInput(link.threadId, snapshot.journalTurn);
      if (!pendingInput && taskLinkInputRequests.get(link.threadId)?.owner === 'desktop') {
        taskLinkInputRequests.delete(link.threadId);
      }
      const next = {
        ...snapshot.publicState,
        turnOwner: recoveredRunningTurnOwner(link, snapshot.publicState, snapshot.turnId),
      };
      if (next.turnState === 'waiting_input' && !pendingInput) {
        const failed = updateTaskLink(link.id, {
          turnState: 'desktop_action_required', turnOwner: 'desktop', actionRequired: 'desktop',
          progress: { ...link.progress, phase: '需要桌面操作', detail: '无法恢复待答请求，请在 Codex Desktop 中继续。' },
        });
        cancelTaskLinkCardPush(link.id);
        await patchTaskLinkCard(failed, 'desktop_action_required', failed.progress.detail);
        continue;
      }
      const observedInput = String(snapshot.lastUserMessage || '').trim();
      const inputChanged = Boolean(observedInput && taskLinkLatestInputs.get(link.id) !== observedInput);
      if (observedInput) taskLinkLatestInputs.set(link.id, observedInput);
      const needsCardRevision = link.cardRevision !== TASK_LINK_CARD_REVISION
        && !taskLinkCardRevisionAttempts.has(link.id);
      if (needsCardRevision) taskLinkCardRevisionAttempts.add(link.id);
      if (!needsCardRevision && !inputChanged && !taskLinkSnapshotRequiresSync(link, snapshot)) continue;
      if (['running', 'waiting_input', 'desktop_action_required'].includes(next.turnState)) {
        taskLinkFinalResults.delete(link.id);
        const sameTurn = String(link.activeTurnId || '') === String(snapshot.turnId || '');
        const observedMode = String(snapshot.journalTurn?.collaborationMode?.mode || '');
        const planText = publicProgressText(snapshot.planText, 3000);
        const commentary = snapshot.lastMessagePhase === 'commentary'
          ? publicProgressText(snapshot.lastMessage) : '';
        const waitingForFeishu = next.turnState === 'waiting_input';
        const desktopRequired = next.actionRequired === 'desktop';
        const detail = waitingForFeishu
          ? 'Codex 等待你的选择。'
          : desktopRequired
          ? '当前轮等待 Codex Desktop 操作。'
          : commentary || (sameTurn && link.turnState === 'running'
            ? link.progress?.detail : 'Codex 正在处理。');
        const observed = updateTaskLink(link.id, {
          ...next,
          activeTurnId: snapshot.turnId || link.activeTurnId,
          activeTurnMode: observedMode || (sameTurn ? link.activeTurnMode : ''),
          detailSummary: commentary || (sameTurn ? link.detailSummary : ''),
          progress: {
            ...taskLinkProgressForTurn(link.progress, snapshot.turnId, snapshot.changedFilePaths),
            phase: waitingForFeishu
              ? '等待输入'
              : desktopRequired ? '需要桌面操作' : commentary ? '当前进展' : '运行中',
            detail,
            plan: planText || (sameTurn ? link.progress?.plan : ''),
            startedAt: sameTurn ? link.progress?.startedAt : snapshot.turnStartedAt || '',
            durationSeconds: sameTurn ? link.progress?.durationSeconds : undefined,
          },
        });
        await patchTaskLinkCard(
          observed,
          next.turnState,
          waitingForFeishu
            ? safeQuestionSummary(pendingInput.questions) || detail
            : detail,
          observed.progress,
          '',
          { questions: waitingForFeishu ? pendingInput.questions : [] },
        );
        continue;
      }
      const turnId = snapshot.turnId || '';
      const finalText = snapshot.finalText || (next.turnState === 'completed'
        ? finalTextFromTurn(snapshot.turn) : '');
      const lastMessage = snapshot.lastMessage || finalText;
      const completed = updateTaskLink(link.id, {
        ...next,
        activeTurnId: '',
        activeTurnMode: String(snapshot.journalTurn?.collaborationMode?.mode || link.activeTurnMode || ''),
        detailSummary: lastMessage ? publicProgressText(lastMessage) : link.detailSummary,
        progress: {
          ...taskLinkProgressForTurn(link.progress, turnId, snapshot.changedFilePaths),
          phase: next.turnState === 'completed' ? '完成' : next.turnState === 'idle' ? '已连接' : '轮次已结束',
          detail: lastMessage ? publicProgressText(lastMessage, 500) : '当前轮已结束，可以从飞书继续任务。',
          plan: publicProgressText(snapshot.planText, 3000) || link.progress?.plan || '',
          startedAt: snapshot.turnStartedAt || link.progress?.startedAt || '',
          durationSeconds: Number.isInteger(snapshot.turnDurationSeconds)
            ? snapshot.turnDurationSeconds : link.progress?.durationSeconds,
        },
      }, undefined, { terminalAt: new Date().toISOString() });
      const hasUndeliveredResult = turnId && turnId !== link.lastDeliveredTurnId
        && finalText && link.rootMessageId;
      if (next.turnState === 'completed' && finalText) {
        rememberTaskLinkFinalResult(link, finalText);
      }
      cancelTaskLinkCardPush(link.id);
      cleanupTaskLinkAssetDirectories(completed);
      const cardUpdated = await patchTaskLinkCard(
        completed,
        completed.turnState,
        taskLinkDisplayDetail(completed, completed.turnState, completed.progress.detail),
        completed.progress,
        '',
        { requireComplete: hasUndeliveredResult },
      );
      if (hasUndeliveredResult && cardUpdated) {
        updateTaskLink(link.id, { lastDeliveredTurnId: turnId });
      } else if (hasUndeliveredResult) {
        const ids = await replyText(link.rootMessageId, '', finalText, { phase: `task-link-observed-result:${link.taskKey}:${turnId}` });
        updateTaskLink(link.id, {
          lastDeliveredTurnId: turnId,
          messageIds: [...new Set([...(link.messageIds || []), ...ids])],
        });
      }
    } catch (error) {
      appendMessageLog({ direction: 'task_link_status_read_failed', at: new Date().toISOString(), taskKey: link.taskKey, error: safeOneLine(error.message, 240) });
    } finally {
      taskLinkRefreshes.delete(link.id);
    }
  }
}

async function sendFailureReply(context, error, route, { cardUpdated = false } = {}) {
  const errorText = `处理失败：${error.message || String(error)}`;
  let errorReplyIds = [];
  let errorReplyError = '';
  if (!cardUpdated) {
    try {
      errorReplyIds = await replyText(
        context.message.message_id,
        context.message.chat_id,
        errorText,
        { phase: `error:${route || 'unknown'}` },
      );
    } catch (replyError) {
      errorReplyError = replyError.stack || replyError.message || String(replyError);
    }
  }
  logError(context, error, {
    route,
    errorReplyIds,
    errorReplyError: errorReplyError || undefined,
  });
  return { errorText, errorReplyIds, errorReplyError };
}

async function runCodexRouteInBackground(route, context) {
  try {
    await sendProcessingReply(route, context);
    if (route.kind === 'project_promotion') {
      const result = await promoteDefaultConversation(route, context);
      logOutbound(context, route.kind, result);
      return;
    }
    const result = route.kind === 'task'
      ? createTask(route.payload, context)
      : await answerWithDefaultCodex(route.payload, context, {
          conversation: context.defaultConversation || null,
        });
    if (route.kind === 'default_chat') {
      const promoted = await promoteDefaultConversationFromProjection(
        context,
        result,
        route.payload,
      );
      if (promoted) {
        logOutbound(context, 'project_promotion', result);
        return;
      }
    }
    const phase = route.kind === 'task' && context.pendingTaskReply?.taskId
      ? `task-created:${context.pendingTaskReply.taskId}`
      : 'final';
    const delivery = await deliverProgressResult(context, route.kind === 'task'
      ? {
          status: 'processing',
          taskId: context.pendingTaskReply?.taskId,
          detail: '后台任务已创建，正在执行。',
        }
      : {
          status: 'completed',
          followupEnabled: true,
          taskDurationSeconds: Number.isFinite(context.lastCodexDurationMs)
            ? Math.round(context.lastCodexDurationMs / 1000)
            : undefined,
          latestInput: route.payload,
          detail: result,
        },
      result,
      { phase },
    );
    if (route.kind === 'default_chat' && context.defaultConversation?.id) {
      extendDefaultConversation(context, { messageIds: delivery.messageIds });
    }
    registerPendingTaskReply(context, delivery.messageIds);
    logOutbound(context, route.kind, result);
  } catch (error) {
    const latestInput = route.kind === 'project_promotion'
      ? projectPromotionInput(route)
      : route.payload;
    const cardUpdated = await updateProgressCard(context, {
      status: 'failed',
      followupEnabled: true,
      latestInput,
      detail: error.message || String(error),
    });
    await sendFailureReply(context, error, route.kind, { cardUpdated });
    appendAudit('处理失败', [
      `- chat_id：${context.message.chat_id}`,
      `- message_id：${context.message.message_id}`,
      `- route：${route.kind}`,
      `- 输入：${safeOneLine(latestInput, 500)}`,
      `- 错误：${safeOneLine(error.message || String(error), 700)}`,
    ]);
  } finally {
    if (route.kind !== 'task') cleanupContextInbound(context);
  }
}

async function handleFeishuMessage(data) {
  const event = normalizeFeishuEvent(data);
  if (!event?.message) {
    appendMessageLog({
      direction: 'event_ignored',
      at: new Date().toISOString(),
      reason: 'missing_message',
      raw: safeOneLine(JSON.stringify(data || {}), 1200),
    });
    return;
  }
  const { message, sender } = event;
  const initialDetails = inboundMessageDetails(message);
  const text = message.message_type === 'text' ? parseText(message.content) : initialDetails.text;
  let command = normalizeCommand(text);
  const receivedAt = new Date().toISOString();
  const trace = extractTrace(text);
  const parsed = parseKeyValueFields(text);

  if (!rememberMessage(message.message_id)) {
    appendMessageLog({
      direction: 'duplicate_ignored',
      at: receivedAt,
      chatId: message.chat_id,
      messageId: message.message_id,
      text,
      command,
    });
    return;
  }

  const inbound = {
    direction: 'inbound',
    receivedAt,
    event: 'im.message.receive_v1',
    chatId: message.chat_id,
    messageId: message.message_id,
    rootId: message.root_id,
    parentId: message.parent_id,
    messageType: message.message_type,
    senderId: sender?.sender_id,
    text,
    command,
  };
  if (trace) inbound.trace = trace;
  if (parsed) inbound.parsed = parsed;
  console.log(JSON.stringify(inbound, null, 2));
  appendMessageLog(inbound);

  const context = { message, sender, inbound: null };
  try {
    const authz = await authorizeMessage(message, sender);
    context.authz = authz;
    appendMessageLog({
      direction: 'authz',
      at: new Date().toISOString(),
      chatId: message.chat_id,
      messageId: message.message_id,
      kind: authz.kind,
      allowed: authz.allowed,
      reason: authz.reason,
      senderOpenId: senderOpenId(sender),
    });

    if (!authz.allowed) {
      if (authz.kind === 'direct') {
        await replyText(
          message.message_id,
          message.chat_id,
          '当前机器人单聊只响应授权用户。',
          { phase: 'authz-denied' },
        );
      }
      return;
    }

    if (message.message_type !== 'text') {
      if (authz.kind !== 'direct') {
        await replyText(
          message.message_id,
          message.chat_id,
          '群聊中的附件暂不交给 Codex 处理；请在授权单聊中发送。',
          { phase: 'unsupported-group-attachment' },
        );
        return;
      }
      if (!SUPPORTED_INBOUND_MESSAGE_TYPES.has(message.message_type)) {
        await replyText(
          message.message_id,
          message.chat_id,
          `暂不支持 ${message.message_type || 'unknown'} 类型；可处理图片、文件、音频、视频和富文本。`,
          { phase: 'unsupported-message' },
        );
        return;
      }
      context.inbound = await stageInboundMessage({
        lark,
        message,
        cwd: __dirname,
        maxBytes: inboundMaxBytes,
      });
      if (!context.inbound.text && !context.inbound.assets.length) {
        await replyText(
          message.message_id,
          message.chat_id,
          '消息中没有可读取的文本或附件资源。',
          { phase: 'empty-inbound-resource' },
        );
        cleanupContextInbound(context);
        return;
      }
      command = normalizeCommand(
        context.inbound.text || `请处理我发送的${message.message_type}附件。`,
      );
    }

    const taskLink = authz.kind === 'direct' ? resolveTaskLinkReply(message) : null;
    if (taskLink && command) {
      if (!taskLinkOperatorMatches(taskLink, senderOpenId(sender))) {
        cleanupContextInbound(context);
        await replyText(message.message_id, message.chat_id, '该任务连接只允许创建连接时指定的飞书用户操作。', { phase: `task-link-operator-denied:${taskLink.taskKey}` });
        return;
      }
      if (taskLinkEffectiveState(taskLink) !== 'active') {
        cleanupContextInbound(context);
        await replyText(message.message_id, message.chat_id, '该任务连接已过期，请回到 CodexAssistant 重新连接。', { phase: `task-link-expired:${taskLink.taskKey}` });
        return;
      }
      logAccepted(context, 'task_link_continue');
      executeTaskLink(taskLink, command, context).catch((error) => sendFailureReply(context, error, 'task_link'));
      return;
    }

    const replyTaskRef = authz.kind === 'direct' ? taskMessageRefFromReply(message) : null;
    const replyTask = replyTaskRef?.task || null;
    if (replyTask && command) {
      logAccepted(context, 'task_continue_reply');
      context.progressMessageId = replyTaskRef.messageId;
      const result = continueTask(replyTask.id, command, context);
      const delivery = await deliverProgressResult(
        context,
        {
          status: 'processing',
          taskId: context.pendingTaskReply?.taskId,
          detail: '续跑任务已创建，正在执行。',
        },
        result,
        { phase: `task-continued:${replyTask.id}` },
      );
      registerPendingTaskReply(context, delivery.messageIds);
      logOutbound(context, 'task_continue_reply', result);
      if (!context.pendingTaskReply) cleanupContextInbound(context);
      return;
    }

    const defaultConversation = authz.kind === 'direct'
      ? resolveDefaultConversation(message)
      : null;
    if (defaultConversation && command) {
      if (!defaultConversationOperatorMatches(defaultConversation, {
        chatId: message.chat_id,
        operatorId: senderOpenId(sender),
      })) {
        cleanupContextInbound(context);
        await replyText(
          message.message_id,
          message.chat_id,
          '该对话卡只允许创建它的飞书用户继续操作。',
          { phase: 'default-conversation-operator-denied' },
        );
        return;
      }
      context.defaultConversation = updateDefaultConversation(defaultConversation.id, {
        messageIds: [message.message_id],
      });
      context.progressMessageId = defaultConversation.rootMessageId;
      context.reuseProgressCard = true;
      const parsedReplyRoute = parseRoute(command);
      const route = parsedReplyRoute.kind === 'project_promotion'
        ? parsedReplyRoute
        : { kind: 'default_chat', payload: command };
      logAccepted(context, route.kind === 'project_promotion'
        ? 'default_conversation_project_promotion'
        : 'default_conversation_reply');
      runCodexRouteInBackground(route, context);
      return;
    }

    const route = parseRoute(command);
    if (authz.kind === 'group' && route.kind !== 'help' && route.kind !== 'cmd') {
      logIgnored(context, route.kind, 'group_route_disabled');
      return;
    }

    if (['default_chat', 'task', 'project_promotion'].includes(route.kind)) {
      logAccepted(context, route.kind);
      runCodexRouteInBackground(route, context);
      return;
    }

    const result = await handleImmediateRoute(route, context);
    if (result === null) {
      logIgnored(context, route.kind, 'group_command_not_allowed');
      cleanupContextInbound(context);
      return;
    }
    const sentIds = await replyText(
      message.message_id,
      message.chat_id,
      result,
      { phase: `immediate:${route.kind}` },
    );
    registerPendingTaskReply(context, sentIds);
    logOutbound(context, route.kind, result);
    cleanupContextInbound(context);
  } catch (error) {
    cleanupContextInbound(context);
    await sendFailureReply({ message }, error, 'event');
    appendAudit('处理失败', [
      `- chat_id：${message.chat_id}`,
      `- message_id：${message.message_id}`,
      `- 输入：${safeOneLine(command, 500)}`,
      `- 错误：${safeOneLine(error.message || String(error), 700)}`,
    ]);
  }
}

async function updateCardActionMessage(event, card) {
  try {
    await lark.larkImPatchCard({ messageId: event.messageId, card, timeoutMs: 30000 });
    return true;
  } catch (patchError) {
    try {
      await lark.larkCardUpdateByToken({ token: event.token, card, timeoutMs: 30000 });
      return true;
    } catch (tokenError) {
      appendMessageLog({
        direction: 'card_followup_update_failed',
        at: new Date().toISOString(),
        messageId: event.messageId,
        patchError: safeOneLine(patchError.message || String(patchError), 300),
        tokenError: safeOneLine(tokenError.message || String(tokenError), 300),
      });
      return false;
    }
  }
}

async function handleChatFollowup(event, action, authz) {
  if (cardFollowupExecutions.has(event.messageId)) {
    appendMessageLog({
      direction: 'card_followup_ignored',
      at: new Date().toISOString(),
      messageId: event.messageId,
      reason: 'followup_already_running',
    });
    return;
  }
  const conversation = resolveDefaultConversation({ message_id: event.messageId });
  if (conversation && !defaultConversationOperatorMatches(conversation, {
    chatId: event.chatId,
    operatorId: event.operatorId,
  })) {
    appendMessageLog({
      direction: 'card_followup_denied',
      at: new Date().toISOString(),
      messageId: event.messageId,
      operatorFingerprint: auditFingerprint(event.operatorId),
      reason: 'operator_or_chat_does_not_match_default_conversation',
    });
    return;
  }
  cardFollowupExecutions.add(event.messageId);
  const context = {
    message: { chat_id: event.chatId, message_id: event.messageId },
    sender: { sender_id: { open_id: event.operatorId } },
    authz,
    progressMessageId: conversation?.rootMessageId || event.messageId,
    defaultConversation: conversation,
  };
  try {
    const parsedRoute = parseRoute(action.followup);
    const isProjectPromotion = parsedRoute.kind === 'project_promotion';
    await updateCardActionMessage(event, progressCard({
      status: 'processing',
      latestInput: action.followup,
      detail: isProjectPromotion
        ? '正在识别项目，并把当前对话升级为项目任务。'
        : '请求已经进入当前飞书 Codex 对话。',
      hideActions: true,
      schemaVersion: '2.0',
    }));
    if (isProjectPromotion) {
      const result = await promoteDefaultConversation(parsedRoute, context, conversation);
      logOutbound(context, 'project_promotion', result);
      return;
    }
    const result = await answerWithDefaultCodex(action.followup, context, { conversation });
    const promoted = await promoteDefaultConversationFromProjection(
      context,
      result,
      action.followup,
    );
    if (promoted) {
      logOutbound(context, 'project_promotion', result);
      return;
    }
    const durationSeconds = Math.round((context.lastCodexDurationMs || 0) / 1000);
    const fitted = fitProgressCardToRequestBudget({
      status: 'completed',
      followupEnabled: true,
      taskDurationSeconds: durationSeconds,
      latestInput: action.followup,
      detail: result,
    });
    const cardUpdated = fitted.card
      ? await updateCardActionMessage(event, fitted.card)
      : false;
    const cardComplete = cardUpdated && fitted.complete;
    let fallbackMessageIds = [];
    if (!cardComplete) {
      fallbackMessageIds = await replyText(
        event.messageId,
        event.chatId,
        result,
        { phase: `card-followup-result:${event.eventId}` },
      );
    }
    if (context.defaultConversation?.id) {
      extendDefaultConversation(context, { messageIds: fallbackMessageIds });
    }
    appendMessageLog({
      direction: 'card_followup_completed',
      at: new Date().toISOString(),
      messageId: event.messageId,
      durationMs: context.lastCodexDurationMs,
      contentLength: action.followup.length,
      delivery: cardComplete ? 'card' : 'text_fallback',
    });
  } catch (error) {
    const cardUpdated = await updateCardActionMessage(event, progressCard({
      status: 'failed',
      followupEnabled: true,
      latestInput: action.followup,
      detail: `继续追问失败：${safeOneLine(error.message || String(error), 1200)}`,
    }));
    await sendFailureReply(context, error, 'card_followup', { cardUpdated });
  } finally {
    cardFollowupExecutions.delete(event.messageId);
  }
}

async function handleTaskLinkFollowup(event, action, authz, link) {
  const context = {
    message: { chat_id: event.chatId, message_id: event.messageId },
    sender: { sender_id: { open_id: event.operatorId } },
    authz,
    progressMessageId: event.messageId,
    taskLinkLatestInput: action.followup,
  };
  taskLinkLatestInputs.set(link.id, action.followup);
  taskLinkFollowupTransitions.set(
    link.threadId,
    Number(taskLinkFollowupTransitions.get(link.threadId) || 0) + 1,
  );
  try {
    const projected = updateTaskLink(
      link.id,
      { ...taskLinkFollowupProjection(link), inputCapture: null },
      undefined,
      { renew: true },
    );
    await patchTaskLinkCard(
      projected,
      'processing',
      projected.progress.detail,
      projected.progress,
      action.followup,
    );
    await executeTaskLink(projected, action.followup, context);
  } finally {
    const remainingTransitions = Number(taskLinkFollowupTransitions.get(link.threadId) || 0) - 1;
    if (remainingTransitions > 0) taskLinkFollowupTransitions.set(link.threadId, remainingTransitions);
    else taskLinkFollowupTransitions.delete(link.threadId);
  }
}

async function handleCardAction(data) {
  const event = normalizeCardAction(data);
  if (!event.eventId || !event.chatId || !event.messageId || !event.operatorId || !event.token) {
    appendMessageLog({
      direction: 'card_action_ignored',
      at: new Date().toISOString(),
      reason: 'missing_required_fields',
    });
    return;
  }
  if (!rememberMessage(`card:${event.eventId}`)) return;
  const action = bridgeCardAction(event);
  if (!action) {
    appendMessageLog({
      direction: 'card_action_ignored',
      at: new Date().toISOString(),
      eventFingerprint: auditFingerprint(event.eventId),
      reason: 'unsupported_bridge_action',
    });
    return;
  }

  const message = { chat_id: event.chatId, message_id: event.messageId };
  const sender = { sender_id: { open_id: event.operatorId } };
  const authz = await authorizeMessage(message, sender);
  if (!authz.allowed) {
    appendMessageLog({
      direction: 'card_action_denied',
      at: new Date().toISOString(),
      action: action.action,
      operatorFingerprint: auditFingerprint(event.operatorId),
      reason: authz.reason,
    });
    return;
  }

  let card;
  if (action.action === 'chat_followup') {
    await handleChatFollowup(event, action, authz);
  } else if (action.action === 'dismiss') {
    const conversation = resolveDefaultConversation({ message_id: event.messageId });
    if (conversation) {
      if (!defaultConversationOperatorMatches(conversation, {
        chatId: event.chatId,
        operatorId: event.operatorId,
      })) {
        appendMessageLog({
          direction: 'default_conversation_dismiss_denied',
          at: new Date().toISOString(),
          messageId: event.messageId,
          operatorFingerprint: auditFingerprint(event.operatorId),
        });
        return;
      }
      updateDefaultConversation(conversation.id, { state: 'closed' });
    }
    card = progressCard({ status: 'dismissed', schemaVersion: '2.0' });
  } else if (action.action === 'task_status') {
    const task = resolveTaskRef(action.taskId);
    card = task
      ? progressCard({
          status: task.status === 'completed' ? 'completed' : task.status === 'failed' ? 'failed' : 'processing',
          taskId: task.id,
          detail: [
            `状态：${task.status || '-'}`,
            task.completedAt ? `完成时间：${task.completedAt}` : '',
            Number.isFinite(task.durationMs)
              ? `本次任务耗时：${formatElapsedDuration(Math.round(task.durationMs / 1000))}`
              : '',
            task.result ? `结果：${safeOneLine(task.result, 900)}` : '',
            task.error ? `错误：${safeOneLine(task.error, 900)}` : '',
          ].filter(Boolean).join('\n'),
          openIds: [event.operatorId],
        })
      : progressCard({
          status: 'failed',
          taskId: action.taskId,
          detail: '未找到该任务，可能已超出本机任务日志的可用范围。',
          openIds: [event.operatorId],
        });
  } else if ([
    'task_link_detail', 'task_link_refresh', 'task_link_interrupt', 'task_link_release',
    'task_link_answer', 'task_link_followup', 'task_link_capture', 'task_link_capture_cancel',
    'task_link_mode',
  ].includes(action.action)) {
    const link = findTaskLinkByKey(action.taskKey);
    if (!link) {
      card = progressCard({ status: 'failed', detail: '未找到该任务连接。', openIds: [event.operatorId] });
    } else if (!taskLinkOperatorMatches(link, event.operatorId)) {
      appendMessageLog({
        direction: 'task_link_card_denied', at: new Date().toISOString(),
        taskKey: link.taskKey, operatorFingerprint: auditFingerprint(event.operatorId),
        reason: 'operator_does_not_match_link_target',
      });
      return;
    } else if (![link.rootMessageId, ...(link.messageIds || [])].includes(event.messageId)) {
      appendMessageLog({
        direction: 'task_link_card_denied', at: new Date().toISOString(),
        taskKey: link.taskKey, operatorFingerprint: auditFingerprint(event.operatorId),
        reason: 'card_message_does_not_match_link',
      });
      return;
    } else if (taskLinkEffectiveState(link) !== 'active' && action.action !== 'task_link_detail') {
      card = fittedTaskLinkCard(
        link,
        'dismissed',
        link.linkState === 'released' ? '任务连接已解除。' : '任务连接已过期。',
        { openIds: [event.operatorId] },
      );
    } else if (action.action === 'task_link_followup') {
      await handleTaskLinkFollowup(event, action, authz, link);
    } else if (['task_link_capture', 'task_link_capture_cancel'].includes(action.action)) {
      const refreshed = updateTaskLink(link.id, { inputCapture: null });
      card = fittedTaskLinkCard(
        refreshed,
        refreshed.turnState,
        refreshed.detailSummary || refreshed.progress?.detail || '等待飞书指令',
        { openIds: [event.operatorId] },
      );
    } else if (action.action === 'task_link_mode') {
      try {
        const supportedModes = await codexAppServer.collaborationModes();
        if (!supportedModes.includes(action.mode)) {
          throw new Error('当前 Codex Desktop 不支持该协作模式，请升级后重试');
        }
        const snapshot = await readTaskLinkSnapshot(link.threadId);
        const collaborationMode = taskLinkCollaborationMode(
          { ...link, nextTurnMode: action.mode },
          snapshot,
        );
        await codexDesktopTaskController.updateCollaborationMode({
          threadId: link.threadId,
          collaborationMode,
        });
        const updated = updateTaskLink(link.id, {
          nextTurnMode: action.mode,
        }, undefined, { renew: true });
        card = fittedTaskLinkCard(
          updated,
          updated.turnState,
          updated.detailSummary || updated.progress?.detail || '等待飞书指令',
          { openIds: [event.operatorId] },
        );
      } catch (error) {
        await replyText(
          event.messageId,
          event.chatId,
          `模式切换失败：${safeOneLine(error.message || String(error), 240)}`,
          { phase: `task-link-mode-error:${event.eventId}` },
        );
        card = fittedTaskLinkCard(
          link,
          link.turnState,
          link.detailSummary || link.progress?.detail || '等待飞书指令',
          { openIds: [event.operatorId] },
        );
      }
    } else if (action.action === 'task_link_release') {
      const released = updateTaskLink(link.id, {
        linkState: 'released', pendingMessageId: '', pendingCleanupDir: '', inputCapture: null,
      });
      taskLinkQueuedContexts.delete(link.threadId);
      cancelTaskLinkCardPush(link.id);
      await taskLinkCardPushState(link.id).writeChain;
      taskLinkLatestInputs.delete(link.id);
      taskLinkFinalResults.delete(link.id);
      cleanupTaskLinkAssetDirectories(link);
      refreshTaskLinkWakeAssertion();
      card = fittedTaskLinkCard(
        released,
        'dismissed',
        '任务连接已解除。',
        { openIds: [event.operatorId] },
      );
    } else if (action.action === 'task_link_interrupt') {
      cancelTaskLinkCardPush(link.id);
      await taskLinkCardPushState(link.id).writeChain;
      const turnId = link.activeTurnId;
      if (turnId) {
        await codexDesktopTaskController.interrupt({
          threadId: link.threadId,
          turnId,
        }).catch(() => false);
      }
      const interruptedAt = Date.now();
      const startedAt = Date.parse(link.progress?.startedAt || '');
      const interrupted = updateTaskLink(link.id, {
        turnState: 'interrupted', turnOwner: 'none', actionRequired: 'none', activeTurnId: '',
        pendingMessageId: '', inputCapture: null, progress: {
          ...link.progress, phase: '已停止', detail: '当前 Codex 轮次已停止，任务连接继续有效。',
          durationSeconds: Number.isFinite(startedAt)
            ? Math.max(0, Math.round((interruptedAt - startedAt) / 1000))
            : link.progress?.durationSeconds,
        },
      }, undefined, { terminalAt: new Date().toISOString() });
      cleanupTaskLinkAssetDirectories(interrupted);
      card = fittedTaskLinkCard(
        interrupted,
        'interrupted',
        interrupted.progress.detail,
        { openIds: [event.operatorId] },
      );
    } else if (action.action === 'task_link_answer') {
      taskLinkLatestInputs.set(link.id, action.answer);
      let answered = await answerTaskLinkUserInput(
        link.threadId,
        { [action.questionId]: { answers: [action.answer] } },
      );
      if (!answered && !taskLinkInputRequests.has(link.threadId)) {
        const snapshot = await readTaskLinkSnapshot(link.threadId).catch(() => null);
        trackDesktopTaskLinkInput(link.threadId, snapshot?.journalTurn);
        answered = await answerTaskLinkUserInput(
          link.threadId,
          { [action.questionId]: { answers: [action.answer] } },
        );
      }
      const resumed = answered ? updateTaskLink(link.id, {
        turnState: 'running',
        turnOwner: ['bridge', 'desktop'].includes(link.turnOwner) ? link.turnOwner : 'bridge',
        actionRequired: 'none',
        inputCapture: null,
        progress: { ...link.progress, phase: '执行', detail: '已收到你的选择。' },
      }, undefined, { renew: true }) : link;
      card = fittedTaskLinkCard(
        resumed,
        answered ? 'processing' : 'failed',
        answered ? '已收到你的选择，Codex 继续执行。' : '该选择请求已失效，请回复任务消息继续。',
        { latestInput: action.answer, openIds: [event.operatorId] },
      );
    } else {
      if (action.action === 'task_link_refresh') await refreshTaskLinks();
      const refreshed = readTaskLinkStore().links.find((item) => item.id === link.id) || link;
      card = fittedTaskLinkCard(
        refreshed,
        refreshed.turnState,
        refreshed.detailSummary || '详细反馈仅包含经过脱敏的阶段、文件数量、验证状态和错误摘要。',
        { openIds: [event.operatorId] },
      );
    }
    if (card && link) {
      await patchPreparedTaskLinkCard(link, card, { cardToken: event.token });
      card = null;
    }
  } else {
    card = progressCard({
      status: 'status',
      detail: [
        `桥状态：online`,
        `能力版本：${RUNTIME_MANIFEST.capabilityVersion}`,
        `事件传输：${eventTransport}`,
        `事件消费者：${eventConsumerRunningCount()}/${eventConsumerKeys.length}`,
        `桥进程已运行：${formatElapsedDuration(process.uptime())}`,
        Number.isInteger(action.taskDurationSeconds)
          ? `本次任务耗时：${formatElapsedDuration(action.taskDurationSeconds)}`
          : '',
      ].filter(Boolean).join('\n'),
      taskDurationSeconds: action.taskDurationSeconds,
      openIds: [event.operatorId],
    });
  }

  if (card) await lark.larkCardUpdateByToken({ token: event.token, card, timeoutMs: 30000 });
  appendMessageLog({
    direction: 'card_action_completed',
    at: new Date().toISOString(),
    action: action.action,
    taskId: action.taskId || undefined,
    taskKey: action.taskKey || undefined,
    eventFingerprint: auditFingerprint(event.eventId),
    operatorFingerprint: auditFingerprint(event.operatorId),
  });
  appendAudit('卡片交互', [
    `- action：${action.action}`,
    `- event：${auditFingerprint(event.eventId)}`,
    `- operator：${auditFingerprint(event.operatorId)}`,
    action.taskId ? `- task：${action.taskId}` : '',
    '- status：completed',
  ].filter(Boolean));
}

function normalizeFeishuEvent(raw) {
  if (!raw || typeof raw !== 'object') return null;
  if (raw.message) return raw;
  if (raw.event?.message) return raw.event;
  if (raw.data?.event?.message) return raw.data.event;
  if (raw.data?.message) return raw.data;
  if (raw.payload?.event?.message) return raw.payload.event;
  if (raw.message_id && raw.chat_id) {
    return {
      message: {
        message_id: raw.message_id || raw.id,
        root_id: raw.root_id || '',
        parent_id: raw.parent_id || '',
        create_time: raw.create_time || raw.timestamp || '',
        chat_id: raw.chat_id,
        chat_type: raw.chat_type,
        message_type: raw.message_type,
        content: raw.content,
      },
      sender: {
        sender_id: {
          open_id: raw.sender_id || raw.open_id || '',
          union_id: raw.sender_union_id || raw.union_id || '',
          user_id: raw.sender_user_id || raw.user_id || '',
        },
        sender_type: raw.sender_type || '',
      },
    };
  }
  return null;
}

function dispatchInboundEvent(eventKey, event) {
  if (eventInbox) {
    try {
      eventInbox.receive(eventKey, event);
    } catch (error) {
      appendMessageLog({
        direction: 'event_inbox_error',
        at: new Date().toISOString(),
        eventKey,
        error: safeOneLine(error.message || String(error), 300),
      });
    }
  }
  if (groupDirectoryEventRequiresRefresh(eventKey)) {
    groupDirectoryService.scheduleRefresh(`event:${eventKey}`);
  }
  if (eventKey === 'im.message.receive_v1') return handleFeishuMessage(event);
  if (eventKey === 'card.action.trigger') return handleCardAction(event);
  return Promise.resolve();
}

async function startEventConsumer() {
  if (!eventConsumerEnabled) {
    updateEventTransportState({ status: 'disabled', errorCode: '', connection: { state: 'idle' } });
    for (const eventKey of eventConsumerKeys) updateEventConsumerState(eventKey, { status: 'disabled' });
    console.log('Event consumer disabled.');
    return;
  }
  if (eventTransport !== OFFICIAL_EVENT_TRANSPORT) {
    throw new Error(`unsupported FEISHU_EVENT_TRANSPORT: ${eventTransport}`);
  }
  if (officialEventAdapter) return;

  const credentials = loadOfficialCredentials({
    env: process.env,
    homeDir,
    platform: process.platform,
    projectRoot: __dirname,
  });
  const handlers = Object.fromEntries(eventConsumerKeys.map((eventKey) => [
    eventKey,
    (data) => dispatchInboundEvent(eventKey, data),
  ]));
  officialEventAdapter = createOfficialEventAdapter({
    credentials,
    eventKeys: eventConsumerKeys,
    handlers,
    onState: (state) => updateEventTransportState(state),
    onEventState: (eventKey, state) => updateEventConsumerState(eventKey, state),
    onHandlerError: (eventKey, error) => {
      appendMessageLog({
        direction: 'event_consumer_handler_error',
        at: new Date().toISOString(),
        eventKey,
        error: safeOneLine(error.message || String(error), 900),
      });
    },
  });
  updateEventTransportState({ status: 'starting', errorCode: '', connection: { state: 'connecting' } });
  for (const eventKey of eventConsumerKeys) {
    updateEventConsumerState(eventKey, { status: 'starting', errorCode: '', restartScheduled: false });
  }
  console.log(`Starting official SDK event transport (${credentials.source}); keys=${eventConsumerKeys.join(',') || '-'}`);
  try {
    await officialEventAdapter.start();
    appendMessageLog({
      direction: 'event_transport_connected',
      at: new Date().toISOString(),
      transport: eventTransport,
      eventKeys: eventConsumerKeys,
    });
  } catch (error) {
    updateEventTransportState({
      status: 'failed',
      errorCode: 'sdk_connection_error',
      connection: { state: 'failed' },
    });
    appendMessageLog({
      direction: 'event_transport_failed',
      at: new Date().toISOString(),
      transport: eventTransport,
      error: safeOneLine(error.message || String(error), 900),
    });
    try {
      await officialEventAdapter.stop();
    } catch {
      // Best-effort cleanup after a failed handshake.
    }
    officialEventAdapter = null;
    throw error;
  }
}

async function stopEventConsumer() {
  if (!officialEventAdapter) return;
  const adapter = officialEventAdapter;
  officialEventAdapter = null;
  await adapter.stop();
}

async function reconcileEventConsumerProfile(reason = 'poll') {
  let selection;
  try {
    selection = readEventConsumerProfile(eventConsumerProfilePath);
  } catch (error) {
    const signature = `invalid:${safeOneLine(error.message || String(error), 240)}`;
    if (signature === lastEventConsumerProfileSignature) return;
    lastEventConsumerProfileSignature = signature;
    activeEventConsumerProfile = 'invalid';
    await stopEventConsumer();
    updateEventTransportState({
      status: 'disabled',
      errorCode: 'invalid_event_consumer_profile',
      profile: 'invalid',
      desiredConnection: false,
      connection: { state: 'idle' },
    });
    for (const eventKey of eventConsumerKeys) {
      updateEventConsumerState(eventKey, { status: 'disabled', errorCode: 'invalid_event_consumer_profile' });
    }
    appendMessageLog({
      direction: 'event_consumer_profile_rejected',
      at: new Date().toISOString(),
      reason,
      error: safeOneLine(error.message || String(error), 300),
    });
    return;
  }

  const signature = `${selection.source}:${selection.profile}:${selection.updatedAt}`;
  if (signature === lastEventConsumerProfileSignature) return;
  lastEventConsumerProfileSignature = signature;
  const desiredConnection = eventConsumerShouldConnect(selection.profile, eventConsumerEnabled);
  const previousProfile = activeEventConsumerProfile || undefined;
  activeEventConsumerProfile = selection.profile;
  updateEventTransportState({
    profile: selection.profile,
    profileSource: selection.source,
    desiredConnection,
    errorCode: '',
  });

  if (desiredConnection) {
    await startEventConsumer();
  } else {
    await stopEventConsumer();
    updateEventTransportState({
      status: 'disabled',
      errorCode: '',
      profile: selection.profile,
      profileSource: selection.source,
      desiredConnection: false,
      connection: { state: 'idle' },
    });
    for (const eventKey of eventConsumerKeys) {
      updateEventConsumerState(eventKey, { status: 'disabled', errorCode: '' });
    }
  }

  appendMessageLog({
    direction: 'event_consumer_profile_applied',
    at: new Date().toISOString(),
    reason,
    previousProfile,
    profile: selection.profile,
    desiredConnection,
  });
  console.log(`Event consumer profile: ${selection.profile} (${desiredConnection ? 'connected' : 'disconnected'})`);
}

function scheduleEventConsumerProfileReconcile(reason) {
  if (eventConsumerProfileReconcilePromise) return eventConsumerProfileReconcilePromise;
  eventConsumerProfileReconcilePromise = reconcileEventConsumerProfile(reason)
    .finally(() => { eventConsumerProfileReconcilePromise = null; });
  return eventConsumerProfileReconcilePromise;
}

async function startEventConsumerProfileController() {
  await scheduleEventConsumerProfileReconcile('startup');
  eventConsumerProfileTimer = setInterval(() => {
    scheduleEventConsumerProfileReconcile('runtime_change').catch((error) => {
      updateEventTransportState({
        status: 'failed',
        errorCode: 'event_consumer_profile_apply_failed',
        profile: activeEventConsumerProfile || 'unknown',
      });
      appendMessageLog({
        direction: 'event_consumer_profile_apply_failed',
        at: new Date().toISOString(),
        error: safeOneLine(error.message || String(error), 300),
      });
    });
  }, eventConsumerProfilePollMs);
}

let shutdownStarted = false;

async function shutdown(signal = 'SIGTERM') {
  if (shutdownStarted) return;
  shutdownStarted = true;
  if (outboxPollTimer) clearInterval(outboxPollTimer);
  if (docboxPollTimer) clearInterval(docboxPollTimer);
  if (actionboxPollTimer) clearInterval(actionboxPollTimer);
  if (eventConsumerProfileTimer) clearInterval(eventConsumerProfileTimer);
  if (taskLinkLeaseTimer) clearInterval(taskLinkLeaseTimer);
  for (const stop of taskLinkJournalWatchers.values()) stop();
  taskLinkJournalWatchers.clear();
  for (const linkId of taskLinkCardPushes.keys()) cancelTaskLinkCardPush(linkId);
  taskLinkFinalResults.clear();
  taskLinkFollowupTransitions.clear();
  if (taskLinkCaffeinate) { taskLinkCaffeinate.kill(); taskLinkCaffeinate = null; }
  directoryService.stop();
  groupDirectoryService.stop();
  if (wakeServer) wakeServer.close();
  if (docboxWakeServer) docboxWakeServer.close();
  if (actionboxWakeServer) actionboxWakeServer.close();
  try {
    await stopEventConsumer();
  } catch {
    // Best-effort transport shutdown.
  }
  await codexAppServer.close();
  releaseInstanceLock();
  process.exit(signal === 'SIGINT' ? 130 : 0);
}

process.on('SIGINT', () => { void shutdown('SIGINT'); });
process.on('SIGTERM', () => { void shutdown('SIGTERM'); });
process.on('exit', releaseInstanceLock);

async function main() {
  try {
    acquireInstanceLock();
    const larkCliVersion = ensureLarkCliReady();
    console.log(`Starting Feishu bot bridge ${RUNTIME_MANIFEST.bridgeVersion} (package ${RUNTIME_MANIFEST.packageVersion})...`);
    console.log(`Lark CLI: ${larkCliVersion}`);
    console.log(`Lark CLI profile: ${larkCliProfile || 'default'}`);
    console.log(`Lark CLI identity: ${larkCliAs}`);
    console.log(`Event consumer enabled: ${eventConsumerEnabled}`);
    console.log(`Event profile poll ms: ${eventConsumerProfilePollMs}`);
    console.log(`Event transport: ${eventTransport}`);
    console.log(`Event consumer keys: ${eventConsumerKeys.join(',') || '-'}`);
    console.log(`Event inbox: ${eventInboxEnabled ? eventInboxDir : 'disabled'}`);
    console.log(`Inbound attachment limit bytes: ${inboundMaxBytes}`);
    console.log(`Message log: ${messageLogPath}`);
    console.log(`Task log: ${taskLogPath}`);
    console.log(`Task run logs: ${taskRunsDir}`);
    console.log(`Sessions: ${sessionsPath}`);
    console.log(`Knowledge management audit dir: ${auditDir}`);
    console.log(`Outbox enabled: ${outboundEnabled}`);
    console.log(`Outbox path: ${outboxPath}`);
    console.log(`Outbox results: ${outboxResultsPath}`);
    console.log(`Docbox enabled: ${docboxEnabled}`);
    console.log(`Docbox path: ${docboxPath}`);
    console.log(`Docbox results: ${docboxResultsPath}`);
    console.log(`Actionbox enabled: ${actionboxEnabled}`);
    console.log(`Actionbox path: ${actionboxPath}`);
    console.log(`Actionbox results: ${actionboxResultsPath}`);
    console.log(`Directory enabled: ${directoryEnabled}`);
    console.log(`Directory refresh ms: ${directoryRefreshMs}`);
    console.log(`Group directory enabled: ${groupDirectoryEnabled}`);
    console.log(`Group directory refresh ms: ${groupDirectoryRefreshMs}`);
    console.log(`Codex: ${getCodexVersion()}`);
    hydrateSeenMessageIds();
    console.log(`Hydrated message dedupe ids: ${seenMessageIds.size}`);
    hydrateTaskMessageIndex();
    console.log(`Hydrated task message refs: ${taskMessageIndex.size}`);
    retireLegacyDefaultSession();
    console.log(`Active default conversations: ${readDefaultConversationStore().conversations.filter((item) => item.state === 'active').length}`);
    startOutboxWorker();
    startWakeServer();
    startDocboxWorker();
    startDocboxWakeServer();
    startActionboxWorker();
    startActionboxWakeServer();
    const migratedTaskLinks = migrateTaskLinkStore();
    for (const link of migratedTaskLinks.links) {
      if (link.inputCapture) updateTaskLink(link.id, { inputCapture: null });
      if (taskLinkEffectiveState(link) !== 'active') cleanupTaskLinkAssetDirectories(link);
    }
    refreshTaskLinkWakeAssertion();
    taskLinkLeaseTimer = setInterval(() => {
      refreshTaskLinkWakeAssertion();
      refreshTaskLinks().catch(() => {});
      processQueuedTaskLinks().catch((error) => appendMessageLog({ direction: 'task_link_queue_failed', at: new Date().toISOString(), error: safeOneLine(error.message, 300) }));
    }, 2 * 1000);
    directoryService.start((error, result) => {
      if (error) {
        console.error(`Directory sync failed: ${safeOneLine(directoryService.status().lastError || 'unknown_error', 240)}`);
        return;
      }
      console.log(`Directory synced: users=${result.userCount}, departments=${result.departmentCount}`);
    });
    groupDirectoryService.start((error, result) => {
      if (error) {
        console.error(`Group directory sync failed: ${safeOneLine(groupDirectoryService.status().lastError || 'unknown_error', 240)}`);
        return;
      }
      console.log(`Group directory synced: groups=${result.groupCount}`);
    });
    await startEventConsumerProfileController();
  } catch (error) {
    console.error(error.message || String(error));
    process.exit(1);
  }
}

void main();
