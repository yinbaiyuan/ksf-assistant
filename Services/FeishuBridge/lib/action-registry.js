const { validateCapabilityRequest } = require('./capability-registry');

const ACTIONBOX_TERMINAL_STATUSES = new Set([
  'completed',
  'dry_run',
  'denied',
  'duplicate',
  'invalid',
  'failed',
]);

const ACTION_KEYS = new Set([
  'capability.execute',
  'drive.add_comment',
  'calendar.create_event',
  'calendar.update_event',
  'calendar.rsvp',
  'task.create',
  'task.update',
  'task.complete',
  'task.reopen',
  'task.assign',
  'task.reminder',
  'sheets.create_sheet',
  'sheets.set_cells',
  'sheets.append_table',
  'base.create_records',
  'base.update_records',
  'minutes.upload',
  'minutes.update_title',
  'minutes.replace_summary',
  'minutes.mutate_todos',
  'minutes.replace_words',
  'minutes.replace_speaker',
]);

const TIMEZONE_AWARE_ISO = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}(?::\d{2}(?:\.\d+)?)?(?:Z|[+-]\d{2}:\d{2})$/;
const ATTENDEE_ID = /^(?:ou|oc|omm)_[A-Za-z0-9_-]+$/;
const MEMBER_ID = /^(?:ou|cli)_[A-Za-z0-9_-]+$/;
const REMOTE_TOKEN = /^[A-Za-z0-9_-]{6,400}$/;

function isObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function stringValue(value, { required = false, max = 100000 } = {}) {
  if (value === undefined) return required ? 'missing' : '';
  if (typeof value !== 'string') return 'invalid';
  if (required && !value.trim()) return 'missing';
  if (value.length > max) return 'too_long';
  return '';
}

function stringArray(value, { max = 100, pattern } = {}) {
  if (value === undefined) return '';
  if (!Array.isArray(value)) return 'invalid';
  if (value.length > max) return 'too_many';
  if (value.some((item) => typeof item !== 'string' || !item.trim() || (pattern && !pattern.test(item)))) {
    return 'invalid_item';
  }
  return '';
}

function exactKeys(value, allowed) {
  if (!isObject(value)) return 'missing_input';
  return Object.keys(value).some((key) => !allowed.has(key)) ? 'unsupported_input_field' : '';
}

function validateTarget(request, kind, { https = false } = {}) {
  if (!isObject(request.target)) return 'missing_target';
  if (request.target.kind !== kind) return 'unsupported_target_kind';
  const error = stringValue(request.target.value, { required: true, max: 4000 });
  if (error) return `target_${error}`;
  if (https && !/^https:\/\//i.test(request.target.value)) return 'invalid_target_url';
  return '';
}

function validateTimePair(input, required = false) {
  if (required && (!input.start || !input.end)) return 'missing_time_range';
  if ((input.start === undefined) !== (input.end === undefined)) return 'incomplete_time_range';
  if (input.start === undefined) return '';
  if (!TIMEZONE_AWARE_ISO.test(input.start) || !TIMEZONE_AWARE_ISO.test(input.end)) return 'timezone_required';
  const start = Date.parse(input.start);
  const end = Date.parse(input.end);
  if (!Number.isFinite(start) || !Number.isFinite(end) || start >= end) return 'invalid_time_range';
  if (end - start > 366 * 24 * 60 * 60 * 1000) return 'time_range_too_large';
  return '';
}

function validateDrive(request) {
  const targetError = validateTarget(request, 'url', { https: true });
  if (targetError) return targetError;
  const keyError = exactKeys(request.input, new Set(['comment', 'blockId']));
  if (keyError) return keyError;
  const commentError = stringValue(request.input.comment, { required: true, max: 100000 });
  if (commentError === 'missing') return 'missing_comment';
  if (commentError) return `comment_${commentError}`;
  if (request.input.blockId !== undefined && typeof request.input.blockId !== 'string') return 'invalid_block_id';
  return '';
}

function validateCalendar(request) {
  if (request.action === 'create_event') {
    const targetError = validateTarget(request, 'calendar_id');
    if (targetError) return targetError;
    const keyError = exactKeys(request.input, new Set([
      'summary', 'start', 'end', 'description', 'attendeeIds', 'rrule',
    ]));
    if (keyError) return keyError;
    const summaryError = stringValue(request.input.summary, { required: true, max: 1000 });
    if (summaryError) return `summary_${summaryError}`;
    const descriptionError = stringValue(request.input.description, { max: 100000 });
    if (descriptionError) return `description_${descriptionError}`;
    const timeError = validateTimePair(request.input, true);
    if (timeError) return timeError;
    const attendeeError = stringArray(request.input.attendeeIds, { max: 100, pattern: ATTENDEE_ID });
    if (attendeeError) return `attendees_${attendeeError}`;
    const rruleError = stringValue(request.input.rrule, { max: 1000 });
    if (rruleError) return `rrule_${rruleError}`;
    return '';
  }

  const targetError = validateTarget(request, 'event_id');
  if (targetError) return targetError;
  if (request.action === 'rsvp') {
    const keyError = exactKeys(request.input, new Set(['calendarId', 'status']));
    if (keyError) return keyError;
    if (!['accept', 'decline', 'tentative'].includes(request.input.status)) return 'invalid_rsvp_status';
    return stringValue(request.input.calendarId, { max: 1000 }) ? 'invalid_calendar_id' : '';
  }
  const keyError = exactKeys(request.input, new Set([
    'calendarId', 'summary', 'description', 'start', 'end', 'rrule',
    'addAttendeeIds', 'removeAttendeeIds', 'notify',
  ]));
  if (keyError) return keyError;
  if (stringValue(request.input.calendarId, { max: 1000 })) return 'invalid_calendar_id';
  if (stringValue(request.input.summary, { max: 1000 })) return 'invalid_summary';
  if (stringValue(request.input.description, { max: 100000 })) return 'invalid_description';
  if (stringValue(request.input.rrule, { max: 1000 })) return 'invalid_rrule';
  const timeError = validateTimePair(request.input);
  if (timeError) return timeError;
  const addError = stringArray(request.input.addAttendeeIds, { max: 100, pattern: ATTENDEE_ID });
  if (addError) return `add_attendees_${addError}`;
  const removeError = stringArray(request.input.removeAttendeeIds, { max: 100, pattern: ATTENDEE_ID });
  if (removeError) return `remove_attendees_${removeError}`;
  if (request.input.notify !== undefined && typeof request.input.notify !== 'boolean') return 'invalid_notify';
  const mutationKeys = ['summary', 'description', 'start', 'rrule', 'addAttendeeIds', 'removeAttendeeIds'];
  if (!mutationKeys.some((key) => request.input[key] !== undefined)) return 'missing_calendar_update';
  return '';
}

function validateTask(request) {
  if (request.action === 'create') {
    const targetError = validateTarget(request, 'task_scope');
    if (targetError) return targetError;
    const keyError = exactKeys(request.input, new Set([
      'summary', 'description', 'due', 'assignee', 'follower', 'tasklistId',
    ]));
    if (keyError) return keyError;
    const summaryError = stringValue(request.input.summary, { required: true, max: 3000 });
    if (summaryError) return `summary_${summaryError}`;
    if (stringValue(request.input.description, { max: 100000 })) return 'invalid_description';
    if (stringValue(request.input.due, { max: 200 })) return 'invalid_due';
    if (request.input.assignee !== undefined && !MEMBER_ID.test(request.input.assignee)) return 'invalid_assignee';
    if (request.input.follower !== undefined && !MEMBER_ID.test(request.input.follower)) return 'invalid_follower';
    if (stringValue(request.input.tasklistId, { max: 4000 })) return 'invalid_tasklist_id';
    return '';
  }
  const targetError = validateTarget(request, 'task_id');
  if (targetError) return targetError;
  if (request.action === 'complete' || request.action === 'reopen') {
    return exactKeys(request.input, new Set());
  }
  if (request.action === 'update') {
    const keyError = exactKeys(request.input, new Set(['summary', 'description', 'due']));
    if (keyError) return keyError;
    if (stringValue(request.input.summary, { max: 3000 })) return 'invalid_summary';
    if (stringValue(request.input.description, { max: 100000 })) return 'invalid_description';
    if (stringValue(request.input.due, { max: 200 })) return 'invalid_due';
    if (!['summary', 'description', 'due'].some((key) => request.input[key] !== undefined)) return 'missing_task_update';
    return '';
  }
  if (request.action === 'assign') {
    const keyError = exactKeys(request.input, new Set(['add', 'remove']));
    if (keyError) return keyError;
    const addError = stringArray(request.input.add, { max: 50, pattern: MEMBER_ID });
    if (addError) return `add_members_${addError}`;
    const removeError = stringArray(request.input.remove, { max: 50, pattern: MEMBER_ID });
    if (removeError) return `remove_members_${removeError}`;
    if (!(request.input.add?.length || request.input.remove?.length)) return 'missing_assignment_change';
    return '';
  }
  const keyError = exactKeys(request.input, new Set(['set', 'remove']));
  if (keyError) return keyError;
  if ((request.input.set !== undefined) === (request.input.remove === true)) return 'reminder_requires_exactly_one_operation';
  if (request.input.set !== undefined && !/^\d+(?:m|h|d)$/.test(request.input.set)) return 'invalid_reminder';
  if (request.input.remove !== undefined && request.input.remove !== true) return 'invalid_reminder_remove';
  return '';
}

function countCells(cells) {
  if (!Array.isArray(cells) || !cells.length) return -1;
  const width = Array.isArray(cells[0]) ? cells[0].length : -1;
  if (width < 1 || cells.some((row) => !Array.isArray(row) || row.length !== width)) return -1;
  return cells.length * width;
}

function validateSheets(request) {
  const targetError = validateTarget(request, 'url', { https: true });
  if (targetError) return targetError;
  if (request.action === 'create_sheet') {
    const keyError = exactKeys(request.input, new Set(['title', 'rowCount', 'colCount', 'index']));
    if (keyError) return keyError;
    if (stringValue(request.input.title, { required: true, max: 100 })) return 'invalid_sheet_title';
    if (request.input.rowCount !== undefined && (!Number.isInteger(request.input.rowCount) || request.input.rowCount < 1 || request.input.rowCount > 50000)) return 'invalid_row_count';
    if (request.input.colCount !== undefined && (!Number.isInteger(request.input.colCount) || request.input.colCount < 1 || request.input.colCount > 200)) return 'invalid_col_count';
    if (request.input.index !== undefined && (!Number.isInteger(request.input.index) || request.input.index < 0)) return 'invalid_sheet_index';
    return '';
  }
  if (request.action === 'set_cells') {
    const keyError = exactKeys(request.input, new Set(['sheetName', 'sheetId', 'range', 'cells', 'allowOverwrite']));
    if (keyError) return keyError;
    if (Boolean(request.input.sheetName) === Boolean(request.input.sheetId)) return 'sheet_selector_required';
    if (stringValue(request.input.range, { required: true, max: 200 })) return 'invalid_range';
    const cellCount = countCells(request.input.cells);
    if (cellCount < 1) return 'invalid_cells';
    if (cellCount > 10000) return 'too_many_cells';
    if (request.input.allowOverwrite !== undefined && typeof request.input.allowOverwrite !== 'boolean') return 'invalid_allow_overwrite';
    if (request.input.allowOverwrite === true && request.confirmHighImpact !== true) return 'overwrite_confirmation_required';
    return '';
  }
  const keyError = exactKeys(request.input, new Set(['sheets']));
  if (keyError) return keyError;
  if (!isObject(request.input.sheets) || !Array.isArray(request.input.sheets.sheets)) return 'invalid_sheets_payload';
  if (request.input.sheets.sheets.length < 1 || request.input.sheets.sheets.length > 10) return 'invalid_sheet_count';
  let totalRows = 0;
  for (const sheet of request.input.sheets.sheets) {
    if (!isObject(sheet) || stringValue(sheet.name, { required: true, max: 100 })) return 'invalid_sheet_entry';
    if (sheet.mode !== undefined && sheet.mode !== 'append') return 'append_mode_required';
    if (!Array.isArray(sheet.columns) || !sheet.columns.length || sheet.columns.length > 200) return 'invalid_sheet_columns';
    if (!Array.isArray(sheet.data)) return 'invalid_sheet_data';
    if (sheet.data.some((row) => !Array.isArray(row) || row.length !== sheet.columns.length)) return 'invalid_sheet_rows';
    totalRows += sheet.data.length;
  }
  if (totalRows < 1 || totalRows > 5000) return 'invalid_total_rows';
  return '';
}

function validateBase(request) {
  const targetError = validateTarget(request, 'url', { https: true });
  if (targetError) return targetError;
  if (request.action === 'create_records') {
    const keyError = exactKeys(request.input, new Set(['tableId', 'records']));
    if (keyError) return keyError;
    if (stringValue(request.input.tableId, { required: true, max: 200 })) return 'invalid_table_id';
    if (!Array.isArray(request.input.records) || request.input.records.length < 1 || request.input.records.length > 200) return 'invalid_records';
    if (request.input.records.some((record) => !isObject(record) || Object.keys(record).length < 1)) return 'invalid_record';
  } else {
    const keyError = exactKeys(request.input, new Set(['tableId', 'updates']));
    if (keyError) return keyError;
    if (stringValue(request.input.tableId, { required: true, max: 200 })) return 'invalid_table_id';
    if (!isObject(request.input.updates)) return 'invalid_updates';
    const entries = Object.entries(request.input.updates);
    if (entries.length < 1 || entries.length > 200) return 'invalid_updates';
    if (entries.some(([recordId, fields]) => !recordId || !isObject(fields) || Object.keys(fields).length < 1)) return 'invalid_record_update';
  }
  if (Buffer.byteLength(JSON.stringify(request.input), 'utf8') > 2 * 1024 * 1024) return 'payload_too_large';
  return '';
}

function validateMinutes(request) {
  const targetKind = request.action === 'upload' ? 'file_token' : 'minute_token';
  const targetError = validateTarget(request, targetKind);
  if (targetError) return targetError;
  if (!REMOTE_TOKEN.test(request.target.value)) return 'invalid_minutes_target';

  if (request.action === 'upload') return exactKeys(request.input, new Set());

  if (request.action === 'update_title') {
    const keyError = exactKeys(request.input, new Set(['topic']));
    if (keyError) return keyError;
    return stringValue(request.input.topic, { required: true, max: 1000 }) ? 'invalid_minutes_topic' : '';
  }

  if (request.action === 'replace_summary') {
    const keyError = exactKeys(request.input, new Set(['summary']));
    if (keyError) return keyError;
    if (stringValue(request.input.summary, { required: true, max: 200000 })) return 'invalid_minutes_summary';
    if (request.confirmHighImpact !== true) return 'high_impact_confirmation_required';
    return '';
  }

  if (request.action === 'mutate_todos') {
    const keyError = exactKeys(request.input, new Set(['todos']));
    if (keyError) return keyError;
    if (!Array.isArray(request.input.todos) || request.input.todos.length < 1 || request.input.todos.length > 100) {
      return 'invalid_minutes_todos';
    }
    for (const todo of request.input.todos) {
      if (!isObject(todo)) return 'invalid_minutes_todo';
      if (todo.operation === 'delete') return 'minutes_todo_delete_not_supported';
      if (!['add', 'update'].includes(todo.operation)) return 'invalid_minutes_todo_operation';
      const allowed = todo.operation === 'add'
        ? new Set(['operation', 'content', 'is_done'])
        : new Set(['operation', 'todo_id', 'content', 'is_done']);
      if (Object.keys(todo).some((key) => !allowed.has(key))) return 'unsupported_minutes_todo_field';
      if (stringValue(todo.content, { required: true, max: 10000 })) return 'invalid_minutes_todo_content';
      if (typeof todo.is_done !== 'boolean') return 'invalid_minutes_todo_done_state';
      if (todo.operation === 'update' && stringValue(todo.todo_id, { required: true, max: 400 })) {
        return 'invalid_minutes_todo_id';
      }
    }
    return '';
  }

  if (request.action === 'replace_words') {
    const keyError = exactKeys(request.input, new Set(['replacements']));
    if (keyError) return keyError;
    if (!Array.isArray(request.input.replacements)
      || request.input.replacements.length < 1
      || request.input.replacements.length > 100) return 'invalid_minutes_replacements';
    for (const replacement of request.input.replacements) {
      if (!isObject(replacement)
        || Object.keys(replacement).some((key) => !['source_word', 'target_word'].includes(key))
        || stringValue(replacement.source_word, { required: true, max: 1000 })
        || stringValue(replacement.target_word, { required: true, max: 1000 })) {
        return 'invalid_minutes_replacement';
      }
    }
    if (request.confirmHighImpact !== true) return 'high_impact_confirmation_required';
    return '';
  }

  const keyError = exactKeys(request.input, new Set(['fromSpeakerId', 'toUserId']));
  if (keyError) return keyError;
  if (stringValue(request.input.fromSpeakerId, { required: true, max: 400 })) return 'invalid_minutes_speaker_id';
  if (!/^ou_[A-Za-z0-9_-]+$/.test(String(request.input.toUserId || ''))) return 'invalid_minutes_user_id';
  if (request.confirmHighImpact !== true) return 'high_impact_confirmation_required';
  return '';
}

function validateActionRequest(request) {
  if (!isObject(request)) return 'request_not_object';
  if (request.dryRun !== undefined && typeof request.dryRun !== 'boolean') return 'invalid_dry_run';
  if (!request.id || typeof request.id !== 'string') return 'missing_id';
  if (request.type === 'feishu_capability') return validateCapabilityRequest(request);
  const allowedRequestFields = new Set([
    'id', 'type', 'domain', 'action', 'identity', 'target', 'input',
    'explicitAuthorization', 'confirmHighImpact', 'dryRun', 'source', 'reason',
    'trace', 'createdAt',
  ]);
  const unknownRequestField = Object.keys(request).find((key) => !allowedRequestFields.has(key));
  if (unknownRequestField) return `unsupported_request_field:${unknownRequestField}`;
  if (request.type !== 'feishu_action') return 'unsupported_type';
  if (request.identity !== 'user') return 'unsupported_identity';
  if (!request.source || typeof request.source !== 'string') return 'missing_source';
  if (request.explicitAuthorization !== true) return 'explicit_authorization_required';
  if (request.confirmHighImpact !== undefined && typeof request.confirmHighImpact !== 'boolean') return 'invalid_high_impact_confirmation';
  if (request.reason !== undefined && (typeof request.reason !== 'string' || request.reason.length > 2000)) return 'invalid_reason';
  if (request.createdAt !== undefined && (typeof request.createdAt !== 'string' || !Number.isFinite(Date.parse(request.createdAt)))) return 'invalid_created_at';
  if (request.trace !== undefined) {
    if (!isObject(request.trace)
      || Object.keys(request.trace).some((key) => !['system', 'code'].includes(key))
      || typeof request.trace.system !== 'string'
      || typeof request.trace.code !== 'string') return 'invalid_trace';
  }
  const key = `${request.domain || ''}.${request.action || ''}`;
  if (!ACTION_KEYS.has(key)) return 'unsupported_domain_or_action';
  if (!isObject(request.input)) return 'missing_input';
  if (request.domain === 'drive') return validateDrive(request);
  if (request.domain === 'calendar') return validateCalendar(request);
  if (request.domain === 'task') return validateTask(request);
  if (request.domain === 'sheets') return validateSheets(request);
  if (request.domain === 'base') return validateBase(request);
  if (request.domain === 'minutes') return validateMinutes(request);
  return 'unsupported_domain';
}

function actionKey(request) {
  return `${request?.domain || ''}.${request?.action || ''}`;
}

module.exports = {
  ACTIONBOX_TERMINAL_STATUSES,
  ACTION_KEYS,
  actionKey,
  validateActionRequest,
};
