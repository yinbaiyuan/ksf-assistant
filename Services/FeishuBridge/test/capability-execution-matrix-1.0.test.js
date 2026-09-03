const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { CAPABILITIES, validateCapabilityInput } = require('../lib/capability-registry');
const { executeRegisteredCapability } = require('../lib/capability-executor');

function valueFor(schema, name) {
  if (schema.type === 'boolean') return true;
  if (schema.type === 'integer') return Math.max(schema.min || 0, 1);
  if (schema.type === 'enum') return schema.values[0];
  if (schema.type === 'csv') return ['id_value_12345'];
  if (schema.type === 'json') return { value: 'private-content-marker' };
  if (schema.type === 'path') return schema.output ? 'artifacts/output.bin' : 'fixtures/input.bin';
  if (schema.pattern) return 'id_value_12345';
  if (schema.private || /^(?:content|message|description|source|pattern|replacement)$/.test(name)) {
    return 'private-content-marker';
  }
  return 'value_12345';
}

function fixture(definition) {
  const input = {};
  for (const [name, schema] of Object.entries(definition.flags)) {
    if (schema.required) input[name] = valueFor(schema, name);
  }
  for (const group of definition.scope.requiredGroups || []) {
    if (!group.some((name) => input[name] !== undefined)) {
      const name = group.find((candidate) => definition.flags[candidate]);
      input[name] = valueFor(definition.flags[name], name);
    }
  }
  for (const group of [definition.scope.requireAny, definition.scope.requireExactlyOne].filter(Boolean)) {
    if (!group.some((name) => input[name] !== undefined)) {
      const name = group.find((candidate) => definition.flags[candidate]);
      input[name] = valueFor(definition.flags[name], name);
    }
  }
  if (definition.id === 'docs.whiteboard.insert') {
    input.doc = 'docx_token:doc_private';
    input['doc-format'] = 'mermaid';
    input.content = 'flowchart LR\nA-->B';
  }
  if (definition.id === 'mindnotes.node.create') {
    input.data = { client_token: 'client-token-create', nodes: [{ parent_id: 'root', texts: [] }] };
  }
  if (definition.id === 'mindnotes.node.update') {
    input.data = { client_token: 'client-token-update', nodes: [{ node_id: 'node_private', texts: [] }] };
  }
  return input;
}

test('every registered write builds only its fixed command, keeps private bodies out of argv, and completes required safety phases', async (t) => {
  const cwd = fs.mkdtempSync(path.join(os.tmpdir(), 'feishu-capability-matrix-'));
  t.after(() => fs.rmSync(cwd, { recursive: true, force: true }));
  fs.mkdirSync(path.join(cwd, 'fixtures'), { recursive: true });
  fs.writeFileSync(path.join(cwd, 'fixtures', 'input.bin'), 'fixture');
  const calls = [];
  const runner = {
    cwd,
    identityArgs: () => ['--as', 'user'],
    larkApiPrivate: async (method, apiPath, options) => {
      calls.push({ transport: 'raw', method, apiPath, options });
      return { data: { status: 'completed', release_id: 'release_value_12345' } };
    },
    runLarkCliJsonWithPrivateFiles: async (args, options) => {
      calls.push({ transport: 'shortcut', args, options });
      return { data: { status: 'completed', release_id: 'release_value_12345' } };
    },
  };

  for (const definition of CAPABILITIES.values()) {
    if (definition.risk === 'read') continue;
    calls.length = 0;
    const input = fixture(definition);
    assert.equal(validateCapabilityInput(definition, input), '', definition.id);
    let result;
    try {
      result = await executeRegisteredCapability(runner, definition.id, input, {
        remoteTimeoutMs: 20,
        pollIntervalMs: 1,
      });
    } catch (error) {
      throw new Error(`${definition.id}: ${error.message}`);
    }
    const execution = calls.find((call) => call.transport === definition.transport
      && (definition.transport === 'raw'
        ? call.apiPath.startsWith(definition.apiPath.split('{')[0])
        : definition.command.every((part, index) => call.args[index] === part)));
    assert.ok(execution, definition.id);
    if (definition.transport === 'shortcut') {
      assert.equal(JSON.stringify(execution.args).includes('private-content-marker'), false, `${definition.id}: ${JSON.stringify(execution.args)}`);
      assert.equal(execution.args.includes('--yes'), Boolean(definition.cliConfirm && definition.risk === 'high-impact-write'), definition.id);
    }
    if (definition.risk === 'high-impact-write') {
      assert.ok(result.preflight, `${definition.id}: preflight`);
      assert.ok(result.verification, `${definition.id}: reread`);
    }
  }
});
