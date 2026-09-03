const fs = require('node:fs');
const path = require('node:path');
const {
  capability,
  docWhiteboardXml,
  validateCapabilityInput,
} = require('./capability-registry');

function getPathValue(value, pathValue) {
  return String(pathValue || '').split('.').filter(Boolean).reduce((current, key) => current?.[key], value);
}

function findKey(value, wanted) {
  if (!value || typeof value !== 'object') return undefined;
  if (Object.hasOwn(value, wanted) && value[wanted] !== undefined) return value[wanted];
  for (const child of Object.values(value)) {
    const found = findKey(child, wanted);
    if (found !== undefined) return found;
  }
  return undefined;
}

function normalizeCsv(value) {
  return (Array.isArray(value) ? value : String(value).split(','))
    .map((item) => String(item).trim())
    .filter(Boolean)
    .join(',');
}

function assertSafeLocalPath(cwd, fieldName, value, schema) {
  const absolute = path.resolve(cwd, value);
  const relative = path.relative(cwd, absolute);
  if (!relative || relative.startsWith('..') || path.isAbsolute(relative)) {
    throw new Error(`unsafe_path:${fieldName}`);
  }
  if (schema.output) {
    const parent = path.dirname(absolute);
    fs.mkdirSync(parent, { recursive: true, mode: 0o700 });
    const parentStat = fs.lstatSync(parent);
    if (parentStat.isSymbolicLink() || !parentStat.isDirectory()) throw new Error(`unsafe_output_parent:${fieldName}`);
    return relative.split(path.sep).join('/');
  }
  const stat = fs.lstatSync(absolute);
  if (stat.isSymbolicLink() || !stat.isFile()) throw new Error(`unsafe_input_file:${fieldName}`);
  return relative.split(path.sep).join('/');
}

function mappedInput(step, input, result) {
  if (!step) return {};
  const output = { ...(step.defaults || {}) };
  for (const [targetName, sourceName] of Object.entries(step.map || {})) {
    let value;
    if (String(sourceName).startsWith('$result.')) {
      const resultPath = String(sourceName).slice('$result.'.length);
      value = getPathValue(result, resultPath) ?? findKey(result, resultPath.split('.').at(-1));
    } else {
      value = input[sourceName];
    }
    if (value !== undefined) output[targetName] = value;
  }
  return output;
}

function shortcutInvocation(definition, input, cwd) {
  const args = [
    ...definition.command,
    ...definition.fixedArgs,
  ];
  const files = {};
  let stdin;
  let sequence = 0;
  for (const [name, schema] of Object.entries(definition.flags)) {
    let value = input[name];
    if (value === undefined || value === false) continue;
    if (schema.type === 'boolean') {
      if (value) args.push(`--${name}`);
      continue;
    }
    if (schema.type === 'csv') value = normalizeCsv(value);
    if (schema.type === 'path') value = assertSafeLocalPath(cwd, name, value, schema);
    args.push(`--${name}`);
    if (schema.private && schema.stdin) {
      if (stdin !== undefined) throw new Error('only_one_private_stdin_field_is_supported');
      stdin = schema.type === 'json' ? JSON.stringify(value) : String(value);
      args.push('-');
    } else if (schema.private) {
      sequence += 1;
      const placeholder = `__PRIVATE_CAPABILITY_${sequence}__`;
      args.push(placeholder);
      files[placeholder] = value;
    } else if (schema.type === 'json') {
      sequence += 1;
      const placeholder = `__PRIVATE_CAPABILITY_${sequence}__`;
      args.push(placeholder);
      files[placeholder] = value;
    } else {
      args.push(String(value));
    }
  }
  if (definition.risk === 'high-impact-write' && definition.cliConfirm) args.push('--yes');
  args.push('--format', 'json');
  return { args, files, input: stdin };
}

function rawInvocation(definition, input) {
  let apiPath = definition.apiPath;
  const data = {};
  const params = {};
  for (const [name, schema] of Object.entries(definition.flags)) {
    const value = input[name];
    if (value === undefined) continue;
    if (schema.path) {
      apiPath = apiPath.replace(`{${name}}`, encodeURIComponent(String(value)));
    } else if (schema.body) {
      data[schema.body] = value;
    } else {
      params[name.replace(/-/g, '_')] = value;
    }
  }
  if (/\{[^}]+\}/.test(apiPath)) throw new Error('missing_api_path_parameter');
  return {
    method: definition.command[1],
    apiPath,
    data: Object.keys(data).length ? data : undefined,
    params: Object.keys(params).length ? params : undefined,
  };
}

async function runDefinition(lark, definition, input, { timeoutMs = 60000 } = {}) {
  const validationError = validateCapabilityInput(definition, input);
  if (validationError) throw new Error(validationError);
  if (definition.transport === 'raw') {
    const invocation = rawInvocation(definition, input);
    return lark.larkApiPrivate(invocation.method, invocation.apiPath, {
      data: invocation.data,
      params: invocation.params,
      timeoutMs,
    });
  }
  const effectiveInput = definition.transform === 'doc-whiteboard'
    ? { ...input, content: docWhiteboardXml(input), 'doc-format': 'xml' }
    : input;
  const invocation = shortcutInvocation(definition, effectiveInput, lark.cwd || process.cwd());
  return lark.runLarkCliJsonWithPrivateFiles([
    ...invocation.args.slice(0, definition.command.length),
    ...lark.identityArgs(),
    ...invocation.args.slice(definition.command.length),
  ], {
    files: invocation.files,
    input: invocation.input,
    timeoutMs,
  });
}

function statusValue(response, paths) {
  for (const item of paths || []) {
    const value = getPathValue(response, item);
    if (value !== undefined && value !== null && value !== '') return String(value).toLowerCase();
  }
  return '';
}

async function pollRemoteOperation(lark, definition, input, response, {
  timeoutMs = 10 * 60 * 1000,
  pollIntervalMs = 2000,
} = {}) {
  if (!definition.poll) return null;
  const pollDefinition = capability(definition.poll.id);
  if (!pollDefinition || pollDefinition.risk !== 'read') throw new Error('invalid_remote_poll_capability');
  const pollInput = mappedInput(definition.poll, input, response);
  const validationError = validateCapabilityInput(pollDefinition, pollInput);
  if (validationError) throw new Error(`remote_poll_${validationError}`);
  const deadline = Date.now() + timeoutMs;
  let last;
  while (Date.now() <= deadline) {
    last = await runDefinition(lark, pollDefinition, pollInput, { timeoutMs: Math.min(60000, timeoutMs) });
    const state = statusValue(last, definition.poll.statuses);
    if (['success', 'succeeded', 'completed', 'done', 'finished', 'published', 'released'].includes(state)) {
      return { status: state, response: last };
    }
    if (['failed', 'error', 'cancelled', 'canceled', 'terminated'].includes(state)) {
      throw new Error(`remote_operation_${state}`);
    }
    if (Date.now() + pollIntervalMs > deadline) break;
    await new Promise((resolve) => setTimeout(resolve, pollIntervalMs));
  }
  const error = new Error('remote_operation_timeout');
  error.remoteLastResponse = last;
  throw error;
}

async function executeRegisteredCapability(lark, capabilityId, input, options = {}) {
  const definition = capability(capabilityId);
  if (!definition) throw new Error('unknown_capability');
  let preflight = null;
  if (definition.preflight) {
    const readDefinition = capability(definition.preflight.id);
    if (!readDefinition || readDefinition.risk !== 'read') throw new Error('invalid_preflight_capability');
    preflight = await runDefinition(lark, readDefinition, mappedInput(definition.preflight, input), options);
  }
  const response = await runDefinition(lark, definition, input, options);
  const remote = await pollRemoteOperation(lark, definition, input, response, {
    timeoutMs: options.remoteTimeoutMs,
    pollIntervalMs: options.pollIntervalMs,
  });
  let verification = null;
  if (definition.reread) {
    const readDefinition = capability(definition.reread.id);
    if (!readDefinition || readDefinition.risk !== 'read') throw new Error('invalid_reread_capability');
    verification = await runDefinition(lark, readDefinition, mappedInput(definition.reread, input, response), options);
  }
  return {
    capabilityId,
    response,
    preflight,
    verification,
    remote,
    verified: Boolean(verification || remote),
  };
}

module.exports = {
  executeRegisteredCapability,
  getPathValue,
  mappedInput,
  pollRemoteOperation,
  rawInvocation,
  runDefinition,
  shortcutInvocation,
  statusValue,
};
