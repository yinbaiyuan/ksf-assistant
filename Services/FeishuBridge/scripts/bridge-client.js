#!/usr/bin/env node

const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn, spawnSync } = require('node:child_process');
const {
  buildRuntime,
  cleanupStagedMedia,
  collectStatus,
  controlBridgeService,
  createLark,
  defaultClientConfigPath,
  directAllowedOpenIds,
  documentIdentityForTarget,
  doctor,
  fingerprintIdentifier,
  initializeClientConfig,
  inspectDocument,
  isProcessAlive,
  loadClientConfig,
  MESSAGE_TYPES,
  parseBool,
  parseCsv,
  parseNumber,
  readCompleteJsonl,
  recentRecords,
  requestId,
  resolveDocumentTarget,
  resolveMessageTarget,
  resolveTestAssetBindings,
  sanitizeForOutput,
  sanitizeReadForOutput,
  sanitizeTarget,
  saveTestAssetBinding,
  stageOutboundMedia,
  submitQueueRequest,
  validateCapabilityLocationTarget,
  validateDocumentRequest,
  validateHighImpactUpdate,
  validateMessageRequest,
  validateBoundedTimeRange,
  validateReadLimit,
  writeSecureJson,
} = require('../lib/bridge-client-core');
const {
  inspectEventConsumerProfile,
  publicEventConsumerProfileCatalog,
  writeEventConsumerProfile,
} = require('../lib/event-consumer-profile');
const { auditFingerprint, auditTargetDescriptor } = require('../lib/audit-policy');
const { validateActionRequest } = require('../lib/action-registry');
const { executeRegisteredCapability } = require('../lib/capability-executor');
const {
  capability,
  capabilityCatalog,
  docWhiteboardXml,
  publicCapability,
  selectCapabilityResultIdentifier,
  validateCapabilityInput,
} = require('../lib/capability-registry');
const { resolveBaseToken } = require('../lib/work-actions');
const { capabilityManifest, permissionReport } = require('../lib/capability-policy');
const { FIXED_EVENT_KEYS, createEventInbox, fingerprint } = require('../lib/event-inbox');
const { publicWatchCatalog, updateEventWatch, watchDefinition } = require('../lib/event-subscriptions');
const { TASK_LINK_CARD_REVISION, progressCard } = require('../lib/progress-card');
const { cleanupInboundAssets } = require('../lib/inbound-media');
const {
  PROTOCOL: TASK_LINK_PROTOCOL,
  SCHEMA_VERSION: TASK_LINK_SCHEMA_VERSION,
  findLinkByTaskKey,
  listPublic: listTaskLinks,
  publicLink,
  updateLink,
  upsertLink,
} = require('../lib/task-link-store');
const {
  latestTurn,
  publicTurnTiming,
  publicTurnState,
  validateAuthoritativeThread,
} = require('../lib/codex-task-control');
const { CodexDesktopTaskController, desktopEndpointReady } = require('../lib/codex-desktop-ipc');
const { chmodPrivate, defaultDesktopIPCPath } = require('../lib/platform-runtime');
const {
  createDirectoryService,
  normalizeDirectoryName,
} = require('../lib/contact-directory');
const {
  createGroupDirectoryService,
  normalizeGroupName,
} = require('../lib/group-directory');
const {
  configureExisting,
  finishUser,
  findValue,
  startConfig,
  startUser,
} = require('../lib/auth-flow');
const { loadRuntimeEnvironment } = require('../lib/runtime-env');

const projectRoot = path.resolve(__dirname, '..');
Object.assign(process.env, loadRuntimeEnvironment(projectRoot));

function camelCaseFlag(name) {
  return name.replace(/-([a-z])/g, (_, letter) => letter.toUpperCase());
}

function parseArgs(argv) {
  const positional = [];
  const flags = {};
  for (let index = 0; index < argv.length; index += 1) {
    const item = argv[index];
    if (!item.startsWith('--')) {
      positional.push(item);
      continue;
    }
    const equals = item.indexOf('=');
    if (equals > 2) {
      flags[camelCaseFlag(item.slice(2, equals))] = item.slice(equals + 1);
      continue;
    }
    const key = camelCaseFlag(item.slice(2));
    const next = argv[index + 1];
    if (next !== undefined && !next.startsWith('--')) {
      flags[key] = next;
      index += 1;
    } else {
      flags[key] = true;
    }
  }
  return { positional, flags };
}

function printJson(value) {
  process.stdout.write(`${JSON.stringify(value, null, 2)}\n`);
}

function readStdin() {
  return new Promise((resolve, reject) => {
    let content = '';
    process.stdin.setEncoding('utf8');
    process.stdin.on('data', (chunk) => { content += chunk; });
    process.stdin.on('end', () => resolve(content));
    process.stdin.on('error', reject);
  });
}

async function readPayload(flags, flagName) {
  const filePath = flags[flagName];
  if (filePath && filePath !== '-') return fs.readFileSync(path.resolve(filePath), 'utf8');
  if (filePath === '-' || !process.stdin.isTTY) return readStdin();
  throw new Error(`provide content through stdin or --${flagName.replace(/[A-Z]/g, (letter) => `-${letter.toLowerCase()}`)}`);
}

function requireValue(value, label) {
  if (value === undefined || value === null || value === '') throw new Error(`missing ${label}`);
  return value;
}

function boundedInteger(value, { minimum, maximum, fallback, label }) {
  const candidate = value === undefined || value === null || value === '' ? fallback : Number(value);
  if (!Number.isInteger(candidate)) throw new Error(`${label} must be an integer`);
  if (candidate < minimum || candidate > maximum) {
    throw new Error(`${label} must be between ${minimum} and ${maximum}`);
  }
  return candidate;
}

function configPathFrom(flags, runtime) {
  return path.resolve(String(
    flags.config
    || process.env.FEISHU_BRIDGE_CLIENT_CONFIG
    || (runtime ? path.join(runtime.dataRoot, 'client.json') : defaultClientConfigPath()),
  ));
}

function loadContext(flags) {
  const runtime = buildRuntime(projectRoot);
  const configPath = configPathFrom(flags, runtime);
  const config = loadClientConfig(configPath);
  return { runtime, configPath, config };
}

function pidAlive(runtime) {
  try {
    const state = JSON.parse(fs.readFileSync(runtime.pidPath, 'utf8'));
    return isProcessAlive(state.pid);
  } catch {
    return false;
  }
}

function buildDirectoryService(runtime) {
  return createDirectoryService({
    lark: createLark(runtime, { as: 'bot' }),
    cachePath: runtime.directoryCachePath,
    statePath: runtime.directoryStatePath,
    enabled: parseBool(runtime.env.FEISHU_DIRECTORY_ENABLED),
    refreshMs: parseNumber(runtime.env.FEISHU_DIRECTORY_REFRESH_MS, 6 * 60 * 60 * 1000),
    maxAgeMs: parseNumber(runtime.env.FEISHU_DIRECTORY_MAX_AGE_MS, 24 * 60 * 60 * 1000),
    pageSize: Math.max(1, Math.min(50, parseNumber(runtime.env.FEISHU_DIRECTORY_PAGE_SIZE, 50))),
    minimumUserCount: Math.max(1, parseNumber(runtime.env.FEISHU_DIRECTORY_MIN_USER_COUNT, 1)),
  });
}

function buildGroupDirectoryService(runtime) {
  return createGroupDirectoryService({
    lark: createLark(runtime, { as: 'bot' }),
    cachePath: runtime.groupDirectoryCachePath,
    statePath: runtime.groupDirectoryStatePath,
    enabled: parseBool(runtime.env.FEISHU_GROUP_DIRECTORY_ENABLED),
    refreshMs: parseNumber(runtime.env.FEISHU_GROUP_DIRECTORY_REFRESH_MS, 30 * 60 * 1000),
    maxAgeMs: parseNumber(runtime.env.FEISHU_GROUP_DIRECTORY_MAX_AGE_MS, 2 * 60 * 60 * 1000),
    pageSize: Math.max(1, Math.min(100, parseNumber(runtime.env.FEISHU_GROUP_DIRECTORY_PAGE_SIZE, 100))),
  });
}

async function ensureBridgeRunning(runtime, config, { autoStart = true } = {}) {
  if (pidAlive(runtime)) return { alreadyRunning: true };
  if (!autoStart) throw new Error('bridge is not running');
  if (runtime.env.CODEX_USAGE_BAR_MANAGED === '1') {
    throw new Error('managed bridge is not ready');
  }
  const action = controlBridgeService(config, {
    restart: false,
    platform: runtime.platform,
    projectRoot: runtime.projectRoot,
    env: runtime.env,
  });
  for (let index = 0; index < 25; index += 1) {
    if (pidAlive(runtime)) return { ...action, running: true };
    await new Promise((resolve) => setTimeout(resolve, 200));
  }
  throw new Error('bridge launch was accepted but the process did not become ready');
}

function docboxSourceAllowed(runtime, source) {
  const allowed = parseCsv(runtime.env.FEISHU_DOCBOX_ALLOWED_SOURCES || 'local,codex');
  return allowed.includes('*') || allowed.includes(source);
}

function actionboxSourceAllowed(runtime, source) {
  const allowed = parseCsv(runtime.env.FEISHU_ACTIONBOX_ALLOWED_SOURCES || 'local,codex');
  return allowed.includes('*') || allowed.includes(source);
}

function appendClientAudit(runtime, category, fields) {
  const date = new Date().toISOString().slice(0, 10);
  const auditPath = path.join(runtime.auditDir, date, 'client-reads.md');
  fs.mkdirSync(path.dirname(auditPath), { recursive: true, mode: 0o700 });
  const lines = [
    `## ${category}`,
    '',
    `- at：${new Date().toISOString()}`,
    ...Object.entries(fields).map(([key, value]) => `- ${key}：${String(value ?? '-')}`),
    '',
  ];
  fs.appendFileSync(auditPath, `${lines.join('\n')}\n`, { encoding: 'utf8', mode: 0o600 });
  chmodPrivate(auditPath, 0o600);
}

async function resolveGroupReadTarget(flags, runtime, config) {
  const targetFlags = [flags.target, flags.targetGroupName].filter(Boolean);
  if (targetFlags.length !== 1) throw new Error('provide exactly one of --target or --target-group-name');
  if (flags.targetGroupName) {
    const directory = buildGroupDirectoryService(runtime);
    if (!directory.status().enabled) throw new Error('group directory feature is disabled');
    await directory.ensureFresh('read_by_group_name');
    const resolution = directory.resolve(flags.targetGroupName, config);
    if (resolution.status !== 'resolved') {
      return { resolution, target: undefined };
    }
    return { resolution, target: resolution.target };
  }
  const target = resolveMessageTarget(flags.target, config);
  if (target.type !== 'chat_id') throw new Error('message history reads currently require a group chat target');
  return { target };
}

async function handleMessage(args, flags) {
  const action = requireValue(args[0], 'message action (list|search|thread)');
  const { runtime, config } = loadContext(flags);
  const lark = createLark(runtime, { as: 'bot' });
  lark.ensureReady();
  let response;
  let auditFields;
  if (action === 'thread') {
    const threadId = (await readPayload(flags, 'messageIdFile')).trim();
    if (!/^(?:om|omt)_[A-Za-z0-9_-]+$/.test(threadId)) throw new Error('thread input must be an om_ or omt_ identifier');
    const limit = validateReadLimit(flags.limit, 50, 50);
    const order = String(flags.order || 'asc');
    if (!['asc', 'desc'].includes(order)) throw new Error('order must be asc or desc');
    response = await lark.larkImListThread({
      threadId,
      pageSize: limit,
      order,
      timeoutMs: parseNumber(flags.timeoutMs, 60000),
    });
    auditFields = {
      action: 'thread',
      target: `thread:${auditFingerprint(threadId)}`,
      limit,
    };
  } else if (action === 'list' || action === 'search') {
    const { target, resolution } = await resolveGroupReadTarget(flags, runtime, config);
    if (!target) {
      return {
        status: resolution.status,
        targetGroupName: String(flags.targetGroupName),
        candidates: resolution.candidates || [],
        bindingFingerprint: resolution.bindingFingerprint,
        executed: false,
      };
    }
    validateBoundedTimeRange(flags.start, flags.end);
    if (action === 'list') {
      const limit = validateReadLimit(flags.limit, 50, 50);
      const order = String(flags.order || 'desc');
      if (!['asc', 'desc'].includes(order)) throw new Error('order must be asc or desc');
      response = await lark.larkImListMessages({
        chatId: target.id,
        start: flags.start,
        end: flags.end,
        pageSize: limit,
        order,
        timeoutMs: parseNumber(flags.timeoutMs, 60000),
      });
      auditFields = { action, target: auditTargetDescriptor(target), start: flags.start, end: flags.end, limit };
    } else {
      const query = requireValue(flags.query, '--query');
      if (Array.from(String(query)).length > 30) throw new Error('message search query cannot exceed 30 characters');
      const limit = validateReadLimit(flags.limit, 20, 20);
      response = await lark.larkImSearchMessages({
        chatId: target.id,
        query,
        start: flags.start,
        end: flags.end,
        pageSize: limit,
        timeoutMs: parseNumber(flags.timeoutMs, 60000),
      });
      auditFields = {
        action,
        target: auditTargetDescriptor(target),
        start: flags.start,
        end: flags.end,
        limit,
        query: `length=${Array.from(String(query)).length}, ${auditFingerprint(query)}`,
      };
    }
  } else {
    throw new Error('message action must be list, search, or thread');
  }
  appendClientAudit(runtime, '消息读取', auditFields);
  return {
    status: 'ok',
    ...auditFields,
    result: sanitizeReadForOutput(response, config, {
      previewLength: validateReadLimit(flags.previewLength, 2000, 500),
    }),
  };
}

function resolvedDocumentUrl(input, config) {
  const target = resolveDocumentTarget(input, config);
  if (!target || !['url', 'wiki_url'].includes(target.kind)) {
    throw new Error('knowledge operations require a configured URL alias or an explicit Feishu URL');
  }
  return target;
}

async function handleKnowledge(args, flags) {
  const action = requireValue(args[0], 'knowledge action (search|read|comments)');
  const { runtime, config } = loadContext(flags);
  const lark = createLark(runtime, { as: 'user' });
  if (action === 'search') {
    const query = requireValue(flags.query, '--query');
    if (Array.from(String(query)).length > 30) throw new Error('knowledge search query cannot exceed 30 characters');
    const limit = validateReadLimit(flags.limit, 20, 15);
    const docTypes = String(flags.types || 'docx,wiki,sheet,bitable,slides,file');
    const allowedTypes = new Set(['doc', 'sheet', 'bitable', 'mindnote', 'file', 'wiki', 'docx', 'folder', 'catalog', 'slides', 'shortcut']);
    if (docTypes.split(',').some((type) => !allowedTypes.has(type.trim()))) throw new Error('unsupported knowledge document type');
    lark.ensureReady();
    const response = await lark.larkKnowledgeSearch({
      query,
      docTypes,
      pageSize: limit,
      sort: String(flags.sort || 'default'),
      timeoutMs: parseNumber(flags.timeoutMs, 60000),
    });
    appendClientAudit(runtime, '知识检索', {
      action,
      query: `length=${Array.from(String(query)).length}, ${auditFingerprint(query)}`,
      docTypes,
      limit,
    });
    return { status: 'ok', result: sanitizeReadForOutput(response, config, { previewLength: 500 }) };
  }
  if (action === 'read') {
    const target = resolvedDocumentUrl(requireValue(flags.target, '--target'), config);
    const scope = String(flags.scope || 'outline');
    if (!['outline', 'keyword', 'section', 'full'].includes(scope)) throw new Error('scope must be outline, keyword, section, or full');
    if (scope === 'keyword' && !flags.keyword) throw new Error('keyword scope requires --keyword');
    if (scope === 'section' && !flags.startBlockId) throw new Error('section scope requires --start-block-id');
    lark.ensureReady();
    const inspected = await lark.larkDriveInspect({ url: target.value, timeoutMs: parseNumber(flags.timeoutMs, 60000) });
    const detail = String(flags.detail || 'simple');
    const docFormat = String(flags.format || 'markdown');
    if (!['simple', 'with-ids', 'full'].includes(detail)) throw new Error('detail must be simple, with-ids, or full');
    if (!['xml', 'markdown', 'im-markdown'].includes(docFormat)) throw new Error('format must be xml, markdown, or im-markdown');
    const fetched = await lark.larkDocFetch(target.value, {
      scope,
      keyword: flags.keyword,
      startBlockId: flags.startBlockId,
      contextBefore: flags.contextBefore === undefined ? undefined : boundedInteger(flags.contextBefore, {
        minimum: 0, maximum: 20, fallback: 0, label: 'context-before',
      }),
      contextAfter: flags.contextAfter === undefined ? undefined : boundedInteger(flags.contextAfter, {
        minimum: 0, maximum: 20, fallback: 0, label: 'context-after',
      }),
      maxDepth: flags.maxDepth === undefined ? undefined : boundedInteger(flags.maxDepth, {
        minimum: -1, maximum: 20, fallback: -1, label: 'max-depth',
      }),
      detail,
      docFormat,
      timeoutMs: parseNumber(flags.timeoutMs, 60000),
    });
    appendClientAudit(runtime, '知识读取', {
      action,
      target: auditTargetDescriptor(target),
      scope,
      query: flags.keyword ? `length=${Array.from(String(flags.keyword)).length}, ${auditFingerprint(flags.keyword)}` : '-',
    });
    return {
      status: 'ok',
      scope,
      inspect: sanitizeReadForOutput(inspected, config, { previewLength: 500 }),
      result: sanitizeReadForOutput(fetched, config, {
        previewLength: validateReadLimit(flags.previewLength, 2000, scope === 'full' ? 2000 : 1000),
      }),
    };
  }
  if (action === 'comments') {
    const commentsAction = requireValue(args[1], 'comments action (list|add)');
    const target = resolvedDocumentUrl(requireValue(flags.target, '--target'), config);
    if (commentsAction === 'list') {
      const limit = validateReadLimit(flags.limit, 100, 50);
      const solvedStatus = String(flags.solvedStatus || 'false');
      const commentScope = String(flags.commentScope || 'all');
      if (!['false', 'true', 'all'].includes(solvedStatus)) throw new Error('solved status must be false, true, or all');
      if (!['all', 'whole', 'partial'].includes(commentScope)) throw new Error('comment scope must be all, whole, or partial');
      lark.ensureReady();
      const response = await lark.larkDriveListComments({
        url: target.value,
        pageSize: limit,
        solvedStatus,
        commentScope,
        timeoutMs: parseNumber(flags.timeoutMs, 60000),
      });
      appendClientAudit(runtime, '评论读取', {
        action: 'comments.list',
        target: auditTargetDescriptor(target),
        limit,
        solvedStatus,
        commentScope,
      });
      return { status: 'ok', result: sanitizeReadForOutput(response, config, { previewLength: 1000 }) };
    }
    if (commentsAction !== 'add') throw new Error('comments action must be list or add');
    if (!parseBool(runtime.env.FEISHU_ACTIONBOX_ENABLED)) throw new Error('actionbox is disabled');
    const source = String(flags.source || config.defaultSource || 'codex');
    if (!actionboxSourceAllowed(runtime, source)) throw new Error('source is not present in the actionbox allowlist');
    const comment = await readPayload(flags, 'contentFile');
    if (!comment.trim()) throw new Error('comment content is empty');
    const id = flags.id || requestId('ACT');
    const request = {
      id,
      type: 'feishu_action',
      domain: 'drive',
      action: 'add_comment',
      identity: 'user',
      target: { kind: 'url', value: target.value },
      input: { comment, blockId: flags.blockId },
      explicitAuthorization: true,
      dryRun: parseBool(flags.dryRun),
      source,
      reason: String(flags.reason || 'Codex 通过飞书桥添加文档评论'),
      trace: { system: 'codex', code: String(flags.traceCode || id) },
      createdAt: new Date().toISOString(),
    };
    const validationError = validateActionRequest(request);
    if (validationError) throw new Error(validationError);
    const startup = await ensureBridgeRunning(runtime, config, { autoStart: flags.autoStart !== 'false' });
    const result = await submitQueueRequest({
      kind: 'actionbox',
      request,
      queuePath: runtime.actionboxPath,
      resultsPath: runtime.actionboxResultsPath,
      statePath: runtime.actionboxStatePath,
      wakeEndpoint: '/internal/actionbox/wake',
      asyncMode: Boolean(flags.async),
      timeoutMs: parseNumber(flags.timeoutMs, 60000),
      pollMs: parseNumber(flags.pollMs, 500),
    });
    return sanitizeForOutput({ ...result, startup }, config);
  }
  throw new Error('knowledge action must be search, read, or comments');
}

function addCommandFlag(args, name, value) {
  if (value !== undefined && value !== null && value !== '') args.push(name, String(value));
}

function addCommandCsv(args, name, value) {
  const values = Array.isArray(value)
    ? value
    : String(value || '').split(',').map((item) => item.trim()).filter(Boolean);
  if (values.length) args.push(name, values.join(','));
}

function validateCalendarReadRange(start, end, { required = false, maximumDays = 40 } = {}) {
  if (required && (!start || !end)) throw new Error('calendar read requires --start and --end');
  if (Boolean(start) !== Boolean(end)) throw new Error('calendar read requires both --start and --end');
  if (!start) return;
  const startMs = Date.parse(start);
  const endMs = Date.parse(end);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || startMs >= endMs) {
    throw new Error('calendar read range is invalid');
  }
  if (endMs - startMs > maximumDays * 24 * 60 * 60 * 1000) {
    throw new Error(`calendar read window cannot exceed ${maximumDays} days`);
  }
}

function validateMeetingReadRange(start, end, { required = false, maximumDays = 90 } = {}) {
  if (required && (!start || !end)) throw new Error('meeting read requires --start and --end');
  if (Boolean(start) !== Boolean(end)) throw new Error('meeting read requires both --start and --end');
  if (!start) return;
  const startMs = Date.parse(start);
  const endMs = Date.parse(end);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs) || startMs >= endMs) {
    throw new Error('meeting read range is invalid');
  }
  if (endMs - startMs > maximumDays * 24 * 60 * 60 * 1000) {
    throw new Error(`meeting read window cannot exceed ${maximumDays} days`);
  }
}

function validateRemoteIdList(value, { label = 'IDs', maximum = 10 } = {}) {
  const values = String(value || '').split(',').map((item) => item.trim()).filter(Boolean);
  if (values.length < 1 || values.length > maximum) {
    throw new Error(`${label} count must be between 1 and ${maximum}`);
  }
  if (values.some((item) => !/^[A-Za-z0-9_-]{1,400}$/.test(item))) {
    throw new Error(`${label} contains an invalid identifier`);
  }
  return values;
}

function collectRegularFiles(root, files = []) {
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    const filePath = path.join(root, entry.name);
    if (entry.isSymbolicLink()) throw new Error('transcript output contains an unsafe symbolic link');
    if (entry.isDirectory()) collectRegularFiles(filePath, files);
    else if (entry.isFile()) files.push(filePath);
  }
  return files;
}

async function readTranscriptArtifact(lark, buildArgs, {
  prefix, maxChars, timeoutMs, workingDirectory,
}) {
  const root = path.resolve(workingDirectory);
  const outputRoot = path.join(root, 'node_modules', '.cache', 'feishu-bridge', 'transcripts');
  fs.mkdirSync(outputRoot, { recursive: true, mode: 0o700 });
  const rootStat = fs.lstatSync(outputRoot);
  if (!rootStat.isDirectory() || rootStat.isSymbolicLink()) {
    throw new Error('unsafe transcript output directory');
  }
  chmodPrivate(outputRoot, 0o700);
  const outputDir = fs.mkdtempSync(path.join(outputRoot, `${prefix}-`));
  chmodPrivate(outputDir, 0o700);
  const outputArgument = path.relative(root, outputDir).split(path.sep).join('/');
  try {
    const response = await lark.runLarkCliJson(buildArgs(outputArgument), { timeoutMs });
    const candidates = collectRegularFiles(outputDir)
      .filter((filePath) => /\.(?:txt|md)$/i.test(filePath));
    if (candidates.length !== 1) throw new Error('transcript output did not contain exactly one text artifact');
    const stat = fs.lstatSync(candidates[0]);
    if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('transcript output is not a regular file');
    const maximumBytes = Math.min(stat.size, maxChars * 4 + 4);
    const descriptor = fs.openSync(candidates[0], 'r');
    let transcript;
    try {
      const buffer = Buffer.alloc(maximumBytes);
      const bytesRead = fs.readSync(descriptor, buffer, 0, maximumBytes, 0);
      transcript = buffer.subarray(0, bytesRead).toString('utf8').slice(0, maxChars);
    } finally {
      fs.closeSync(descriptor);
    }
    return {
      response,
      transcript,
      transcriptMeta: {
        sourceBytes: stat.size,
        returnedCharacters: transcript.length,
        truncated: stat.size > Buffer.byteLength(transcript, 'utf8'),
      },
    };
  } finally {
    fs.rmSync(outputDir, { recursive: true, force: true });
  }
}

function columnNumber(value) {
  return [...String(value).toUpperCase()].reduce((total, character) => total * 26 + character.charCodeAt(0) - 64, 0);
}

function validateBoundedA1Range(value, maximumCells = 10000) {
  const match = String(value || '').match(/^([A-Za-z]+)([1-9]\d*)(?::([A-Za-z]+)([1-9]\d*))?$/);
  if (!match) throw new Error('range must be a bounded A1 cell range');
  const startColumn = columnNumber(match[1]);
  const startRow = Number(match[2]);
  const endColumn = match[3] ? columnNumber(match[3]) : startColumn;
  const endRow = match[4] ? Number(match[4]) : startRow;
  if (startColumn > endColumn || startRow > endRow) throw new Error('range start must precede range end');
  const cells = (endColumn - startColumn + 1) * (endRow - startRow + 1);
  if (cells > maximumCells) throw new Error(`range cannot exceed ${maximumCells} cells`);
  return cells;
}

async function readJsonPayload(flags) {
  const text = await readPayload(flags, 'payloadFile');
  let payload;
  try {
    payload = JSON.parse(text);
  } catch {
    throw new Error('payload must be valid JSON');
  }
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) throw new Error('payload must be a JSON object');
  return payload;
}

async function submitFeishuAction({ runtime, config, flags, domain, action, target, input }) {
  if (!parseBool(runtime.env.FEISHU_ACTIONBOX_ENABLED)) throw new Error('actionbox is disabled');
  const source = String(flags.source || config.defaultSource || 'codex');
  if (!actionboxSourceAllowed(runtime, source)) throw new Error('source is not present in the actionbox allowlist');
  const id = flags.id || requestId('ACT');
  const request = {
    id,
    type: 'feishu_action',
    domain,
    action,
    identity: 'user',
    target,
    input,
    explicitAuthorization: true,
    confirmHighImpact: Boolean(flags.confirmHighImpact),
    dryRun: parseBool(flags.dryRun),
    source,
    reason: String(flags.reason || `Codex 通过飞书桥执行 ${domain}.${action}`),
    trace: { system: 'codex', code: String(flags.traceCode || id) },
    createdAt: new Date().toISOString(),
  };
  const validationError = validateActionRequest(request);
  if (validationError) throw new Error(validationError);
  const startup = await ensureBridgeRunning(runtime, config, { autoStart: flags.autoStart !== 'false' });
  const result = await submitQueueRequest({
    kind: 'actionbox',
    request,
    queuePath: runtime.actionboxPath,
    resultsPath: runtime.actionboxResultsPath,
    statePath: runtime.actionboxStatePath,
    wakeEndpoint: '/internal/actionbox/wake',
    asyncMode: Boolean(flags.async),
    timeoutMs: parseNumber(flags.timeoutMs, 180000),
    pollMs: parseNumber(flags.pollMs, 500),
  });
  return sanitizeForOutput({ ...result, startup }, config);
}

async function handleCalendar(args, flags) {
  const action = requireValue(args[0], 'calendar action');
  const { runtime, config } = loadContext(flags);
  const writes = new Set(['create', 'update', 'rsvp']);
  if (writes.has(action)) {
    const payload = await readJsonPayload(flags);
    if (action === 'create') {
      const { calendarId = 'primary', ...input } = payload;
      return submitFeishuAction({
        runtime, config, flags, domain: 'calendar', action: 'create_event',
        target: { kind: 'calendar_id', value: calendarId }, input,
      });
    }
    const { eventId, ...input } = payload;
    return submitFeishuAction({
      runtime, config, flags, domain: 'calendar', action: action === 'update' ? 'update_event' : 'rsvp',
      target: { kind: 'event_id', value: requireValue(eventId, 'payload.eventId') }, input,
    });
  }
  const lark = createLark(runtime, { as: 'user' });
  lark.ensureReady();
  let command;
  let auditFields;
  if (action === 'agenda') {
    validateCalendarReadRange(flags.start, flags.end);
    command = ['calendar', '+agenda', '--as', 'user'];
    addCommandFlag(command, '--start', flags.start);
    addCommandFlag(command, '--end', flags.end);
    addCommandFlag(command, '--calendar-id', flags.calendarId);
    auditFields = { action, start: flags.start || 'today', end: flags.end || 'same-day' };
  } else if (action === 'search') {
    validateCalendarReadRange(flags.start, flags.end, { required: true });
    const limit = validateReadLimit(flags.limit, 30, 20);
    command = ['calendar', '+search-event', '--as', 'user', '--start', flags.start, '--end', flags.end, '--page-size', String(limit)];
    addCommandFlag(command, '--query', flags.query);
    addCommandFlag(command, '--calendar-id', flags.calendarId);
    addCommandCsv(command, '--attendee-ids', flags.attendeeIds);
    auditFields = { action, start: flags.start, end: flags.end, limit, query: flags.query ? auditFingerprint(flags.query) : '-' };
  } else if (action === 'get') {
    command = ['calendar', '+get', '--as', 'user', '--event-id', requireValue(flags.eventId, '--event-id')];
    addCommandFlag(command, '--calendar-id', flags.calendarId);
    auditFields = { action, target: `event:${auditFingerprint(flags.eventId)}` };
  } else if (action === 'freebusy') {
    validateCalendarReadRange(flags.start, flags.end, { required: true, maximumDays: 31 });
    command = ['calendar', '+freebusy', '--as', 'user', '--start', flags.start, '--end', flags.end];
    addCommandFlag(command, '--user-id', flags.userId);
    auditFields = { action, start: flags.start, end: flags.end, target: flags.userId ? `user:${auditFingerprint(flags.userId)}` : 'current-user' };
  } else {
    throw new Error('calendar action must be agenda, search, get, freebusy, create, update, or rsvp');
  }
  command.push('--format', 'json');
  const response = await lark.runLarkCliJson(command, { timeoutMs: parseNumber(flags.timeoutMs, 60000) });
  appendClientAudit(runtime, '日程读取', auditFields);
  return { status: 'ok', ...auditFields, result: sanitizeReadForOutput(response, config, { previewLength: 1000 }) };
}

async function handleTask(args, flags) {
  const action = requireValue(args[0], 'task action');
  const { runtime, config } = loadContext(flags);
  const writes = new Set(['create', 'update', 'complete', 'reopen', 'assign', 'reminder']);
  if (writes.has(action)) {
    const payload = await readJsonPayload(flags);
    if (action === 'create') {
      const targetValue = payload.tasklistId || 'self';
      return submitFeishuAction({
        runtime, config, flags, domain: 'task', action,
        target: { kind: 'task_scope', value: targetValue }, input: payload,
      });
    }
    const { taskId, ...input } = payload;
    return submitFeishuAction({
      runtime, config, flags, domain: 'task', action,
      target: { kind: 'task_id', value: requireValue(taskId, 'payload.taskId') }, input,
    });
  }
  const lark = createLark(runtime, { as: 'user' });
  lark.ensureReady();
  let command;
  let auditFields;
  if (action === 'mine') {
    command = ['task', '+get-my-tasks', '--as', 'user', '--page-limit', '1'];
    if (flags.complete !== undefined) command.push(`--complete=${parseBool(flags.complete) ? 'true' : 'false'}`);
    addCommandFlag(command, '--created_at', flags.createdAt);
    addCommandFlag(command, '--due-start', flags.dueStart);
    addCommandFlag(command, '--due-end', flags.dueEnd);
    addCommandFlag(command, '--query', flags.query);
    auditFields = { action, query: flags.query ? auditFingerprint(flags.query) : '-', pageLimit: 1 };
  } else if (action === 'related') {
    command = ['task', '+get-related-tasks', '--as', 'user', '--page-limit', '1'];
    if (flags.createdByMe) command.push('--created-by-me');
    if (flags.followedByMe) command.push('--followed-by-me');
    if (flags.includeComplete !== undefined) command.push(`--include-complete=${parseBool(flags.includeComplete) ? 'true' : 'false'}`);
    auditFields = { action, pageLimit: 1 };
  } else if (action === 'search') {
    const query = requireValue(flags.query, '--query');
    command = ['task', '+search', '--as', 'user', '--query', query, '--page-limit', '1'];
    addCommandCsv(command, '--assignee', flags.assignee);
    addCommandCsv(command, '--creator', flags.creator);
    addCommandCsv(command, '--follower', flags.follower);
    addCommandFlag(command, '--due', flags.due);
    if (flags.completed !== undefined) command.push(`--completed=${parseBool(flags.completed) ? 'true' : 'false'}`);
    auditFields = { action, query: auditFingerprint(query), pageLimit: 1 };
  } else if (action === 'get') {
    const taskId = requireValue(flags.taskId, '--task-id');
    command = ['task', 'tasks', 'get', '--as', 'user', '--task-guid', taskId];
    auditFields = { action, target: `task:${auditFingerprint(taskId)}` };
  } else if (action === 'tasklists') {
    const limit = validateReadLimit(flags.limit, 100, 50);
    command = ['task', 'tasklists', 'list', '--as', 'user', '--page-size', String(limit)];
    auditFields = { action, limit };
  } else if (action === 'tasklist-search') {
    const query = requireValue(flags.query, '--query');
    command = ['task', '+tasklist-search', '--as', 'user', '--query', query, '--page-limit', '1'];
    addCommandCsv(command, '--creator', flags.creator);
    addCommandFlag(command, '--create-time', flags.createTime);
    auditFields = { action, query: auditFingerprint(query), pageLimit: 1 };
  } else {
    throw new Error('unsupported task action');
  }
  command.push('--format', 'json');
  const response = await lark.runLarkCliJson(command, { timeoutMs: parseNumber(flags.timeoutMs, 60000) });
  appendClientAudit(runtime, '任务读取', auditFields);
  return { status: 'ok', ...auditFields, result: sanitizeReadForOutput(response, config, { previewLength: 1000 }) };
}

function sheetSelector(flags) {
  if (Boolean(flags.sheetName) === Boolean(flags.sheetId)) throw new Error('provide exactly one of --sheet-name or --sheet-id');
  return flags.sheetName ? ['--sheet-name', flags.sheetName] : ['--sheet-id', flags.sheetId];
}

async function handleSheets(args, flags) {
  const action = requireValue(args[0], 'sheets action');
  const { runtime, config } = loadContext(flags);
  const target = requireValue(flags.target, '--target');
  if (!/^https:\/\//i.test(target)) throw new Error('sheets target must be an HTTPS URL');
  const writeActions = {
    'create-sheet': 'create_sheet',
    'set-cells': 'set_cells',
    'append-table': 'append_table',
  };
  if (writeActions[action]) {
    const input = await readJsonPayload(flags);
    return submitFeishuAction({
      runtime, config, flags, domain: 'sheets', action: writeActions[action],
      target: { kind: 'url', value: target }, input,
    });
  }
  const lark = createLark(runtime, { as: 'user' });
  lark.ensureReady();
  const urlArgs = ['--url', target];
  let response;
  let auditFields;
  if (action === 'inspect') {
    const workbook = await lark.runLarkCliJson(['sheets', '+workbook-info', '--as', 'user', ...urlArgs, '--format', 'json']);
    const revision = await lark.runLarkCliJson(['sheets', '+revision-get', '--as', 'user', ...urlArgs, '--format', 'json']);
    response = { workbook, revision };
    auditFields = { action, target: `sheet:${auditFingerprint(target)}` };
  } else if (action === 'cells') {
    const range = requireValue(flags.range, '--range');
    const cells = validateBoundedA1Range(range);
    response = await lark.runLarkCliJson([
      'sheets', '+cells-get', '--as', 'user', ...urlArgs, ...sheetSelector(flags),
      '--range', range, '--include', String(flags.include || 'value,formula'), '--format', 'json',
    ]);
    auditFields = { action, target: `sheet:${auditFingerprint(target)}`, range, cells };
  } else if (action === 'table') {
    const range = requireValue(flags.range, '--range');
    const cells = validateBoundedA1Range(range, 20000);
    response = await lark.runLarkCliJson([
      'sheets', '+table-get', '--as', 'user', ...urlArgs, ...sheetSelector(flags),
      '--range', range, '--max-chars', String(validateReadLimit(flags.maxChars, 500000, 100000)), '--format', 'json',
    ]);
    auditFields = { action, target: `sheet:${auditFingerprint(target)}`, range, cells };
  } else if (action === 'search') {
    const range = requireValue(flags.range, '--range');
    const cells = validateBoundedA1Range(range, 20000);
    const find = requireValue(flags.find, '--find');
    response = await lark.runLarkCliJson([
      'sheets', '+cells-search', '--as', 'user', ...urlArgs, ...sheetSelector(flags),
      '--range', range, '--find', find, '--format', 'json',
    ]);
    auditFields = { action, target: `sheet:${auditFingerprint(target)}`, range, cells, query: auditFingerprint(find) };
  } else if (action === 'revision') {
    response = await lark.runLarkCliJson(['sheets', '+revision-get', '--as', 'user', ...urlArgs, '--format', 'json']);
    auditFields = { action, target: `sheet:${auditFingerprint(target)}` };
  } else {
    throw new Error('sheets action must be inspect, cells, table, search, revision, create-sheet, set-cells, or append-table');
  }
  appendClientAudit(runtime, '电子表格读取', auditFields);
  return { status: 'ok', ...auditFields, result: sanitizeReadForOutput(response, config, { previewLength: 1000 }) };
}

async function handleBase(args, flags) {
  const action = requireValue(args[0], 'base action');
  const { runtime, config } = loadContext(flags);
  const target = requireValue(flags.target, '--target');
  if (!/^https:\/\//i.test(target)) throw new Error('base target must be an HTTPS URL');
  const writeActions = { 'create-records': 'create_records', 'update-records': 'update_records' };
  if (writeActions[action]) {
    const input = await readJsonPayload(flags);
    return submitFeishuAction({
      runtime, config, flags, domain: 'base', action: writeActions[action],
      target: { kind: 'url', value: target }, input,
    });
  }
  const lark = createLark(runtime, { as: 'user' });
  lark.ensureReady();
  const timeoutMs = parseNumber(flags.timeoutMs, 60000);
  const { baseToken, response: resolved } = await resolveBaseToken(lark, target, timeoutMs);
  let response;
  let auditFields;
  if (action === 'inspect') {
    const limit = validateReadLimit(flags.limit, 100, 50);
    const base = await lark.runLarkCliJson(['base', '+base-get', '--as', 'user', '--base-token', baseToken, '--format', 'json']);
    const tables = await lark.runLarkCliJson(['base', '+table-list', '--as', 'user', '--base-token', baseToken, '--limit', String(limit), '--format', 'json']);
    response = { resolved, base, tables };
    auditFields = { action, target: `base:${auditFingerprint(target)}`, limit };
  } else if (action === 'schema') {
    const tableId = requireValue(flags.tableId, '--table-id');
    const limit = validateReadLimit(flags.limit, 200, 100);
    const fields = await lark.runLarkCliJson(['base', '+field-list', '--as', 'user', '--base-token', baseToken, '--table-id', tableId, '--limit', String(limit), '--format', 'json']);
    const views = await lark.runLarkCliJson(['base', '+view-list', '--as', 'user', '--base-token', baseToken, '--table-id', tableId, '--limit', String(limit), '--format', 'json']);
    response = { fields, views };
    auditFields = { action, target: `base:${auditFingerprint(target)}`, table: auditFingerprint(tableId), limit };
  } else if (action === 'records' || action === 'search' || action === 'get') {
    const tableId = requireValue(flags.tableId, '--table-id');
    const fields = String(flags.fields || '').split(',').map((item) => item.trim()).filter(Boolean);
    if (fields.length > 50) throw new Error('field projection cannot exceed 50 fields');
    if (action === 'get') {
      const recordIds = String(requireValue(flags.recordIds, '--record-ids')).split(',').map((item) => item.trim()).filter(Boolean);
      if (recordIds.length < 1 || recordIds.length > 100) throw new Error('record ID count must be between 1 and 100');
      const command = ['base', '+record-get', '--as', 'user', '--base-token', baseToken, '--table-id', tableId];
      recordIds.forEach((recordId) => command.push('--record-id', recordId));
      fields.forEach((field) => command.push('--field-id', field));
      command.push('--format', 'json');
      response = await lark.runLarkCliJson(command, { timeoutMs });
      auditFields = { action, target: `base:${auditFingerprint(target)}`, table: auditFingerprint(tableId), count: recordIds.length };
    } else {
      const limit = validateReadLimit(flags.limit, 200, action === 'search' ? 20 : 100);
      const command = ['base', action === 'search' ? '+record-search' : '+record-list', '--as', 'user', '--base-token', baseToken, '--table-id', tableId, '--limit', String(limit)];
      fields.forEach((field) => command.push('--field-id', field));
      if (action === 'search') {
        const query = requireValue(flags.query, '--query');
        const searchFields = String(requireValue(flags.searchFields, '--search-fields')).split(',').map((item) => item.trim()).filter(Boolean);
        if (searchFields.length < 1 || searchFields.length > 20) throw new Error('search field count must be between 1 and 20');
        command.push('--keyword', query);
        searchFields.forEach((field) => command.push('--search-field', field));
      }
      if (flags.filterJson) command.push('--filter-json', String(flags.filterJson));
      if (flags.sortJson) command.push('--sort-json', String(flags.sortJson));
      command.push('--format', 'json');
      response = await lark.runLarkCliJson(command, { timeoutMs });
      auditFields = {
        action, target: `base:${auditFingerprint(target)}`, table: auditFingerprint(tableId), limit,
        query: flags.query ? auditFingerprint(flags.query) : '-',
      };
    }
  } else {
    throw new Error('base action must be inspect, schema, records, search, get, create-records, or update-records');
  }
  appendClientAudit(runtime, '多维表格读取', auditFields);
  return { status: 'ok', ...auditFields, result: sanitizeReadForOutput(response, config, { previewLength: 1000 }) };
}

async function handleMeeting(args, flags) {
  const action = requireValue(args[0], 'meeting action');
  const { runtime, config } = loadContext(flags);
  const lark = createLark(runtime, { as: 'user' });
  lark.ensureReady();
  const timeoutMs = parseNumber(flags.timeoutMs, 60000);
  let command;
  let auditFields;

  if (action === 'search') {
    validateMeetingReadRange(flags.start, flags.end);
    const organizerIds = String(flags.organizerIds || '').trim();
    const participantIds = String(flags.participantIds || '').trim();
    const roomIds = String(flags.roomIds || '').trim();
    if (![flags.query, flags.start, organizerIds, participantIds, roomIds].some(Boolean)) {
      throw new Error('meeting search requires a query, time range, organizer, participant, or room filter');
    }
    const limit = validateReadLimit(flags.limit, 30, 15);
    command = ['vc', '+search', '--as', 'user', '--page-size', String(limit)];
    addCommandFlag(command, '--query', flags.query);
    addCommandFlag(command, '--start', flags.start);
    addCommandFlag(command, '--end', flags.end);
    addCommandCsv(command, '--organizer-ids', organizerIds);
    addCommandCsv(command, '--participant-ids', participantIds);
    addCommandCsv(command, '--room-ids', roomIds);
    auditFields = {
      action,
      start: flags.start || '-',
      end: flags.end || '-',
      limit,
      query: flags.query ? auditFingerprint(flags.query) : '-',
      organizers: organizerIds ? organizerIds.split(',').filter(Boolean).length : 0,
      participants: participantIds ? participantIds.split(',').filter(Boolean).length : 0,
      rooms: roomIds ? roomIds.split(',').filter(Boolean).length : 0,
    };
  } else if (action === 'active') {
    command = ['vc', '+meeting-list-active', '--as', 'user'];
    auditFields = { action, identity: 'user' };
  } else if (action === 'get') {
    const meetingId = requireValue(flags.meetingId, '--meeting-id');
    validateRemoteIdList(meetingId, { label: 'meeting IDs', maximum: 1 });
    command = ['vc', 'meeting', 'get', '--as', 'user', '--meeting-id', meetingId];
    if (flags.withParticipants) command.push('--with-participants');
    if (flags.artifactsOnly) command.push('--query-mode', '1');
    auditFields = {
      action,
      target: `meeting:${auditFingerprint(meetingId)}`,
      participants: Boolean(flags.withParticipants),
      artifactsOnly: Boolean(flags.artifactsOnly),
    };
  } else if (action === 'detail') {
    const meetingIds = validateRemoteIdList(flags.meetingIds || flags.meetingId, {
      label: 'meeting IDs', maximum: 10,
    });
    command = ['vc', '+detail', '--as', 'user', '--meeting-ids', meetingIds.join(',')];
    auditFields = { action, count: meetingIds.length };
  } else if (action === 'events') {
    const meetingId = requireValue(flags.meetingId, '--meeting-id');
    validateRemoteIdList(meetingId, { label: 'meeting IDs', maximum: 1 });
    validateMeetingReadRange(flags.start, flags.end);
    const limit = validateReadLimit(flags.limit, 100, 50);
    command = [
      'vc', '+meeting-events', '--as', 'user', '--meeting-id', meetingId,
      '--page-size', String(limit),
    ];
    addCommandFlag(command, '--start', flags.start);
    addCommandFlag(command, '--end', flags.end);
    auditFields = {
      action,
      target: `meeting:${auditFingerprint(meetingId)}`,
      start: flags.start || '-',
      end: flags.end || '-',
      limit,
    };
  } else if (action === 'recording') {
    const hasMeetingIds = Boolean(flags.meetingIds || flags.meetingId);
    const hasCalendarIds = Boolean(flags.calendarEventIds);
    if (hasMeetingIds === hasCalendarIds) {
      throw new Error('provide exactly one of --meeting-ids or --calendar-event-ids');
    }
    command = ['vc', '+recording', '--as', 'user'];
    if (hasMeetingIds) {
      const ids = validateRemoteIdList(flags.meetingIds || flags.meetingId, {
        label: 'meeting IDs', maximum: 10,
      });
      command.push('--meeting-ids', ids.join(','));
      auditFields = { action, source: 'meeting', count: ids.length };
    } else {
      const ids = validateRemoteIdList(flags.calendarEventIds, {
        label: 'calendar event IDs', maximum: 10,
      });
      command.push('--calendar-event-ids', ids.join(','));
      auditFields = { action, source: 'calendar', count: ids.length };
    }
  } else {
    throw new Error('meeting action must be search, active, get, detail, events, or recording');
  }

  command.push('--format', 'json');
  const response = await lark.runLarkCliJson(command, { timeoutMs });
  appendClientAudit(runtime, '会议读取', auditFields);
  return {
    status: 'ok',
    ...auditFields,
    result: sanitizeReadForOutput(response, config, { previewLength: 2000, preserveUrls: false }),
  };
}

async function handleNote(args, flags) {
  const action = requireValue(args[0], 'note action');
  const { runtime, config } = loadContext(flags);
  const noteId = requireValue(flags.noteId, '--note-id');
  validateRemoteIdList(noteId, { label: 'note IDs', maximum: 1 });
  const lark = createLark(runtime, { as: 'user' });
  lark.ensureReady();
  const timeoutMs = parseNumber(flags.timeoutMs, 60000);
  const auditFields = { action, target: `note:${auditFingerprint(noteId)}` };
  let response;
  let previewLength = 2000;

  if (action === 'detail') {
    response = await lark.runLarkCliJson([
      'note', '+detail', '--as', 'user', '--note-id', noteId, '--format', 'json',
    ], { timeoutMs });
  } else if (action === 'transcript') {
    const maxChars = boundedInteger(flags.maxChars, {
      minimum: 1, maximum: 100000, fallback: 20000, label: '--max-chars',
    });
    previewLength = maxChars;
    response = await readTranscriptArtifact(lark, (outputDir) => [
      'note', '+transcript', '--as', 'user', '--note-id', noteId,
      '--transcript-format', String(flags.transcriptFormat || 'markdown'),
      '--output', path.join(outputDir, 'transcript.md'), '--format', 'json',
    ], { prefix: 'note', maxChars, timeoutMs, workingDirectory: runtime.projectRoot });
    auditFields.maxChars = maxChars;
    auditFields.returnedCharacters = response.transcriptMeta.returnedCharacters;
  } else {
    throw new Error('note action must be detail or transcript');
  }

  appendClientAudit(runtime, '智能纪要读取', auditFields);
  return {
    status: 'ok',
    ...auditFields,
    result: sanitizeReadForOutput(response, config, { previewLength, preserveUrls: false }),
  };
}

async function handleMinutes(args, flags) {
  const action = requireValue(args[0], 'minutes action');
  const { runtime, config } = loadContext(flags);
  const writeActions = {
    upload: 'upload',
    'update-title': 'update_title',
    'replace-summary': 'replace_summary',
    todos: 'mutate_todos',
    'replace-words': 'replace_words',
    'replace-speaker': 'replace_speaker',
  };
  if (writeActions[action]) {
    const payload = await readJsonPayload(flags);
    if (action === 'upload') {
      const fileToken = requireValue(payload.fileToken, 'payload.fileToken');
      return submitFeishuAction({
        runtime, config, flags, domain: 'minutes', action: 'upload',
        target: { kind: 'file_token', value: fileToken }, input: {},
      });
    }
    const { minuteToken, ...input } = payload;
    return submitFeishuAction({
      runtime, config, flags, domain: 'minutes', action: writeActions[action],
      target: { kind: 'minute_token', value: requireValue(minuteToken, 'payload.minuteToken') },
      input,
    });
  }

  const lark = createLark(runtime, { as: 'user' });
  lark.ensureReady();
  const timeoutMs = parseNumber(flags.timeoutMs, 60000);
  let response;
  let auditFields;
  let previewLength = 2000;

  if (action === 'search') {
    validateMeetingReadRange(flags.start, flags.end);
    const ownerIds = String(flags.ownerIds || '').trim();
    const participantIds = String(flags.participantIds || '').trim();
    if (![flags.query, flags.start, ownerIds, participantIds].some(Boolean)) {
      throw new Error('minutes search requires a query, time range, owner, or participant filter');
    }
    const limit = validateReadLimit(flags.limit, 30, 15);
    const command = ['minutes', '+search', '--as', 'user', '--page-size', String(limit)];
    addCommandFlag(command, '--query', flags.query);
    addCommandFlag(command, '--start', flags.start);
    addCommandFlag(command, '--end', flags.end);
    addCommandCsv(command, '--owner-ids', ownerIds);
    addCommandCsv(command, '--participant-ids', participantIds);
    command.push('--format', 'json');
    response = await lark.runLarkCliJson(command, { timeoutMs });
    auditFields = {
      action,
      start: flags.start || '-',
      end: flags.end || '-',
      limit,
      query: flags.query ? auditFingerprint(flags.query) : '-',
      owners: ownerIds ? ownerIds.split(',').filter(Boolean).length : 0,
      participants: participantIds ? participantIds.split(',').filter(Boolean).length : 0,
    };
  } else if (action === 'get') {
    const minuteToken = requireValue(flags.minuteToken, '--minute-token');
    validateRemoteIdList(minuteToken, { label: 'minute tokens', maximum: 1 });
    response = await lark.runLarkCliJson([
      'minutes', 'minutes', 'get', '--as', 'user', '--minute-token', minuteToken, '--format', 'json',
    ], { timeoutMs });
    auditFields = { action, target: `minutes:${auditFingerprint(minuteToken)}` };
  } else if (action === 'detail') {
    const minuteTokens = validateRemoteIdList(flags.minuteTokens || flags.minuteToken, {
      label: 'minute tokens', maximum: 10,
    });
    const command = [
      'minutes', '+detail', '--as', 'user', '--minute-tokens', minuteTokens.join(','),
    ];
    for (const artifact of ['summary', 'todo', 'chapter', 'keyword']) {
      if (flags[artifact]) command.push(`--${artifact}`);
    }
    command.push('--format', 'json');
    response = await lark.runLarkCliJson(command, { timeoutMs });
    auditFields = {
      action,
      count: minuteTokens.length,
      artifacts: ['summary', 'todo', 'chapter', 'keyword'].filter((artifact) => flags[artifact]).join(',') || 'basic',
    };
  } else if (action === 'transcript') {
    const minuteToken = requireValue(flags.minuteToken, '--minute-token');
    validateRemoteIdList(minuteToken, { label: 'minute tokens', maximum: 1 });
    const maxChars = boundedInteger(flags.maxChars, {
      minimum: 1, maximum: 100000, fallback: 20000, label: '--max-chars',
    });
    previewLength = maxChars;
    response = await readTranscriptArtifact(lark, (outputDir) => [
      'minutes', '+detail', '--as', 'user', '--minute-tokens', minuteToken,
      '--transcript', '--output-dir', outputDir, '--format', 'json',
    ], { prefix: 'minutes', maxChars, timeoutMs, workingDirectory: runtime.projectRoot });
    auditFields = {
      action,
      target: `minutes:${auditFingerprint(minuteToken)}`,
      maxChars,
      returnedCharacters: response.transcriptMeta.returnedCharacters,
    };
  } else {
    throw new Error('minutes action must be search, get, detail, transcript, upload, update-title, replace-summary, todos, replace-words, or replace-speaker');
  }

  appendClientAudit(runtime, '妙记读取', auditFields);
  return {
    status: 'ok',
    ...auditFields,
    result: sanitizeReadForOutput(response, config, { previewLength, preserveUrls: false }),
  };
}

function publicTargets(config, runtime) {
  const allowedOpenIds = directAllowedOpenIds(config, runtime?.env);
  const messages = Object.entries(config.messageTargets || {}).map(([alias, target]) => ({
    alias,
    ...sanitizeTarget(target, config),
    taskLinkEligible: target.type === 'open_id' && allowedOpenIds.has(target.id),
  }));
  const documents = Object.entries(config.documentTargets || {}).map(([alias, target]) => ({
    alias,
    kind: target.kind,
    valueFingerprint: fingerprintIdentifier(target.value),
  }));
  const nameBindings = Object.entries(config.nameBindings || {}).map(([normalizedName, binding]) => ({
    name: binding.name || normalizedName,
    type: binding.type,
    idFingerprint: fingerprintIdentifier(binding.id),
    boundAt: binding.boundAt,
  }));
  const groupNameBindings = Object.entries(config.groupNameBindings || {}).map(([normalizedName, binding]) => ({
    name: binding.name || normalizedName,
    type: binding.type,
    idFingerprint: fingerprintIdentifier(binding.id),
    boundAt: binding.boundAt,
  }));
  const testAssets = Object.entries(config.testAssets || {}).map(([alias, target]) => ({
    alias,
    kind: target.kind,
    valueFingerprint: fingerprintIdentifier(target.value),
    capabilityId: target.capabilityId || undefined,
    savedAt: target.savedAt || undefined,
  }));
  return { messages, nameBindings, groupNameBindings, documents, testAssets };
}

async function handleTargets(args, flags) {
  const action = args[0];
  const configPath = configPathFrom(flags);
  const runtime = buildRuntime(projectRoot);
  if (action === 'init') {
    const result = initializeClientConfig(runtime, configPath);
    return {
      status: 'initialized',
      configPath,
      targets: publicTargets(result.config, runtime),
    };
  }
  const config = loadClientConfig(configPath);
  if (action === 'directory') {
    const directoryAction = requireValue(args[1], 'directory action (status|sync|search|bind|unbind)');
    const directory = buildDirectoryService(runtime);
    if (directoryAction === 'status') return { status: 'ok', directory: directory.status() };
    if (!directory.status().enabled) throw new Error('directory feature is disabled');
    if (directoryAction === 'sync') {
      return { status: 'ok', directory: await directory.sync('manual_cli') };
    }
    if (directoryAction === 'search') {
      const query = requireValue(flags.query, '--query');
      await directory.ensureFresh('search');
      return {
        status: 'ok',
        query,
        matches: directory.search(query, { limit: parseNumber(flags.limit, 20) }),
      };
    }
    if (directoryAction === 'bind') {
      const name = requireValue(flags.name, '--name');
      const candidate = requireValue(flags.candidate, '--candidate');
      await directory.ensureFresh('bind');
      const result = directory.bind(name, candidate, config);
      writeSecureJson(configPath, result.config);
      return { status: 'bound', name, target: result.binding };
    }
    if (directoryAction === 'unbind') {
      const name = requireValue(flags.name, '--name');
      const normalized = normalizeDirectoryName(name);
      const existed = Boolean(config.nameBindings?.[normalized]);
      delete config.nameBindings[normalized];
      writeSecureJson(configPath, config);
      return { status: existed ? 'unbound' : 'not_bound', name };
    }
    throw new Error('directory action must be status, sync, search, bind, or unbind');
  }
  if (action === 'group-directory') {
    const directoryAction = requireValue(args[1], 'group directory action (status|sync|search|bind|unbind)');
    const directory = buildGroupDirectoryService(runtime);
    if (directoryAction === 'status') return { status: 'ok', groupDirectory: directory.status() };
    if (!directory.status().enabled) throw new Error('group directory feature is disabled');
    if (directoryAction === 'sync') {
      return { status: 'ok', groupDirectory: await directory.sync('manual_cli') };
    }
    if (directoryAction === 'search') {
      const query = requireValue(flags.query, '--query');
      await directory.ensureFresh('search');
      return {
        status: 'ok',
        query,
        matches: directory.search(query, { limit: parseNumber(flags.limit, 20) }),
      };
    }
    if (directoryAction === 'bind') {
      const name = requireValue(flags.name, '--name');
      const candidate = requireValue(flags.candidate, '--candidate');
      await directory.ensureFresh('bind');
      const result = directory.bind(name, candidate, config);
      writeSecureJson(configPath, result.config);
      return { status: 'bound', name, target: result.binding };
    }
    if (directoryAction === 'unbind') {
      const name = requireValue(flags.name, '--name');
      const normalized = normalizeGroupName(name);
      const existed = Boolean(config.groupNameBindings?.[normalized]);
      delete config.groupNameBindings[normalized];
      writeSecureJson(configPath, config);
      return { status: existed ? 'unbound' : 'not_bound', name };
    }
    throw new Error('group directory action must be status, sync, search, bind, or unbind');
  }
  if (action === 'list') return { status: 'ok', configPath, targets: publicTargets(config, runtime) };
  const targetType = requireValue(args[1], 'target category (message|document)');
  const alias = requireValue(args[2], 'target alias');
  if (!['message', 'document'].includes(targetType)) throw new Error('target category must be message or document');
  if (action === 'remove') {
    if (targetType === 'message') delete config.messageTargets[alias];
    else delete config.documentTargets[alias];
    writeSecureJson(configPath, config);
    return { status: 'removed', category: targetType, alias };
  }
  if (action !== 'set') throw new Error('targets action must be init, list, set, or remove');
  const value = (await readPayload(flags, 'valueFile')).trim();
  if (!value) throw new Error('target value is empty');
  if (targetType === 'message') {
    const type = requireValue(flags.type, '--type');
    if (!['chat_id', 'open_id'].includes(type)) throw new Error('message target type must be chat_id or open_id');
    const target = { type, id: value };
    config.messageTargets[alias] = target;
  } else {
    const kind = requireValue(flags.kind, '--kind');
    const target = resolveDocumentTarget(`${kind}:${value}`, config);
    config.documentTargets[alias] = { kind: target.kind, value: target.value };
  }
  writeSecureJson(configPath, config);
  return { status: 'saved', category: targetType, alias, targets: publicTargets(config, runtime) };
}

async function handleSend(flags) {
  const targetFlags = [flags.target, flags.targetName, flags.targetGroupName].filter(Boolean);
  if (targetFlags.length > 1) {
    throw new Error('--target, --target-name, and --target-group-name are mutually exclusive');
  }
  const { runtime, config } = loadContext(flags);
  if (!parseBool(runtime.env.FEISHU_OUTBOUND_ENABLED)) throw new Error('outbound messaging is disabled');
  let target;
  let directoryResolution;
  let groupDirectoryResolution;
  if (flags.targetName) {
    const directory = buildDirectoryService(runtime);
    if (!directory.status().enabled) throw new Error('directory feature is disabled');
    await directory.ensureFresh('send_by_name');
    directoryResolution = directory.resolve(flags.targetName, config);
    if (directoryResolution.status !== 'resolved') {
      return {
        status: directoryResolution.status,
        targetName: String(flags.targetName),
        candidates: directoryResolution.candidates || [],
        bindingFingerprint: directoryResolution.bindingFingerprint,
        submitted: false,
      };
    }
    target = directoryResolution.target;
  } else if (flags.targetGroupName) {
    const directory = buildGroupDirectoryService(runtime);
    if (!directory.status().enabled) throw new Error('group directory feature is disabled');
    await directory.ensureFresh('send_by_group_name');
    groupDirectoryResolution = directory.resolve(flags.targetGroupName, config);
    if (groupDirectoryResolution.status !== 'resolved') {
      return {
        status: groupDirectoryResolution.status,
        targetGroupName: String(flags.targetGroupName),
        candidates: groupDirectoryResolution.candidates || [],
        bindingFingerprint: groupDirectoryResolution.bindingFingerprint,
        submitted: false,
      };
    }
    target = groupDirectoryResolution.target;
  } else {
    target = resolveMessageTarget(requireValue(flags.target, '--target'), config);
  }
  const id = flags.id || requestId('OUT');
  const format = String(flags.format || 'text').toLowerCase();
  if (!MESSAGE_TYPES.has(format)) throw new Error(`unsupported message format: ${format}`);
  let text;
  let filePath;
  if (['image', 'file'].includes(format)) {
    const mediaSource = requireValue(flags.mediaFile, '--media-file');
    filePath = stageOutboundMedia(runtime, id, mediaSource);
  } else {
    const contentFlag = flags.contentFile !== undefined ? 'contentFile' : 'textFile';
    text = await readPayload(flags, contentFlag);
    if (!text.trim()) throw new Error('message content is empty');
  }
  const source = String(flags.source || config.defaultSource || 'codex');
  const request = {
    id,
    type: format,
    target: { type: target.type, id: target.id },
    text,
    filePath,
    explicitAuthorization: true,
    dryRun: parseBool(flags.dryRun),
    source,
    reason: String(flags.reason || `Codex 通过飞书桥发送${format}消息`),
    trace: { system: 'codex', code: String(flags.traceCode || id) },
    createdAt: new Date().toISOString(),
  };
  const validationError = validateMessageRequest(request);
  if (validationError) {
    cleanupStagedMedia(runtime, filePath);
    throw new Error(validationError);
  }
  let startup;
  let result;
  try {
    startup = await ensureBridgeRunning(runtime, config, { autoStart: flags.autoStart !== 'false' });
    result = await submitQueueRequest({
      kind: 'outbox',
      request,
      queuePath: runtime.outboxPath,
      resultsPath: runtime.outboxResultsPath,
      statePath: runtime.outboxStatePath,
      wakeEndpoint: '/internal/outbox/wake',
      asyncMode: Boolean(flags.async),
      timeoutMs: parseNumber(flags.timeoutMs, 60000),
      pollMs: parseNumber(flags.pollMs, 250),
    });
  } catch (error) {
    cleanupStagedMedia(runtime, filePath);
    throw error;
  }
  return sanitizeForOutput({
    ...result,
    startup,
    directoryResolution: directoryResolution ? {
      source: directoryResolution.source,
      candidate: directoryResolution.candidate,
    } : undefined,
    groupDirectoryResolution: groupDirectoryResolution ? {
      source: groupDirectoryResolution.source,
      candidate: groupDirectoryResolution.candidate,
    } : undefined,
  }, config);
}

async function authoritativeTaskThread(runtime, threadId) {
  const controller = new CodexDesktopTaskController({
    socketPath: defaultDesktopIPCPath({
      platform: runtime.platform,
      env: runtime.env,
      homeDir: os.homedir(),
    }),
    requestTimeoutMs: parseNumber(runtime.env.CODEX_DESKTOP_REQUEST_TIMEOUT_MS, 20000),
    platform: runtime.platform,
    projectRoot: runtime.projectRoot,
  });
  const thread = validateAuthoritativeThread(await controller.readThreadSnapshot(threadId));
  if (thread.id !== threadId) throw new Error('Codex returned a different thread');
  const cwd = fs.realpathSync(thread.cwd);
  if (!fs.statSync(cwd).isDirectory()) throw new Error('Codex thread working directory is not available');
  const turn = latestTurn(thread);
  return {
    thread: { ...thread, cwd }, turn,
    state: publicTurnState(thread, turn),
    timing: publicTurnTiming(turn),
  };
}

function taskLinkReadiness(runtime, config, configPath) {
  const status = collectStatus(runtime, config, configPath);
  const events = status.eventConsumer?.events || {};
  const daemon = spawnSync(runtime.codexBin, ['app-server', 'daemon', 'version'], {
    cwd: runtime.projectRoot,
    env: runtime.env,
    encoding: 'utf8',
    timeout: 5000,
  });
  const desktopSocketPath = defaultDesktopIPCPath({
    platform: runtime.platform,
    env: runtime.env,
    homeDir: os.homedir(),
  });
  const desktopIPC = desktopEndpointReady(desktopSocketPath, {
    platform: runtime.platform,
    projectRoot: runtime.projectRoot,
    env: runtime.env,
  });
  const checks = {
    outbound: Boolean(status.outbound?.enabled && !status.outbound?.dryRun),
    bridgeProcess: Boolean(status.pid?.alive || status.service?.running),
    directInbound: status.eventConsumer?.status === 'connected'
      && status.eventConsumer?.connection?.state === 'connected'
      && events['im.message.receive_v1']?.status === 'running',
    cardCallbacks: events['card.action.trigger']?.status === 'running',
    codexDaemon: daemon.status === 0,
    codexDesktopIPC: desktopIPC,
  };
  return {
    ready: Object.values(checks).every(Boolean),
    checks,
    blockers: Object.entries(checks).filter(([, ready]) => !ready).map(([name]) => name),
  };
}

async function handleTaskLink(args, flags) {
  const action = requireValue(args[0], 'task-link action (create|list|status|interrupt|release|protocol)');
  const { runtime, config, configPath } = loadContext(flags);
  if (action === 'protocol') return {
    status: 'ok',
    protocol: TASK_LINK_PROTOCOL,
    version: 2,
    schemaVersion: TASK_LINK_SCHEMA_VERSION,
    leaseSeconds: 24 * 60 * 60,
    permissionMode: 'danger-full-access',
    approvalPolicy: 'never',
    features: {
      observeCurrentTurn: true,
      steerCurrentTurn: true,
      interruptTurn: true,
      answerNonSecretInput: true,
      ordinaryAttachments: true,
      exactDirectOperator: true,
    },
    readiness: taskLinkReadiness(runtime, config, configPath),
  };
  if (action === 'list') return { status: 'ok', protocol: TASK_LINK_PROTOCOL, version: 2, links: listTaskLinks() };
  if (action === 'status') {
    const taskKey = requireValue(flags.taskKey, '--task-key');
    const link = listTaskLinks().find((item) => item.taskKey === taskKey);
    if (!link) throw new Error('task link not found');
    return { status: 'ok', protocol: TASK_LINK_PROTOCOL, version: 2, link };
  }
  if (action === 'release' || action === 'interrupt') {
    const taskKey = requireValue(flags.taskKey, '--task-key');
    const privateLink = findLinkByTaskKey(taskKey);
    if (!privateLink) throw new Error('task link not found');
    if (action === 'interrupt') {
      if (privateLink.activeTurnId) {
        const controller = new CodexDesktopTaskController({
          socketPath: defaultDesktopIPCPath({
            platform: runtime.platform,
            env: runtime.env,
            homeDir: os.homedir(),
          }),
          requestTimeoutMs: parseNumber(runtime.env.CODEX_DESKTOP_REQUEST_TIMEOUT_MS, 20000),
          platform: runtime.platform,
          projectRoot: runtime.projectRoot,
        });
        await controller.interrupt({
          threadId: privateLink.threadId,
          turnId: privateLink.activeTurnId,
        });
      }
      const interrupted = updateLink(privateLink.id, {
        turnState: 'interrupted', turnOwner: 'none', actionRequired: 'none', activeTurnId: '',
        pendingMessageId: '', progress: {
          ...privateLink.progress, phase: '已停止', detail: '当前 Codex 轮次已停止，任务连接继续有效。',
          durationSeconds: Number.isFinite(Date.parse(privateLink.progress?.startedAt || ''))
            ? Math.max(0, Math.round(
              (Date.now() - Date.parse(privateLink.progress.startedAt)) / 1000,
            ))
            : privateLink.progress?.durationSeconds,
        },
      }, undefined, { terminalAt: new Date().toISOString() });
      return { status: 'interrupted', protocol: TASK_LINK_PROTOCOL, version: 2, link: publicLink(interrupted) };
    }
    for (const directory of [...new Set([
      ...(privateLink.stagedCleanupDirs || []), privateLink.pendingCleanupDir || '',
    ].filter(Boolean))]) {
      try { cleanupInboundAssets(runtime.projectRoot, directory); } catch {}
    }
    const released = updateLink(privateLink.id, {
      linkState: 'released', pendingMessageId: '', pendingCleanupDir: '', stagedCleanupDirs: [],
    });
    return { status: 'released', protocol: TASK_LINK_PROTOCOL, version: 2, link: publicLink(released) };
  }
  if (action !== 'create') throw new Error('task-link action must be create, list, status, interrupt, release, or protocol');
  if (!parseBool(runtime.env.FEISHU_OUTBOUND_ENABLED)) throw new Error('outbound messaging is disabled');
  const payload = JSON.parse(await readPayload(flags, 'payloadFile'));
  const target = resolveMessageTarget(requireValue(payload.targetAlias, 'payload.targetAlias'), config);
  if (target.type !== 'open_id') throw new Error('task links require an authorized direct-message target');
  const allowedOpenIds = directAllowedOpenIds(config, runtime.env);
  if (!allowedOpenIds.has(target.id)) throw new Error('task link target is not present in the direct-message authorization whitelist');
  await ensureBridgeRunning(runtime, config, { autoStart: flags.autoStart !== 'false' });
  const authoritative = await authoritativeTaskThread(runtime, requireValue(payload.threadId, 'payload.threadId'));
  const initialState = authoritative.state;
  const activeTurnId = ['running', 'desktop_action_required'].includes(initialState.turnState)
    ? authoritative.turn?.id || '' : '';
  const lastDeliveredTurnId = activeTurnId ? '' : authoritative.turn?.id || '';
  let link = upsertLink({
    threadId: authoritative.thread.id,
    cwd: authoritative.thread.cwd,
    title: String(authoritative.thread.name || payload.title || '').trim(),
    projectName: payload.projectName,
    targetAlias: payload.targetAlias,
    target: { type: target.type, id: target.id },
    ...initialState,
    activeTurnId,
    progress: {
      phase: initialState.turnState === 'running' ? '运行中' : '已连接',
      detail: initialState.turnState === 'running' ? '正在读取当前 Codex 轮次。' : '等待飞书指令',
      changedFiles: 0,
      testStatus: '未运行',
      startedAt: authoritative.timing.startedAt,
      durationSeconds: authoritative.timing.durationSeconds,
    },
  });
  if (lastDeliveredTurnId) link = updateLink(link.id, { lastDeliveredTurnId });
  const card = progressCard({
    status: link.turnState,
    title: link.title,
    detail: `${link.projectName ? `${link.projectName} · ` : ''}已连接到 Codex。全权限控制将在 24 小时闲置后失效。`,
    taskLink: publicLink(link),
    progress: link.progress,
  });
  const request = {
    id: requestId('OUT-LINK'), type: 'card', target: link.target,
    text: JSON.stringify(card), source: 'codex-usage-bar',
    explicitAuthorization: true,
    dryRun: parseBool(flags.dryRun),
    reason: '用户在 CodexAssistant 中主动连接指定任务到飞书',
    trace: { system: 'codex-usage-bar', code: link.taskKey }, createdAt: new Date().toISOString(),
  };
  const result = await submitQueueRequest({
    kind: 'outbox', request, queuePath: runtime.outboxPath, resultsPath: runtime.outboxResultsPath,
    statePath: runtime.outboxStatePath, wakeEndpoint: '/internal/outbox/wake',
    timeoutMs: parseNumber(flags.timeoutMs, 60000), pollMs: 250,
  });
  if (!['sent', 'dry_run'].includes(result.status)) {
    updateLink(link.id, { linkState: 'released', progress: { phase: 'bridge', detail: result.error || result.status } });
    throw new Error(result.error || `task link card was not sent: ${result.status}`);
  }
  link = updateLink(link.id, {
    rootMessageId: result.messageIds?.[0] || '', messageIds: result.messageIds || [],
    cardRevision: result.status === 'dry_run' ? 0 : TASK_LINK_CARD_REVISION,
    linkState: result.status === 'dry_run' ? 'released' : link.linkState,
  });
  return {
    status: result.status === 'dry_run' ? 'dry_run' : 'connected',
    protocol: TASK_LINK_PROTOCOL,
    version: 2,
    link: publicLink(link),
  };
}

function buildDocumentInstruction(action, mode) {
  if (action === 'create_document') {
    return '请通过飞书桥既有 lark-cli 文档能力创建新版飞书文档，并返回标题、链接、token 和摘要。';
  }
  return `请先读取目标文档并创建飞书官方版本，再以 ${mode} 模式更新正文，完成后复读验证并返回版本与 revision 信息。`;
}

async function handleDoc(args, flags) {
  const action = args[0];
  const { runtime, config } = loadContext(flags);
  if (action === 'inspect') {
    const target = resolveDocumentTarget(requireValue(flags.target, '--target'), config);
    return sanitizeForOutput(await inspectDocument(runtime, target, { full: Boolean(flags.full) }), config);
  }
  if (!parseBool(runtime.env.FEISHU_DOCBOX_ENABLED)) throw new Error('docbox is disabled');
  const source = String(flags.source || config.defaultSource || 'codex');
  if (!docboxSourceAllowed(runtime, source)) throw new Error('source is not present in the docbox allowlist');
  if (!['create', 'update'].includes(action)) throw new Error('doc action must be inspect, create, or update');
  const content = await readPayload(flags, 'contentFile');
  if (!content.trim()) throw new Error('document content is empty');
  const target = flags.target ? resolveDocumentTarget(flags.target, config) : undefined;
  const mode = String(flags.mode || 'append');
  let replacementPattern = flags.selectionWithEllipsis;
  if (flags.patternFile !== undefined) {
    if (flags.patternFile === '-') throw new Error('--pattern-file must be a file because document content may use stdin');
    replacementPattern = fs.readFileSync(path.resolve(requireValue(flags.patternFile, '--pattern-file')), 'utf8').trim();
    if (!replacementPattern) throw new Error('replacement pattern is empty');
  }
  if (action === 'update') {
    if (!target) throw new Error('document update requires --target');
    validateHighImpactUpdate({
      mode,
      confirmHighImpact: Boolean(flags.confirmHighImpact),
      selectionByTitle: flags.selectionByTitle,
      selectionWithEllipsis: replacementPattern,
    });
  }
  const id = flags.id || requestId('DOC');
  const request = {
    id,
    type: 'document_task',
    action: action === 'create' ? 'create_document' : 'update_document',
    identity: documentIdentityForTarget(target),
    target: target ? { kind: target.kind, value: target.value } : undefined,
    content: {
      format: String(flags.format || 'markdown'),
      text: content,
    },
    instruction: buildDocumentInstruction(action === 'create' ? 'create_document' : 'update_document', mode),
    explicitAuthorization: true,
    dryRun: parseBool(flags.dryRun),
    source,
    reason: String(flags.reason || `Codex 通过飞书桥${action === 'create' ? '创建' : '更新'}文档`),
    trace: { system: 'codex', code: String(flags.traceCode || id) },
    createdAt: new Date().toISOString(),
  };
  if (action === 'update') {
    request.versionPolicy = 'official_before_update';
    request.updateMode = mode;
    if (flags.newTitle) request.newTitle = flags.newTitle;
    if (flags.selectionByTitle || replacementPattern) {
      request.selection = {
        byTitle: flags.selectionByTitle,
        withEllipsis: replacementPattern,
      };
    }
  }
  const validationError = validateDocumentRequest(request);
  if (validationError) throw new Error(validationError);
  const startup = await ensureBridgeRunning(runtime, config, { autoStart: flags.autoStart !== 'false' });
  const timeoutDefault = action === 'create'
    ? parseNumber(runtime.env.CODEX_TASK_TIMEOUT_MS, 1800000) + 60000
    : 360000;
  const result = await submitQueueRequest({
    kind: 'docbox',
    request,
    queuePath: runtime.docboxPath,
    resultsPath: runtime.docboxResultsPath,
    statePath: runtime.docboxStatePath,
    wakeEndpoint: '/internal/docbox/wake',
    asyncMode: Boolean(flags.async),
    timeoutMs: parseNumber(flags.timeoutMs, timeoutDefault),
    pollMs: parseNumber(flags.pollMs, 500),
  });
  return sanitizeForOutput(
    { ...result, startup },
    config,
    '',
    { preserveUrls: action === 'create' },
  );
}

async function submitRegisteredCapability({ runtime, config, flags, definition, input }) {
  if (!parseBool(runtime.env.FEISHU_ACTIONBOX_ENABLED)) throw new Error('actionbox is disabled');
  const source = String(flags.source || config.defaultSource || 'codex');
  if (!actionboxSourceAllowed(runtime, source)) throw new Error('source is not present in the actionbox allowlist');
  const id = flags.id || requestId('CAP');
  const request = {
    id,
    type: 'feishu_capability',
    domain: 'capability',
    action: 'execute',
    capabilityId: definition.id,
    identity: definition.identity,
    input,
    explicitAuthorization: true,
    confirmHighImpact: Boolean(flags.confirmHighImpact),
    dryRun: parseBool(flags.dryRun),
    source,
    reason: String(flags.reason || `Codex 通过飞书桥执行 ${definition.id}`),
    trace: { system: 'codex', code: String(flags.traceCode || id) },
    createdAt: new Date().toISOString(),
  };
  if (definition.risk === 'remote-operation') {
    request.remoteTimeoutMs = Math.max(10000, Math.min(30 * 60 * 1000, parseNumber(flags.remoteTimeoutMs, 10 * 60 * 1000)));
    request.pollIntervalMs = Math.max(250, Math.min(30000, parseNumber(flags.remotePollMs, 2000)));
  }
  if (flags.saveAs !== undefined) request.saveAs = String(flags.saveAs);
  const validationError = validateActionRequest(request);
  if (validationError) throw new Error(validationError);
  const startup = await ensureBridgeRunning(runtime, config, { autoStart: flags.autoStart !== 'false' });
  const result = await submitQueueRequest({
    kind: 'actionbox',
    request,
    queuePath: runtime.actionboxPath,
    resultsPath: runtime.actionboxResultsPath,
    statePath: runtime.actionboxStatePath,
    wakeEndpoint: '/internal/actionbox/wake',
    asyncMode: Boolean(flags.async),
    timeoutMs: parseNumber(flags.timeoutMs, definition.risk === 'remote-operation' ? 11 * 60 * 1000 : 180000),
    pollMs: parseNumber(flags.pollMs, 500),
  });
  return sanitizeForOutput({ ...result, startup }, config);
}

async function submitDocboxCapability({ runtime, config, flags, definition, input }) {
  if (definition.id !== 'docs.whiteboard.insert') throw new Error('unsupported docbox capability');
  if (!parseBool(runtime.env.FEISHU_DOCBOX_ENABLED)) throw new Error('docbox is disabled');
  const source = String(flags.source || config.defaultSource || 'codex');
  if (!docboxSourceAllowed(runtime, source)) throw new Error('source is not present in the docbox allowlist');
  const validationError = validateCapabilityInput(definition, input);
  if (validationError) throw new Error(validationError);
  const target = resolveDocumentTarget(input.doc, config);
  const id = flags.id || requestId('DOC');
  const request = {
    id,
    type: 'document_task',
    action: 'update_document',
    identity: documentIdentityForTarget(target),
    target: { kind: target.kind, value: target.value },
    content: { format: 'text', text: docWhiteboardXml(input) },
    instruction: '请先读取目标文档并创建飞书官方版本，再追加一个经过飞书桥窄类型校验的 Whiteboard，完成后复读验证。',
    versionPolicy: 'official_before_update',
    updateMode: 'append',
    explicitAuthorization: true,
    dryRun: parseBool(flags.dryRun),
    source,
    reason: String(flags.reason || 'Codex 通过飞书桥在文档中插入 Whiteboard'),
    trace: { system: 'codex', code: String(flags.traceCode || id) },
    createdAt: new Date().toISOString(),
  };
  const requestError = validateDocumentRequest(request);
  if (requestError) throw new Error(requestError);
  const startup = await ensureBridgeRunning(runtime, config, { autoStart: flags.autoStart !== 'false' });
  const result = await submitQueueRequest({
    kind: 'docbox',
    request,
    queuePath: runtime.docboxPath,
    resultsPath: runtime.docboxResultsPath,
    statePath: runtime.docboxStatePath,
    wakeEndpoint: '/internal/docbox/wake',
    asyncMode: Boolean(flags.async),
    timeoutMs: parseNumber(flags.timeoutMs, 360000),
    pollMs: parseNumber(flags.pollMs, 500),
  });
  return sanitizeForOutput({ ...result, capability: definition.id, startup }, config);
}

async function handleCapability(args, flags) {
  const action = requireValue(args[0], 'capability action (catalog|get|read|write)');
  if (action === 'catalog') {
    const domain = String(flags.domain || '');
    const risk = String(flags.risk || '');
    const items = capabilityCatalog().filter((item) => (!domain || item.domain === domain) && (!risk || item.risk === risk));
    return { status: 'ok', count: items.length, capabilities: items };
  }
  const capabilityId = requireValue(args[1], 'capability id');
  const definition = capability(capabilityId);
  if (!definition) throw new Error('unknown capability');
  if (action === 'get') return { status: 'ok', capability: publicCapability(definition) };
  if (!['read', 'write'].includes(action)) throw new Error('capability action must be catalog, get, read, or write');
  const { runtime, config, configPath } = loadContext(flags);
  const rawInput = await readJsonPayload(flags);
  const locationError = validateCapabilityLocationTarget(definition, rawInput, config);
  if (locationError) throw new Error(locationError);
  const input = resolveTestAssetBindings(definition, rawInput, config);
  if (action === 'read') {
    if (definition.risk !== 'read') throw new Error('write capability must enter actionbox');
    if (flags.saveAs !== undefined) {
      if (typeof flags.saveAs !== 'string' || !/^Codex桥测试[^\r\n]{0,80}$/.test(flags.saveAs)) {
        throw new Error('invalid_test_asset_alias');
      }
      if (!definition.resultIdentifiers.length) throw new Error('capability_result_cannot_be_saved');
    }
    const lark = createLark(runtime, { as: definition.identity });
    lark.ensureReady();
    const execution = await executeRegisteredCapability(lark, definition.id, input, {
      timeoutMs: Math.max(1000, Math.min(180000, parseNumber(flags.timeoutMs, 60000))),
    });
    let savedAsset;
    if (flags.saveAs !== undefined) {
      const match = selectCapabilityResultIdentifier(definition, {
        input,
        response: execution.response,
        remoteResponse: execution.remote?.response,
      });
      if (!match) throw new Error('registered_result_identifier_not_found');
      savedAsset = await saveTestAssetBinding(configPath, {
        alias: flags.saveAs,
        kind: match.kind,
        value: match.value,
        capabilityId: definition.id,
      });
    }
    appendClientAudit(runtime, '注册能力读取', {
      capability: definition.id,
      identity: definition.identity,
      fields: Object.keys(input).sort().join(',') || '-',
      savedAsset: savedAsset ? `${savedAsset.alias}:${savedAsset.kind}` : '-',
    });
    return {
      status: 'ok',
      capability: definition.id,
      savedAsset,
      result: sanitizeReadForOutput(execution.response, config, {
        previewLength: validateReadLimit(flags.previewLength, 5000, 2000),
        preserveUrls: false,
      }),
    };
  }
  if (definition.risk === 'read') throw new Error('read capability must not enter actionbox');
  if (definition.queue === 'docbox') {
    return submitDocboxCapability({ runtime, config, flags, definition, input });
  }
  return submitRegisteredCapability({ runtime, config, flags, definition, input });
}

function publicEventWatches(config) {
  return (config.eventWatches || []).map((item) => ({
    type: item.type,
    eventKeys: item.eventKeys,
    identity: item.identity,
    targetFingerprint: fingerprint(item.target),
    addedAt: item.addedAt,
  }));
}

async function handleEvents(args, flags) {
  const action = requireValue(args[0], 'events action (catalog|status|recent|get|watch)');
  const { runtime, config, configPath } = loadContext(flags);
  const inbox = createEventInbox({ dir: runtime.eventInboxDir });
  if (action === 'catalog') {
    return {
      status: 'ok',
      fixed: true,
      eventCount: FIXED_EVENT_KEYS.length,
      events: [...FIXED_EVENT_KEYS],
      watchTypes: publicWatchCatalog(),
    };
  }
  if (action === 'status') {
    return {
      status: 'ok',
      transport: collectStatus(runtime, config, configPath).eventConsumer,
      inbox: inbox.status(),
      watches: publicEventWatches(config),
    };
  }
  if (action === 'recent') {
    return { status: 'ok', events: inbox.recent(validateReadLimit(flags.limit, 100, 20)) };
  }
  if (action === 'get') {
    const eventFingerprint = requireValue(args[1], 'event fingerprint');
    if (!/^sha256:[a-f0-9]{20}$/.test(eventFingerprint)) throw new Error('invalid event fingerprint');
    const record = inbox.get(eventFingerprint);
    return { status: 'ok', found: Boolean(record), event: record || undefined };
  }
  if (action !== 'watch') throw new Error('events action must be catalog, status, recent, get, or watch');
  const watchAction = requireValue(args[1], 'watch action (add|remove|list)');
  if (watchAction === 'list') return { status: 'ok', watches: publicEventWatches(config), catalog: publicWatchCatalog() };
  if (!['add', 'remove'].includes(watchAction)) throw new Error('watch action must be add, remove, or list');
  const type = requireValue(args[2], 'watch type');
  const watch = watchDefinition(type);
  if (!watch) throw new Error('invalid watch type');
  if (watchAction === 'remove' && !watch.remove) throw new Error('this watch type has no unsubscribe API');
  let target = '';
  if (watch.targetRequired) {
    if (flags.targetFile === undefined) throw new Error('provide the private watch target through --target-file -');
    target = (await readPayload(flags, 'targetFile')).trim();
    if (!target) throw new Error('watch target is empty');
  }
  const definition = capability(`events.watch.${type}.${watchAction}`);
  if (!definition) throw new Error('watch capability is not registered');
  const result = await submitRegisteredCapability({
    runtime,
    config,
    flags: { ...flags, async: false },
    definition,
    input: target ? { target } : {},
  });
  if (result.status === 'completed') {
    const targetFingerprint = fingerprint(target);
    const current = config.eventWatches || [];
    if (watchAction === 'add') {
      const retained = current.filter((item) => !(item.type === type && fingerprint(item.target) === targetFingerprint));
      config.eventWatches = [...retained, {
        type,
        target,
        identity: watch.identity,
        eventKeys: [...watch.eventKeys],
        addedAt: new Date().toISOString(),
      }];
    } else {
      config.eventWatches = current.filter((item) => !(item.type === type && fingerprint(item.target) === targetFingerprint));
    }
    writeSecureJson(configPath, config);
  }
  return { ...result, watch: { type, action: watchAction, targetFingerprint: fingerprint(target) } };
}

function collectObjects(value, output = []) {
  if (Array.isArray(value)) {
    value.forEach((item) => collectObjects(item, output));
    return output;
  }
  if (!value || typeof value !== 'object') return output;
  output.push(value);
  Object.values(value).forEach((item) => collectObjects(item, output));
  return output;
}

function dateValue(value) {
  if (value && typeof value === 'object') value = value.timestamp || value.time || value.date_time || value.date;
  if (typeof value === 'number' || /^\d{10,13}$/.test(String(value || ''))) {
    const numeric = Number(value);
    return new Date(String(value).length === 10 ? numeric * 1000 : numeric);
  }
  const parsed = new Date(String(value || ''));
  return Number.isNaN(parsed.getTime()) ? null : parsed;
}

function scheduleAnalysis(calendarResult, start, end) {
  const slots = collectObjects(calendarResult).flatMap((item) => {
    const begin = dateValue(item.start_time || item.start || item.begin_time);
    const finish = dateValue(item.end_time || item.end || item.finish_time);
    if (!begin || !finish || begin >= finish) return [];
    return [{
      title: String(item.summary || item.title || item.name || '日程'),
      start: begin.toISOString(),
      end: finish.toISOString(),
    }];
  }).filter((item, index, all) => all.findIndex((candidate) => candidate.start === item.start && candidate.end === item.end && candidate.title === item.title) === index)
    .sort((left, right) => left.start.localeCompare(right.start));
  const conflicts = [];
  for (let index = 1; index < slots.length; index += 1) {
    if (Date.parse(slots[index].start) < Date.parse(slots[index - 1].end)) {
      conflicts.push({ left: slots[index - 1], right: slots[index] });
    }
  }
  const windowStart = dateValue(start);
  const windowEnd = dateValue(end);
  const free = [];
  if (windowStart && windowEnd && windowStart < windowEnd) {
    let cursor = windowStart.getTime();
    for (const slot of slots) {
      const slotStart = Math.max(windowStart.getTime(), Date.parse(slot.start));
      const slotEnd = Math.min(windowEnd.getTime(), Date.parse(slot.end));
      if (slotEnd <= windowStart.getTime() || slotStart >= windowEnd.getTime()) continue;
      if (slotStart > cursor) free.push({ start: new Date(cursor).toISOString(), end: new Date(slotStart).toISOString() });
      cursor = Math.max(cursor, slotEnd);
    }
    if (cursor < windowEnd.getTime()) free.push({ start: new Date(cursor).toISOString(), end: windowEnd.toISOString() });
  }
  return { slots, conflicts, free };
}

function collectValues(value, keys, output = new Set()) {
  if (Array.isArray(value)) {
    value.forEach((item) => collectValues(item, keys, output));
    return [...output];
  }
  if (!value || typeof value !== 'object') return [...output];
  for (const [key, child] of Object.entries(value)) {
    if (keys.includes(key) && typeof child === 'string' && child) output.add(child);
    collectValues(child, keys, output);
  }
  return [...output];
}

function resolveMeetingSummaryTarget(flags) {
  const meetingValue = flags.meetingIds || flags.meetingId;
  const minuteValue = flags.minuteToken;
  const minuteUrl = flags.minutesUrl;
  const supplied = [Boolean(meetingValue), Boolean(minuteValue), Boolean(minuteUrl)]
    .filter(Boolean).length;
  if (supplied !== 1) {
    throw new Error('provide exactly one of --meeting-ids, --minute-token, or --minutes-url');
  }
  if (meetingValue) {
    return {
      source: 'meeting',
      meetingIds: validateRemoteIdList(meetingValue, { label: 'meeting IDs', maximum: 10 }),
      minuteTokens: [],
    };
  }
  let token = minuteValue;
  if (minuteUrl) {
    let parsed;
    try {
      parsed = new URL(String(minuteUrl));
    } catch {
      throw new Error('minutes URL is invalid');
    }
    const match = parsed.protocol === 'https:'
      ? parsed.pathname.match(/^\/minutes\/([A-Za-z0-9_-]{1,400})\/?$/)
      : null;
    if (!match) throw new Error('minutes URL must be an HTTPS /minutes/<token> URL');
    token = match[1];
  }
  return {
    source: 'minutes',
    meetingIds: [],
    minuteTokens: validateRemoteIdList(token, { label: 'minute tokens', maximum: 1 }),
  };
}

async function handleWorkflow(args, flags) {
  const action = requireValue(args[0], 'workflow action (standup-report|meeting-summary)');
  const { runtime, config } = loadContext(flags);
  if (action === 'standup-report') {
    validateCalendarReadRange(flags.start, flags.end, { required: true, maximumDays: 14 });
    const limit = validateReadLimit(flags.limit, 100, 50);
    const lark = createLark(runtime, { as: 'user' });
    lark.ensureReady();
    const timeoutMs = parseNumber(flags.timeoutMs, 60000);
    const [calendar, tasks] = await Promise.all([
      lark.runLarkCliJson([
        'calendar', '+agenda', '--as', 'user', '--start', flags.start, '--end', flags.end, '--format', 'json',
      ], { timeoutMs }),
      lark.runLarkCliJson([
        'task', '+get-my-tasks', '--as', 'user', '--page-limit', '1', '--complete=false', '--format', 'json',
      ], { timeoutMs }),
    ]);
    const schedule = scheduleAnalysis(calendar, flags.start, flags.end);
    appendClientAudit(runtime, '组合工作流', {
      workflow: action,
      start: flags.start,
      end: flags.end,
      calendarItems: schedule.slots.length,
      conflicts: schedule.conflicts.length,
      publish: false,
    });
    return {
      status: 'ok',
      workflow: action,
      publish: false,
      range: { start: flags.start, end: flags.end },
      schedule,
      tasks: sanitizeReadForOutput(tasks, config, { previewLength: Math.max(1000, limit * 200) }),
    };
  }
  if (action !== 'meeting-summary') throw new Error('workflow action must be standup-report or meeting-summary');
  const target = resolveMeetingSummaryTarget(flags);
  const meetingIds = target.meetingIds;
  const maxChars = boundedInteger(flags.maxChars, {
    minimum: 1, maximum: 50000, fallback: 20000, label: '--max-chars',
  });
  const lark = createLark(runtime, { as: 'user' });
  lark.ensureReady();
  const timeoutMs = parseNumber(flags.timeoutMs, 120000);
  let meetings = {};
  let minuteLookup = {};
  if (target.source === 'meeting') {
    meetings = await lark.runLarkCliJson([
      'vc', '+detail', '--as', 'user', '--meeting-ids', meetingIds.join(','), '--format', 'json',
    ], { timeoutMs });
  } else {
    minuteLookup = await lark.runLarkCliJson([
      'minutes', 'minutes', 'get', '--as', 'user',
      '--minute-token', target.minuteTokens[0], '--format', 'json',
    ], { timeoutMs });
  }
  const noteIds = collectValues(meetings, ['note_id', 'noteId']).slice(0, 10);
  collectValues(minuteLookup, ['note_id', 'noteId']).slice(0, 10)
    .forEach((noteId) => {
      if (!noteIds.includes(noteId)) noteIds.push(noteId);
    });
  const minuteTokens = target.source === 'minutes'
    ? target.minuteTokens
    : collectValues(meetings, ['minute_token', 'minuteToken']).slice(0, 10);
  const notes = [];
  for (const noteId of noteIds) {
    notes.push(await lark.runLarkCliJson([
      'note', '+detail', '--as', 'user', '--note-id', noteId, '--format', 'json',
    ], { timeoutMs }));
  }
  let minutes = {};
  if (minuteTokens.length) {
    minutes = await lark.runLarkCliJson([
      'minutes', '+detail', '--as', 'user', '--minute-tokens', minuteTokens.join(','),
      '--summary', '--todo', '--chapter', '--keyword', '--format', 'json',
    ], { timeoutMs });
  }
  const transcripts = [];
  if (flags.includeTranscript) {
    const eachLimit = Math.max(1, Math.floor(maxChars / Math.max(1, minuteTokens.length)));
    for (const minuteToken of minuteTokens) {
      const artifact = await readTranscriptArtifact(lark, (outputDir) => [
        'minutes', '+detail', '--as', 'user', '--minute-tokens', minuteToken,
        '--transcript', '--output-dir', outputDir, '--format', 'json',
      ], {
        prefix: 'meeting-summary', maxChars: eachLimit, timeoutMs,
        workingDirectory: runtime.projectRoot,
      });
      transcripts.push({ minuteFingerprint: auditFingerprint(minuteToken), transcript: artifact.transcript, meta: artifact.transcriptMeta });
    }
  }
  appendClientAudit(runtime, '组合工作流', {
    workflow: action,
    source: target.source,
    meetings: meetingIds.length,
    notes: noteIds.length,
    minutes: minuteTokens.length,
    transcriptCharacters: transcripts.reduce((total, item) => total + item.transcript.length, 0),
    publish: false,
  });
  return {
    status: 'ok',
    workflow: action,
    publish: false,
    source: target.source,
    result: sanitizeReadForOutput({ meetings, minuteLookup, notes, minutes, transcripts }, config, {
      previewLength: maxChars + 5000,
      preserveUrls: false,
    }),
  };
}

function handleResult(args, flags) {
  const kind = requireValue(args[0], 'result kind (outbox|docbox|actionbox)');
  const id = requireValue(args[1], 'request id');
  const { runtime, config } = loadContext(flags);
  const filePath = {
    outbox: runtime.outboxResultsPath,
    docbox: runtime.docboxResultsPath,
    actionbox: runtime.actionboxResultsPath,
  }[kind];
  if (!filePath) throw new Error('result kind must be outbox, docbox, or actionbox');
  const records = readCompleteJsonl(filePath).filter((record) => record.id === id);
  return {
    id,
    kind,
    found: records.length > 0,
    records: records.map((record) => sanitizeForOutput(record, config)),
  };
}

function handleRecent(args, flags) {
  const kind = requireValue(args[0], 'recent kind');
  const { runtime, config } = loadContext(flags);
  return {
    kind,
    records: recentRecords(runtime, config, kind, Math.max(1, Math.min(100, parseNumber(flags.limit, 10)))),
  };
}

async function ensureCurrentUserTarget(runtime, configPath) {
  const lark = createLark(runtime, { as: 'user' });
  lark.ensureReady();
  const response = await lark.runLarkCliJson([
    'contact', '+search-user', '--user-ids', 'me', '--as', 'user', '--json',
  ]);
  const openId = findValue(response, ['open_id', 'openId']);
  if (!/^ou_[A-Za-z0-9_-]+$/.test(openId)) {
    throw new Error('无法从飞书授权中确认当前用户身份');
  }
  const config = loadClientConfig(configPath);
  config.messageTargets['我'] = { type: 'open_id', id: openId };
  config.directAllowedAliases = [...new Set([...(config.directAllowedAliases || []), '我'])];
  writeSecureJson(configPath, config);
  return { status: 'configured', targetAlias: '我' };
}

async function handleAuth(args, flags) {
  const action = requireValue(args[0], 'auth action (configure-existing|start-config|start-user|finish-user|ensure-current-user)');
  const { runtime, configPath } = loadContext(flags);
  if (action === 'configure-existing') {
    const payload = JSON.parse(await readPayload(flags, 'payloadFile'));
    return configureExisting(runtime, {
      appId: requireValue(payload.appId, 'payload.appId'),
      appSecret: requireValue(payload.appSecret, 'payload.appSecret'),
      brand: payload.brand || flags.brand || 'feishu',
      profile: String(flags.profile || runtime.env.LARK_CLI_PROFILE || 'default'),
    });
  }
  if (action === 'start-config') {
    return startConfig(runtime, {
      profile: String(flags.profile || runtime.env.LARK_CLI_PROFILE || 'default'),
      timeoutMs: parseNumber(flags.timeoutMs, 15000),
      createNew: parseBool(flags.createNew),
    });
  }
  if (action === 'start-user') {
    return startUser(runtime, {
      scope: String(flags.scope || 'required'),
    });
  }
  if (action === 'finish-user') {
    return finishUser(runtime, {
      deviceCode: String(flags.deviceCode || ''),
    });
  }
  if (action === 'ensure-current-user') {
    return ensureCurrentUserTarget(runtime, configPath);
  }
  throw new Error('auth action must be configure-existing, start-config, start-user, finish-user, or ensure-current-user');
}

function handleProfile(args, flags) {
  const action = args[0] || 'show';
  const { runtime, configPath, config } = loadContext(flags);
  if (action === 'catalog') {
    return {
      status: 'ok',
      profiles: publicEventConsumerProfileCatalog(),
      sharedAppRule: 'exactly_one_primary',
    };
  }
  if (action === 'show') {
    return {
      status: 'ok',
      eventConsumer: collectStatus(runtime, config, configPath).eventConsumer,
      sharedAppRule: 'exactly_one_primary',
    };
  }
  if (action === 'set') {
    const profile = requireValue(args[1], 'profile (primary|manual-only)');
    const previous = inspectEventConsumerProfile(runtime.eventConsumerProfilePath);
    const configured = writeEventConsumerProfile(runtime.eventConsumerProfilePath, profile);
    return {
      status: 'updated',
      previousProfile: previous.valid ? previous.profile : 'invalid',
      profile: configured.profile,
      appliedByRunningBridge: pidAlive(runtime) ? 'pending_realtime_reconcile' : 'on_next_start',
      sharedAppRule: 'exactly_one_primary',
    };
  }
  throw new Error('profile action must be catalog, show, or set');
}

function help() {
  return {
    usage: [
      'bridge-client.js status|doctor|start|restart',
      'bridge-client.js capabilities|permissions',
      'bridge-client.js auth configure-existing --payload-file - [--profile default]',
      'bridge-client.js auth start-config [--profile default] [--create-new]',
      'bridge-client.js auth start-user [--scope required|recommend|<scope-list>]',
      'bridge-client.js auth finish-user [--device-code <device-code>]',
      'bridge-client.js auth ensure-current-user',
      'bridge-client.js profile catalog|show|set <primary|manual-only>',
      'bridge-client.js capability catalog|get|read|write [capability-id] --payload-file - [--dry-run] [--save-as Codex桥测试…]',
      'bridge-client.js events catalog|status|recent|get',
      'bridge-client.js events watch add|remove|list <task|whiteboard|meeting|note|recording|minutes>',
      'bridge-client.js workflow standup-report --start <ISO> --end <ISO>',
      'bridge-client.js workflow meeting-summary (--meeting-ids <ids> | --minute-token <token> | --minutes-url <url>) [--include-transcript --max-chars 20000]',
      'bridge-client.js targets init|list',
      'bridge-client.js targets directory status|sync',
      'bridge-client.js targets directory search --query <name>',
      'bridge-client.js targets directory bind|unbind --name <name> [--candidate <fingerprint>]',
      'bridge-client.js targets group-directory status|sync',
      'bridge-client.js targets group-directory search --query <name>',
      'bridge-client.js targets group-directory bind|unbind --name <name> [--candidate <fingerprint>]',
      'bridge-client.js targets set <message|document> <alias> --type/--kind <value> --value-file -',
      'bridge-client.js targets remove <message|document> <alias>',
      'bridge-client.js send --target <alias|type:id> --format text|markdown|card --content-file - [--dry-run] [--async]',
      'bridge-client.js send --target-name <exact-name> --format text|markdown|card --content-file - [--dry-run] [--async]',
      'bridge-client.js send --target-group-name <exact-group-name> --format text|markdown|card --content-file - [--dry-run] [--async]',
      'bridge-client.js send --target ... --format image|file --media-file <path> [--dry-run] [--async]',
      'bridge-client.js task-link protocol|list',
      'bridge-client.js task-link create --payload-file -',
      'bridge-client.js task-link status|interrupt|release --task-key <task-key>',
      'bridge-client.js message list --target-group-name <exact-group-name> --start <ISO> --end <ISO> [--limit 50]',
      'bridge-client.js message search --target-group-name <exact-group-name> --query <text> --start <ISO> --end <ISO> [--limit 20]',
      'bridge-client.js message thread --message-id-file - [--limit 50]',
      'bridge-client.js doc inspect --target <alias|url|kind:value> [--full]',
      'bridge-client.js doc create [--target ...] --content-file - [--dry-run] [--async]',
      'bridge-client.js doc update --target ... --content-file - [--dry-run] [--mode append|overwrite|str_replace --pattern-file <path>]',
      'bridge-client.js knowledge search --query <text> [--types docx,wiki] [--limit 20]',
      'bridge-client.js knowledge read --target <alias|url> [--scope outline|keyword|section|full]',
      'bridge-client.js knowledge comments list --target <alias|url> [--limit 100]',
      'bridge-client.js knowledge comments add --target <alias|url> --content-file - [--async]',
      'bridge-client.js calendar agenda|search|get|freebusy [bounded read flags]',
      'bridge-client.js calendar create|update|rsvp --payload-file - [--async]',
      'bridge-client.js task mine|related|search|get|tasklists|tasklist-search [bounded read flags]',
      'bridge-client.js task create|update|complete|reopen|assign|reminder --payload-file - [--async]',
      'bridge-client.js sheets inspect|cells|table|search|revision --target <url> [read flags]',
      'bridge-client.js sheets create-sheet|set-cells|append-table --target <url> --payload-file - [--async]',
      'bridge-client.js base inspect|schema|records|search|get --target <url> [read flags]',
      'bridge-client.js base create-records|update-records --target <url> --payload-file - [--async]',
      'bridge-client.js meeting search|active|get|detail|events|recording [bounded read flags]',
      'bridge-client.js note detail|transcript --note-id <id> [--max-chars 20000]',
      'bridge-client.js minutes search|get|detail|transcript [bounded read flags]',
      'bridge-client.js minutes upload|update-title|replace-summary|todos|replace-words|replace-speaker --payload-file - [--async]',
      'bridge-client.js result <outbox|docbox|actionbox> <id>',
      'bridge-client.js recent <outbox|docbox|actionbox|tasks|messages|audit> [--limit 10]',
    ],
  };
}

async function main(argv = process.argv.slice(2)) {
  const { positional, flags } = parseArgs(argv);
  const command = positional[0] || 'help';
  const args = positional.slice(1);
  if (command === 'help' || flags.help) return help();
  if (!['status', 'doctor', 'capabilities'].includes(command)) {
    const boundary = buildRuntime(projectRoot).privatePathBoundary;
    if (!boundary.secure) {
      throw new Error(`Windows private paths escape FEISHU_BRIDGE_DATA_DIR: ${boundary.escaped.join(',')}`);
    }
  }
  if (command === 'targets') return handleTargets(args, flags);
  if (command === 'auth') return handleAuth(args, flags);
  if (command === 'profile') return handleProfile(args, flags);
  if (command === 'send') return handleSend(flags);
  if (command === 'task-link') return handleTaskLink(args, flags);
  if (command === 'message') return handleMessage(args, flags);
  if (command === 'doc') return handleDoc(args, flags);
  if (command === 'knowledge') return handleKnowledge(args, flags);
  if (command === 'calendar') return handleCalendar(args, flags);
  if (command === 'task') return handleTask(args, flags);
  if (command === 'sheets') return handleSheets(args, flags);
  if (command === 'base') return handleBase(args, flags);
  if (command === 'meeting') return handleMeeting(args, flags);
  if (command === 'note') return handleNote(args, flags);
  if (command === 'minutes') return handleMinutes(args, flags);
  if (command === 'capability') return handleCapability(args, flags);
  if (command === 'events') return handleEvents(args, flags);
  if (command === 'workflow') return handleWorkflow(args, flags);
  if (command === 'result') return handleResult(args, flags);
  if (command === 'recent') return handleRecent(args, flags);
  if (command === 'capabilities') return { status: 'ok', capabilities: capabilityManifest() };
  const { runtime, configPath, config } = loadContext(flags);
  if (command === 'permissions') {
    const auth = createLark(runtime);
    const [authStatus, appScopes] = await Promise.all([
      auth.runLarkCliJson(['auth', 'status', '--verify', '--json']),
      auth.runLarkCliJson(['auth', 'scopes', '--json']),
    ]);
    return { status: 'ok', permissions: permissionReport({ authStatus, appScopes }) };
  }
  if (command === 'status') {
    return {
      ...collectStatus(runtime, config, configPath),
      directory: buildDirectoryService(runtime).status(),
      groupDirectory: buildGroupDirectoryService(runtime).status(),
    };
  }
  if (command === 'doctor') {
    const result = doctor(runtime, config, configPath);
    const directory = buildDirectoryService(runtime).status();
    const directoryUsable = !directory.enabled
      || (directory.exists && directory.secure && directory.complete);
    const directoryDegraded = directory.enabled
      && directoryUsable
      && (directory.stale || directory.hasLastError);
    const directoryCheck = {
      name: 'directory_cache',
      ok: directoryUsable,
      severity: directoryUsable ? (directoryDegraded ? 'warning' : 'ok') : 'error',
      detail: directory.enabled
        ? `users=${directory.userCount}, stale=${directory.stale}, error=${directory.hasLastError}`
        : 'disabled',
    };
    result.checks.push(directoryCheck);
    result.status.directory = directory;
    const groupDirectory = buildGroupDirectoryService(runtime).status();
    const groupDirectoryUsable = !groupDirectory.enabled
      || (groupDirectory.exists && groupDirectory.secure && groupDirectory.complete);
    const groupDirectoryDegraded = groupDirectory.enabled
      && groupDirectoryUsable
      && (groupDirectory.stale || groupDirectory.hasLastError);
    const groupDirectoryCheck = {
      name: 'group_directory_cache',
      ok: groupDirectoryUsable,
      severity: groupDirectoryUsable ? (groupDirectoryDegraded ? 'warning' : 'ok') : 'error',
      detail: groupDirectory.enabled
        ? `groups=${groupDirectory.groupCount}, stale=${groupDirectory.stale}, error=${groupDirectory.hasLastError}`
        : 'disabled',
    };
    result.checks.push(groupDirectoryCheck);
    result.status.groupDirectory = groupDirectory;
    result.ok = !result.checks.some((check) => check.severity === 'error');
    result.health = result.ok
      ? result.checks.some((check) => check.severity === 'warning') || result.status.warnings.length
        ? 'degraded'
        : 'healthy'
      : 'failed';
    return result;
  }
  if (command === 'start' || command === 'restart') {
    const action = controlBridgeService(config, {
      restart: command === 'restart',
      platform: runtime.platform,
      projectRoot: runtime.projectRoot,
      env: runtime.env,
    });
    return { ...action, status: collectStatus(runtime, config, configPath) };
  }
  throw new Error(`unknown command: ${command}`);
}

if (require.main === module) {
  main()
    .then((value) => {
      printJson(value);
      if (value?.status === 'timeout') process.exitCode = 3;
    })
    .catch((error) => {
      process.stderr.write(`${JSON.stringify({ status: 'error', error: String(error.message || error) }, null, 2)}\n`);
      process.exitCode = 1;
    });
}

module.exports = {
  buildDocumentInstruction,
  handleBase,
  handleCapability,
  handleAuth,
  handleCalendar,
  handleDoc,
  handleEvents,
  handleKnowledge,
  handleMeeting,
  handleMessage,
  handleMinutes,
  handleNote,
  handleProfile,
  handleSend,
  handleSheets,
  handleTask,
  handleTaskLink,
  handleTargets,
  handleWorkflow,
  main,
  parseArgs,
  readPayload,
  scheduleAnalysis,
  collectValues,
  validateBoundedA1Range,
  validateCalendarReadRange,
  validateMeetingReadRange,
  validateRemoteIdList,
};
