const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { createLarkCliRunner } = require('../lib/lark-cli-runner');
const {
  CAPABILITIES,
  capability,
  capabilityCatalog,
  docWhiteboardXml,
  selectCapabilityResultIdentifier,
  validateCapabilityInput,
  validateCapabilityRequest,
} = require('../lib/capability-registry');
const { executeRegisteredCapability } = require('../lib/capability-executor');
const { LARK_CLI_FLAG_SNAPSHOT, LARK_CLI_FLAG_SNAPSHOT_VERSION } = require('../lib/lark-cli-flag-snapshot');

function temp(t) {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-capability-'));
  t.after(() => fs.rmSync(dir, { recursive: true, force: true }));
  return dir;
}

function request(capabilityId, input, overrides = {}) {
  const definition = capability(capabilityId);
  return {
    id: 'CAP-TEST',
    type: 'feishu_capability',
    domain: 'capability',
    action: 'execute',
    capabilityId,
    identity: definition?.identity || 'user',
    input,
    explicitAuthorization: true,
    source: 'codex',
    ...overrides,
  };
}

function fakeRunner(t) {
  const dir = temp(t);
  fs.mkdirSync(path.join(dir, 'node_modules'), { recursive: true });
  const bin = path.join(dir, 'fake-lark.js');
  const callsPath = path.join(dir, 'calls.jsonl');
  fs.writeFileSync(bin, `#!/usr/bin/env node
const fs = require('node:fs');
const args = process.argv.slice(2);
let input = '';
process.stdin.setEncoding('utf8');
process.stdin.on('data', (chunk) => { input += chunk; });
process.stdin.on('end', () => {
  const privateBodies = [];
  for (const arg of args) {
    if (arg.startsWith('@')) privateBodies.push(fs.readFileSync(arg.slice(1), 'utf8'));
  }
  fs.appendFileSync(process.env.CALLS, JSON.stringify({ args, input, privateBodies }) + '\\n');
  process.stdout.write(JSON.stringify({ ok: true, data: { status: 'completed', release_id: 'release_private' } }) + '\\n');
});
`);
  fs.chmodSync(bin, 0o700);
  return {
    dir,
    callsPath,
    runner: createLarkCliRunner({ bin, cwd: dir, env: { ...process.env, CALLS: callsPath }, as: 'user' }),
  };
}

function calls(filePath) {
  return fs.readFileSync(filePath, 'utf8').trim().split('\n').filter(Boolean).map(JSON.parse);
}

test('1.0 registry is fixed, declarative, broad, and contains no Approval or excluded destructive commands', () => {
  assert.ok(CAPABILITIES.size >= 200);
  assert.equal(capabilityCatalog().length, CAPABILITIES.size);
  for (const definition of CAPABILITIES.values()) {
    assert.ok(['bot', 'user'].includes(definition.identity), definition.id);
    assert.ok(['read', 'write', 'high-impact-write', 'remote-operation'].includes(definition.risk), definition.id);
    assert.ok(Array.isArray(definition.command) && definition.command.length >= 2, definition.id);
    assert.equal(definition.scope.bounded, true, definition.id);
    assert.deepEqual(definition.redaction, { identifiers: true, body: true });
    if (definition.risk === 'high-impact-write') {
      assert.ok(definition.preflight, definition.id);
      assert.ok(definition.reread, definition.id);
    }
    assert.doesNotMatch(`${definition.id} ${definition.command.join(' ')}`, /approval|openapi-key|urgent_phone|urgent_sms|\+.*delete|workflow-(?:create|update|enable)/i);
  }
  for (const excluded of [
    'im.message.delete', 'wiki.node.move', 'base.workflow.create', 'apps.openapi-key.create',
    'sheets.cells.clear', 'minutes.media.download', 'vc.meeting.join',
  ]) assert.equal(capability(excluded), undefined);
  for (const id of [
    'wiki.space.create', 'wiki.node.create', 'wiki.node.copy',
    'mindnotes.node.create', 'mindnotes.node.update',
  ]) assert.equal(capability(id).identity, 'bot', id);
  assert.equal(capability('markdown.create').identity, 'user');
});

test('registered shortcut arguments are a strict subset of the pinned lark-cli flag snapshot', () => {
  assert.equal(LARK_CLI_FLAG_SNAPSHOT_VERSION, require('../package.json').dependencies['@larksuite/cli']);
  for (const definition of CAPABILITIES.values()) {
    if (definition.transport === 'raw') continue;
    const flags = LARK_CLI_FLAG_SNAPSHOT[definition.command.join(' ')];
    assert.ok(flags, definition.id);
    for (const name of Object.keys(definition.flags)) assert.ok(Object.hasOwn(flags, name), `${definition.id}: ${name}`);
    for (const item of definition.fixedArgs) {
      if (String(item).startsWith('--')) assert.ok(Object.hasOwn(flags, String(item).slice(2)), `${definition.id}: ${item}`);
    }
  }
});

test('capability requests reject arbitrary fields, implicit writes, identity changes, destructive nested operations, and missing high-impact confirmation', () => {
  assert.equal(validateCapabilityRequest(request('im.chat.create', { name: '项目群' })), '');
  assert.equal(validateCapabilityRequest(request('im.chat.create', { name: '项目群' }, { dryRun: true })), '');
  assert.equal(validateCapabilityRequest(request('im.chat.create', { name: '项目群' }, { dryRun: 'true' })), 'invalid_dry_run');
  assert.equal(validateCapabilityRequest(request('im.chat.create', { name: '项目群' }, { shortcut: 'api DELETE /open-apis/drive' })), 'unsupported_request_field:shortcut');
  assert.equal(validateCapabilityRequest(request('im.chat.create', { name: '项目群' }, { domain: 'openapi' })), 'unsupported_domain_or_action');
  assert.equal(validateCapabilityRequest(request('im.chat.create', { name: '项目群', shortcut: 'api DELETE /open-apis/drive' })), 'unsupported_input_field:shortcut');
  assert.equal(validateCapabilityRequest(request('im.chat.create', { name: '项目群' }, { explicitAuthorization: false })), 'explicit_authorization_required');
  assert.equal(validateCapabilityRequest(request('im.message.edit', { 'message-id': 'om_message', text: 'x' }, { identity: 'user' })), 'unsupported_identity');
  assert.equal(validateCapabilityRequest(request('markdown.overwrite', { 'file-token': 'file_token', content: '正文' })), 'high_impact_confirmation_required');
  assert.equal(validateCapabilityRequest(request('markdown.overwrite', { 'file-token': 'file_token', content: '正文' }, { confirmHighImpact: true })), '');
  assert.equal(validateCapabilityRequest(request('im.chat.create', { name: '项目群' }, { remoteTimeoutMs: 10000 })), 'remote_poll_not_supported');
  assert.equal(validateCapabilityRequest(request('apps.session.chat', {
    'app-id': 'app_private', 'session-id': 'session_private', message: '生成页面',
  }, { remoteTimeoutMs: 9999 })), 'invalid_remote_timeout');
  assert.equal(validateCapabilityRequest(request('apps.app.create', {
    name: '测试应用', 'app-type': 'frontend',
  }, { saveAs: 'Codex桥测试-Apps-1.0.0' })), '');
  assert.equal(validateCapabilityRequest(request('apps.app.update', {
    'app-id': 'app_private', name: '测试应用',
  }, { saveAs: 'Codex桥测试-Apps-1.0.0' })), 'capability_result_cannot_be_saved');
  assert.equal(validateCapabilityRequest(request('apps.app.create', {
    name: '测试应用', 'app-type': 'frontend',
  }, { saveAs: '普通资产' })), 'invalid_test_asset_alias');
  assert.equal(validateCapabilityRequest(request('docs.whiteboard.insert', {
    doc: 'docx_token:doc_private', 'doc-format': 'mermaid', content: 'flowchart LR\nA-->B',
  })), 'capability_requires_docbox');
  assert.match(validateCapabilityInput(capability('base.table.create'), {
    'base-token': 'base_token',
    name: 'Tasks',
    fields: [{ name: 'Title', type: 'text', operation: 'delete' }],
  }), /^forbidden_payload_operation:/);
  assert.equal(validateCapabilityInput(capability('base.base-block.create'), {
    'base-token': 'base_token', name: '审批流', type: 'workflow',
  }), 'invalid_enum:type');
  assert.equal(Object.hasOwn(capability('base.app-block.create').flags, 'no-validate'), false);
  for (const definition of CAPABILITIES.values()) {
    assert.equal(Object.hasOwn(definition.flags, 'params'), false, definition.id);
    assert.equal(Object.hasOwn(definition.flags, 'no-validate'), false, definition.id);
  }
});

test('document whiteboard input is wrapped as a narrow self-contained block and unsafe SVG is rejected', () => {
  assert.equal(
    docWhiteboardXml({ 'doc-format': 'mermaid', content: 'flowchart LR\nA-->B' }),
    '<whiteboard type="mermaid">\nflowchart LR\nA-->B\n</whiteboard>',
  );
  assert.equal(validateCapabilityInput(capability('docs.whiteboard.insert'), {
    doc: 'docx_token:doc_private', 'doc-format': 'svg', content: '<svg><script>alert(1)</script></svg>',
  }), 'unsafe_doc_whiteboard_svg');
  assert.equal(validateCapabilityInput(capability('mindnotes.node.create'), {
    'mindnote-id': 'mind_private',
    data: { client_token: 'client-token-123', nodes: [{ node_id: 'node_existing', parent_id: 'root' }] },
  }), 'mindnote_create_must_not_include_node_id');
  assert.equal(validateCapabilityInput(capability('mindnotes.node.update'), {
    'mindnote-id': 'mind_private',
    data: { client_token: 'client-token-123', nodes: [{ parent_id: 'root' }] },
  }), 'mindnote_update_requires_node_id');
});

test('Wiki Mindnote identifiers are captured only for the reviewed Mindnote object type', () => {
  const created = selectCapabilityResultIdentifier(capability('wiki.node.create'), {
    input: { 'obj-type': 'mindnote' },
    response: { data: { node: { node_token: 'wikcn_private', obj_token: 'mindnote_private' } } },
  });
  assert.deepEqual(created, { kind: 'mindnote_id', value: 'mindnote_private' });

  const ordinaryWikiNode = selectCapabilityResultIdentifier(capability('wiki.node.create'), {
    input: { 'obj-type': 'docx' },
    response: { data: { node: { node_token: 'wikcn_private', obj_token: 'docx_private' } } },
  });
  assert.deepEqual(ordinaryWikiNode, { kind: 'node_token', value: 'wikcn_private' });

  const resolved = selectCapabilityResultIdentifier(capability('wiki.node.get'), {
    response: { data: { node: { obj_type: 'mindnote', obj_token: 'mindnote_private' } } },
  });
  assert.deepEqual(resolved, { kind: 'mindnote_id', value: 'mindnote_private' });
  assert.equal(selectCapabilityResultIdentifier(capability('wiki.node.get'), {
    response: { data: { node: { obj_type: 'docx', obj_token: 'docx_private' } } },
  }), null);

  const root = selectCapabilityResultIdentifier(capability('mindnotes.nodes.list'), {
    response: { data: { nodes: [
      { node_id: 'root_private', texts: [] },
      { node_id: 'child_private', parent_id: 'root_private', texts: [] },
    ] } },
  });
  assert.deepEqual(root, { kind: 'mindnote_node_id', value: 'root_private' });
});

test('private Markdown patch content never enters argv and high-impact flow performs preflight then reread', async (t) => {
  const { runner, callsPath, dir } = fakeRunner(t);
  const result = await executeRegisteredCapability(runner, 'markdown.patch', {
    'file-token': 'file_private',
    pattern: '需要保密的匹配串',
    content: '需要保密的新正文',
  });
  assert.equal(result.verified, true);
  const invocations = calls(callsPath);
  assert.deepEqual(invocations.map((item) => item.args.slice(0, 2)), [
    ['markdown', '+fetch'],
    ['markdown', '+patch'],
    ['markdown', '+fetch'],
  ]);
  assert.equal(invocations.some((item) => JSON.stringify(item.args).includes('需要保密')), false);
  assert.equal(invocations[1].input, '需要保密的新正文');
  assert.equal(invocations[1].privateBodies.includes('需要保密的匹配串'), true);
  const payloadDir = path.join(dir, 'node_modules', '.cache', 'feishu-bridge', 'action-payloads');
  assert.deepEqual(fs.readdirSync(payloadDir), []);
});

test('Apps descriptions and chat prompts use fixed raw endpoints with private request bodies', async (t) => {
  const { runner, callsPath } = fakeRunner(t);
  await executeRegisteredCapability(runner, 'apps.app.create', {
    name: 'Codex桥测试应用',
    'app-type': 'frontend',
    description: '不得出现在进程参数中的应用说明',
  });
  const [call] = calls(callsPath);
  assert.deepEqual(call.args.slice(0, 3), ['api', 'POST', '/open-apis/spark/v1/apps']);
  assert.equal(JSON.stringify(call.args).includes('应用说明'), false);
  assert.equal(call.privateBodies.some((body) => body.includes('不得出现在进程参数中的应用说明')), true);
  assert.equal(call.args.some((item) => item.includes('/open-apis/approval/')), false);
});

test('remote Apps operations poll the fixed read capability to terminal state', async () => {
  let statusReads = 0;
  const runner = {
    cwd: process.cwd(),
    identityArgs: () => ['--as', 'user'],
    larkApiPrivate: async () => ({ data: { run_id: 'run_private' } }),
    runLarkCliJsonWithPrivateFiles: async (args) => {
      assert.deepEqual(args.slice(0, 2), ['apps', '+session-get']);
      statusReads += 1;
      return { data: { status: statusReads < 2 ? 'running' : 'completed' } };
    },
  };
  const result = await executeRegisteredCapability(runner, 'apps.session.chat', {
    'app-id': 'app_private',
    'session-id': 'session_private',
    message: '生成最小页面',
  }, { remoteTimeoutMs: 1000, pollIntervalMs: 1 });
  assert.equal(result.remote.status, 'completed');
  assert.equal(statusReads, 2);
});
