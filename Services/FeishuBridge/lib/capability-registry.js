const path = require('node:path');
const {
  LARK_CLI_FLAG_SNAPSHOT,
  LARK_CLI_FLAG_SNAPSHOT_VERSION,
} = require('./lark-cli-flag-snapshot');

const RISKS = new Set(['read', 'write', 'high-impact-write', 'remote-operation']);
const IDENTITIES = new Set(['bot', 'user']);
const REMOTE_ID = /^[A-Za-z0-9][A-Za-z0-9._:/-]{0,399}$/;

const field = (type = 'string', options = {}) => Object.freeze({ type, ...options });
const string = (options = {}) => field('string', { max: 4000, ...options });
const id = (options = {}) => string({ pattern: REMOTE_ID, max: 400, ...options });
const integer = (options = {}) => field('integer', { min: 1, max: 100, ...options });
const bool = (options = {}) => field('boolean', options);
const csv = (options = {}) => field('csv', { maxItems: 50, itemPattern: REMOTE_ID, ...options });
const json = (options = {}) => field('json', { maxBytes: 2 * 1024 * 1024, private: true, ...options });
const localPath = (options = {}) => field('path', options);

const GLOBAL_SHORTCUT_FLAGS = new Set([
  'as', 'dry-run', 'format', 'help', 'jq', 'json', 'yes',
  'page-all', 'page-limit', 'print-schema', 'print-example', 'flag-name',
]);
const CONTROL_FLAGS = new Set(['action', 'command', 'operation', 'overwrite']);
const REQUIRED_FIELDS = Object.freeze({
  'im.message.forward': ['receive-id-type', 'data'],
  'im.message.merge-forward': ['receive-id-type', 'data'],
  'im.thread.forward': ['receive-id-type', 'data'],
  'im.feed.shortcuts.add': ['chat-id'],
  'im.pin.add': ['data'],
  'calendar.event.join': ['token'],
  'calendar.search': ['data'],
  'calendar.room.find': ['slot'],
  'calendar.time.suggestion': ['start', 'end', 'attendee-ids', 'duration-minutes'],
  'contact.user.batch-profile': ['data'],
  'docs.media.preview': ['token'],
  'docs.media.download': ['token'],
  'wiki.node.get': ['node-token'],
  'task.tasklist.add-tasks': ['task-id'],
  'task.attachment.upload': ['resource-id', 'resource-type'],
  'task.sections.list': ['resource-id', 'resource-type'],
  'task.custom-fields.list': ['resource-id', 'resource-type'],
  'sheets.workbook.export': ['file-extension'],
  'sheets.sheet.rename': ['title'],
  'sheets.sheet.move': ['index'],
  'sheets.dimension.insert': ['position', 'count'],
  'sheets.dimension.move': ['source-range', 'target'],
  'sheets.dimension.hide': ['range'],
  'sheets.dimension.unhide': ['range'],
  'sheets.dimension.group': ['range'],
  'sheets.dimension.ungroup': ['range'],
  'sheets.rows.resize': ['range'],
  'sheets.cols.resize': ['range'],
  'sheets.cells.style': ['range'],
  'sheets.cells.image': ['range', 'image'],
  'sheets.cells.merge': ['range'],
  'sheets.cells.unmerge': ['range'],
  'sheets.cells.replace': ['find', 'replacement'],
  'sheets.range.copy': ['source-range', 'target-range'],
  'sheets.range.fill': ['source-range', 'target-range'],
  'sheets.range.move': ['source-range', 'target-range'],
  'sheets.range.sort': ['range'],
  'sheets.chart.create': ['properties'],
  'sheets.chart.update': ['chart-id', 'properties'],
  'sheets.chart.config.update': ['chart-id'],
  'sheets.chart.data.update': ['chart-id', 'data-range'],
  'sheets.pivot.create': ['properties'],
  'sheets.pivot.update': ['pivot-table-id'],
  'sheets.conditional-format.create': ['properties'],
  'sheets.conditional-format.update': ['rule-id'],
  'sheets.filter.create': ['range', 'properties'],
  'sheets.filter.update': ['range', 'properties'],
  'sheets.filter-view.create': ['range', 'properties'],
  'sheets.filter-view.update': ['view-id', 'properties'],
  'sheets.dropdown.set': ['range', 'options'],
  'sheets.dropdown.update': ['ranges', 'options'],
  'sheets.dropdown.get': ['range'],
  'sheets.sparkline.create': ['properties'],
  'sheets.sparkline.update': ['group-id', 'properties'],
  'sheets.floating-image.update': ['float-image-id'],
  'sheets.conditional-format.results': ['range'],
  'base.data.query': ['base-token', 'dsl'],
  'base.record.history': ['base-token', 'table-id', 'record-id'],
  'base.record.attachment.download': ['base-token', 'table-id', 'record-id'],
  'base.templates.list': ['category-key'],
  'base.templates.search': ['keyword'],
  'base.workspace.entities': ['workspace-token'],
  'base.base.get': ['base-token'],
  'base.app.get': ['app-token'],
  'base.table.get': ['base-token', 'table-id'],
  'base.table.list': ['base-token'],
  'base.field.get': ['base-token', 'table-id', 'field-id'],
  'base.field.list': ['base-token', 'table-id'],
  'base.view.get': ['base-token', 'table-id', 'view-id'],
  'base.view.list': ['base-token', 'table-id'],
  'base.form.get': ['base-token', 'table-id', 'form-id'],
  'base.form.list': ['base-token', 'table-id'],
  'base.dashboard.get': ['base-token', 'dashboard-id'],
  'base.dashboard.list': ['base-token'],
  'base.app-page.get': ['app-token', 'page-id'],
  'base.app-page.list': ['app-token'],
  'base.app-block.get': ['app-token', 'page-id', 'block-id'],
  'base.app-block.list': ['app-token', 'page-id'],
  'base.base-block.list': ['base-token'],
  'base.base.create': ['name'],
  'base.app.create': ['workspace-token', 'name'],
  'base.table.create': ['base-token', 'name', 'fields'],
  'base.view.create': ['base-token', 'table-id', 'json'],
  'base.form.create': ['base-token', 'table-id', 'name'],
  'base.dashboard.create': ['base-token', 'name'],
  'base.app-page.create': ['app-token', 'name'],
  'base.app-block.create': ['app-token', 'page-id', 'type', 'name', 'data-config'],
  'base.base-block.create': ['base-token', 'type', 'name'],
  'base.workspace.create': ['name'],
  'base.record.attachment.upload': ['base-token', 'table-id', 'record-id', 'field-id', 'file'],
  'base.table.update': ['base-token', 'table-id', 'name'],
  'base.field.update': ['base-token', 'table-id', 'field-id', 'json'],
  'base.view.rename': ['base-token', 'table-id', 'view-id', 'name'],
  'base.form.update': ['base-token', 'table-id', 'form-id'],
  'base.dashboard.update': ['base-token', 'dashboard-id'],
  'base.app-page.update': ['app-token', 'page-id', 'name'],
  'base.app-block.update': ['app-token', 'page-id', 'block-id', 'data-config'],
});

const REQUIRED_GROUPS = Object.freeze({
  'im.message.reply': [['text', 'markdown', 'content', 'image', 'file', 'video', 'audio']],
  'im.message.edit': [['text', 'markdown', 'content', 'set-attachments']],
  'contact.user.search': [['query', 'queries', 'user-ids']],
  'docs.draft.preflight': [['doc', 'content']],
  'sheets.dimension.freeze': [['rows', 'cols']],
  'sheets.sheet.copy': [['sheet-id', 'sheet-name']],
  'sheets.sheet.hide': [['sheet-id', 'sheet-name']],
  'sheets.sheet.unhide': [['sheet-id', 'sheet-name']],
  'sheets.floating-image.create': [['image', 'image-token', 'image-uri']],
  'base.app-block.data': [['app-token', 'base-token']],
  'base.form.update': [['name', 'description']],
  'base.dashboard.update': [['name', 'theme-style']],
  'apps.app.update': [['name', 'description']],
});

const RESULT_IDENTIFIERS = Object.freeze({
  'im.chat.create': [{ key: 'chat_id', kind: 'chat_id' }],
  'markdown.create': [{ key: 'file_token', kind: 'file_token' }, { key: 'token', kind: 'file_token' }],
  'wiki.space.create': [{ key: 'space_id', kind: 'space_id' }],
  'wiki.node.get': [{
    key: 'obj_token',
    kind: 'mindnote_id',
    whenResult: { key: 'obj_type', equals: 'mindnote' },
  }],
  'wiki.node.create': [
    {
      key: 'obj_token',
      kind: 'mindnote_id',
      whenInput: { key: 'obj-type', equals: 'mindnote' },
    },
    { key: 'node_token', kind: 'node_token' },
  ],
  'wiki.node.copy': [{ key: 'node_token', kind: 'node_token' }],
  'mindnotes.nodes.list': [{
    key: 'node_id',
    kind: 'mindnote_node_id',
    arrayKey: 'nodes',
    whereMissing: ['parent_id'],
  }],
  'calendar.create': [{ key: 'calendar_id', kind: 'calendar_id' }],
  'task.subtask.create': [{ key: 'guid', kind: 'task_id' }, { key: 'task_guid', kind: 'task_id' }],
  'task.tasklist.create': [{ key: 'guid', kind: 'tasklist_id' }, { key: 'tasklist_id', kind: 'tasklist_id' }],
  'sheets.workbook.create': [{ key: 'spreadsheet_token', kind: 'spreadsheet_token' }],
  'base.base.create': [{ key: 'base_token', kind: 'base_token' }],
  'base.app.create': [{ key: 'app_token', kind: 'app_token' }],
  'base.table.create': [{ key: 'table_id', kind: 'table_id' }],
  'base.view.create': [{ key: 'view_id', kind: 'view_id' }],
  'base.form.create': [{ key: 'form_id', kind: 'form_id' }],
  'base.dashboard.create': [{ key: 'dashboard_id', kind: 'dashboard_id' }],
  'base.app-page.create': [{ key: 'page_id', kind: 'page_id' }],
  'base.app-block.create': [{ key: 'block_id', kind: 'block_id' }],
  'base.base-block.create': [{ key: 'block_id', kind: 'block_id' }],
  'base.workspace.create': [{ key: 'workspace_id', kind: 'workspace_id' }],
  'apps.app.create': [{ key: 'app_id', kind: 'app_id' }],
  'apps.session.create': [{ key: 'session_id', kind: 'session_id' }],
  'apps.release.create': [{ key: 'release_id', kind: 'release_id' }],
});

function inferredSchema(name, metadata = {}) {
  const kind = String(metadata.kind || '').toLowerCase();
  const privateInput = Boolean(metadata.fileInput);
  if (name === 'json' && kind) return json();
  if (kind === '' || kind === 'bool') return bool();
  if (/^(?:int|int32|int64|uint|uint32|uint64|float64)$/.test(kind)) {
    return integer({ min: 0, max: name === 'page-size' || name === 'limit' ? 200 : 1000000 });
  }
  if (/strings|slice/.test(kind) || /(?:^|-)ids$/.test(name)) return csv({ maxItems: 200 });
  if (/^(?:file|files|image|dir|directory|output|output-dir|output-path|path)$/.test(name)) {
    return localPath({ output: /^(?:output|output-dir|output-path)$/.test(name) });
  }
  if (privateInput && /(?:data|json|properties|fields|views|rules|options|sorts|styles|values|sheets|ranges)/.test(name)) {
    return json();
  }
  return string({ max: /(?:content|description|message|markdown|source|pattern|replacement|prompt)/.test(name) ? 1000000 : 4000, private: privateInput });
}

function unsafeShortcutFlag(name) {
  return GLOBAL_SHORTCUT_FLAGS.has(name)
    || /(?:^|[-_])(?:delete|remove|clear|transfer|permission|role|member)(?:$|[-_])/.test(name)
    || /(?:phone|sms)/.test(name)
    || name === 'owner'
    || name === 'set-bot-manager'
    || name === 'no-validate'
    || name === 'params';
}

function reviewedShortcutFlags(definition) {
  const commandKey = definition.command.join(' ');
  const snapshot = LARK_CLI_FLAG_SNAPSHOT[commandKey];
  if (!snapshot) throw new Error(`lark-cli ${LARK_CLI_FLAG_SNAPSHOT_VERSION} command not in reviewed snapshot: ${commandKey}`);
  const fixedNames = new Set((definition.fixedArgs || [])
    .filter((item) => String(item).startsWith('--'))
    .map((item) => String(item).slice(2)));
  for (const fixedName of fixedNames) {
    if (!Object.hasOwn(snapshot, fixedName)) throw new Error(`invalid fixed lark-cli flag ${fixedName}: ${definition.id}`);
  }
  const excluded = new Set(definition.excludeFlags || []);
  const result = {};
  for (const [name, metadata] of Object.entries(snapshot)) {
    if ((unsafeShortcutFlag(name) && !(name === 'json' && metadata.kind)) || excluded.has(name) || fixedNames.has(name)) continue;
    if (CONTROL_FLAGS.has(name)) continue;
    result[name] = definition.flags?.[name] || inferredSchema(name, metadata);
  }
  return result;
}

function applyRequiredConstraints(value) {
  const flags = { ...value.flags };
  for (const name of REQUIRED_FIELDS[value.id] || []) {
    if (!flags[name]) throw new Error(`required reviewed flag ${name} is unavailable: ${value.id}`);
    flags[name] = Object.freeze({ ...flags[name], required: true });
  }
  if (value.id === 'base.app-block.create' && flags.type) {
    flags.type = field('enum', {
      required: flags.type.required,
      values: ['column', 'bar', 'line', 'pie', 'ring', 'scatter', 'funnel', 'wordCloud', 'area', 'combo', 'radar', 'statistics', 'text', 'list'],
    });
    if (flags['sub-type']) flags['sub-type'] = field('enum', { values: ['standard', 'grouped', 'collapsible', 'card', 'detail'] });
  }
  if (value.id === 'base.base-block.create' && flags.type) {
    flags.type = field('enum', { required: flags.type.required, values: ['folder', 'table', 'docx', 'dashboard'] });
  }
  if (value.id === 'base.base-block.list' && flags.type) {
    flags.type = field('enum', { values: ['folder', 'table', 'docx', 'dashboard'] });
  }
  value.flags = flags;
  const groups = [...(value.scope?.requiredGroups || []), ...(REQUIRED_GROUPS[value.id] || [])];
  if (value.domain === 'sheets' && flags.url && flags['spreadsheet-token']) {
    groups.push(['url', 'spreadsheet-token']);
  }
  if (value.domain === 'sheets'
    && flags['sheet-id'] && flags['sheet-name']
    && !['sheets.workbook.info', 'sheets.history.list', 'sheets.workbook.export'].includes(value.id)) {
    groups.push(['sheet-id', 'sheet-name']);
  }
  value.scope = { ...(value.scope || {}), requiredGroups: groups };
}

function define(definition) {
  const value = {
    identity: 'user',
    risk: 'read',
    transport: 'shortcut',
    queue: 'actionbox',
    flags: {},
    fixedArgs: [],
    scope: { bounded: true },
    preflight: null,
    reread: null,
    redaction: { identifiers: true, body: true },
    resultIdentifiers: RESULT_IDENTIFIERS[definition.id] || [],
    ...definition,
  };
  if (value.transport === 'shortcut') {
    value.cliConfirm = Boolean(LARK_CLI_FLAG_SNAPSHOT[value.command.join(' ')]?.yes);
    value.flags = reviewedShortcutFlags(value);
  }
  applyRequiredConstraints(value);
  if (!value.id || typeof value.id !== 'string') throw new Error('capability id is required');
  if (!RISKS.has(value.risk)) throw new Error(`invalid capability risk: ${value.id}`);
  if (!IDENTITIES.has(value.identity)) throw new Error(`invalid capability identity: ${value.id}`);
  if (!Array.isArray(value.command) || value.command.length < 2) {
    throw new Error(`fixed command is required: ${value.id}`);
  }
  if (value.risk === 'high-impact-write' && (!value.preflight || !value.reread)) {
    throw new Error(`high-impact capability requires preflight and reread: ${value.id}`);
  }
  return Object.freeze(value);
}

const page = {
  'page-size': integer({ max: 100 }),
  'page-token': string({ max: 1000 }),
};
const timeWindow = {
  start: string({ max: 80 }),
  end: string({ max: 80 }),
};
const spreadsheet = {
  url: string({ max: 4000 }),
  'spreadsheet-token': id(),
  'sheet-id': id(),
  'sheet-name': string({ max: 200 }),
};
const base = {
  url: string({ max: 4000 }),
  'base-token': id(),
  'table-id': id(),
  'table-name': string({ max: 200 }),
};

const definitions = [
  // Messages and chats. Destructive membership, moderation and delete commands are intentionally absent.
  define({ id: 'im.chat.list', domain: 'im', identity: 'bot', command: ['im', '+chat-list'], flags: { ...page, types: csv({ itemPattern: /^(?:p2p|group|topic)$/ }), 'exclude-muted': bool() } }),
  define({ id: 'im.chat.search', domain: 'im', command: ['im', '+chat-search'], flags: { query: string({ required: true, max: 200 }), 'member-ids': csv(), ...page, types: csv({ itemPattern: /^(?:p2p|group|topic)$/ }) } }),
  define({ id: 'im.chat.members.list', domain: 'im', identity: 'bot', command: ['im', '+chat-members-list'], flags: { 'chat-id': id({ required: true }), 'member-types': csv({ itemPattern: /^(?:user|bot)$/ }), ...page } }),
  define({ id: 'im.message.batch-get', domain: 'im', identity: 'bot', command: ['im', '+messages-mget'], flags: { 'message-ids': csv({ required: true, maxItems: 50 }) } }),
  define({ id: 'im.message.read-users', domain: 'im', identity: 'bot', command: ['im', '+message-read-users'], flags: { 'message-id': id({ required: true }), ...page } }),
  define({ id: 'im.message.read-status', domain: 'im', command: ['im', '+messages-read-status'], flags: { 'message-ids': csv({ required: true, maxItems: 50 }) } }),
  define({ id: 'im.feed.groups.list', domain: 'im', command: ['im', '+feed-group-list'], flags: { ...page } }),
  define({ id: 'im.feed.items.list', domain: 'im', command: ['im', '+feed-group-list-item'], flags: { 'feed-group-id': id({ required: true }), ...page } }),
  define({ id: 'im.feed.items.query', domain: 'im', command: ['im', '+feed-group-query-item'], flags: { 'feed-group-id': id({ required: true }), 'feed-ids': csv({ required: true, maxItems: 50 }) } }),
  define({ id: 'im.feed.shortcuts.list', domain: 'im', command: ['im', '+feed-shortcut-list'], flags: { ...page, 'no-detail': bool() } }),
  define({ id: 'im.bookmarks.list', domain: 'im', command: ['im', '+flag-list'], flags: { ...page, 'flag-type': field('enum', { values: ['message', 'feed'] }) } }),
  define({ id: 'im.pins.list', domain: 'im', identity: 'bot', command: ['im', 'pins', 'list'], flags: { 'chat-id': id({ required: true }), ...page } }),
  define({ id: 'im.reactions.list', domain: 'im', identity: 'bot', command: ['im', 'reactions', 'list'], flags: { 'message-id': id({ required: true }), 'reaction-type': string({ max: 100 }), ...page } }),
  define({ id: 'im.chat.create', domain: 'im', risk: 'write', command: ['im', '+chat-create'], flags: { name: string({ required: true, max: 60 }), description: string({ max: 100 }), users: csv({ maxItems: 50 }), bots: csv({ maxItems: 5 }), type: field('enum', { values: ['private', 'public'] }), 'chat-mode': field('enum', { values: ['group', 'topic'] }), owner: id() }, reread: { id: 'im.chat.search', map: { query: 'name' } } }),
  define({ id: 'im.chat.update', domain: 'im', risk: 'write', command: ['im', '+chat-update'], flags: { 'chat-id': id({ required: true }), name: string({ max: 60 }), description: string({ max: 100 }) }, scope: { bounded: true, requireAny: ['name', 'description'] }, reread: { id: 'im.chat.list', map: {} } }),
  define({ id: 'im.message.reply', domain: 'im', risk: 'write', command: ['im', '+messages-reply'], flags: { 'message-id': id({ required: true }), text: string({ max: 100000, private: true, stdin: true }), markdown: string({ max: 100000, private: true, stdin: true }), 'reply-in-thread': bool(), 'idempotency-key': id() }, scope: { bounded: true, requireExactlyOne: ['text', 'markdown'] } }),
  define({ id: 'im.message.edit', domain: 'im', identity: 'bot', risk: 'write', command: ['im', '+messages-edit'], flags: { 'message-id': id({ required: true }), text: string({ max: 100000, private: true, stdin: true }), markdown: string({ max: 100000, private: true, stdin: true }) }, scope: { bounded: true, requireExactlyOne: ['text', 'markdown'] }, reread: { id: 'im.message.batch-get', map: { 'message-ids': 'message-id' } } }),
  define({ id: 'im.message.forward', domain: 'im', risk: 'write', command: ['im', 'messages', 'forward'], flags: { 'message-id': id({ required: true }), 'receive-id': id({ required: true }), 'receive-id-type': field('enum', { values: ['chat_id', 'open_id', 'user_id', 'union_id'] }) } }),
  define({ id: 'im.message.merge-forward', domain: 'im', risk: 'write', command: ['im', 'messages', 'merge_forward'], flags: { 'message-id': csv({ required: true, maxItems: 50 }), 'receive-id': id({ required: true }), 'receive-id-type': field('enum', { values: ['chat_id', 'open_id', 'user_id', 'union_id'] }) } }),
  define({ id: 'im.thread.forward', domain: 'im', risk: 'write', command: ['im', 'threads', 'forward'], flags: { 'thread-id': id({ required: true }), 'receive-id': id({ required: true }), 'receive-id-type': field('enum', { values: ['chat_id', 'open_id', 'user_id', 'union_id'] }) } }),
  define({ id: 'im.feed.shortcuts.add', domain: 'im', risk: 'write', command: ['im', '+feed-shortcut-create'], flags: { 'chat-ids': csv({ required: true, maxItems: 10 }), head: bool(), tail: bool() }, scope: { bounded: true, mutuallyExclusive: ['head', 'tail'] } }),
  define({ id: 'im.bookmark.add', domain: 'im', risk: 'write', command: ['im', '+flag-create'], flags: { 'message-id': id({ required: true }), 'flag-type': field('enum', { values: ['message', 'feed'] }) } }),
  define({ id: 'im.pin.add', domain: 'im', identity: 'bot', risk: 'write', command: ['im', 'pins', 'create'], flags: { 'message-id': id({ required: true }) } }),
  define({ id: 'im.reaction.add', domain: 'im', identity: 'bot', risk: 'write', command: ['im', 'reactions', 'create'], flags: { 'message-id': id({ required: true }), 'reaction-type': string({ required: true, max: 100 }) } }),
  define({ id: 'im.urgent.app', domain: 'im', identity: 'bot', risk: 'write', command: ['im', 'messages', 'urgent_app'], flags: { 'message-id': id({ required: true }), 'user-id-list': csv({ required: true, maxItems: 200 }) } }),

  // Contact details are read on demand. The bridge cache module remains intentionally smaller.
  define({ id: 'contact.user.get', domain: 'contact', command: ['contact', '+get-user'], flags: { 'user-id': id(), 'user-id-type': field('enum', { values: ['open_id', 'union_id', 'user_id'] }) } }),
  define({ id: 'contact.user.search', domain: 'contact', command: ['contact', '+search-user'], flags: { query: string({ max: 200 }), 'open-ids': csv({ maxItems: 50 }), filter: json(), ...page }, scope: { bounded: true, requireAny: ['query', 'open-ids', 'filter'] } }),
  define({ id: 'contact.bot.search', domain: 'contact', command: ['contact', '+search-bot'], flags: { query: string({ required: true, max: 200 }), 'chat-ids': csv({ maxItems: 20 }), ...page } }),
  define({ id: 'contact.user.batch-profile', domain: 'contact', command: ['contact', 'user_profiles', 'batch_query'], flags: { 'user-id': csv({ required: true, maxItems: 50 }), 'user-id-type': field('enum', { values: ['open_id', 'union_id', 'user_id'] }) } }),

  // Fixed event subscription adapters. Approval events and arbitrary EventKeys are absent.
  define({ id: 'events.watch.task.add', domain: 'events', risk: 'write', transport: 'raw', command: ['api', 'POST'], apiPath: '/open-apis/task/v2/task_v2/task_subscription?user_id_type=open_id', flags: {} }),
  define({ id: 'events.watch.whiteboard.add', domain: 'events', risk: 'write', transport: 'raw', command: ['api', 'POST'], apiPath: '/open-apis/board/v1/whiteboards/{target}/subscribe', flags: { target: id({ required: true, path: true }) } }),
  define({ id: 'events.watch.whiteboard.remove', domain: 'events', risk: 'write', transport: 'raw', command: ['api', 'POST'], apiPath: '/open-apis/board/v1/whiteboards/{target}/unsubscribe', flags: { target: id({ required: true, path: true }) } }),
  ...['meeting', 'note', 'recording', 'minutes'].flatMap((type) => {
    const resource = { meeting: 'meetings', note: 'notes', recording: 'recordings', minutes: 'minutes' }[type];
    const prefix = type === 'minutes' ? 'minutes' : 'vc';
    return ['add', 'remove'].map((action) => define({
      id: `events.watch.${type}.${action}`,
      domain: 'events',
      risk: 'write',
      transport: 'raw',
      command: ['api', 'POST'],
      apiPath: `/open-apis/${prefix}/v1/${resource}/${action === 'add' ? 'subscription' : 'unsubscription'}`,
      flags: {},
    }));
  }),

  // Docs, whiteboards, Mindnotes and Drive-native Markdown.
  define({ id: 'docs.history.list', domain: 'docs', command: ['docs', '+history-list'], flags: { doc: string({ required: true, max: 4000 }), ...page } }),
  define({ id: 'docs.media.preview', domain: 'docs', command: ['docs', '+media-preview'], flags: { doc: string({ required: true, max: 4000 }), 'file-token': id(), 'block-id': id(), output: localPath({ output: true }) } }),
  define({ id: 'docs.media.download', domain: 'docs', command: ['docs', '+media-download'], flags: { doc: string({ required: true, max: 4000 }), 'file-token': id(), 'block-id': id(), output: localPath({ output: true }) } }),
  define({ id: 'docs.resource.read', domain: 'docs', command: ['docs', '+resource-download'], flags: { doc: string({ required: true, max: 4000 }), type: field('enum', { values: ['cover'] }), output: localPath({ required: true, output: true }) } }),
  define({ id: 'docs.draft.preflight', domain: 'docs', command: ['docs', '+script'], fixedArgs: ['preflight'], flags: { dir: localPath({ required: true }), profile: bool() } }),
  define({ id: 'docs.history.revert', domain: 'docs', risk: 'high-impact-write', command: ['docs', '+history-revert'], flags: { doc: string({ required: true, max: 4000 }), 'history-version-id': id({ required: true }), 'wait-timeout-ms': integer({ min: 0, max: 30000 }) }, preflight: { id: 'docs.history.list', map: { doc: 'doc' } }, reread: { id: 'docs.history.list', map: { doc: 'doc' } } }),
  define({ id: 'docs.media.upload', domain: 'docs', risk: 'write', command: ['docs', '+media-upload'], flags: { doc: string({ required: true, max: 4000 }), file: localPath({ required: true }), 'parent-type': field('enum', { values: ['docx_image', 'docx_file', 'whiteboard', 'mindnote_image'] }), 'parent-node': id() } }),
  define({ id: 'docs.media.insert', domain: 'docs', risk: 'write', command: ['docs', '+media-insert'], flags: { doc: string({ required: true, max: 4000 }), file: localPath({ required: true }), type: field('enum', { values: ['image', 'file'] }) }, reread: { id: 'docs.history.list', map: { doc: 'doc' } } }),
  define({ id: 'docs.resource.update', domain: 'docs', risk: 'write', command: ['docs', '+resource-update'], flags: { doc: string({ required: true, max: 4000 }), type: field('enum', { values: ['cover'] }), file: localPath({ required: true }) }, reread: { id: 'docs.history.list', map: { doc: 'doc' } } }),
  define({ id: 'docs.whiteboard.insert', domain: 'docs', risk: 'write', queue: 'docbox', command: ['docs', '+update'], fixedArgs: ['--command', 'append'], transform: 'doc-whiteboard', flags: { doc: string({ required: true, max: 4000 }), content: string({ required: true, max: 500000, private: true, stdin: true }), 'doc-format': field('enum', { required: true, values: ['mermaid', 'plantuml', 'svg'] }) }, reread: { id: 'docs.history.list', map: { doc: 'doc' } } }),
  define({ id: 'whiteboard.export', domain: 'whiteboard', command: ['whiteboard', '+export'], flags: { 'whiteboard-token': id({ required: true }), output_as: field('enum', { values: ['image', 'svg', 'code', 'raw'] }), output: localPath({ output: true }) } }),
  define({ id: 'whiteboard.append', domain: 'whiteboard', risk: 'write', command: ['whiteboard', '+update'], flags: { 'whiteboard-token': id({ required: true }), input_format: field('enum', { required: true, values: ['raw', 'plantuml', 'mermaid', 'svg'] }), source: string({ required: true, max: 1000000, private: true, stdin: true }), 'idempotent-token': id({ min: 10 }) }, reread: { id: 'whiteboard.export', map: { 'whiteboard-token': 'whiteboard-token' }, defaults: { 'output-type': 'raw' } } }),
  define({ id: 'whiteboard.overwrite', domain: 'whiteboard', risk: 'high-impact-write', command: ['whiteboard', '+update'], fixedArgs: ['--overwrite'], flags: { 'whiteboard-token': id({ required: true }), input_format: field('enum', { required: true, values: ['raw', 'plantuml', 'mermaid', 'svg'] }), source: string({ required: true, max: 1000000, private: true, stdin: true }), 'idempotent-token': id({ min: 10 }) }, preflight: { id: 'whiteboard.export', map: { 'whiteboard-token': 'whiteboard-token' }, defaults: { 'output-type': 'raw' } }, reread: { id: 'whiteboard.export', map: { 'whiteboard-token': 'whiteboard-token' }, defaults: { 'output-type': 'raw' } } }),
  define({ id: 'mindnotes.nodes.list', domain: 'mindnotes', command: ['mindnotes', 'nodes', 'list'], flags: { 'mindnote-id': id({ required: true }), 'parent-node-id': id(), ...page } }),
  define({ id: 'mindnotes.node.create', domain: 'mindnotes', identity: 'bot', risk: 'write', command: ['mindnotes', 'nodes', 'create'], flags: { 'mindnote-id': id({ required: true }), data: json({ required: true }) }, reread: { id: 'mindnotes.nodes.list', map: { 'mindnote-id': 'mindnote-id' } } }),
  define({ id: 'mindnotes.node.update', domain: 'mindnotes', identity: 'bot', risk: 'high-impact-write', command: ['mindnotes', 'nodes', 'create'], flags: { 'mindnote-id': id({ required: true }), data: json({ required: true }) }, preflight: { id: 'mindnotes.nodes.list', map: { 'mindnote-id': 'mindnote-id' } }, reread: { id: 'mindnotes.nodes.list', map: { 'mindnote-id': 'mindnote-id' } } }),
  define({ id: 'markdown.fetch', domain: 'markdown', command: ['markdown', '+fetch'], flags: { 'file-token': id({ required: true }), output: localPath({ output: true }) } }),
  define({ id: 'markdown.diff', domain: 'markdown', command: ['markdown', '+diff'], flags: { 'file-token': id({ required: true }), 'base-version': id(), 'target-version': id(), file: localPath() } }),
  define({ id: 'markdown.create', domain: 'markdown', risk: 'write', command: ['markdown', '+create'], flags: { name: string({ required: true, max: 200 }), content: string({ required: true, max: 1000000, private: true, stdin: true }), 'folder-token': id(), 'wiki-token': id() }, scope: { bounded: true, mutuallyExclusive: ['folder-token', 'wiki-token'] } }),
  define({ id: 'markdown.overwrite', domain: 'markdown', risk: 'high-impact-write', command: ['markdown', '+overwrite'], flags: { 'file-token': id({ required: true }), content: string({ required: true, max: 1000000, private: true, stdin: true }), name: string({ max: 200 }) }, preflight: { id: 'markdown.fetch', map: { 'file-token': 'file-token' } }, reread: { id: 'markdown.fetch', map: { 'file-token': 'file-token' } } }),
  define({ id: 'markdown.patch', domain: 'markdown', risk: 'high-impact-write', command: ['markdown', '+patch'], flags: { 'file-token': id({ required: true }), pattern: string({ required: true, max: 100000, private: true }), content: string({ required: true, max: 1000000, private: true, stdin: true }), regex: bool() }, preflight: { id: 'markdown.fetch', map: { 'file-token': 'file-token' } }, reread: { id: 'markdown.fetch', map: { 'file-token': 'file-token' } } }),

  // Wiki and comments. Member mutation, move and delete commands are intentionally absent.
  define({ id: 'wiki.space.list', domain: 'wiki', command: ['wiki', '+space-list'], flags: { ...page } }),
  define({ id: 'wiki.node.get', domain: 'wiki', command: ['wiki', '+node-get'], flags: { 'node-token': id(), 'obj-token': id(), url: string({ max: 4000 }) }, scope: { bounded: true, requireExactlyOne: ['node-token', 'obj-token', 'url'] } }),
  define({ id: 'wiki.node.list', domain: 'wiki', command: ['wiki', '+node-list'], flags: { 'space-id': id({ required: true }), 'parent-node-token': id(), ...page } }),
  define({ id: 'wiki.member.list', domain: 'wiki', command: ['wiki', '+member-list'], flags: { 'space-id': id({ required: true }), ...page } }),
  define({ id: 'wiki.space.create', domain: 'wiki', identity: 'bot', risk: 'write', command: ['wiki', '+space-create'], flags: { name: string({ required: true, max: 200 }), description: string({ max: 1000 }) } }),
  define({ id: 'wiki.node.create', domain: 'wiki', identity: 'bot', risk: 'write', command: ['wiki', '+node-create'], flags: { 'space-id': id(), 'parent-node-token': id(), title: string({ required: true, max: 200 }), 'node-type': field('enum', { values: ['origin', 'shortcut'] }), 'obj-type': field('enum', { values: ['sheet', 'mindnote', 'bitable', 'file', 'docx', 'slides'] }), 'origin-node-token': id() }, reread: { id: 'wiki.node.list', map: { 'space-id': 'space-id', 'parent-node-token': 'parent-node-token' }, defaults: { 'space-id': 'my_library' } } }),
  define({ id: 'wiki.node.copy', domain: 'wiki', identity: 'bot', risk: 'write', command: ['wiki', '+node-copy'], flags: { 'node-token': id({ required: true }), 'target-space-id': id({ required: true }), 'target-parent-token': id(), title: string({ max: 200 }) }, reread: { id: 'wiki.node.list', map: { 'space-id': 'target-space-id', 'parent-node-token': 'target-parent-token' } } }),
  define({ id: 'comment.batch-get', domain: 'drive', command: ['drive', '+batch-query-comments'], flags: { url: string({ required: true, max: 4000 }), 'comment-ids': csv({ required: true, maxItems: 50 }) } }),
  define({ id: 'comment.replies.list', domain: 'drive', command: ['drive', '+list-replies'], flags: { url: string({ required: true, max: 4000 }), 'comment-id': id({ required: true }), ...page } }),
  define({ id: 'comment.reply.add', domain: 'drive', risk: 'write', command: ['drive', '+add-reply'], flags: { url: string({ required: true, max: 4000 }), 'comment-id': id({ required: true }), content: json({ required: true, stdin: true }) }, reread: { id: 'comment.replies.list', map: { url: 'url', 'comment-id': 'comment-id' } } }),
  define({ id: 'comment.reply.update', domain: 'drive', risk: 'high-impact-write', command: ['drive', '+update-reply'], flags: { url: string({ required: true, max: 4000 }), 'comment-id': id({ required: true }), 'reply-id': id({ required: true }), content: json({ required: true, stdin: true }) }, preflight: { id: 'comment.replies.list', map: { url: 'url', 'comment-id': 'comment-id' } }, reread: { id: 'comment.replies.list', map: { url: 'url', 'comment-id': 'comment-id' } } }),
  define({ id: 'comment.resolve', domain: 'drive', risk: 'write', command: ['drive', '+resolve-comment'], flags: { url: string({ required: true, max: 4000 }), 'comment-id': id({ required: true }) } }),
  define({ id: 'comment.restore', domain: 'drive', risk: 'write', command: ['drive', '+restore-comment'], flags: { url: string({ required: true, max: 4000 }), 'comment-id': id({ required: true }) } }),
  define({ id: 'comment.reply.reaction.add', domain: 'drive', risk: 'write', command: ['drive', '+react-reply'], fixedArgs: ['--action', 'add'], flags: { url: string({ required: true, max: 4000 }), 'reply-id': id({ required: true }), emoji: string({ required: true, max: 100 }) } }),

  // Calendar and tasks.
  define({ id: 'calendar.list', domain: 'calendar', command: ['calendar', 'calendars', 'list'], flags: { ...page } }),
  define({ id: 'calendar.search', domain: 'calendar', command: ['calendar', 'calendars', 'search'], flags: { query: string({ required: true, max: 200 }), ...page } }),
  define({ id: 'calendar.event.join', domain: 'calendar', risk: 'write', command: ['calendar', '+join-event'], flags: { 'share-token': id({ required: true }) } }),
  define({ id: 'calendar.event.meeting', domain: 'calendar', command: ['calendar', '+meeting'], flags: { 'calendar-id': id({ required: true }), 'event-id': id({ required: true }) } }),
  define({ id: 'calendar.room.find', domain: 'calendar', command: ['calendar', '+room-find'], flags: { slots: json({ required: true }), 'building-ids': csv({ maxItems: 20 }), capacity: integer({ max: 10000 }), ...page } }),
  define({ id: 'calendar.time.suggestion', domain: 'calendar', command: ['calendar', '+suggestion'], flags: { ranges: json({ required: true }), 'user-ids': csv({ required: true, maxItems: 50 }), duration: integer({ min: 5, max: 1440 }) } }),
  define({ id: 'calendar.create', domain: 'calendar', risk: 'write', command: ['calendar', 'calendars', 'create'], flags: { data: json({ required: true }) } }),
  define({ id: 'calendar.update', domain: 'calendar', risk: 'write', command: ['calendar', 'calendars', 'patch'], flags: { 'calendar-id': id({ required: true }), data: json({ required: true }) }, reread: { id: 'calendar.list', map: {} } }),
  define({ id: 'task.comment.add', domain: 'task', risk: 'write', command: ['task', '+comment'], flags: { 'task-id': id({ required: true }), content: string({ required: true, max: 100000, private: true, stdin: true }) } }),
  define({ id: 'task.followers.add', domain: 'task', risk: 'write', command: ['task', '+followers'], flags: { 'task-id': id({ required: true }), add: csv({ required: true, maxItems: 50 }) }, excludeFlags: ['remove'] }),
  define({ id: 'task.ancestor.set', domain: 'task', risk: 'write', command: ['task', '+set-ancestor'], flags: { 'task-id': id({ required: true }), 'ancestor-id': id({ required: true }) } }),
  define({ id: 'task.subtasks.list', domain: 'task', command: ['task', 'subtasks', 'list'], flags: { 'task-guid': id({ required: true }), ...page } }),
  define({ id: 'task.subtask.create', domain: 'task', risk: 'write', command: ['task', 'subtasks', 'create'], flags: { 'task-guid': id({ required: true }), data: json({ required: true }) }, reread: { id: 'task.subtasks.list', map: { 'task-guid': 'task-guid' } } }),
  define({ id: 'task.tasklist.get', domain: 'task', command: ['task', 'tasklists', 'get'], flags: { 'tasklist-guid': id({ required: true }) } }),
  define({ id: 'task.tasklist.create', domain: 'task', risk: 'write', command: ['task', '+tasklist-create'], flags: { name: string({ required: true, max: 200 }), data: json(), member: csv({ maxItems: 50 }) } }),
  define({ id: 'task.tasklist.add-tasks', domain: 'task', risk: 'write', command: ['task', '+tasklist-task-add'], flags: { 'tasklist-id': id({ required: true }), 'task-ids': csv({ required: true, maxItems: 100 }) } }),
  define({ id: 'task.attachment.upload', domain: 'task', risk: 'write', command: ['task', '+upload-attachment'], flags: { 'task-id': id({ required: true }), file: localPath({ required: true }) } }),
  define({ id: 'task.sections.list', domain: 'task', command: ['task', 'sections', 'list'], flags: { 'tasklist-guid': id({ required: true }), ...page } }),
  define({ id: 'task.section.create', domain: 'task', risk: 'write', command: ['task', 'sections', 'create'], flags: { data: json({ required: true }) } }),
  define({ id: 'task.section.update', domain: 'task', risk: 'write', command: ['task', 'sections', 'patch'], flags: { 'section-guid': id({ required: true }), data: json({ required: true }) } }),
  define({ id: 'task.custom-fields.list', domain: 'task', command: ['task', 'custom_fields', 'list'], flags: { ...page } }),
  define({ id: 'task.custom-field.get', domain: 'task', command: ['task', 'custom_fields', 'get'], flags: { 'custom-field-guid': id({ required: true }) } }),
  define({ id: 'task.custom-field.create', domain: 'task', risk: 'write', command: ['task', 'custom_fields', 'create'], flags: { data: json({ required: true }) } }),
  define({ id: 'task.custom-field.update', domain: 'task', risk: 'high-impact-write', command: ['task', 'custom_fields', 'patch'], flags: { 'custom-field-guid': id({ required: true }), data: json({ required: true }) }, preflight: { id: 'task.custom-field.get', map: { 'custom-field-guid': 'custom-field-guid' } }, reread: { id: 'task.custom-field.get', map: { 'custom-field-guid': 'custom-field-guid' } } }),

  // Sheets safe subset. Clear/delete commands and generic batch-update are not registered.
  define({ id: 'sheets.workbook.info', domain: 'sheets', command: ['sheets', '+workbook-info'], flags: { ...spreadsheet } }),
  define({ id: 'sheets.history.list', domain: 'sheets', command: ['sheets', '+history-list'], flags: { ...spreadsheet, ...page } }),
  define({ id: 'sheets.workbook.create', domain: 'sheets', risk: 'write', command: ['sheets', '+workbook-create'], flags: { title: string({ required: true, max: 200 }), values: json(), sheets: json() }, scope: { bounded: true, mutuallyExclusive: ['values', 'sheets'] } }),
  define({ id: 'sheets.workbook.import', domain: 'sheets', risk: 'remote-operation', command: ['sheets', '+workbook-import'], flags: { file: localPath({ required: true }), 'folder-token': id() } }),
  define({ id: 'sheets.workbook.export', domain: 'sheets', risk: 'remote-operation', command: ['sheets', '+workbook-export'], flags: { ...spreadsheet, type: field('enum', { values: ['xlsx', 'csv'] }), output: localPath({ output: true }) } }),
  define({ id: 'sheets.sheet.copy', domain: 'sheets', risk: 'write', command: ['sheets', '+sheet-copy'], flags: { ...spreadsheet, name: string({ max: 200 }), index: integer({ min: 0, max: 1000 }) }, reread: { id: 'sheets.workbook.info', map: { url: 'url', 'spreadsheet-token': 'spreadsheet-token' } } }),
  define({ id: 'sheets.sheet.rename', domain: 'sheets', risk: 'write', command: ['sheets', '+sheet-rename'], flags: { ...spreadsheet, name: string({ required: true, max: 200 }) }, reread: { id: 'sheets.workbook.info', map: { url: 'url', 'spreadsheet-token': 'spreadsheet-token' } } }),
  define({ id: 'sheets.sheet.move', domain: 'sheets', risk: 'write', command: ['sheets', '+sheet-move'], flags: { ...spreadsheet, index: integer({ min: 0, max: 1000, required: true }) }, reread: { id: 'sheets.workbook.info', map: { url: 'url', 'spreadsheet-token': 'spreadsheet-token' } } }),
  define({ id: 'sheets.sheet.hide', domain: 'sheets', risk: 'write', command: ['sheets', '+sheet-hide'], flags: { ...spreadsheet }, reread: { id: 'sheets.workbook.info', map: { url: 'url', 'spreadsheet-token': 'spreadsheet-token' } } }),
  define({ id: 'sheets.sheet.unhide', domain: 'sheets', risk: 'write', command: ['sheets', '+sheet-unhide'], flags: { ...spreadsheet }, reread: { id: 'sheets.workbook.info', map: { url: 'url', 'spreadsheet-token': 'spreadsheet-token' } } }),
  ...['insert', 'move', 'hide', 'unhide', 'freeze', 'group', 'ungroup'].map((action) => define({ id: `sheets.dimension.${action}`, domain: 'sheets', risk: 'write', command: ['sheets', `+dim-${action}`], flags: { ...spreadsheet, range: string({ max: 100 }), dimension: field('enum', { values: ['ROWS', 'COLUMNS', 'rows', 'columns'] }), start: integer({ min: 0, max: 100000 }), end: integer({ min: 0, max: 100000 }), destination: integer({ min: 0, max: 100000 }), count: integer({ max: 10000 }), rows: integer({ min: 0, max: 10000 }), cols: integer({ min: 0, max: 1000 }) }, reread: { id: 'sheets.workbook.info', map: { url: 'url', 'spreadsheet-token': 'spreadsheet-token' } } })),
  define({ id: 'sheets.rows.resize', domain: 'sheets', risk: 'write', command: ['sheets', '+rows-resize'], flags: { ...spreadsheet, range: string({ required: true, max: 100 }), height: integer({ max: 1000 }), heights: json(), type: field('enum', { values: ['standard', 'auto'] }) }, reread: { id: 'sheets.workbook.info', map: { url: 'url', 'spreadsheet-token': 'spreadsheet-token' } } }),
  define({ id: 'sheets.cols.resize', domain: 'sheets', risk: 'write', command: ['sheets', '+cols-resize'], flags: { ...spreadsheet, range: string({ required: true, max: 100 }), width: integer({ max: 1000 }), widths: json(), type: field('enum', { values: ['standard'] }) }, reread: { id: 'sheets.workbook.info', map: { url: 'url', 'spreadsheet-token': 'spreadsheet-token' } } }),
  ...[
    ['cells.style', '+cells-set-style'], ['cells.image', '+cells-set-image'], ['cells.merge', '+cells-merge'],
    ['cells.unmerge', '+cells-unmerge'], ['cells.replace', '+cells-replace'], ['range.copy', '+range-copy'],
    ['range.fill', '+range-fill'], ['range.move', '+range-move'], ['range.sort', '+range-sort'],
    ['chart.create', '+chart-create'], ['chart.update', '+chart-update'], ['chart.config.update', '+chart-config-update'],
    ['chart.data.update', '+chart-data-update'], ['pivot.create', '+pivot-create'], ['pivot.update', '+pivot-update'],
    ['conditional-format.create', '+cond-format-create'], ['conditional-format.update', '+cond-format-update'],
    ['filter.create', '+filter-create'], ['filter.update', '+filter-update'], ['filter-view.create', '+filter-view-create'],
    ['filter-view.update', '+filter-view-update'], ['dropdown.set', '+dropdown-set'], ['dropdown.update', '+dropdown-update'],
    ['sparkline.create', '+sparkline-create'], ['sparkline.update', '+sparkline-update'],
    ['floating-image.create', '+float-image-create'], ['floating-image.update', '+float-image-update'],
  ].map(([name, shortcut]) => define({
    id: `sheets.${name}`,
    domain: 'sheets',
    risk: name.includes('update') || name.includes('replace') || name === 'range.move' ? 'high-impact-write' : 'write',
    command: ['sheets', shortcut],
    flags: { ...spreadsheet, range: string({ max: 200 }), 'data-range': string({ max: 200 }), find: string({ max: 10000, private: true }), replacement: string({ max: 10000, private: true }), properties: json(), rules: json(), options: json(), image: localPath(), 'image-token': id(), 'chart-id': id(), 'pivot-table-id': id(), 'rule-id': id(), 'view-id': id(), 'group-id': id(), 'float-image-id': id(), 'source-range': string({ max: 200 }), 'destination-range': string({ max: 200 }), sorts: json() },
    preflight: (name.includes('update') || name.includes('replace') || name === 'range.move') ? { id: 'sheets.workbook.info', map: { url: 'url', 'spreadsheet-token': 'spreadsheet-token' } } : null,
    reread: { id: 'sheets.workbook.info', map: { url: 'url', 'spreadsheet-token': 'spreadsheet-token' } },
  })),
  ...[
    ['chart.list', '+chart-list'], ['pivot.list', '+pivot-list'], ['conditional-format.list', '+cond-format-list'],
    ['conditional-format.results', '+cond-format-result-get'], ['filter.list', '+filter-list'],
    ['filter-view.list', '+filter-view-list'], ['dropdown.get', '+dropdown-get'], ['sparkline.list', '+sparkline-list'],
    ['floating-image.list', '+float-image-list'], ['sheet.info', '+sheet-info'],
  ].map(([name, shortcut]) => define({ id: `sheets.${name}`, domain: 'sheets', command: ['sheets', shortcut], flags: { ...spreadsheet, range: string({ max: 200 }), 'chart-id': id(), 'pivot-table-id': id(), 'rule-id': id(), 'view-id': id(), 'group-id': id(), 'float-image-id': id() } })),

  // Base safe subset. Workflow, button binding, sharing, permissions and deletion are intentionally absent.
  define({ id: 'base.data.query', domain: 'base', command: ['base', '+data-query'], flags: { ...base, query: json({ required: true }) } }),
  define({ id: 'base.record.history', domain: 'base', command: ['base', '+record-history-list'], flags: { ...base, 'record-id': id({ required: true }), ...page } }),
  define({ id: 'base.record.attachment.download', domain: 'base', command: ['base', '+record-download-attachment'], flags: { ...base, 'record-id': id({ required: true }), 'field-id': id(), 'file-token': id(), output: localPath({ output: true }) } }),
  define({ id: 'base.templates.categories', domain: 'base', command: ['base', '+template-categories'], flags: { ...page } }),
  define({ id: 'base.templates.list', domain: 'base', command: ['base', '+template-list'], flags: { 'category-id': id({ required: true }), ...page } }),
  define({ id: 'base.templates.search', domain: 'base', command: ['base', '+template-search'], flags: { query: string({ required: true, max: 200 }), ...page } }),
  define({ id: 'base.workspace.entities', domain: 'base', command: ['base', '+workspace-entity-list'], flags: { 'workspace-id': id({ required: true }), ...page } }),
  ...[
    ['base.get', '+base-get'], ['app.get', '+app-get'], ['table.get', '+table-get'], ['table.list', '+table-list'],
    ['field.get', '+field-get'], ['field.list', '+field-list'], ['view.get', '+view-get'], ['view.list', '+view-list'],
    ['form.get', '+form-get'], ['form.list', '+form-list'], ['dashboard.get', '+dashboard-get'], ['dashboard.list', '+dashboard-list'],
    ['app-page.get', '+app-page-get'], ['app-page.list', '+app-page-list'], ['app-block.get', '+app-block-get'],
    ['app-block.list', '+app-block-list'], ['app-block.data', '+app-block-get-data'], ['base-block.list', '+base-block-list'],
  ].map(([name, shortcut]) => define({ id: `base.${name}`, domain: 'base', command: ['base', shortcut], flags: { ...base, 'app-token': id(), 'workspace-id': id(), 'page-id': id(), 'block-id': id(), 'view-id': id(), 'field-id': id(), 'form-id': id(), 'dashboard-id': id(), ...page } })),
  ...[
    ['base.create', '+base-create'], ['app.create', '+app-create'], ['table.create', '+table-create'],
    ['view.create', '+view-create'], ['form.create', '+form-create'], ['dashboard.create', '+dashboard-create'],
    ['app-page.create', '+app-page-create'], ['app-block.create', '+app-block-create'], ['base-block.create', '+base-block-create'],
    ['workspace.create', '+workspace-create'], ['record.attachment.upload', '+record-upload-attachment'],
  ].map(([name, shortcut]) => define({ id: `base.${name}`, domain: 'base', risk: 'write', command: ['base', shortcut], flags: { ...base, 'workspace-id': id(), 'app-token': id(), 'page-id': id(), 'record-id': id(), 'field-id': id(), name: string({ max: 200 }), title: string({ max: 200 }), description: string({ max: 2000 }), fields: json(), views: json(), data: json(), file: localPath(), files: json() }, reread: name.includes('table') || name.includes('field') || name.includes('view') ? { id: 'base.table.list', map: { url: 'url', 'base-token': 'base-token' } } : null })),
  ...[
    ['table.update', '+table-update', 'base.table.get'], ['field.update', '+field-update', 'base.field.get'],
    ['view.rename', '+view-rename', 'base.view.get'], ['form.update', '+form-update', 'base.form.get'],
    ['dashboard.update', '+dashboard-update', 'base.dashboard.get'], ['app-page.update', '+app-page-update', 'base.app-page.get'],
    ['app-block.update', '+app-block-update', 'base.app-block.get'],
  ].map(([name, shortcut, readId]) => define({ id: `base.${name}`, domain: 'base', risk: 'high-impact-write', command: ['base', shortcut], flags: { ...base, 'app-token': id(), 'page-id': id(), 'block-id': id(), 'field-id': id(), 'view-id': id(), 'form-id': id(), 'dashboard-id': id(), name: string({ max: 200 }), title: string({ max: 200 }), data: json({ required: true }) }, preflight: { id: readId, map: { url: 'url', 'base-token': 'base-token', 'table-id': 'table-id', 'table-name': 'table-name', 'field-id': 'field-id', 'view-id': 'view-id', 'form-id': 'form-id', 'dashboard-id': 'dashboard-id', 'app-token': 'app-token', 'page-id': 'page-id', 'block-id': 'block-id' } }, reread: { id: readId, map: { url: 'url', 'base-token': 'base-token', 'table-id': 'table-id', 'table-name': 'table-name', 'field-id': 'field-id', 'view-id': 'view-id', 'form-id': 'form-id', 'dashboard-id': 'dashboard-id', 'app-token': 'app-token', 'page-id': 'page-id', 'block-id': 'block-id' } } })),

  // Meeting and Minutes additions remain read-only here; existing reviewed Minutes writes stay in the legacy registry.
  define({ id: 'meeting.events', domain: 'vc', command: ['vc', '+meeting-events'], flags: { 'meeting-id': csv({ required: true, maxItems: 10 }), ...page } }),
  define({ id: 'minutes.detail', domain: 'minutes', command: ['minutes', '+detail'], flags: { 'minute-tokens': csv({ required: true, maxItems: 10 }), summary: bool(), todo: bool(), chapter: bool(), keyword: bool(), transcript: bool(), 'output-dir': localPath({ output: true }) } }),

  // Apps development mainline. Secret, access, role, database, automation, cache, plugin and delete commands are absent.
  define({ id: 'apps.app.list', domain: 'apps', command: ['apps', '+list'], flags: { ...page } }),
  define({ id: 'apps.app.get', domain: 'apps', command: ['apps', '+get'], flags: { 'app-id': id({ required: true }) } }),
  define({ id: 'apps.session.list', domain: 'apps', command: ['apps', '+session-list'], flags: { 'app-id': id({ required: true }), ...page } }),
  define({ id: 'apps.session.get', domain: 'apps', command: ['apps', '+session-get'], flags: { 'app-id': id({ required: true }), 'session-id': id({ required: true }) } }),
  define({ id: 'apps.session.messages', domain: 'apps', command: ['apps', '+session-messages-list'], flags: { 'app-id': id({ required: true }), 'session-id': id({ required: true }), 'turn-id': id({ required: true }), ...page } }),
  define({ id: 'apps.release.list', domain: 'apps', command: ['apps', '+release-list'], flags: { 'app-id': id({ required: true }), ...page } }),
  define({ id: 'apps.release.get', domain: 'apps', command: ['apps', '+release-get'], flags: { 'app-id': id({ required: true }), 'release-id': id({ required: true }) } }),
  ...[['log.list', '+log-list'], ['log.get', '+log-get'], ['metric.list', '+metric-list'], ['trace.list', '+trace-list'], ['trace.get', '+trace-get'], ['analytics.list', '+analytics-list']].map(([name, shortcut]) => define({ id: `apps.${name}`, domain: 'apps', command: ['apps', shortcut], flags: { 'app-id': id({ required: true }), 'log-id': id(), 'trace-id': id(), ...timeWindow, ...page, query: string({ max: 200 }), metric: string({ max: 100 }), granularity: string({ max: 100 }), series: string({ max: 100 }) } })),
  define({ id: 'apps.app.create', domain: 'apps', risk: 'write', transport: 'raw', command: ['api', 'POST'], apiPath: '/open-apis/spark/v1/apps', flags: { name: string({ required: true, max: 200, body: 'name' }), 'app-type': field('enum', { required: true, values: ['html', 'frontend', 'full_stack'], body: 'app_type' }), description: string({ max: 10000, body: 'description', private: true }), 'icon-url': string({ max: 4000, body: 'icon_url' }) } }),
  define({ id: 'apps.app.update', domain: 'apps', risk: 'write', transport: 'raw', command: ['api', 'PATCH'], apiPath: '/open-apis/spark/v1/apps/{app-id}', flags: { 'app-id': id({ required: true, path: true }), name: string({ max: 200, body: 'name' }), description: string({ max: 10000, body: 'description', private: true }) }, reread: { id: 'apps.app.get', map: { 'app-id': 'app-id' } } }),
  define({ id: 'apps.session.create', domain: 'apps', risk: 'write', transport: 'raw', command: ['api', 'POST'], apiPath: '/open-apis/aily/v1/sessions', flags: { 'app-id': id({ required: true, body: 'app_id' }) } }),
  define({ id: 'apps.session.chat', domain: 'apps', risk: 'remote-operation', transport: 'raw', command: ['api', 'POST'], apiPath: '/open-apis/aily/v1/sessions/{session-id}/runs', flags: { 'app-id': id({ required: true, body: 'app_id' }), 'session-id': id({ required: true, path: true }), message: string({ required: true, max: 200000, body: 'message', private: true }) }, poll: { id: 'apps.session.get', map: { 'app-id': 'app-id', 'session-id': 'session-id' }, statuses: ['status', 'data.status', 'data.session.status'] } }),
  define({ id: 'apps.session.stop', domain: 'apps', risk: 'write', transport: 'raw', command: ['api', 'POST'], apiPath: '/open-apis/aily/v1/sessions/{session-id}/runs/{run-id}/cancel', flags: { 'session-id': id({ required: true, path: true }), 'run-id': id({ required: true, path: true }) } }),
  define({ id: 'apps.release.create', domain: 'apps', risk: 'remote-operation', transport: 'raw', command: ['api', 'POST'], apiPath: '/open-apis/spark/v1/apps/{app-id}/releases', flags: { 'app-id': id({ required: true, path: true }), branch: string({ max: 200, body: 'branch' }) }, poll: { id: 'apps.release.get', map: { 'app-id': 'app-id', 'release-id': '$result.release_id' }, statuses: ['status', 'data.status', 'data.release.status'] } }),
];

const CAPABILITIES = new Map(definitions.map((item) => [item.id, item]));
if (CAPABILITIES.size !== definitions.length) throw new Error('duplicate capability id');

const FORBIDDEN_TOKENS = new Set([
  'delete', 'remove', 'clear', 'permission', 'role', 'member', 'move-to-drive', 'wiki-move',
  'workflow', 'automation', 'openapi-key', 'database', 'cache', 'plugin', 'approval',
  'urgent-phone', 'urgent-sms', 'meeting-join', 'meeting-end', 'meeting-leave', 'minutes-download',
]);

function capability(idValue) {
  return CAPABILITIES.get(String(idValue || ''));
}

function isObject(value) {
  return Boolean(value) && typeof value === 'object' && !Array.isArray(value);
}

function forbiddenPayloadKey(value) {
  if (Array.isArray(value)) {
    for (const item of value) {
      const found = forbiddenPayloadKey(item);
      if (found) return found;
    }
    return '';
  }
  if (!isObject(value)) return '';
  for (const [key, child] of Object.entries(value)) {
    const normalized = key.toLowerCase().replace(/_/g, '-');
    if (FORBIDDEN_TOKENS.has(normalized) || normalized.endsWith('-delete') || normalized.startsWith('delete-')) {
      return key;
    }
    if (normalized === 'operation' && ['delete', 'remove', 'clear'].includes(String(child).toLowerCase())) return key;
    const found = forbiddenPayloadKey(child);
    if (found) return found;
  }
  return '';
}

function docWhiteboardXml(input) {
  const format = String(input?.['doc-format'] || '');
  const content = String(input?.content || '').trim();
  if (!['mermaid', 'plantuml', 'svg'].includes(format)) throw new Error('invalid_doc_whiteboard_format');
  if (!content || /<\/whiteboard\s*>/i.test(content)) throw new Error('invalid_doc_whiteboard_content');
  if (format === 'svg') {
    if (!/^<svg(?:\s|>)[\s\S]*<\/svg>$/i.test(content)) throw new Error('invalid_doc_whiteboard_svg');
    if (/<(?:script|foreignObject|iframe|object|embed)\b/i.test(content)
      || /\bon[a-z]+\s*=/i.test(content)
      || /(?:href|src)\s*=\s*["'](?:https?:|data:|javascript:)/i.test(content)) {
      throw new Error('unsafe_doc_whiteboard_svg');
    }
  }
  return `<whiteboard type="${format}">\n${content}\n</whiteboard>`;
}

function specialInputError(definition, input) {
  if (definition.id === 'docs.whiteboard.insert') {
    try { docWhiteboardXml(input); } catch (error) { return String(error.message || error); }
  }
  if (definition.id === 'mindnotes.node.create' || definition.id === 'mindnotes.node.update') {
    const data = input.data;
    if (!isObject(data) || typeof data.client_token !== 'string' || data.client_token.length < 10) {
      return 'mindnote_client_token_required';
    }
    if (!Array.isArray(data.nodes) || data.nodes.length < 1 || data.nodes.length > 100) return 'invalid_mindnote_nodes';
    const hasNodeId = data.nodes.map((node) => isObject(node) && typeof node.node_id === 'string' && Boolean(node.node_id));
    if (definition.id === 'mindnotes.node.create' && hasNodeId.some(Boolean)) return 'mindnote_create_must_not_include_node_id';
    if (definition.id === 'mindnotes.node.update' && hasNodeId.some((value) => !value)) return 'mindnote_update_requires_node_id';
  }
  return '';
}

function validateField(name, value, schema) {
  if (value === undefined) return schema.required ? `missing_input:${name}` : '';
  if (schema.type === 'string') {
    if (typeof value !== 'string') return `invalid_string:${name}`;
    if (schema.required && !value.trim()) return `empty_input:${name}`;
    if (schema.min && value.length < schema.min) return `input_too_short:${name}`;
    if (value.length > schema.max) return `input_too_long:${name}`;
    if (schema.pattern && !schema.pattern.test(value)) return `invalid_identifier:${name}`;
    return '';
  }
  if (schema.type === 'integer') {
    if (!Number.isInteger(value) || value < schema.min || value > schema.max) return `invalid_integer:${name}`;
    return '';
  }
  if (schema.type === 'boolean') return typeof value === 'boolean' ? '' : `invalid_boolean:${name}`;
  if (schema.type === 'enum') return schema.values.includes(value) ? '' : `invalid_enum:${name}`;
  if (schema.type === 'csv') {
    const items = Array.isArray(value) ? value : String(value).split(',').map((item) => item.trim()).filter(Boolean);
    if ((schema.required && !items.length) || items.length > schema.maxItems) return `invalid_list_size:${name}`;
    if (schema.itemPattern && items.some((item) => !schema.itemPattern.test(String(item)))) return `invalid_list_item:${name}`;
    return '';
  }
  if (schema.type === 'json') {
    if (!(isObject(value) || Array.isArray(value))) return `invalid_json_value:${name}`;
    if (Buffer.byteLength(JSON.stringify(value), 'utf8') > schema.maxBytes) return `input_too_large:${name}`;
    return '';
  }
  if (schema.type === 'path') {
    if (typeof value !== 'string' || !value || path.isAbsolute(value) || value.split(/[\\/]/).includes('..')) return `unsafe_path:${name}`;
    return '';
  }
  return `unsupported_field_type:${name}`;
}

function validateCapabilityInput(definition, input) {
  if (!definition) return 'unknown_capability';
  if (!isObject(input)) return 'missing_input';
  const unknown = Object.keys(input).find((key) => !Object.hasOwn(definition.flags, key));
  if (unknown) return `unsupported_input_field:${unknown}`;
  for (const [name, schema] of Object.entries(definition.flags)) {
    const error = validateField(name, input[name], schema);
    if (error) return error;
  }
  const { requireAny, requireExactlyOne, mutuallyExclusive, requiredGroups } = definition.scope || {};
  if (requireAny && !requireAny.some((name) => input[name] !== undefined)) return 'required_input_group_missing';
  if (requiredGroups?.some((group) => !group.some((name) => input[name] !== undefined))) return 'required_input_group_missing';
  if (requireExactlyOne && requireExactlyOne.filter((name) => input[name] !== undefined).length !== 1) return 'exactly_one_input_required';
  if (mutuallyExclusive && mutuallyExclusive.filter((name) => input[name] !== undefined && input[name] !== false).length > 1) return 'mutually_exclusive_inputs';
  const specialError = specialInputError(definition, input);
  if (specialError) return specialError;
  const forbidden = forbiddenPayloadKey(input);
  if (forbidden) return `forbidden_payload_operation:${forbidden}`;
  return '';
}

function validateCapabilityRequest(request) {
  if (!isObject(request)) return 'request_not_object';
  const allowedRequestFields = new Set([
    'id', 'type', 'domain', 'action', 'capabilityId', 'identity', 'input',
    'explicitAuthorization', 'confirmHighImpact', 'dryRun', 'remoteTimeoutMs',
    'pollIntervalMs', 'saveAs', 'source', 'reason', 'trace', 'createdAt',
  ]);
  const unknownRequestField = Object.keys(request).find((key) => !allowedRequestFields.has(key));
  if (unknownRequestField) return `unsupported_request_field:${unknownRequestField}`;
  if (request.dryRun !== undefined && typeof request.dryRun !== 'boolean') return 'invalid_dry_run';
  if (request.type !== 'feishu_capability') return 'unsupported_type';
  if (!request.id || typeof request.id !== 'string') return 'missing_id';
  if (!request.source || typeof request.source !== 'string') return 'missing_source';
  if (request.domain !== 'capability' || request.action !== 'execute') return 'unsupported_domain_or_action';
  const definition = capability(request.capabilityId);
  if (!definition) return 'unknown_capability';
  if (definition.risk === 'read') return 'read_capability_must_not_enter_actionbox';
  if (definition.queue !== 'actionbox') return `capability_requires_${definition.queue}`;
  if (request.identity !== definition.identity) return 'unsupported_identity';
  if (request.explicitAuthorization !== true) return 'explicit_authorization_required';
  if (request.saveAs !== undefined) {
    if (typeof request.saveAs !== 'string' || !/^Codex桥测试[^\r\n]{0,80}$/.test(request.saveAs)) return 'invalid_test_asset_alias';
    if (!definition.resultIdentifiers.length) return 'capability_result_cannot_be_saved';
  }
  if (request.confirmHighImpact !== undefined && typeof request.confirmHighImpact !== 'boolean') return 'invalid_high_impact_confirmation';
  if (request.reason !== undefined && (typeof request.reason !== 'string' || request.reason.length > 2000)) return 'invalid_reason';
  if (request.createdAt !== undefined && (typeof request.createdAt !== 'string' || !Number.isFinite(Date.parse(request.createdAt)))) return 'invalid_created_at';
  if (request.trace !== undefined) {
    if (!isObject(request.trace)
      || Object.keys(request.trace).some((key) => !['system', 'code'].includes(key))
      || typeof request.trace.system !== 'string'
      || typeof request.trace.code !== 'string') return 'invalid_trace';
  }
  if (definition.risk === 'remote-operation') {
    if (request.remoteTimeoutMs !== undefined
      && (!Number.isInteger(request.remoteTimeoutMs) || request.remoteTimeoutMs < 10000 || request.remoteTimeoutMs > 30 * 60 * 1000)) {
      return 'invalid_remote_timeout';
    }
    if (request.pollIntervalMs !== undefined
      && (!Number.isInteger(request.pollIntervalMs) || request.pollIntervalMs < 250 || request.pollIntervalMs > 30000)) {
      return 'invalid_remote_poll_interval';
    }
  } else if (request.remoteTimeoutMs !== undefined || request.pollIntervalMs !== undefined) {
    return 'remote_poll_not_supported';
  }
  if (definition.risk === 'high-impact-write' && request.confirmHighImpact !== true) return 'high_impact_confirmation_required';
  return validateCapabilityInput(definition, request.input);
}

function publicCapability(definition) {
  return {
    id: definition.id,
    domain: definition.domain,
    identity: definition.identity,
    risk: definition.risk,
    queue: definition.risk === 'read' ? 'direct' : definition.queue,
    inputFields: Object.entries(definition.flags).map(([name, schema]) => ({
      name,
      type: schema.type,
      required: Boolean(schema.required),
      private: Boolean(schema.private),
    })),
    bounded: definition.scope?.bounded !== false,
    preflight: definition.preflight?.id || null,
    reread: definition.reread?.id || null,
    savableResultKinds: [...new Set(definition.resultIdentifiers.map((item) => item.kind))],
    redaction: definition.redaction,
  };
}

function capabilityCatalog() {
  return definitions.map(publicCapability);
}

function findNestedKey(value, wanted) {
  if (!value || typeof value !== 'object') return undefined;
  if (Object.hasOwn(value, wanted) && value[wanted] !== undefined) return value[wanted];
  for (const child of Object.values(value)) {
    const found = findNestedKey(child, wanted);
    if (found !== undefined) return found;
  }
  return undefined;
}

function resultConditionMatches(condition, source) {
  if (!condition) return true;
  return findNestedKey(source, condition.key) === condition.equals;
}

function resultDescriptorSource(descriptor, response) {
  if (!descriptor.arrayKey) return response;
  const items = findNestedKey(response, descriptor.arrayKey);
  if (!Array.isArray(items)) return undefined;
  return items.find((item) => item && typeof item === 'object'
    && (descriptor.whereMissing || []).every((key) => item[key] === undefined || item[key] === null || item[key] === ''));
}

function selectCapabilityResultIdentifier(definition, { input = {}, response, remoteResponse } = {}) {
  for (const descriptor of definition?.resultIdentifiers || []) {
    if (!resultConditionMatches(descriptor.whenInput, input)) continue;
    const responseSource = resultDescriptorSource(descriptor, response);
    const remoteSource = resultDescriptorSource(descriptor, remoteResponse);
    if (!resultConditionMatches(descriptor.whenResult, responseSource)) continue;
    const value = findNestedKey(responseSource, descriptor.key) ?? findNestedKey(remoteSource, descriptor.key);
    if (typeof value === 'string' && value) return { kind: descriptor.kind, value };
  }
  return null;
}

module.exports = {
  CAPABILITIES,
  FORBIDDEN_TOKENS,
  capability,
  capabilityCatalog,
  docWhiteboardXml,
  forbiddenPayloadKey,
  publicCapability,
  selectCapabilityResultIdentifier,
  validateCapabilityInput,
  validateCapabilityRequest,
};
