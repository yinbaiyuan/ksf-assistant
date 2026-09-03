const { actionKey } = require('./action-registry');
const { executeRegisteredCapability } = require('./capability-executor');

function addFlag(args, name, value) {
  if (value === undefined || value === null) return;
  args.push(name, String(value));
}

function addBooleanFlag(args, name, value) {
  if (value === undefined) return;
  args.push(`${name}=${value ? 'true' : 'false'}`);
}

function addCsvFlag(args, name, values) {
  if (Array.isArray(values) && values.length) args.push(name, values.join(','));
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

function nonEmptyCellValue(value) {
  if (value === null || value === undefined || value === '') return false;
  if (Array.isArray(value)) return value.some(nonEmptyCellValue);
  if (typeof value === 'object') return Object.keys(value).length > 0;
  return true;
}

function containsNonEmptyCells(value) {
  if (!value || typeof value !== 'object') return false;
  for (const [key, child] of Object.entries(value)) {
    if (key === 'values' && Array.isArray(child) && nonEmptyCellValue(child)) return true;
    if (containsNonEmptyCells(child)) return true;
  }
  return false;
}

function collectFieldKeys(value, result = new Set()) {
  if (Array.isArray(value)) {
    value.forEach((item) => collectFieldKeys(item, result));
    return result;
  }
  if (!value || typeof value !== 'object') return result;

  // lark-cli 1.0.92 returns Base field-list entries as { id, name, type },
  // while older responses use { field_id, field_name }. Only accept generic
  // id/name pairs inside an explicit fields collection so unrelated nested
  // objects cannot satisfy the field whitelist by accident.
  if (Array.isArray(value.fields)) {
    value.fields.forEach((field) => {
      if (!field || typeof field !== 'object' || Array.isArray(field)) return;
      const fieldId = field.field_id || field.fieldId || field.id;
      const fieldName = field.field_name || field.fieldName || field.name;
      if (typeof fieldId === 'string' && fieldId) result.add(fieldId);
      if (typeof fieldName === 'string' && fieldName) result.add(fieldName);
    });
  }

  const id = value.field_id || value.fieldId;
  const name = value.field_name || value.fieldName || (id ? value.name : undefined);
  if (typeof id === 'string' && id) result.add(id);
  if (typeof name === 'string' && name) result.add(name);
  Object.values(value).forEach((child) => collectFieldKeys(child, result));
  return result;
}

function submittedFieldKeys(request) {
  if (request.action === 'create_records') {
    return new Set(request.input.records.flatMap((record) => Object.keys(record)));
  }
  return new Set(Object.values(request.input.updates).flatMap((fields) => Object.keys(fields)));
}

function assertKnownBaseFields(fieldResponse, request) {
  const available = collectFieldKeys(fieldResponse);
  if (!available.size) throw new Error('base_field_preflight_unparseable');
  const unknown = [...submittedFieldKeys(request)].filter((field) => !available.has(field));
  if (unknown.length) throw new Error(`base_unknown_fields:${unknown.length}`);
}

function collectSheetTitles(value, result = new Set()) {
  if (Array.isArray(value)) {
    value.forEach((item) => collectSheetTitles(item, result));
    return result;
  }
  if (!value || typeof value !== 'object') return result;
  const hasSheetId = value.sheet_id || value.sheetId;
  const title = value.title || (hasSheetId ? value.name : undefined);
  if (hasSheetId && typeof title === 'string' && title) result.add(title);
  Object.values(value).forEach((child) => collectSheetTitles(child, result));
  return result;
}

async function resolveBaseToken(lark, url, timeoutMs) {
  const response = await lark.runLarkCliJson([
    'base', '+url-resolve', '--as', 'user', '--url', url, '--format', 'json',
  ], { timeoutMs });
  const baseToken = findFirstKey(response, ['base_token', 'baseToken']);
  if (!baseToken) throw new Error('base_url_did_not_resolve_to_base');
  return { baseToken: String(baseToken), response };
}

async function calendarAction(lark, request, timeoutMs) {
  const input = request.input;
  if (request.action === 'create_event') {
    const args = [
      'calendar', '+create', '--as', 'user', '--calendar-id', request.target.value,
      '--summary', input.summary, '--start', input.start, '--end', input.end,
    ];
    if (input.description !== undefined) args.push('--description', '-');
    addCsvFlag(args, '--attendee-ids', input.attendeeIds);
    addFlag(args, '--rrule', input.rrule);
    const response = await lark.runLarkCliJson(args, {
      timeoutMs,
      input: input.description === undefined ? undefined : input.description,
    });
    return { response, preflight: false, verified: false };
  }
  const calendarId = input.calendarId || 'primary';
  const preflight = await lark.runLarkCliJson([
    'calendar', '+get', '--as', 'user', '--calendar-id', calendarId,
    '--event-id', request.target.value, '--format', 'json',
  ], { timeoutMs });
  if (request.action === 'rsvp') {
    const response = await lark.runLarkCliJson([
      'calendar', '+rsvp', '--as', 'user', '--calendar-id', calendarId,
      '--event-id', request.target.value, '--rsvp-status', input.status, '--format', 'json',
    ], { timeoutMs });
    return { response, preflight, verified: false };
  }
  const args = [
    'calendar', '+update', '--as', 'user', '--calendar-id', calendarId,
    '--event-id', request.target.value,
  ];
  addFlag(args, '--summary', input.summary);
  if (input.description !== undefined) args.push('--description', '-');
  addFlag(args, '--start', input.start);
  addFlag(args, '--end', input.end);
  addFlag(args, '--rrule', input.rrule);
  addCsvFlag(args, '--add-attendee-ids', input.addAttendeeIds);
  addCsvFlag(args, '--remove-attendee-ids', input.removeAttendeeIds);
  addBooleanFlag(args, '--notify', input.notify);
  const response = await lark.runLarkCliJson(args, {
    timeoutMs,
    input: input.description === undefined ? undefined : input.description,
  });
  return { response, preflight, verified: false };
}

async function taskAction(lark, request, timeoutMs) {
  const input = request.input;
  if (request.action === 'create') {
    const args = ['task', '+create', '--as', 'user', '--summary', input.summary, '--idempotency-key', request.id];
    addFlag(args, '--description', input.description);
    addFlag(args, '--due', input.due);
    addFlag(args, '--assignee', input.assignee);
    addFlag(args, '--follower', input.follower);
    addFlag(args, '--tasklist-id', input.tasklistId);
    const response = await lark.runLarkCliJson(args, { timeoutMs });
    return { response, preflight: false, verified: false };
  }
  const preflight = await lark.runLarkCliJson([
    'task', 'tasks', 'get', '--as', 'user', '--task-guid', request.target.value, '--format', 'json',
  ], { timeoutMs });
  const args = ['task', `+${request.action}`, '--as', 'user', '--task-id', request.target.value];
  if (request.action === 'update') {
    addFlag(args, '--summary', input.summary);
    addFlag(args, '--description', input.description);
    addFlag(args, '--due', input.due);
  } else if (request.action === 'assign') {
    addCsvFlag(args, '--add', input.add);
    addCsvFlag(args, '--remove', input.remove);
    if (input.add?.length) args.push('--idempotency-key', request.id);
  } else if (request.action === 'reminder') {
    addFlag(args, '--set', input.set);
    if (input.remove === true) args.push('--remove');
  }
  const response = await lark.runLarkCliJson(args, { timeoutMs });
  return { response, preflight, verified: false };
}

function sheetSelectorArgs(input) {
  return input.sheetName ? ['--sheet-name', input.sheetName] : ['--sheet-id', input.sheetId];
}

async function sheetsAction(lark, request, timeoutMs) {
  const input = request.input;
  const urlArgs = ['--url', request.target.value];
  const revisionBefore = await lark.runLarkCliJson([
    'sheets', '+revision-get', '--as', 'user', ...urlArgs, '--format', 'json',
  ], { timeoutMs });
  if (request.action === 'create_sheet') {
    const workbook = await lark.runLarkCliJson([
      'sheets', '+workbook-info', '--as', 'user', ...urlArgs, '--format', 'json',
    ], { timeoutMs });
    if (collectSheetTitles(workbook).has(input.title)) throw new Error('sheet_title_already_exists');
    const args = ['sheets', '+sheet-create', '--as', 'user', ...urlArgs, '--title', input.title];
    addFlag(args, '--row-count', input.rowCount);
    addFlag(args, '--col-count', input.colCount);
    addFlag(args, '--index', input.index);
    const response = await lark.runLarkCliJson(args, { timeoutMs });
    const revisionAfter = await lark.runLarkCliJson([
      'sheets', '+revision-get', '--as', 'user', ...urlArgs, '--format', 'json',
    ], { timeoutMs });
    return { response, preflight: { revisionBefore, workbook }, verification: { revisionAfter }, verified: true };
  }
  if (request.action === 'set_cells') {
    const selector = sheetSelectorArgs(input);
    const before = await lark.runLarkCliJson([
      'sheets', '+cells-get', '--as', 'user', ...urlArgs, ...selector,
      '--range', input.range, '--include', 'value,formula', '--format', 'json',
    ], { timeoutMs });
    const allowOverwrite = input.allowOverwrite === true;
    if (!allowOverwrite && containsNonEmptyCells(before)) throw new Error('sheet_target_not_empty');
    const args = [
      'sheets', '+cells-set', '--as', 'user', ...urlArgs, ...selector,
      '--range', input.range, '--cells', '__PRIVATE_JSON_PAYLOAD__',
      `--allow-overwrite=${allowOverwrite ? 'true' : 'false'}`, '--format', 'json',
    ];
    const response = await lark.runLarkCliJsonWithPayloadFile(args, {
      payload: input.cells,
      timeoutMs,
    });
    const after = await lark.runLarkCliJson([
      'sheets', '+cells-get', '--as', 'user', ...urlArgs, ...selector,
      '--range', input.range, '--include', 'value,formula', '--format', 'json',
    ], { timeoutMs });
    const revisionAfter = await lark.runLarkCliJson([
      'sheets', '+revision-get', '--as', 'user', ...urlArgs, '--format', 'json',
    ], { timeoutMs });
    return { response, preflight: { revisionBefore, before }, verification: { revisionAfter, after }, verified: true };
  }
  const sheets = {
    sheets: input.sheets.sheets.map((sheet) => ({ ...sheet, mode: 'append' })),
  };
  const workbook = await lark.runLarkCliJson([
    'sheets', '+workbook-info', '--as', 'user', ...urlArgs, '--format', 'json',
  ], { timeoutMs });
  const response = await lark.runLarkCliJsonWithPayloadFile([
    'sheets', '+table-put', '--as', 'user', ...urlArgs,
    '--sheets', '__PRIVATE_JSON_PAYLOAD__', '--format', 'json',
  ], { payload: sheets, timeoutMs });
  const revisionAfter = await lark.runLarkCliJson([
    'sheets', '+revision-get', '--as', 'user', ...urlArgs, '--format', 'json',
  ], { timeoutMs });
  return { response, preflight: { revisionBefore, workbook }, verification: { revisionAfter }, verified: true };
}

async function baseAction(lark, request, timeoutMs) {
  const { baseToken, response: resolved } = await resolveBaseToken(lark, request.target.value, timeoutMs);
  const tableId = request.input.tableId;
  const fieldResponse = await lark.runLarkCliJson([
    'base', '+field-list', '--as', 'user', '--base-token', baseToken,
    '--table-id', tableId, '--limit', '200', '--format', 'json',
  ], { timeoutMs });
  assertKnownBaseFields(fieldResponse, request);
  const recordIds = request.action === 'update_records' ? Object.keys(request.input.updates) : [];
  let before;
  if (recordIds.length) {
    const args = ['base', '+record-get', '--as', 'user', '--base-token', baseToken, '--table-id', tableId];
    recordIds.forEach((recordId) => args.push('--record-id', recordId));
    args.push('--format', 'json');
    before = await lark.runLarkCliJson(args, { timeoutMs });
  }
  const payload = request.action === 'create_records'
    ? { create_records: request.input.records }
    : { update_records: request.input.updates };
  const shortcut = request.action === 'create_records' ? '+record-batch-create' : '+record-batch-update';
  const response = await lark.runLarkCliJsonWithPayloadFile([
    'base', shortcut, '--as', 'user', '--base-token', baseToken, '--table-id', tableId,
    '--json', '__PRIVATE_JSON_PAYLOAD__', '--format', 'json',
  ], { payload, timeoutMs });
  let after;
  if (recordIds.length) {
    const args = ['base', '+record-get', '--as', 'user', '--base-token', baseToken, '--table-id', tableId];
    recordIds.forEach((recordId) => args.push('--record-id', recordId));
    args.push('--format', 'json');
    after = await lark.runLarkCliJson(args, { timeoutMs });
  }
  return {
    response,
    preflight: { resolved, fields: fieldResponse, before },
    verification: after ? { after } : undefined,
    verified: Boolean(after),
  };
}

async function minutesDetail(lark, minuteToken, artifact, timeoutMs) {
  const args = [
    'minutes', '+detail', '--as', 'user', '--minute-tokens', minuteToken,
  ];
  if (artifact) args.push(`--${artifact}`);
  args.push('--format', 'json');
  return lark.runLarkCliJson(args, { timeoutMs });
}

async function minutesAction(lark, request, timeoutMs) {
  if (request.action === 'upload') {
    const response = await lark.runLarkCliJson([
      'minutes', '+upload', '--as', 'user', '--file-token', request.target.value, '--format', 'json',
    ], { timeoutMs });
    return { response, preflight: false, verified: false };
  }

  const minuteToken = request.target.value;
  if (request.action === 'update_title') {
    const preflight = await minutesDetail(lark, minuteToken, '', timeoutMs);
    const response = await lark.runLarkCliJson([
      'minutes', '+update', '--as', 'user', '--minute-token', minuteToken,
      '--topic', request.input.topic, '--format', 'json',
    ], { timeoutMs });
    const after = await minutesDetail(lark, minuteToken, '', timeoutMs);
    return { response, preflight, verification: { after }, verified: true };
  }

  if (request.action === 'replace_summary') {
    const preflight = await minutesDetail(lark, minuteToken, 'summary', timeoutMs);
    const response = await lark.runLarkCliJson([
      'minutes', '+summary', '--as', 'user', '--minute-token', minuteToken,
      '--summary', '-', '--format', 'json',
    ], { timeoutMs, input: request.input.summary });
    const after = await minutesDetail(lark, minuteToken, 'summary', timeoutMs);
    return { response, preflight, verification: { after }, verified: true };
  }

  if (request.action === 'mutate_todos') {
    const preflight = await minutesDetail(lark, minuteToken, 'todo', timeoutMs);
    const response = await lark.runLarkCliJsonWithPayloadFile([
      'minutes', '+todo', '--as', 'user', '--minute-token', minuteToken,
      '--todos', '__PRIVATE_JSON_PAYLOAD__', '--format', 'json',
    ], { payload: request.input.todos, timeoutMs });
    const after = await minutesDetail(lark, minuteToken, 'todo', timeoutMs);
    return { response, preflight, verification: { after }, verified: true };
  }

  if (request.action === 'replace_words') {
    const response = await lark.runLarkCliJsonWithPayloadFile([
      'minutes', '+word-replace', '--as', 'user', '--minute-token', minuteToken,
      '--replace-words', '__PRIVATE_JSON_PAYLOAD__', '--format', 'json',
    ], { payload: request.input.replacements, timeoutMs });
    return {
      response,
      preflight: false,
      verification: { method: 'operation_result' },
      verified: true,
    };
  }

  const speakerPath = `/open-apis/minutes/v1/minutes/${encodeURIComponent(minuteToken)}/transcript/speakerlist`;
  const preflight = await lark.runLarkCliJson([
    'api', 'GET', speakerPath, '--as', 'user', '--format', 'json',
  ], { timeoutMs });
  const response = await lark.runLarkCliJson([
    'minutes', '+speaker-replace', '--as', 'user', '--minute-token', minuteToken,
    '--from-speaker-id', request.input.fromSpeakerId,
    '--to-user-id', request.input.toUserId,
    '--format', 'json',
  ], { timeoutMs });
  const after = await lark.runLarkCliJson([
    'api', 'GET', speakerPath, '--as', 'user', '--format', 'json',
  ], { timeoutMs });
  return { response, preflight, verification: { after }, verified: true };
}

async function executeActionRequest(lark, request, { timeoutMs = 60000 } = {}) {
  if (request.type === 'feishu_capability') {
    return executeRegisteredCapability(lark, request.capabilityId, request.input, {
      timeoutMs,
      remoteTimeoutMs: request.remoteTimeoutMs,
      pollIntervalMs: request.pollIntervalMs,
    });
  }
  const key = actionKey(request);
  if (key === 'drive.add_comment') {
    const response = await lark.larkDriveAddComment({
      doc: request.target.value,
      comment: request.input.comment,
      blockId: request.input.blockId,
      timeoutMs,
    });
    return { response, preflight: false, verified: false };
  }
  if (request.domain === 'calendar') return calendarAction(lark, request, timeoutMs);
  if (request.domain === 'task') return taskAction(lark, request, timeoutMs);
  if (request.domain === 'sheets') return sheetsAction(lark, request, timeoutMs);
  if (request.domain === 'base') return baseAction(lark, request, timeoutMs);
  if (request.domain === 'minutes') return minutesAction(lark, request, timeoutMs);
  throw new Error(`unsupported action execution: ${key}`);
}

function actionExecutionSummary(execution) {
  const response = execution?.response || {};
  const identifier = findFirstKey(response, [
    'event_id', 'guid', 'task_guid', 'record_id', 'record_id_list',
    'sheet_id', 'spreadsheet_token', 'comment_id', 'reply_id', 'minute_token',
    'note_id', 'meeting_id', 'release_id', 'session_id', 'run_id', 'id',
  ]);
  const revisionBefore = findFirstKey(execution?.preflight, ['revision', 'revision_id', 'revisionId']);
  const revisionAfter = findFirstKey(execution?.verification, ['revision', 'revision_id', 'revisionId']);
  return {
    identifier,
    preflightPerformed: Boolean(execution?.preflight),
    verificationPerformed: Boolean(execution?.verified),
    revisionBefore,
    revisionAfter,
  };
}

module.exports = {
  actionExecutionSummary,
  executeActionRequest,
  findFirstKey,
  resolveBaseToken,
};
