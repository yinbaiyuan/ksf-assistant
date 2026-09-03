const assert = require('node:assert/strict');
const fs = require('node:fs');
const http = require('node:http');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');
const test = require('node:test');
const {
  appendJsonlLocked,
  buildRuntime,
  documentIdentityForTarget,
  fingerprintIdentifier,
  initializeClientConfig,
  loadClientConfig,
  readCompleteJsonl,
  requestId,
  resolveDocumentTarget,
  resolveMessageTarget,
  resolveTestAssetBindings,
  sanitizeForOutput,
  sanitizeReadForOutput,
  saveTestAssetBinding,
  summarizeAuditText,
  submitQueueRequest,
  validateCapabilityLocationTarget,
  validateDocumentRequest,
  validateHighImpactUpdate,
  validateMessageRequest,
  eventConnectionAssessment,
  isLocalRequestId,
  waitForResult,
  wakeWorker,
} = require('../lib/bridge-client-core');
const { assertPrivateMode, bridgeTestEnv } = require('./helpers/platform-private');

test('doctor treats the official SDK startup handshake as a bounded warning, then fails closed', () => {
  const now = Date.parse('2026-09-02T04:00:10.000Z');
  assert.deepEqual(eventConnectionAssessment(
    { status: 'stopped', connection: { state: 'idle' } },
    { alive: true, startedAt: '2026-09-02T04:00:05.000Z' },
    now,
  ), { ok: false, severity: 'warning', detail: 'idle:startup_grace' });
  assert.deepEqual(eventConnectionAssessment(
    { status: 'stopped', connection: { state: 'idle' } },
    { alive: true, startedAt: '2026-09-02T03:59:00.000Z' },
    now,
  ), { ok: false, severity: 'error', detail: 'idle' });
});
const { parseArgs } = require('../scripts/bridge-client');

function temporaryDirectory(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-bridge-client-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

test('parseArgs separates positional commands and camel-cases flags', () => {
  const parsed = parseArgs([
    'doc',
    'update',
    '--target',
    '测试文档',
    '--confirm-high-impact',
    '--timeout-ms=5000',
  ]);
  assert.deepEqual(parsed.positional, ['doc', 'update']);
  assert.equal(parsed.flags.target, '测试文档');
  assert.equal(parsed.flags.confirmHighImpact, true);
  assert.equal(parsed.flags.timeoutMs, '5000');
});

test('client config initializes securely without deriving aliases from outbound policy', (t) => {
  const dir = temporaryDirectory(t);
  const runtime = buildRuntime(dir, {
    FEISHU_OUTBOUND_ALLOWED_OPEN_IDS: 'ou_private_value',
    FEISHU_OUTBOUND_ALLOWED_CHAT_IDS: 'oc_one,oc_two',
  });
  const configPath = path.join(dir, 'private', 'client.json');
  initializeClientConfig(runtime, configPath);
  assert.deepEqual(loadClientConfig(configPath).messageTargets, {});
  assertPrivateMode(path.dirname(configPath), 0o700);
  assertPrivateMode(configPath, 0o600);
});

test('test asset identifiers are stored only in private client config and resolve by fixed field kind', async (t) => {
  const dir = temporaryDirectory(t);
  const configPath = path.join(dir, 'private', 'client.json');
  initializeClientConfig(buildRuntime(dir, {}), configPath);
  const saved = await saveTestAssetBinding(configPath, {
    alias: 'Codex桥测试-Apps-1.0.0',
    kind: 'app_id',
    value: 'app_private_value',
    capabilityId: 'apps.app.create',
  });
  assert.equal(saved.valueFingerprint.includes('app_private_value'), false);
  assertPrivateMode(configPath, 0o600);
  const config = loadClientConfig(configPath);
  assert.equal(config.testAssets['Codex桥测试-Apps-1.0.0'].value, 'app_private_value');
  assert.deepEqual(resolveTestAssetBindings(
    { flags: { 'app-id': { type: 'string' }, message: { type: 'string', private: true } } },
    { 'app-id': 'asset:Codex桥测试-Apps-1.0.0', message: 'asset:正文不是引用' },
    config,
  ), { 'app-id': 'app_private_value', message: 'asset:正文不是引用' });
  assert.throws(() => resolveTestAssetBindings(
    { flags: { 'session-id': { type: 'string' } } },
    { 'session-id': 'asset:Codex桥测试-Apps-1.0.0' },
    config,
  ), /test_asset_kind_mismatch/);

  const rootSaved = await saveTestAssetBinding(configPath, {
    alias: 'Codex桥测试-Mindnote根节点-1.0.0',
    kind: 'mindnote_node_id',
    value: 'mindnote_root_private',
    capabilityId: 'mindnotes.nodes.list',
  });
  assert.match(rootSaved.valueFingerprint, /^sha256:/);
  const currentConfig = loadClientConfig(configPath);
  assert.deepEqual(resolveTestAssetBindings(
    { id: 'mindnotes.node.create', flags: { data: { type: 'json', private: true } } },
    { data: { client_token: 'client-token-123', nodes: [{
      parent_id: 'asset:Codex桥测试-Mindnote根节点-1.0.0',
      texts: [{ element_type: 'text', text: { content: '正文' } }],
    }] } },
    currentConfig,
  ), { data: { client_token: 'client-token-123', nodes: [{
    parent_id: 'mindnote_root_private',
    texts: [{ element_type: 'text', text: { content: '正文' } }],
  }] } });
  assert.throws(() => resolveTestAssetBindings(
    { id: 'mindnotes.node.create', flags: { data: { type: 'json', private: true } } },
    { data: { client_token: 'client-token-123', nodes: [{
      parent_id: 'asset:Codex桥测试-Apps-1.0.0',
    }] } },
    currentConfig,
  ), /test_asset_kind_mismatch:parent_id/);
});

test('location-bound writes fail closed when identity cannot be determined', () => {
  const definition = { id: 'mindnotes.node.create' };
  const config = {
    testAssets: {
      WikiMindnote: {
        kind: 'mindnote_id',
        value: 'mindnote_private_value',
        capabilityId: 'wiki.node.get',
      },
      UnknownMindnote: {
        kind: 'mindnote_id',
        value: 'mindnote_private_value',
        capabilityId: 'mindnotes.nodes.list',
      },
    },
  };
  assert.equal(validateCapabilityLocationTarget(
    definition,
    { 'mindnote-id': 'asset:WikiMindnote' },
    config,
  ), '');
  assert.equal(validateCapabilityLocationTarget(
    definition,
    { 'mindnote-id': 'mindnote_private_value' },
    config,
  ), 'wiki_mindnote_asset_required');
  assert.equal(validateCapabilityLocationTarget(
    definition,
    { 'mindnote-id': 'asset:UnknownMindnote' },
    config,
  ), 'wiki_mindnote_asset_required');
  assert.equal(validateCapabilityLocationTarget(
    { id: 'markdown.create' },
    { 'wiki-token': 'wiki_private_value' },
    config,
  ), 'wiki_markdown_write_not_supported');
});

test('message aliases and arbitrary explicit targets resolve while output hides identifiers', () => {
  const config = {
    messageTargets: { 我: { type: 'open_id', id: 'ou_private_value' } },
    documentTargets: {},
  };
  const target = resolveMessageTarget('我', config);
  assert.equal(target.alias, '我');
  assert.deepEqual(resolveMessageTarget('open_id:ou_other', config), {
    type: 'open_id',
    id: 'ou_other',
  });
  assert.deepEqual(resolveMessageTarget('chat_id:oc_other', config), {
    type: 'chat_id',
    id: 'oc_other',
  });
  const sanitized = sanitizeForOutput({ target, messageIds: ['om_private'] }, config);
  assert.equal(sanitized.target.alias, '我');
  assert.equal(sanitized.target.idFingerprint, fingerprintIdentifier('ou_private_value'));
  assert.equal(JSON.stringify(sanitized).includes('ou_private_value'), false);
  assert.equal(JSON.stringify(sanitized).includes('om_private'), false);
});

test('production outbound has no local destination allowlist gate', () => {
  const bridgeSource = fs.readFileSync(path.join(__dirname, '..', 'bot-bridge.js'), 'utf8');
  const clientCoreSource = fs.readFileSync(path.join(__dirname, '..', 'lib', 'bridge-client-core.js'), 'utf8');
  const clientSource = fs.readFileSync(path.join(__dirname, '..', 'scripts', 'bridge-client.js'), 'utf8');
  assert.doesNotMatch(bridgeSource, /FEISHU_OUTBOUND_ALLOWED_(?:CHAT|OPEN)_IDS/);
  assert.doesNotMatch(bridgeSource, /target_not_allowed|isOutboundTargetAllowed/);
  assert.doesNotMatch(clientCoreSource, /FEISHU_OUTBOUND_ALLOWED_(?:CHAT|OPEN)_IDS/);
  assert.doesNotMatch(clientCoreSource, /assertMessageTargetAllowed/);
  assert.doesNotMatch(clientSource, /assertMessageTargetAllowed/);
});

test('task-link card creation carries the explicit local authorization into outbox', () => {
  const clientSource = fs.readFileSync(path.join(__dirname, '..', 'scripts', 'bridge-client.js'), 'utf8');
  const taskLinkRequest = clientSource.match(/const request = \{[\s\S]*?id: requestId\('OUT-LINK'\)[\s\S]*?\n  \};/);
  assert.ok(taskLinkRequest, 'task-link outbox request was not found');
  assert.match(taskLinkRequest[0], /explicitAuthorization: true/);
});

test('production worker dry-runs an arbitrary explicit target without calling Feishu send', async (t) => {
  const dir = temporaryDirectory(t);
  const fakeLarkPath = path.join(dir, 'fake-lark.js');
  const invocationLogPath = path.join(dir, 'lark-invocations.jsonl');
  fs.writeFileSync(fakeLarkPath, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
fs.appendFileSync(process.env.FAKE_LARK_INVOCATIONS, JSON.stringify(args) + '\\n');
if (args.includes('--version')) process.stdout.write('fake-lark 1.0.0\\n');
else process.stdout.write('{}\\n');
`);
  fs.chmodSync(fakeLarkPath, 0o700);
  const outboxPath = path.join(dir, 'outbox.jsonl');
  const resultPath = path.join(dir, 'outbox-results.jsonl');
  fs.writeFileSync(outboxPath, `${JSON.stringify({
    id: 'OUT-ANY-EXPLICIT-DRY-RUN',
    type: 'text',
    target: { type: 'open_id', id: 'ou_not_configured_locally' },
    text: 'dry-run only',
    explicitAuthorization: true,
    source: 'test',
  })}\n`);
  const child = spawn(process.execPath, [path.join(__dirname, '..', 'bot-bridge.js')], {
    cwd: path.join(__dirname, '..'),
    env: bridgeTestEnv(dir, {
      ...process.env,
      FEISHU_BRIDGE_LOG_DIR: dir,
      FEISHU_AUDIT_DIR: path.join(dir, 'audit'),
      FEISHU_OUTBOX_PATH: outboxPath,
      FEISHU_OUTBOX_POLL_MS: '500',
      FEISHU_OUTBOUND_ENABLED: 'true',
      FEISHU_OUTBOUND_DRY_RUN: 'true',
      FEISHU_OUTBOUND_WAKE_ENABLED: 'false',
      FEISHU_DOCBOX_ENABLED: 'false',
      FEISHU_EVENT_CONSUMER_ENABLED: 'false',
      LARK_CLI_BIN: fakeLarkPath,
      FAKE_LARK_INVOCATIONS: invocationLogPath,
    }),
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let diagnostics = '';
  child.stdout.on('data', (chunk) => { diagnostics += chunk; });
  child.stderr.on('data', (chunk) => { diagnostics += chunk; });
  t.after(() => {
    if (!child.killed) child.kill('SIGTERM');
  });
  const deadline = Date.now() + 5000;
  let result;
  while (Date.now() < deadline) {
    result = readCompleteJsonl(resultPath).find((item) => item.id === 'OUT-ANY-EXPLICIT-DRY-RUN');
    if (result) break;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  child.kill('SIGTERM');
  await new Promise((resolve) => child.once('exit', resolve));
  assert.ok(result, diagnostics || 'bridge did not produce an outbox result');
  assert.equal(result.status, 'dry_run');
  assert.deepEqual(result.target, { type: 'open_id', id: 'ou_not_configured_locally' });
  const invocations = readCompleteJsonl(invocationLogPath);
  assert.equal(invocations.some((args) => args.includes('+messages-send')), false);
});

test('output hides message text, document targets, and Feishu URLs by default', () => {
  const config = {
    messageTargets: {},
    documentTargets: {
      周报: { kind: 'wiki_url', value: 'https://example.feishu.cn/wiki/private_target_token' },
    },
  };
  const sanitized = sanitizeForOutput({
    target: { kind: 'wiki_url', value: 'https://example.feishu.cn/wiki/private_target_token' },
    content: { text: 'confidential body' },
    result: { url: 'https://example.feishu.cn/docx/new_document_token' },
  }, config);
  assert.equal(sanitized.target.alias, '周报');
  assert.equal(sanitized.target.valueFingerprint, fingerprintIdentifier('https://example.feishu.cn/wiki/private_target_token'));
  assert.deepEqual(sanitized.content.text, { redacted: true, length: 17 });
  assert.equal(sanitized.result.url, fingerprintIdentifier('https://example.feishu.cn/docx/new_document_token'));
  assert.equal(JSON.stringify(sanitized.target).includes('private_target_token'), false);
});

test('output fingerprints embedded block, version, Apps, and remote-operation identifiers', () => {
  const sanitized = sanitizeForOutput({
    versionId: 'version_private',
    app_id: 'app_private',
    release_id: 'release_private',
    sync_token: 'sync_private',
    revision_id: 12,
    capabilityId: 'sheets.workbook.create',
    updateResultSummary: '{"block_id":"doxcn_private","block_token":"board_private"}',
  }, { messageTargets: {}, documentTargets: {} });
  assert.match(sanitized.versionId, /^sha256:/);
  assert.match(sanitized.app_id, /^sha256:/);
  assert.match(sanitized.release_id, /^sha256:/);
  assert.match(sanitized.sync_token, /^sha256:/);
  assert.equal(sanitized.revision_id, 12);
  assert.equal(sanitized.capabilityId, 'sheets.workbook.create');
  assert.equal(sanitized.updateResultSummary.includes('doxcn_private'), false);
  assert.equal(sanitized.updateResultSummary.includes('board_private'), false);
});

test('output preserves local queue request IDs for async result lookup but fingerprints remote IDs', () => {
  const requestIdValue = 'CAP-20260902040823-8753D3DD';
  assert.equal(isLocalRequestId(requestIdValue), true);
  const sanitized = sanitizeForOutput({ id: requestIdValue, response: { id: 'remote_private' } }, {
    messageTargets: {}, documentTargets: {},
  });
  assert.equal(sanitized.id, requestIdValue);
  assert.match(sanitized.response.id, /^sha256:/);
  assert.equal(
    sanitizeForOutput('已创建官方版本 0nVBn9，并以 append 模式更新文档。', {}, 'summary'),
    '已创建官方版本 [REDACTED]，并以 append 模式更新文档。',
  );
  assert.equal(
    sanitizeReadForOutput({ url: 'https://applink.feishu.cn/client/todo/task_list?guid=private' }, {}, {}).url,
    fingerprintIdentifier('https://applink.feishu.cn/client/todo/task_list?guid=private'),
  );
});

test('new document results may explicitly preserve their user-facing URL', () => {
  const url = 'https://example.feishu.cn/docx/new_document_token';
  const sanitized = sanitizeForOutput({ result: { url } }, { messageTargets: {}, documentTargets: {} }, '', {
    preserveUrls: true,
  });
  assert.equal(sanitized.result.url, url);
});

test('audit summaries expose event metadata without historical targets or bodies', () => {
  const secretUrl = 'https://example.feishu.cn/wiki/private_target_token';
  const secretBody = 'confidential body';
  const summary = summarizeAuditText(`# 审计\n## 文档任务 15:03:51\n\n- status：completed\n- action：update_document\n- source：codex\n- target：wiki_url:${secretUrl}\n- content：${secretBody}\n## 出站消息 15:04:00\n\n- status：sent\n- action：send\n- text：${secretBody}\n`);
  assert.equal(summary.entryCount, 2);
  assert.deepEqual(summary.recentEntries[0], {
    title: '出站消息 15:04:00',
    status: 'sent',
    action: 'send',
  });
  assert.equal(JSON.stringify(summary).includes(secretUrl), false);
  assert.equal(JSON.stringify(summary).includes(secretBody), false);
});

test('document targets resolve and high-impact modes require explicit confirmation and selection', () => {
  assert.deepEqual(
    resolveDocumentTarget('https://example.feishu.cn/wiki/abc123', { documentTargets: {} }),
    { kind: 'wiki_url', value: 'https://example.feishu.cn/wiki/abc123' },
  );
  assert.doesNotThrow(() => validateHighImpactUpdate({ mode: 'append' }));
  assert.throws(() => validateHighImpactUpdate({ mode: 'overwrite' }), /confirm-high-impact/);
  assert.throws(
    () => validateHighImpactUpdate({ mode: 'str_replace', confirmHighImpact: true }),
    /exact replacement pattern/,
  );
  assert.doesNotThrow(() => validateHighImpactUpdate({
    mode: 'str_replace',
    confirmHighImpact: true,
    selectionWithEllipsis: '旧内容...结束',
  }));
  assert.throws(() => validateHighImpactUpdate({
    mode: 'str_replace',
    confirmHighImpact: true,
    selectionByTitle: '标题',
  }), /not supported by lark-cli 1.0.92/);
});

test('request validators enforce the bridge schemas and official version policy', () => {
  assert.equal(documentIdentityForTarget(undefined), 'user');
  assert.equal(documentIdentityForTarget({ kind: 'url' }), 'user');
  assert.equal(documentIdentityForTarget({ kind: 'docx_token' }), 'user');
  assert.equal(documentIdentityForTarget({ kind: 'wiki_url' }), 'bot');
  assert.equal(documentIdentityForTarget({ kind: 'wiki_token' }), 'bot');
  const message = {
    id: requestId('OUT', new Date('2026-08-31T00:00:00Z')),
    type: 'text',
    target: { type: 'open_id', id: 'ou_allowed' },
    text: 'hello',
    explicitAuthorization: true,
    source: 'codex',
  };
  assert.equal(validateMessageRequest(message), '');
  const document = {
    id: requestId('DOC', new Date('2026-08-31T00:00:00Z')),
    type: 'document_task',
    action: 'update_document',
    identity: 'bot',
    versionPolicy: 'official_before_update',
    target: { kind: 'wiki_url', value: 'https://example.feishu.cn/wiki/abc123' },
    content: { format: 'markdown', text: 'append me' },
    updateMode: 'append',
    instruction: 'inspect, version, update, verify',
    explicitAuthorization: true,
    source: 'codex',
  };
  assert.equal(validateDocumentRequest(document), '');
  assert.equal(validateDocumentRequest({ ...document, identity: 'user' }), 'unsupported_identity');
  assert.equal(validateDocumentRequest({ ...document, identity: undefined }), 'unsupported_identity');
  assert.equal(validateDocumentRequest({
    ...document,
    identity: 'user',
    target: { kind: 'url', value: 'https://example.feishu.cn/docx/abc123' },
  }), '');
  assert.equal(validateDocumentRequest({ ...document, versionPolicy: 'skip' }), 'unsupported_version_policy');
  assert.equal(validateDocumentRequest({ ...document, explicitAuthorization: false }), 'explicit_authorization_required');
  assert.equal(validateMessageRequest({ ...message, explicitAuthorization: false }), 'explicit_authorization_required');
});

test('concurrent writers append complete parseable JSONL records', async (t) => {
  const dir = temporaryDirectory(t);
  const queuePath = path.join(dir, 'queue.jsonl');
  await Promise.all(Array.from({ length: 30 }, (_, index) => appendJsonlLocked(queuePath, {
    id: `item-${index}`,
    content: 'x'.repeat(4096),
  })));
  const records = readCompleteJsonl(queuePath);
  assert.equal(records.length, 30);
  assert.equal(records.some((record) => record._invalid), false);
  assert.equal(new Set(records.map((record) => record.id)).size, 30);
});

test('wakeWorker accepts local 202 responses', async (t) => {
  const dir = temporaryDirectory(t);
  const statePath = path.join(dir, 'state.json');
  const server = http.createServer((request, response) => {
    assert.equal(request.method, 'POST');
    assert.equal(request.url, '/internal/outbox/wake');
    request.resume();
    response.writeHead(202);
    response.end();
  });
  await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
  t.after(() => server.close());
  fs.writeFileSync(statePath, `${JSON.stringify({
    wake: { enabled: true, host: '127.0.0.1', actualPort: server.address().port },
  })}\n`);
  const result = await wakeWorker(statePath, '/internal/outbox/wake');
  assert.equal(result.accepted, true);
  assert.equal(result.pollingFallback, false);
});

test('submit keeps polling after wake is unavailable and returns the delayed terminal result', async (t) => {
  const dir = temporaryDirectory(t);
  const queuePath = path.join(dir, 'outbox.jsonl');
  const resultsPath = path.join(dir, 'outbox-results.jsonl');
  const statePath = path.join(dir, 'outbox-state.json');
  fs.writeFileSync(statePath, `${JSON.stringify({ wake: { enabled: false } })}\n`);
  const request = { id: 'OUT-DELAYED', type: 'text' };
  setTimeout(() => {
    fs.writeFileSync(resultsPath, `${JSON.stringify({ id: request.id, status: 'sent' })}\n`);
  }, 80);
  const result = await submitQueueRequest({
    kind: 'outbox',
    request,
    queuePath,
    resultsPath,
    statePath,
    wakeEndpoint: '/internal/outbox/wake',
    timeoutMs: 1500,
    pollMs: 20,
  });
  assert.equal(result.status, 'sent');
  assert.equal(result.wake.pollingFallback, true);
  assert.deepEqual(readCompleteJsonl(queuePath), [request]);
});

test('docbox waiting skips accepted and returns completed; timeout preserves the same id', async (t) => {
  const dir = temporaryDirectory(t);
  const resultsPath = path.join(dir, 'docbox-results.jsonl');
  fs.writeFileSync(resultsPath, `${JSON.stringify({ id: 'DOC-1', status: 'accepted' })}\n`);
  setTimeout(() => fs.appendFileSync(resultsPath, `${JSON.stringify({ id: 'DOC-1', status: 'completed' })}\n`), 60);
  const completed = await waitForResult({
    kind: 'docbox',
    id: 'DOC-1',
    resultsPath,
    timeoutMs: 1000,
    pollMs: 20,
  });
  assert.equal(completed.status, 'completed');
  const timeout = await waitForResult({
    kind: 'docbox',
    id: 'DOC-STILL-PENDING',
    resultsPath,
    timeoutMs: 60,
    pollMs: 15,
  });
  assert.deepEqual(timeout, {
    id: 'DOC-STILL-PENDING',
    status: 'timeout',
    pending: true,
    timeoutMs: 60,
  });
});

test('all documented outbox and docbox terminal statuses are observable without resubmission', async (t) => {
  const dir = temporaryDirectory(t);
  const outboxResults = path.join(dir, 'outbox-results.jsonl');
  const docboxResults = path.join(dir, 'docbox-results.jsonl');
  const outboxStatuses = ['sent', 'dry_run', 'denied', 'duplicate', 'invalid', 'failed', 'partial_sent'];
  const docboxStatuses = ['completed', 'dry_run', 'denied', 'duplicate', 'invalid', 'failed'];
  fs.writeFileSync(outboxResults, outboxStatuses.map((status) => JSON.stringify({ id: `OUT-${status}`, status })).join('\n') + '\n');
  fs.writeFileSync(docboxResults, docboxStatuses.map((status) => JSON.stringify({ id: `DOC-${status}`, status })).join('\n') + '\n');
  for (const status of outboxStatuses) {
    const result = await waitForResult({
      kind: 'outbox',
      id: `OUT-${status}`,
      resultsPath: outboxResults,
      timeoutMs: 100,
      pollMs: 5,
    });
    assert.equal(result.status, status);
  }
  for (const status of docboxStatuses) {
    const result = await waitForResult({
      kind: 'docbox',
      id: `DOC-${status}`,
      resultsPath: docboxResults,
      timeoutMs: 100,
      pollMs: 5,
    });
    assert.equal(result.status, status);
  }
});

test('production docbox code keeps fetch, official version, update, and refetch ordering', () => {
  const source = fs.readFileSync(path.join(__dirname, '..', 'bot-bridge.js'), 'utf8');
  const start = source.indexOf('async function processDocboxUpdateTask');
  const end = source.indexOf('async function processDocboxTask', start);
  const body = source.slice(start, end);
  const firstFetch = body.indexOf('documentLark.larkDocFetch');
  const createVersion = body.indexOf('documentLark.larkDocCreateVersion');
  const update = body.indexOf('documentLark.larkDocUpdate');
  const secondFetch = body.indexOf('documentLark.larkDocFetch', firstFetch + 1);
  assert.ok(firstFetch >= 0);
  assert.ok(createVersion > firstFetch);
  assert.ok(update > createVersion);
  assert.ok(secondFetch > update);
  const versionFailureBlock = body.slice(createVersion, update);
  assert.match(versionFailureBlock, /document_version_create_failed/);
  assert.match(versionFailureBlock, /return recordDocboxResult/);
});

test('production docbox creation is fixed lark-cli create then reread, without a Codex agent', () => {
  const source = fs.readFileSync(path.join(__dirname, '..', 'bot-bridge.js'), 'utf8');
  const start = source.indexOf('async function processDocboxCreateTask');
  const end = source.indexOf('async function processDocboxUpdateTask', start);
  const body = source.slice(start, end);
  const create = body.indexOf('documentLark.larkDocCreate');
  const reread = body.indexOf('documentLark.larkDocFetch');
  assert.ok(create >= 0);
  assert.ok(reread > create);
  assert.match(body, /docboxLarkForRequest\(request\)/);
  assert.doesNotMatch(body, /runCodex|docbox\.log|buildDocboxTaskPrompt/);
});
