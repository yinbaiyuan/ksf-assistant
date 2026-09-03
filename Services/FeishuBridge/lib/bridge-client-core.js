const crypto = require('node:crypto');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { createLarkCliRunner } = require('./lark-cli-runner');
const { loadOfficialCredentials } = require('./lark-cli-credentials');
const { OFFICIAL_EVENT_TRANSPORT } = require('./official-event-adapter');
const { FIXED_EVENT_KEYS, inspectEventInbox } = require('./event-inbox');
const {
  defaultEventConsumerProfilePath,
  eventConsumerProfileDefinition,
  eventConsumerShouldConnect,
  inspectEventConsumerProfile,
} = require('./event-consumer-profile');
const { capabilityCatalog } = require('./capability-registry');
const { LARK_CLI_FLAG_SNAPSHOT_VERSION } = require('./lark-cli-flag-snapshot');
const { cleanupStagedMedia, stageOutboundMedia } = require('./media-staging');
const { ACTIONBOX_TERMINAL_STATUSES } = require('./action-registry');
const { runtimeManifest } = require('./runtime-manifest');
const {
  bridgeServiceStatus,
  chmodPrivate,
  controlBridgeService,
  defaultCodexBin,
  defaultDataRoot,
  defaultLarkCliBin,
  defaultLogDir,
  privatePathBoundary,
  privateRootSecurityStatus,
} = require('./platform-runtime');

const OUTBOX_TERMINAL_STATUSES = new Set([
  'sent',
  'dry_run',
  'denied',
  'duplicate',
  'invalid',
  'failed',
  'partial_sent',
]);
const DOCBOX_TERMINAL_STATUSES = new Set([
  'completed',
  'dry_run',
  'denied',
  'duplicate',
  'invalid',
  'failed',
]);
const MESSAGE_TARGET_TYPES = new Set(['chat_id', 'open_id']);
const MESSAGE_TYPES = new Set(['text', 'markdown', 'card', 'image', 'file']);
const DOCUMENT_TARGET_KINDS = new Set([
  'url',
  'docx_token',
  'wiki_url',
  'wiki_token',
  'folder_token',
]);
const UPDATE_MODES = new Set(['append', 'overwrite', 'str_replace']);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function safeOneLine(value, limit = 240) {
  const text = String(value || '').replace(/\s+/g, ' ').trim();
  if (text.length <= limit) return text;
  return `${text.slice(0, Math.max(0, limit - 16))}…`;
}

function parseBool(value, fallback = false) {
  if (value === undefined || value === null || value === '') return fallback;
  return /^(1|true|yes|on)$/i.test(String(value).trim());
}

function parseNumber(value, fallback) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}

function parseCsv(value) {
  return String(value || '')
    .split(',')
    .map((item) => item.trim())
    .filter(Boolean);
}

function parseEnvFile(filePath) {
  if (!fs.existsSync(filePath)) return {};
  const result = {};
  for (const line of fs.readFileSync(filePath, 'utf8').split('\n')) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const index = trimmed.indexOf('=');
    if (index <= 0) continue;
    const key = trimmed.slice(0, index).trim();
    let value = trimmed.slice(index + 1).trim();
    if (
      (value.startsWith('"') && value.endsWith('"'))
      || (value.startsWith("'") && value.endsWith("'"))
    ) {
      value = value.slice(1, -1);
    }
    result[key] = value;
  }
  return result;
}

function resolveFromRoot(projectRoot, value, fallback) {
  const candidate = value || fallback;
  if (path.isAbsolute(candidate)) return path.normalize(candidate);
  return path.resolve(projectRoot, candidate);
}

function buildRuntime(projectRoot, processEnv = process.env, options = {}) {
  const root = path.resolve(projectRoot);
  const platform = options.platform || process.platform;
  const homeDir = options.homeDir || os.homedir();
  const localEnvPath = path.join(root, '.env.local');
  const localEnv = parseEnvFile(localEnvPath);
  const env = { ...localEnv, ...processEnv };
  const dataRoot = defaultDataRoot({ platform, env, homeDir });
  const logDir = resolveFromRoot(root, env.FEISHU_BRIDGE_LOG_DIR, defaultLogDir(root, { platform, env, homeDir }));
  const runtime = {
    platform,
    projectRoot: root,
    dataRoot,
    localEnvPath,
    localEnv,
    env,
    logDir,
    outboxPath: resolveFromRoot(root, env.FEISHU_OUTBOX_PATH, path.join(logDir, 'outbox.jsonl')),
    outboxResultsPath: path.join(logDir, 'outbox-results.jsonl'),
    outboxStatePath: path.join(logDir, 'outbox-state.json'),
    docboxPath: resolveFromRoot(root, env.FEISHU_DOCBOX_PATH, path.join(logDir, 'docbox.jsonl')),
    docboxResultsPath: path.join(logDir, 'docbox-results.jsonl'),
    docboxStatePath: path.join(logDir, 'docbox-state.json'),
    actionboxPath: resolveFromRoot(root, env.FEISHU_ACTIONBOX_PATH, path.join(logDir, 'actionbox.jsonl')),
    actionboxResultsPath: path.join(logDir, 'actionbox-results.jsonl'),
    actionboxStatePath: path.join(logDir, 'actionbox-state.json'),
    eventConsumerStatePath: path.join(logDir, 'event-consumer-state.json'),
    eventConsumerProfilePath: defaultEventConsumerProfilePath(dataRoot),
    eventInboxDir: resolveFromRoot(
      root,
      env.FEISHU_EVENT_INBOX_DIR,
      path.join(dataRoot, 'events'),
    ),
    taskLogPath: path.join(logDir, 'tasks.jsonl'),
    messageLogPath: path.join(logDir, 'messages.jsonl'),
    auditDir: resolveFromRoot(root, env.FEISHU_AUDIT_DIR, path.join(logDir, 'audit')),
    directoryCachePath: resolveFromRoot(
      root,
      env.FEISHU_DIRECTORY_CACHE_PATH,
      path.join(dataRoot, 'directory.json'),
    ),
    directoryStatePath: resolveFromRoot(
      root,
      env.FEISHU_DIRECTORY_STATE_PATH,
      path.join(dataRoot, 'directory-state.json'),
    ),
    groupDirectoryCachePath: resolveFromRoot(
      root,
      env.FEISHU_GROUP_DIRECTORY_CACHE_PATH,
      path.join(dataRoot, 'group-directory.json'),
    ),
    groupDirectoryStatePath: resolveFromRoot(
      root,
      env.FEISHU_GROUP_DIRECTORY_STATE_PATH,
      path.join(dataRoot, 'group-directory-state.json'),
    ),
    mediaStagingDir: platform === 'win32'
      ? path.join(dataRoot, 'private-cache', 'outbox-assets')
      : path.join(root, 'node_modules', '.cache', 'feishu-bridge', 'outbox-assets'),
    pidPath: path.join(logDir, 'bridge.pid'),
    larkCliBin: defaultLarkCliBin(root, { platform, env }),
    codexBin: defaultCodexBin({ platform, env }),
  };
  runtime.privatePathBoundary = privatePathBoundary(dataRoot, {
    logDir: runtime.logDir,
    eventInboxDir: runtime.eventInboxDir,
    eventConsumerProfilePath: runtime.eventConsumerProfilePath,
    directoryCachePath: runtime.directoryCachePath,
    directoryStatePath: runtime.directoryStatePath,
    groupDirectoryCachePath: runtime.groupDirectoryCachePath,
    groupDirectoryStatePath: runtime.groupDirectoryStatePath,
    mediaStagingDir: runtime.mediaStagingDir,
  }, platform);
  return runtime;
}

function defaultClientConfigPath(homeDir = os.homedir(), options = {}) {
  return path.join(defaultDataRoot({
    platform: options.platform || process.platform,
    env: options.env || process.env,
    homeDir,
  }), 'client.json');
}

function defaultClientConfig() {
  return {
    schemaVersion: 4,
    launchdLabel: 'com.example.feishu-bot-bridge',
    windowsTaskName: 'FeishuBotBridge',
    defaultSource: 'codex',
    messageTargets: {},
    nameBindings: {},
    groupNameBindings: {},
    documentTargets: {},
    testAssets: {},
    eventWatches: [],
  };
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

function readJsonFile(filePath, fallback = {}) {
  try {
    return JSON.parse(fs.readFileSync(filePath, 'utf8'));
  } catch (error) {
    if (error.code === 'ENOENT') return fallback;
    throw new Error(`invalid JSON file ${filePath}: ${safeOneLine(error.message)}`);
  }
}

function loadClientConfig(configPath = defaultClientConfigPath()) {
  assertRegularOrMissing(configPath);
  const loaded = readJsonFile(configPath, {});
  return {
    ...defaultClientConfig(),
    ...loaded,
    schemaVersion: Math.max(4, Number(loaded.schemaVersion) || 0),
    messageTargets: { ...(loaded.messageTargets || {}) },
    nameBindings: { ...(loaded.nameBindings || {}) },
    groupNameBindings: { ...(loaded.groupNameBindings || {}) },
    documentTargets: { ...(loaded.documentTargets || {}) },
    testAssets: { ...(loaded.testAssets || {}) },
  };
}

function writeSecureJson(filePath, value) {
  const dir = path.dirname(filePath);
  fs.mkdirSync(dir, { recursive: true, mode: 0o700 });
  const dirStat = fs.lstatSync(dir);
  if (dirStat.isSymbolicLink() || !dirStat.isDirectory()) {
    throw new Error(`unsafe config directory: ${dir}`);
  }
  chmodPrivate(dir, 0o700);
  assertRegularOrMissing(filePath);
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

function initializeClientConfig(_runtime, configPath) {
  const config = loadClientConfig(configPath);
  writeSecureJson(configPath, config);
  return { config };
}

async function saveTestAssetBinding(configPath, { alias, kind, value, capabilityId }) {
  if (typeof alias !== 'string' || !/^Codex桥测试[^\r\n]{0,80}$/.test(alias)) throw new Error('invalid_test_asset_alias');
  if (typeof kind !== 'string' || !/^[a-z][a-z0-9_]{1,79}$/.test(kind)) throw new Error('invalid_test_asset_kind');
  if (typeof value !== 'string' || !value || value.length > 4000) throw new Error('invalid_test_asset_value');
  fs.mkdirSync(path.dirname(configPath), { recursive: true, mode: 0o700 });
  chmodPrivate(path.dirname(configPath), 0o700);
  const release = await acquireFileLock(`${configPath}.lock`);
  try {
    const config = loadClientConfig(configPath);
    config.testAssets[alias] = {
      kind,
      value,
      capabilityId: String(capabilityId || ''),
      savedAt: new Date().toISOString(),
    };
    writeSecureJson(configPath, config);
    return {
      alias,
      kind,
      valueFingerprint: fingerprintIdentifier(value),
      capabilityId: String(capabilityId || ''),
    };
  } finally {
    release();
  }
}

function resolveTestAssetBindings(definition, input, config) {
  const resolved = { ...input };
  const compatibleKinds = {
    task_id: new Set(['task_id', 'task_guid']),
    tasklist_id: new Set(['tasklist_id', 'tasklist_guid']),
  };
  for (const [name, schema] of Object.entries(definition?.flags || {})) {
    const value = resolved[name];
    if (schema.private || typeof value !== 'string' || !value.startsWith('asset:')) continue;
    const alias = value.slice('asset:'.length);
    const binding = config.testAssets?.[alias];
    if (!binding) throw new Error(`unknown_test_asset:${alias}`);
    const expectedKind = name.replace(/-/g, '_');
    if (binding.kind !== expectedKind && !compatibleKinds[binding.kind]?.has(expectedKind)) {
      throw new Error(`test_asset_kind_mismatch:${name}`);
    }
    resolved[name] = binding.value;
  }
  if (['mindnotes.node.create', 'mindnotes.node.update'].includes(definition?.id)
    && resolved.data && typeof resolved.data === 'object' && Array.isArray(resolved.data.nodes)) {
    resolved.data = {
      ...resolved.data,
      nodes: resolved.data.nodes.map((node) => {
        if (!node || typeof node !== 'object' || Array.isArray(node)) return node;
        const next = { ...node };
        for (const fieldName of ['parent_id', 'node_id']) {
          const value = next[fieldName];
          if (typeof value !== 'string' || !value.startsWith('asset:')) continue;
          const alias = value.slice('asset:'.length);
          const binding = config.testAssets?.[alias];
          if (!binding) throw new Error(`unknown_test_asset:${alias}`);
          if (binding.kind !== 'mindnote_node_id') throw new Error(`test_asset_kind_mismatch:${fieldName}`);
          next[fieldName] = binding.value;
        }
        return next;
      }),
    };
  }
  return resolved;
}

function validateCapabilityLocationTarget(definition, input, config) {
  if (['mindnotes.node.create', 'mindnotes.node.update'].includes(definition?.id)) {
    const value = input?.['mindnote-id'];
    if (typeof value !== 'string' || !value.startsWith('asset:')) return 'wiki_mindnote_asset_required';
    const binding = config.testAssets?.[value.slice('asset:'.length)];
    if (!binding || binding.kind !== 'mindnote_id') return 'wiki_mindnote_asset_required';
    if (!['wiki.node.create', 'wiki.node.get'].includes(binding.capabilityId)) return 'wiki_mindnote_asset_required';
  }
  if (definition?.id === 'markdown.create' && input?.['wiki-token'] !== undefined) {
    return 'wiki_markdown_write_not_supported';
  }
  return '';
}

function fingerprintIdentifier(value) {
  if (!value) return '';
  return `sha256:${crypto.createHash('sha256').update(String(value)).digest('hex').slice(0, 12)}`;
}

function isLocalRequestId(value) {
  return /^(?:OUT|DOC|ACT|CAP)-\d{14}-[A-F0-9]{8}$/.test(String(value || ''));
}

function redactSensitiveText(value) {
  return String(value || '')
    .replace(
      /https:\/\/[^\s/]+\.feishu\.cn\/(?:wiki|docx|base|sheets|minutes)\/[A-Za-z0-9_-]+(?:\?[^\s]*)?/gi,
      (match) => `[feishu-url:${fingerprintIdentifier(match)}]`,
    )
    .replace(
      /https:\/\/vc\.feishu\.cn\/j\/[A-Za-z0-9_-]+(?:\?[^\s]*)?/gi,
      (match) => `[feishu-url:${fingerprintIdentifier(match)}]`,
    )
    .replace(
      /https:\/\/applink\.feishu\.cn\/[^\s]+/gi,
      (match) => `[feishu-url:${fingerprintIdentifier(match)}]`,
    )
    .replace(
      /(["'](?:token|node_token|obj_token|file_token|wiki_token|document_id|space_id|block_id|block_token|version_id|release_id|session_id|run_id|app_id|whiteboard_id|board_id)["']\s*:\s*["'])([^"'\[]+)(["'])/gi,
      (_match, prefix, identifier, suffix) => `${prefix}[${fingerprintIdentifier(identifier)}]${suffix}`,
    )
    .replace(/\b(?:ou|oc|om)_[A-Za-z0-9_-]+\b|\bod-[A-Za-z0-9_-]+\b/g, (match) => `[${fingerprintIdentifier(match)}]`)
    .replace(/\b(?:docx|wikcn|bascn|shtcn|obcn|tbl|rec)[A-Za-z0-9_-]{8,}\b/g, (match) => `[${fingerprintIdentifier(match)}]`)
    .replace(/(已创建官方版本)\s+[A-Za-z0-9_-]+(?=，)/g, '$1 [REDACTED]')
    .replace(/(app[_-]?secret|tenant[_-]?access[_-]?token|authorization)\s*[:=]\s*\S+/gi, '$1=[REDACTED]');
}

function isSensitiveRemoteIdentifierKey(key) {
  const name = String(key || '');
  if (/^(?:capabilityId|(?:before|after)?RevisionId|revision_id|document_revision_id)$/i.test(name)) return false;
  return /^(?:id|guid|token|.*(?:Id|_id|Guid|_guid|Token|_token|No|_no))$/i.test(name);
}

function messageAliasForTarget(target, config) {
  return Object.entries(config.messageTargets || {})
    .find(([, item]) => item.type === target?.type && item.id === target?.id)?.[0] || '';
}

function sanitizeTarget(target, config) {
  if (!target) return undefined;
  return {
    alias: messageAliasForTarget(target, config) || undefined,
    type: target.type,
    idFingerprint: fingerprintIdentifier(target.id),
  };
}

function sanitizeDocumentTarget(target, config) {
  const alias = Object.entries(config.documentTargets || {})
    .find(([, candidate]) => candidate.kind === target.kind && candidate.value === target.value)?.[0];
  return {
    alias,
    kind: target.kind,
    valueFingerprint: fingerprintIdentifier(target.value),
  };
}

function sanitizeForOutput(value, config, key = '', options = {}) {
  if (Array.isArray(value)) return value.map((item) => sanitizeForOutput(item, config, key, options));
  if (!value || typeof value !== 'object') {
    if (typeof value === 'string') {
      if (key === 'id' && isLocalRequestId(value)) return value;
      if (key === 'url') return options.preserveUrls ? value : fingerprintIdentifier(value);
      if (/^(text|body|replacement|byTitle|withEllipsis|content)$/i.test(key)) {
        return { redacted: true, length: value.length };
      }
      if (/^(filePath|path)$/i.test(key)) return { redacted: true };
      if (/^(open_?id|chat_?id|message_?id|token)$/i.test(key)) return fingerprintIdentifier(value);
      return redactSensitiveText(value);
    }
    return value;
  }
  const result = {};
  for (const [childKey, childValue] of Object.entries(value)) {
    if (childKey === 'id' && isLocalRequestId(childValue)) {
      result[childKey] = childValue;
      continue;
    }
    if (childKey === 'target' && childValue?.type && childValue?.id) {
      result[childKey] = sanitizeTarget(childValue, config);
      continue;
    }
    if (childKey === 'target' && childValue?.kind && childValue?.value) {
      result[childKey] = sanitizeDocumentTarget(childValue, config);
      continue;
    }
    if (childKey === 'messageIds' || isSensitiveRemoteIdentifierKey(childKey)) {
      result[childKey] = Array.isArray(childValue)
        ? childValue.map(fingerprintIdentifier)
        : fingerprintIdentifier(childValue);
      continue;
    }
    result[childKey] = sanitizeForOutput(childValue, config, childKey, options);
  }
  return result;
}

function sanitizeReadForOutput(value, config, options = {}, key = '') {
  const previewLength = Math.max(1, Math.min(100000, Number(options.previewLength) || 500));
  if (Array.isArray(value)) return value.map((item) => sanitizeReadForOutput(item, config, options, key));
  if (value === null || typeof value !== 'object') {
    if (isSensitiveRemoteIdentifierKey(key) && (typeof value === 'string' || typeof value === 'number')) {
      return fingerprintIdentifier(String(value));
    }
    if (typeof value !== 'string') return value;
    if (key === 'url') return options.preserveUrls === true ? value : fingerprintIdentifier(value);
    if (/^(?:filePath|path|transcript_file)$/i.test(key)) return { redacted: true };
    if (/^(text|body|content|plain_text|markdown|summary|transcript)$/i.test(key)) {
      return {
        preview: redactSensitiveText(value.slice(0, previewLength)),
        length: value.length,
        truncated: value.length > previewLength,
      };
    }
    if (key === 'id') {
      return fingerprintIdentifier(value);
    }
    if (/secret|access_?token|authorization/i.test(key)) return { redacted: true };
    return redactSensitiveText(value);
  }
  const result = {};
  for (const [childKey, childValue] of Object.entries(value)) {
    result[childKey] = sanitizeReadForOutput(childValue, config, options, childKey);
  }
  return result;
}

function validateBoundedTimeRange(start, end, maxWindowMs = 31 * 24 * 60 * 60 * 1000) {
  const timezoneAware = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:\d{2})$/;
  if (!timezoneAware.test(String(start || '')) || !timezoneAware.test(String(end || ''))) {
    throw new Error('start and end must be timezone-aware ISO 8601 values');
  }
  const startMs = Date.parse(start);
  const endMs = Date.parse(end);
  if (!Number.isFinite(startMs) || !Number.isFinite(endMs)) throw new Error('start and end must be valid dates');
  if (startMs >= endMs) throw new Error('start must be before end');
  if (endMs - startMs > maxWindowMs) throw new Error('message read window cannot exceed 31 days');
  return { start: new Date(startMs), end: new Date(endMs) };
}

function validateReadLimit(value, maximum, fallback = maximum) {
  const candidate = value === undefined || value === null || value === '' ? fallback : Number(value);
  if (!Number.isInteger(candidate)) throw new Error('limit must be an integer');
  if (candidate < 1 || candidate > maximum) throw new Error(`limit must be between 1 and ${maximum}`);
  return candidate;
}

function resolveMessageTarget(input, config) {
  const aliasTarget = config.messageTargets?.[input];
  if (aliasTarget) return { ...aliasTarget, alias: input };
  const match = String(input || '').match(/^(chat_id|open_id):(.+)$/);
  if (!match) throw new Error('message target must be a configured alias or chat_id:<id>/open_id:<id>');
  return { type: match[1], id: match[2] };
}

function resolveDocumentTarget(input, config) {
  const aliasTarget = config.documentTargets?.[input];
  if (aliasTarget) return { ...aliasTarget, alias: input };
  const value = String(input || '').trim();
  if (!value) return undefined;
  if (/^https?:\/\//i.test(value)) {
    return { kind: value.includes('/wiki/') ? 'wiki_url' : 'url', value };
  }
  const match = value.match(/^(url|docx_token|wiki_url|wiki_token|folder_token):(.+)$/);
  if (!match) throw new Error('document target must be an alias, URL, or kind:value');
  return { kind: match[1], value: match[2] };
}

function validateMessageRequest(request) {
  if (!request || typeof request !== 'object') return 'request_not_object';
  const allowedFields = new Set([
    'id', 'type', 'target', 'text', 'filePath', 'explicitAuthorization',
    'dryRun', 'source', 'reason', 'trace', 'createdAt',
  ]);
  const unknownField = Object.keys(request).find((key) => !allowedFields.has(key));
  if (unknownField) return `unsupported_request_field:${unknownField}`;
  if (request.dryRun !== undefined && typeof request.dryRun !== 'boolean') return 'invalid_dry_run';
  if (!request.id || typeof request.id !== 'string') return 'missing_id';
  if (!MESSAGE_TYPES.has(request.type)) return 'unsupported_type';
  if (!request.target || typeof request.target !== 'object') return 'missing_target';
  if (!MESSAGE_TARGET_TYPES.has(request.target.type)) return 'unsupported_target_type';
  if (!request.target.id || typeof request.target.id !== 'string') return 'missing_target_id';
  if (request.explicitAuthorization !== true) return 'explicit_authorization_required';
  if (['text', 'markdown', 'card'].includes(request.type)) {
    if (!request.text || typeof request.text !== 'string') return 'missing_text';
    if (request.type === 'card') {
      try {
        const card = JSON.parse(request.text);
        if (!card || typeof card !== 'object' || Array.isArray(card)) return 'invalid_card_json';
      } catch {
        return 'invalid_card_json';
      }
    }
  }
  if (['image', 'file'].includes(request.type) && (!request.filePath || typeof request.filePath !== 'string')) {
    return 'missing_file_path';
  }
  if (!request.source || typeof request.source !== 'string') return 'missing_source';
  return '';
}

function validateDocumentRequest(request) {
  if (!request || typeof request !== 'object') return 'request_not_object';
  const allowedFields = new Set([
    'id', 'type', 'action', 'target', 'content', 'instruction',
    'explicitAuthorization', 'dryRun', 'source', 'reason', 'trace', 'createdAt',
    'versionPolicy', 'updateMode', 'newTitle', 'selection', 'identity',
  ]);
  const unknownField = Object.keys(request).find((key) => !allowedFields.has(key));
  if (unknownField) return `unsupported_request_field:${unknownField}`;
  if (request.dryRun !== undefined && typeof request.dryRun !== 'boolean') return 'invalid_dry_run';
  if (!request.id || typeof request.id !== 'string') return 'missing_id';
  if (request.type !== 'document_task') return 'unsupported_type';
  if (!['create_document', 'update_document'].includes(request.action)) return 'unsupported_action';
  if (request.identity !== documentIdentityForTarget(request.target)) return 'unsupported_identity';
  if (request.explicitAuthorization !== true) return 'explicit_authorization_required';
  if (request.versionPolicy && request.versionPolicy !== 'official_before_update') return 'unsupported_version_policy';
  if (!request.source || typeof request.source !== 'string') return 'missing_source';
  if (!request.instruction || typeof request.instruction !== 'string') return 'missing_instruction';
  if (!request.content || typeof request.content !== 'object') return 'missing_content';
  if (!['markdown', 'text'].includes(request.content.format)) return 'unsupported_content_format';
  if (typeof request.content.text !== 'string' || !request.content.text.trim()) return 'missing_content_text';
  if (request.target !== undefined) {
    if (!request.target || typeof request.target !== 'object') return 'invalid_target';
    if (!DOCUMENT_TARGET_KINDS.has(request.target.kind)) return 'unsupported_target_kind';
    if (!request.target.value || typeof request.target.value !== 'string') return 'missing_target_value';
  }
  if (request.action === 'update_document' && !request.target) return 'missing_target';
  if (request.updateMode !== undefined && !UPDATE_MODES.has(request.updateMode)) return 'unsupported_update_mode';
  return '';
}

function documentIdentityForTarget(target) {
  return ['wiki_url', 'wiki_token'].includes(target?.kind) ? 'bot' : 'user';
}

function validateHighImpactUpdate({ mode, confirmHighImpact, selectionByTitle, selectionWithEllipsis }) {
  if (!UPDATE_MODES.has(mode)) throw new Error(`unsupported update mode: ${mode}`);
  if (['overwrite', 'str_replace'].includes(mode) && !confirmHighImpact) {
    throw new Error(`${mode} requires --confirm-high-impact`);
  }
  if (mode === 'str_replace' && !selectionByTitle && !selectionWithEllipsis) {
    throw new Error('str_replace requires an exact replacement pattern via --pattern-file');
  }
  if (mode === 'str_replace' && selectionByTitle) {
    throw new Error('selection-by-title is not supported by lark-cli 1.0.92; provide an exact pattern file');
  }
}

function requestId(prefix, date = new Date()) {
  const timestamp = date.toISOString().replace(/[-:.TZ]/g, '').slice(0, 14);
  return `${prefix}-${timestamp}-${crypto.randomBytes(4).toString('hex').toUpperCase()}`;
}

function readCompleteJsonl(filePath) {
  if (!fs.existsSync(filePath)) return [];
  const content = fs.readFileSync(filePath, 'utf8');
  const lines = content.split('\n');
  if (content && !content.endsWith('\n')) lines.pop();
  return lines
    .filter((line) => line.trim())
    .map((line) => {
      try {
        return JSON.parse(line);
      } catch {
        return { _invalid: true, error: 'invalid_json_line' };
      }
    });
}

async function acquireFileLock(lockPath, { timeoutMs = 5000, staleMs = 30000 } = {}) {
  const started = Date.now();
  while (true) {
    try {
      const fd = fs.openSync(lockPath, 'wx', 0o600);
      fs.writeFileSync(fd, `${JSON.stringify({ pid: process.pid, createdAt: new Date().toISOString() })}\n`);
      fs.closeSync(fd);
      return () => {
        try {
          fs.unlinkSync(lockPath);
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        }
      };
    } catch (error) {
      if (error.code !== 'EEXIST') throw error;
      try {
        const stat = fs.lstatSync(lockPath);
        if (stat.isSymbolicLink()) throw new Error(`unsafe queue lock: ${lockPath}`);
        if (Date.now() - stat.mtimeMs > staleMs) {
          fs.unlinkSync(lockPath);
          continue;
        }
      } catch (statError) {
        if (statError.code !== 'ENOENT') throw statError;
      }
      if (Date.now() - started >= timeoutMs) throw new Error(`queue lock timeout: ${lockPath}`);
      await sleep(20);
    }
  }
}

async function appendJsonlLocked(filePath, record, options = {}) {
  fs.mkdirSync(path.dirname(filePath), { recursive: true });
  const release = await acquireFileLock(`${filePath}.client.lock`, options);
  try {
    const buffer = Buffer.from(`${JSON.stringify(record)}\n`, 'utf8');
    const fd = fs.openSync(filePath, 'a', 0o600);
    try {
      let offset = 0;
      while (offset < buffer.length) {
        const written = fs.writeSync(fd, buffer, offset, buffer.length - offset);
        if (written <= 0) throw new Error(`queue write made no progress: ${filePath}`);
        offset += written;
      }
      fs.fsyncSync(fd);
    } finally {
      fs.closeSync(fd);
    }
  } finally {
    release();
  }
}

function readState(filePath) {
  return readJsonFile(filePath, {});
}

async function wakeWorker(statePath, endpoint, { timeoutMs = 2500, httpModule = http } = {}) {
  const state = readState(statePath);
  const host = state.wake?.host || '127.0.0.1';
  const port = Number(state.wake?.actualPort || 0);
  if (!port || state.wake?.enabled === false) {
    return { attempted: false, accepted: false, pollingFallback: true, reason: 'wake_unavailable' };
  }
  if (host !== '127.0.0.1') {
    return { attempted: false, accepted: false, pollingFallback: true, reason: 'wake_host_not_local' };
  }
  return new Promise((resolve) => {
    const request = httpModule.request({ host, port, path: endpoint, method: 'POST', timeout: timeoutMs }, (response) => {
      response.resume();
      resolve({
        attempted: true,
        accepted: response.statusCode === 202,
        statusCode: response.statusCode,
        pollingFallback: response.statusCode !== 202,
      });
    });
    request.on('timeout', () => request.destroy(new Error('wake_timeout')));
    request.on('error', (error) => resolve({
      attempted: true,
      accepted: false,
      pollingFallback: true,
      reason: safeOneLine(error.message),
    }));
    request.end();
  });
}

function terminalStatuses(kind) {
  if (kind === 'outbox') return OUTBOX_TERMINAL_STATUSES;
  if (kind === 'docbox') return DOCBOX_TERMINAL_STATUSES;
  if (kind === 'actionbox') return ACTIONBOX_TERMINAL_STATUSES;
  throw new Error(`unsupported result kind: ${kind}`);
}

async function waitForResult({ kind, id, resultsPath, timeoutMs, pollMs = 250 }) {
  const started = Date.now();
  const terminals = terminalStatuses(kind);
  while (Date.now() - started < timeoutMs) {
    const records = readCompleteJsonl(resultsPath).filter((record) => record.id === id);
    const terminal = [...records].reverse().find((record) => terminals.has(record.status));
    if (terminal) return terminal;
    await sleep(pollMs);
  }
  return { id, status: 'timeout', pending: true, timeoutMs };
}

async function submitQueueRequest({
  kind,
  request,
  queuePath,
  resultsPath,
  statePath,
  wakeEndpoint,
  asyncMode = false,
  timeoutMs,
  pollMs,
  wakeOptions,
}) {
  await appendJsonlLocked(queuePath, request);
  const wake = await wakeWorker(statePath, wakeEndpoint, wakeOptions);
  if (asyncMode) return { id: request.id, status: 'submitted', wake };
  const result = await waitForResult({ kind, id: request.id, resultsPath, timeoutMs, pollMs });
  return { ...result, wake };
}

function isProcessAlive(pid) {
  if (!Number.isInteger(Number(pid)) || Number(pid) <= 0) return false;
  try {
    process.kill(Number(pid), 0);
    return true;
  } catch {
    return false;
  }
}

function readPidState(pidPath) {
  const value = readJsonFile(pidPath, {});
  return {
    present: Boolean(value.pid),
    alive: isProcessAlive(value.pid),
    pid: value.pid || undefined,
    startedAt: value.startedAt || undefined,
  };
}

function launchAgentStatus(label, uid = process.getuid?.()) {
  return bridgeServiceStatus({ launchdLabel: label }, {
    platform: 'darwin', projectRoot: process.cwd(), uid,
  });
}

function controlLaunchAgent(label, { restart = false, uid = process.getuid?.() } = {}) {
  return controlBridgeService({ launchdLabel: label }, {
    restart, platform: 'darwin', projectRoot: process.cwd(), uid,
  });
}

function stateSummary(filePath) {
  const state = readState(filePath);
  const rawLastError = state.lastError === undefined
    ? ''
    : typeof state.lastError === 'string' ? state.lastError : JSON.stringify(state.lastError);
  return {
    exists: fs.existsSync(filePath),
    processedLineCount: state.processedLineCount || 0,
    processedIdCount: Object.keys(state.processedIds || {}).length,
    lastProcessedAt: state.lastProcessedAt || undefined,
    hasLastError: Boolean(state.lastError),
    lastError: rawLastError ? redactSensitiveText(safeOneLine(rawLastError)) : undefined,
    wake: {
      enabled: Boolean(state.wake?.enabled),
      host: state.wake?.host,
      actualPort: state.wake?.actualPort || undefined,
    },
  };
}

function regularFileSize(filePath) {
  try {
    const stat = fs.lstatSync(filePath);
    return stat.isFile() && !stat.isSymbolicLink() ? stat.size : 0;
  } catch (error) {
    if (error.code === 'ENOENT') return 0;
    throw error;
  }
}

function queueMaintenanceSummary(runtime, queuePath, resultsPath, statePath) {
  const warnBytes = Math.max(1024, parseNumber(runtime.env.FEISHU_QUEUE_WARN_BYTES, 100 * 1024 * 1024));
  const sizes = {
    queueBytes: regularFileSize(queuePath),
    resultsBytes: regularFileSize(resultsPath),
    stateBytes: regularFileSize(statePath),
  };
  return {
    ...sizes,
    warnBytes,
    warnings: [
      sizes.queueBytes >= warnBytes ? 'queue_file_large' : '',
      sizes.resultsBytes >= warnBytes ? 'results_file_large' : '',
    ].filter(Boolean),
  };
}

function configPermissions(configPath, platform = process.platform) {
  try {
    const stat = fs.lstatSync(configPath);
    const regular = stat.isFile() && !stat.isSymbolicLink();
    return {
      exists: true,
      regular,
      mode: platform === 'win32' ? undefined : `0${(stat.mode & 0o777).toString(8)}`,
      securityModel: platform === 'win32' ? 'windows-acl' : 'posix-mode',
      secure: regular && (platform === 'win32' || (stat.mode & 0o077) === 0),
    };
  } catch (error) {
    if (error.code === 'ENOENT') return { exists: false, secure: false };
    return { exists: true, secure: false, error: safeOneLine(error.message) };
  }
}

function eventConsumerSummary(runtime) {
  const enabled = parseBool(runtime.env.FEISHU_EVENT_CONSUMER_ENABLED, true);
  const configuredTransport = runtime.env.FEISHU_EVENT_TRANSPORT || OFFICIAL_EVENT_TRANSPORT;
  const expected = parseCsv(runtime.env.FEISHU_EVENT_KEYS || FIXED_EVENT_KEYS.join(','));
  const state = readState(runtime.eventConsumerStatePath);
  const configuredProfile = inspectEventConsumerProfile(runtime.eventConsumerProfilePath);
  const profileDefinition = configuredProfile.valid
    ? eventConsumerProfileDefinition(configuredProfile.profile)
    : { inboundConnection: false, inboundScope: 'none', localCapabilitiesPreserved: true };
  const desiredConnection = configuredProfile.valid
    && eventConsumerShouldConnect(configuredProfile.profile, enabled);
  const events = {};
  for (const key of expected) {
    const item = state.events?.[key] || {};
    events[key] = {
      status: item.status || 'unknown',
      errorCode: item.errorCode || undefined,
      restartScheduled: Boolean(item.restartScheduled),
      lastReceivedAt: item.lastReceivedAt || undefined,
      updatedAt: item.updatedAt || undefined,
    };
  }
  return {
    enabled,
    profile: configuredProfile.profile,
    profileValid: configuredProfile.valid,
    profileSource: configuredProfile.source,
    profileUpdatedAt: configuredProfile.updatedAt || undefined,
    profileError: configuredProfile.error || undefined,
    activeProfile: state.profile || undefined,
    desiredConnection,
    inboundScope: profileDefinition.inboundScope,
    localCapabilitiesPreserved: profileDefinition.localCapabilitiesPreserved,
    realtimeSwitching: true,
    configuredTransport,
    transport: state.transport || configuredTransport,
    status: state.status || (enabled ? 'unknown' : 'disabled'),
    connection: state.connection || undefined,
    expected,
    fixedCatalogCount: FIXED_EVENT_KEYS.length,
    missingFixedKeys: FIXED_EVENT_KEYS.filter((key) => !expected.includes(key)),
    unsupportedKeys: expected.filter((key) => !FIXED_EVENT_KEYS.includes(key)),
    stateExists: fs.existsSync(runtime.eventConsumerStatePath),
    events,
  };
}

function collectStatus(runtime, config, configPath) {
  const service = bridgeServiceStatus(config, {
    platform: runtime.platform,
    projectRoot: runtime.projectRoot,
    env: runtime.env,
  });
  const pid = readPidState(runtime.pidPath);
  const serviceStatus = runtime.platform === 'win32'
    ? {
      ...service,
      taskState: service.state,
      bridgeProcessAlive: Boolean(pid.alive),
      running: Boolean(service.running || pid.alive),
      state: pid.alive ? 'Running' : service.state,
    }
    : service;
  const personDirectoryEnabled = parseBool(runtime.env.FEISHU_DIRECTORY_ENABLED);
  const groupDirectoryEnabled = parseBool(runtime.env.FEISHU_GROUP_DIRECTORY_ENABLED);
  let targetPolicy = 'any_explicit_id';
  if (personDirectoryEnabled && groupDirectoryEnabled) {
    targetPolicy = 'any_explicit_id_or_unique_person_or_group_name';
  } else if (personDirectoryEnabled) {
    targetPolicy = 'any_explicit_id_or_unique_person_name';
  } else if (groupDirectoryEnabled) {
    targetPolicy = 'any_explicit_id_or_unique_group_name';
  }
  return {
    runtime: runtimeManifest(),
    platform: runtime.platform,
    projectRoot: runtime.projectRoot,
    privateData: privateRootSecurityStatus(runtime.dataRoot, {
      platform: runtime.platform,
      projectRoot: runtime.projectRoot,
      env: runtime.env,
    }),
    privatePathBoundary: runtime.privatePathBoundary,
    service: serviceStatus,
    ...(runtime.platform === 'darwin' ? { launchd: serviceStatus } : {}),
    ...(runtime.platform === 'win32' ? { windowsTask: serviceStatus } : {}),
    eventConsumer: eventConsumerSummary(runtime),
    eventInbox: inspectEventInbox(runtime.eventInboxDir, {
      enabled: parseBool(runtime.env.FEISHU_EVENT_INBOX_ENABLED, true),
    }),
    pid: { ...pid, pid: pid.pid ? fingerprintIdentifier(String(pid.pid)) : undefined },
    outbound: {
      enabled: parseBool(runtime.env.FEISHU_OUTBOUND_ENABLED),
      dryRun: parseBool(runtime.env.FEISHU_OUTBOUND_DRY_RUN),
      targetPolicy,
      state: stateSummary(runtime.outboxStatePath),
      maintenance: queueMaintenanceSummary(runtime, runtime.outboxPath, runtime.outboxResultsPath, runtime.outboxStatePath),
    },
    docbox: {
      enabled: parseBool(runtime.env.FEISHU_DOCBOX_ENABLED),
      dryRun: parseBool(runtime.env.FEISHU_DOCBOX_DRY_RUN, true),
      allowedSourceCount: parseCsv(runtime.env.FEISHU_DOCBOX_ALLOWED_SOURCES || 'local,codex').length,
      state: stateSummary(runtime.docboxStatePath),
      maintenance: queueMaintenanceSummary(runtime, runtime.docboxPath, runtime.docboxResultsPath, runtime.docboxStatePath),
    },
    actionbox: {
      enabled: parseBool(runtime.env.FEISHU_ACTIONBOX_ENABLED),
      dryRun: parseBool(runtime.env.FEISHU_ACTIONBOX_DRY_RUN, true),
      allowedSourceCount: parseCsv(runtime.env.FEISHU_ACTIONBOX_ALLOWED_SOURCES || 'local,codex').length,
      state: stateSummary(runtime.actionboxStatePath),
      maintenance: queueMaintenanceSummary(runtime, runtime.actionboxPath, runtime.actionboxResultsPath, runtime.actionboxStatePath),
    },
    clientConfig: configPermissions(configPath, runtime.platform),
    warnings: runtime.localEnv.FEISHU_BRIDGE_PROJECT_ROOT
      && path.resolve(runtime.localEnv.FEISHU_BRIDGE_PROJECT_ROOT) !== runtime.projectRoot
      ? ['local_env_project_root_mismatch']
      : [],
  };
}

function createLark(runtime, { as } = {}) {
  return createLarkCliRunner({
    bin: runtime.larkCliBin,
    cwd: runtime.projectRoot,
    env: runtime.env,
    profile: runtime.env.LARK_CLI_PROFILE || '',
    as: as || runtime.env.LARK_CLI_AS || 'bot',
    privateDir: runtime.platform === 'win32'
      ? path.join(runtime.dataRoot, 'private-cache', 'action-payloads')
      : '',
    mediaRoot: runtime.mediaStagingDir,
  });
}

function eventConnectionAssessment(eventConsumer, pidState, now = Date.now()) {
  const state = eventConsumer?.connection?.state || eventConsumer?.status || 'unknown';
  if (eventConsumer?.status === 'connected' && state === 'connected') {
    return { ok: true, severity: 'ok', detail: 'connected' };
  }
  const startedAt = Date.parse(pidState?.startedAt || '');
  const ageMs = Number.isFinite(startedAt) ? now - startedAt : Number.POSITIVE_INFINITY;
  if (pidState?.alive && ageMs >= 0 && ageMs <= 15000
    && ['idle', 'connecting', 'stopped', 'unknown'].includes(state)) {
    return { ok: false, severity: 'warning', detail: `${state}:startup_grace` };
  }
  return { ok: false, severity: 'error', detail: state };
}

function doctor(runtime, config, configPath) {
  const status = collectStatus(runtime, config, configPath);
  const pidState = readPidState(runtime.pidPath);
  const checks = [];
  const check = (name, ok, detail = '') => checks.push({
    name,
    ok: Boolean(ok),
    severity: ok ? 'ok' : 'error',
    ...(detail ? { detail } : {}),
  });
  check('project_root', fs.existsSync(path.join(runtime.projectRoot, 'bot-bridge.js')));
  check(runtime.platform === 'win32' ? 'scheduled_task_loaded' : 'launch_agent_loaded', status.service.loaded);
  check('bridge_process_alive', pidState.alive);
  check('private_data_root', status.privateData.secure, status.privateData.model);
  check(
    'private_path_boundary',
    status.privatePathBoundary.secure,
    status.privatePathBoundary.enforced
      ? status.privatePathBoundary.escaped.join(',') || 'inside_private_root'
      : 'not_required_on_this_platform',
  );
  check('client_config_secure', status.clientConfig.secure);
  try {
    const version = createLark(runtime).ensureReady();
    check('lark_cli_auth', true, safeOneLine(version));
    check(
      'lark_cli_registry_snapshot',
      String(version).includes(LARK_CLI_FLAG_SNAPSHOT_VERSION),
      `expected=${LARK_CLI_FLAG_SNAPSHOT_VERSION}, capabilities=${capabilityCatalog().length}`,
    );
  } catch (error) {
    check('lark_cli_auth', false, redactSensitiveText(error.message));
  }
  check(
    'event_consumer_profile',
    status.eventConsumer.profileValid,
    status.eventConsumer.profileValid
      ? `${status.eventConsumer.profile}:${status.eventConsumer.desiredConnection ? 'connected' : 'disconnected'}`
      : status.eventConsumer.profileError,
  );
  if (status.eventConsumer.desiredConnection) {
    check(
      'event_catalog_fixed',
      status.eventConsumer.missingFixedKeys.length === 0
        && status.eventConsumer.unsupportedKeys.length === 0,
      `fixed=${status.eventConsumer.fixedCatalogCount}, missing=${status.eventConsumer.missingFixedKeys.length}, unsupported=${status.eventConsumer.unsupportedKeys.length}`,
    );
    check(
      'event_transport_single',
      status.eventConsumer.configuredTransport === OFFICIAL_EVENT_TRANSPORT
        && status.eventConsumer.transport === OFFICIAL_EVENT_TRANSPORT,
      status.eventConsumer.transport,
    );
    try {
      const credentials = loadOfficialCredentials({
        env: runtime.env,
        homeDir: os.homedir(),
        platform: runtime.platform,
        projectRoot: runtime.projectRoot,
      });
      check('official_sdk_credentials', true, credentials.source);
    } catch (error) {
      check('official_sdk_credentials', false, redactSensitiveText(error.message));
    }
    const connection = eventConnectionAssessment(status.eventConsumer, pidState);
    checks.push({ name: 'event_transport_connection', ...connection });
  }
  check(
    'event_inbox_private',
    !status.eventInbox.enabled || (status.eventInbox.exists && status.eventInbox.secure),
    status.eventInbox.enabled
      ? `exists=${status.eventInbox.exists}, files=${status.eventInbox.files || 0}`
      : 'disabled',
  );
  const codex = spawnSync(runtime.codexBin, ['app-server', 'daemon', 'version'], {
    cwd: runtime.projectRoot,
    encoding: 'utf8',
    timeout: 10000,
  });
  const codexDetail = redactSensitiveText(safeOneLine(codex.stdout || codex.stderr || codex.error?.message));
  if (runtime.platform === 'win32' && codex.status !== 0 && /only supported on Unix platforms/i.test(codexDetail)) {
    checks.push({
      name: 'codex_daemon',
      ok: true,
      severity: 'warning',
      detail: 'not_applicable_on_windows',
    });
  } else {
    check('codex_daemon', codex.status === 0, codexDetail);
  }
  if (status.eventConsumer.desiredConnection) {
    for (const [eventKey, event] of Object.entries(status.eventConsumer.events)) {
      if (event.status === 'running') {
        checks.push({ name: `event:${eventKey}`, ok: true, severity: 'ok', detail: 'running' });
        continue;
      }
      checks.push({
        name: `event:${eventKey}`,
        ok: false,
        severity: 'error',
        detail: event.errorCode || event.status,
      });
    }
  }
  for (const [name, queue] of Object.entries({
    outbox: status.outbound,
    docbox: status.docbox,
    actionbox: status.actionbox,
  })) {
    if (queue.enabled) {
      checks.push({
        name: `${name}_wake`,
        ok: Boolean(queue.state.wake.enabled && queue.state.wake.host === '127.0.0.1' && queue.state.wake.actualPort),
        severity: queue.state.wake.enabled && queue.state.wake.host === '127.0.0.1' && queue.state.wake.actualPort ? 'ok' : 'warning',
        detail: queue.state.wake.enabled
          ? `host=${queue.state.wake.host || '-'}, listening=${Boolean(queue.state.wake.actualPort)}`
          : 'polling_fallback_only',
      });
    }
    if (!queue.maintenance.warnings.length) continue;
    checks.push({
      name: `${name}_maintenance`,
      ok: true,
      severity: 'warning',
      detail: queue.maintenance.warnings.join(','),
    });
  }
  const hasError = checks.some((item) => item.severity === 'error');
  const hasWarning = checks.some((item) => item.severity === 'warning') || status.warnings.length > 0;
  return {
    ok: !hasError,
    health: hasError ? 'failed' : hasWarning ? 'degraded' : 'healthy',
    checks,
    status,
  };
}

function parseDocxTokenFromUrl(value) {
  return String(value || '').match(/\/docx\/([A-Za-z0-9]+)/)?.[1] || '';
}

function parseWikiTokenFromUrl(value) {
  return String(value || '').match(/\/wiki\/([A-Za-z0-9]+)/)?.[1] || '';
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

async function resolveInspectableDocument(target, lark) {
  const kind = target?.kind;
  const value = String(target?.value || '').trim();
  if (!value) throw new Error('missing document target');
  if (kind === 'docx_token') return { token: value, objType: 'docx' };
  let wikiToken = '';
  if (kind === 'wiki_token') wikiToken = value;
  if (kind === 'url' || kind === 'wiki_url') {
    const docxToken = parseDocxTokenFromUrl(value);
    if (docxToken) return { token: docxToken, objType: 'docx', url: value };
    wikiToken = parseWikiTokenFromUrl(value);
  }
  if (wikiToken) {
    const response = await lark.larkApi('GET', '/open-apis/wiki/v2/spaces/get_node', {
      params: { token: wikiToken, obj_type: 'wiki' },
      timeoutMs: 60000,
    });
    const data = lark.larkApiData(response) || {};
    const node = data.node || data;
    if (node.obj_type !== 'docx' || !node.obj_token) throw new Error('wiki node is not a docx document');
    return { token: node.obj_token, objType: 'docx', url: value, title: node.title || '' };
  }
  throw new Error(`unsupported inspect target: ${kind || '-'}`);
}

async function inspectDocument(runtime, target, { full = false, previewLength = 2000, lark } = {}) {
  const client = lark || createLark(runtime);
  client.ensureReady();
  const document = await resolveInspectableDocument(target, client);
  const fetched = await client.larkDocFetch(document.token, { timeoutMs: 60000 });
  const content = findFirstKey(fetched, ['markdown', 'content', 'text', 'plain_text']);
  const text = typeof content === 'string' ? content : JSON.stringify(fetched || {});
  return {
    document: {
      title: document.title || findFirstKey(fetched, ['title', 'name']) || undefined,
      url: document.url,
      objType: document.objType,
      tokenFingerprint: fingerprintIdentifier(document.token),
    },
    revisionId: findFirstKey(fetched, ['document_revision_id', 'revision_id', 'revisionId', 'revision']),
    contentLength: text.length,
    content: full ? text : text.slice(0, previewLength),
    truncated: !full && text.length > previewLength,
  };
}

function collectAuditFiles(root) {
  if (!fs.existsSync(root)) return [];
  const result = [];
  for (const entry of fs.readdirSync(root, { withFileTypes: true })) {
    const entryPath = path.join(root, entry.name);
    if (entry.isDirectory()) result.push(...collectAuditFiles(entryPath));
    if (entry.isFile() && entry.name.endsWith('.md')) result.push(entryPath);
  }
  return result;
}

function summarizeAuditText(value, limit = 10) {
  const sections = [];
  let current;
  for (const line of String(value || '').split('\n')) {
    const heading = line.match(/^##\s+(.+)$/);
    if (heading) {
      current = { title: safeOneLine(redactSensitiveText(heading[1]), 120) };
      sections.push(current);
      continue;
    }
    if (!current) continue;
    const field = line.match(/^-\s+(status|action|source)：(.+)$/);
    if (field) current[field[1]] = safeOneLine(redactSensitiveText(field[2]), 120);
  }
  return {
    entryCount: sections.length,
    recentEntries: sections.slice(-Math.max(1, limit)).reverse(),
  };
}

function recentRecords(runtime, config, kind, limit = 10) {
  const paths = {
    outbox: runtime.outboxResultsPath,
    docbox: runtime.docboxResultsPath,
    actionbox: runtime.actionboxResultsPath,
    tasks: runtime.taskLogPath,
    messages: runtime.messageLogPath,
  };
  if (kind === 'audit') {
    return collectAuditFiles(runtime.auditDir)
      .map((filePath) => ({ filePath, stat: fs.statSync(filePath) }))
      .sort((a, b) => b.stat.mtimeMs - a.stat.mtimeMs)
      .slice(0, limit)
      .map(({ filePath, stat }) => {
        const summary = summarizeAuditText(fs.readFileSync(filePath, 'utf8'));
        return {
          file: path.relative(runtime.auditDir, filePath),
          modifiedAt: stat.mtime.toISOString(),
          sizeBytes: stat.size,
          ...summary,
        };
      });
  }
  const filePath = paths[kind];
  if (!filePath) throw new Error(`unsupported recent kind: ${kind}`);
  return readCompleteJsonl(filePath).slice(-limit).reverse().map((record) => sanitizeForOutput(record, config));
}

module.exports = {
  DOCBOX_TERMINAL_STATUSES,
  OUTBOX_TERMINAL_STATUSES,
  ACTIONBOX_TERMINAL_STATUSES,
  MESSAGE_TYPES,
  acquireFileLock,
  appendJsonlLocked,
  buildRuntime,
  cleanupStagedMedia,
  collectStatus,
  configPermissions,
  controlBridgeService,
  controlLaunchAgent,
  createLark,
  defaultClientConfig,
  defaultClientConfigPath,
  doctor,
  documentIdentityForTarget,
  eventConnectionAssessment,
  fingerprintIdentifier,
  isLocalRequestId,
  initializeClientConfig,
  inspectDocument,
  isProcessAlive,
  launchAgentStatus,
  loadClientConfig,
  parseBool,
  parseCsv,
  parseEnvFile,
  parseNumber,
  readCompleteJsonl,
  recentRecords,
  redactSensitiveText,
  requestId,
  resolveTestAssetBindings,
  resolveDocumentTarget,
  resolveMessageTarget,
  sanitizeForOutput,
  sanitizeReadForOutput,
  summarizeAuditText,
  sanitizeTarget,
  saveTestAssetBinding,
  stageOutboundMedia,
  submitQueueRequest,
  validateDocumentRequest,
  validateCapabilityLocationTarget,
  validateHighImpactUpdate,
  validateMessageRequest,
  validateBoundedTimeRange,
  validateReadLimit,
  waitForResult,
  wakeWorker,
  writeSecureJson,
};
