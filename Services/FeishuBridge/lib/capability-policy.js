const { ACTION_KEYS } = require('./action-registry');
const { capabilityCatalog } = require('./capability-registry');
const { FIXED_EVENT_KEYS } = require('./event-inbox');
const { runtimeManifest } = require('./runtime-manifest');

const REQUIRED_SCOPES = Object.freeze({
  bot: Object.freeze([
    'im:message',
    'im:message:readonly',
    'im:message:send_as_bot',
    'im:message:update',
    'im:message.group_at_msg:readonly',
    'im:message.p2p_msg:readonly',
    'im:message.pins:read',
    'im:message.pins:write_only',
    'im:message.reactions:read',
    'im:message.reactions:write_only',
    'im:message.urgent',
    'im:resource',
    'im:chat:read',
    'im:chat:readonly',
    'im:chat.members:read',
    'contact:user.base:readonly',
    'contact:department.base:readonly',
  ]),
  user: Object.freeze([
    'im:chat:read',
    'im:chat:create_by_user',
    'im:chat:update',
    'im:message',
    'im:message:readonly',
    'im:message.send_as_user',
    'im:message.group_msg:get_as_user',
    'im:message.p2p_msg:get_as_user',
    'im:feed_group_v1:read',
    'im:feed.shortcut:read',
    'im:feed.shortcut:write',
    'im:feed.flag:read',
    'im:feed.flag:write',
    'contact:user:search',
    'search:bot',
    'docx:document:readonly',
    'docx:document:write_only',
    'docx:document:create',
    'docs:document.content:read',
    'docs:document.comment:read',
    'docs:document.comment:create',
    'docs:document.comment:update',
    'docs:document.comment:write_only',
    'docs:document.media:download',
    'docs:document.media:upload',
    'docs:document:export',
    'docs:document:import',
    'drive:drive.metadata:readonly',
    'drive:file:download',
    'drive:file:upload',
    'space:document:retrieve',
    'space:folder:create',
    'search:docs:read',
    'wiki:space:read',
    'wiki:node:retrieve',
    'wiki:node:read',
    'wiki:member:retrieve',
    'calendar:calendar:read',
    'calendar:calendar:create',
    'calendar:calendar:update',
    'calendar:calendar.event:read',
    'calendar:calendar.event:create',
    'calendar:calendar.event:update',
    'calendar:calendar.event:join',
    'calendar:calendar.event:reply',
    'calendar:calendar.free_busy:read',
    'calendar:room:readonly',
    'task:task:read',
    'task:task:write',
    'task:tasklist:read',
    'task:tasklist:write',
    'task:comment:write',
    'task:attachment:write',
    'task:section:read',
    'task:section:write',
    'task:custom_field:read',
    'task:custom_field:write',
    'sheets:spreadsheet:read',
    'sheets:spreadsheet:write_only',
    'sheets:spreadsheet:create',
    'sheets:spreadsheet.meta:read',
    'sheets:spreadsheet.meta:write_only',
    'base:app:read',
    'base:app:create',
    'base:app:update',
    'base:appmode:create',
    'base:appmode:read',
    'base:appmode_page:create',
    'base:appmode_page:read',
    'base:appmode_page:update',
    'base:appmode_block:create',
    'base:appmode_block:read',
    'base:appmode_block:update',
    'base:block:create',
    'base:block:read',
    'base:block:update',
    'base:dashboard:create',
    'base:dashboard:read',
    'base:dashboard:update',
    'base:table:read',
    'base:table:create',
    'base:table:update',
    'base:field:read',
    'base:field:create',
    'base:field:update',
    'base:view:read',
    'base:view:write_only',
    'base:form:create',
    'base:form:read',
    'base:form:update',
    'base:history:read',
    'base:template:read',
    'base:workspace:create',
    'base:workspace:read',
    'base:workspace:update',
    'base:record:read',
    'base:record:create',
    'base:record:update',
    'vc:meeting.search:read',
    'vc:meeting',
    'vc:meeting.meetingevent:read',
    'vc:record:readonly',
    'vc:recording:read',
    'vc:note:read',
    'minutes:minutes.search:read',
    'minutes:minutes.basic:read',
    'minutes:minutes:readonly',
    'minutes:minutes:update',
    'minutes:minutes.upload:write',
    'board:whiteboard:node:read',
    'board:whiteboard:node:create',
    'mindnote:node:read',
    'mindnote:node:create',
    'wiki:space:retrieve',
    'wiki:space:write_only',
    'wiki:node:create',
    'wiki:node:copy',
    'spark:app:read',
    'spark:app:write',
  ]),
});

const READ_CAPABILITIES = Object.freeze([
  'message.list', 'message.search', 'message.thread',
  'document.inspect', 'knowledge.search', 'knowledge.read', 'comment.list',
  'calendar.agenda', 'calendar.search', 'calendar.get', 'calendar.freebusy',
  'task.mine', 'task.related', 'task.search', 'task.get', 'task.tasklists',
  'sheets.inspect', 'sheets.cells', 'sheets.table', 'sheets.search', 'sheets.revision',
  'base.inspect', 'base.schema', 'base.records', 'base.search', 'base.get',
  'meeting.search', 'meeting.active', 'meeting.get', 'meeting.detail',
  'meeting.events', 'meeting.recording',
  'note.detail', 'note.transcript',
  'minutes.search', 'minutes.get', 'minutes.detail', 'minutes.transcript',
]);

function capabilityManifest() {
  const registered = capabilityCatalog();
  return {
    ...runtimeManifest(),
    identities: ['bot', 'user'],
    eventTransport: 'official-sdk',
    singleInboundConnection: true,
    events: [...FIXED_EVENT_KEYS],
    fixedEventCatalog: true,
    inboundMessageTypes: ['text', 'image', 'file', 'audio', 'media', 'post'],
    outboundMessageFormats: ['text', 'markdown', 'card', 'image', 'file'],
    readCapabilities: [...READ_CAPABILITIES],
    queuedWriteCapabilities: [...ACTION_KEYS].sort(),
    registeredCapabilities: registered,
    registeredCapabilityCount: registered.length,
    documentWrites: ['create_document', 'append', 'overwrite', 'str_replace'],
    intentionallyExcluded: [
      'delete',
      'permission_mutation',
      'wiki_move',
      'arbitrary_openapi',
      'automatic_approval',
      'approval_api',
      'approval_event',
      'background_full_crawl',
      'live_meeting_control',
      'minutes_media_download',
      'minutes_permission_mutation',
      'message_delete',
      'chat_member_or_admin_mutation',
      'phone_or_sms_urgent',
      'sheet_clear_or_delete',
      'base_delete_share_permission_workflow_or_button_binding',
      'apps_access_member_role_secret_database_automation_cache_plugin_or_delete',
    ],
    requiredScopes: {
      bot: [...REQUIRED_SCOPES.bot],
      user: [...REQUIRED_SCOPES.user],
    },
  };
}

function compareScopes(required, granted) {
  const grantedSet = new Set(granted || []);
  const requiredSet = new Set(required || []);
  const missing = [...requiredSet].filter((scope) => !grantedSet.has(scope)).sort();
  const excess = [...grantedSet].filter((scope) => !requiredSet.has(scope)).sort();
  return {
    requiredCount: requiredSet.size,
    grantedCount: grantedSet.size,
    missing,
    excess,
    complete: missing.length === 0,
  };
}

function permissionReport({ authStatus = {}, appScopes = {} } = {}) {
  const oauthGranted = String(authStatus.identities?.user?.scope || '')
    .split(/\s+/)
    .filter(Boolean);
  const appGranted = Array.isArray(appScopes.userScopes) ? appScopes.userScopes : [];
  const hasAppScopeReport = Array.isArray(appScopes.userScopes);
  const appGrantedSet = new Set(appGranted);
  const effectiveGranted = hasAppScopeReport
    ? oauthGranted.filter((scope) => appGrantedSet.has(scope))
    : oauthGranted;
  const user = compareScopes(REQUIRED_SCOPES.user, effectiveGranted);
  const application = hasAppScopeReport
    ? compareScopes(REQUIRED_SCOPES.user, appGranted)
    : null;
  const oauth = compareScopes(REQUIRED_SCOPES.user, oauthGranted);
  const botReady = Boolean(authStatus.identities?.bot?.available && authStatus.identities?.bot?.verified);
  return {
    verified: Boolean(authStatus.verified),
    identities: {
      bot: {
        ready: botReady,
        requiredCount: REQUIRED_SCOPES.bot.length,
        scopeVerification: 'not_exposed_by_lark_cli_auth_scopes',
        required: [...REQUIRED_SCOPES.bot],
      },
      user: {
        ready: Boolean(authStatus.identities?.user?.available && authStatus.identities?.user?.verified),
        ...user,
        application,
        oauth,
      },
    },
    note: 'User completeness requires both application permissions and user OAuth grants; extra scopes are not bridge capabilities.',
  };
}

module.exports = {
  READ_CAPABILITIES,
  REQUIRED_SCOPES,
  capabilityManifest,
  compareScopes,
  permissionReport,
};
