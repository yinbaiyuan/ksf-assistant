const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { assertPrivateMode } = require('./helpers/platform-private');

const originalHome = os.homedir;
const modulePath = require.resolve('../lib/default-conversation-store');

function isolatedStore() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'default-conversation-store-'));
  os.homedir = () => root;
  delete require.cache[modulePath];
  const api = require('../lib/default-conversation-store');
  const cwd = path.join(root, 'repo');
  fs.mkdirSync(cwd);
  return { root, cwd, api };
}

test.afterEach(() => {
  os.homedir = originalHome;
  delete require.cache[modulePath];
});

test('binds each root card to an independent Codex thread with private storage', () => {
  const { root, cwd, api } = isolatedStore();
  const first = api.upsertConversation({
    threadId: '019c1234-abcd-7890-abcd-123456789abc',
    cwd,
    title: '飞书对话一',
    chatId: 'oc_direct',
    operatorId: 'ou_owner',
    rootMessageId: 'om_first_card',
    messageIds: ['om_first_input'],
  });
  const second = api.upsertConversation({
    threadId: '019c5678-abcd-7890-abcd-123456789abc',
    cwd,
    title: '飞书对话二',
    chatId: 'oc_direct',
    operatorId: 'ou_owner',
    rootMessageId: 'om_second_card',
    messageIds: ['om_second_input'],
  });

  assert.notEqual(first.id, second.id);
  assert.notEqual(first.threadId, second.threadId);
  assert.equal(api.resolveConversation({ parent_id: 'om_first_card' }).threadId, first.threadId);
  assert.equal(api.resolveConversation({ root_id: 'om_second_card' }).threadId, second.threadId);
  assert.equal(api.operatorMatches(first, { chatId: 'oc_direct', operatorId: 'ou_owner' }), true);
  assert.equal(api.operatorMatches(first, { chatId: 'oc_other', operatorId: 'ou_owner' }), false);

  const file = api.defaultConversationPath();
  assertPrivateMode(path.dirname(file), 0o700);
  assertPrivateMode(file, 0o600);
  fs.rmSync(root, { recursive: true, force: true });
});

test('project promotion retires only its own default conversation and cannot be reversed', () => {
  const { root, cwd, api } = isolatedStore();
  let first = api.upsertConversation({
    threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd,
    chatId: 'oc_direct', operatorId: 'ou_owner', rootMessageId: 'om_first_card',
  });
  const second = api.upsertConversation({
    threadId: '019c5678-abcd-7890-abcd-123456789abc', cwd,
    chatId: 'oc_direct', operatorId: 'ou_owner', rootMessageId: 'om_second_card',
  });

  first = api.updateConversation(first.id, { state: 'promoted' });
  assert.equal(api.resolveConversation({ parent_id: 'om_first_card' }), null);
  assert.equal(api.resolveConversation({ parent_id: 'om_second_card' }).id, second.id);
  first = api.updateConversation(first.id, { state: 'active' });
  assert.equal(first.state, 'promoted');
  fs.rmSync(root, { recursive: true, force: true });
});

test('rejects incomplete bindings and unsafe store permissions', () => {
  const { root, cwd, api } = isolatedStore();
  assert.throws(() => api.upsertConversation({
    threadId: '019c1234-abcd-7890-abcd-123456789abc',
    cwd: '',
    chatId: 'oc_direct',
    operatorId: 'ou_owner',
    rootMessageId: 'om_card',
  }), /working directory/);

  api.upsertConversation({
    threadId: '019c1234-abcd-7890-abcd-123456789abc',
    cwd,
    chatId: 'oc_direct',
    operatorId: 'ou_owner',
    rootMessageId: 'om_card',
  });
  const file = api.defaultConversationPath();
  if (process.platform !== 'win32') {
    fs.chmodSync(file, 0o644);
    assert.throws(() => api.readStore(), /permissions/);
  }
  fs.rmSync(root, { recursive: true, force: true });
});

test('bridge routes default roots and card replies without a shared execution session', () => {
  const source = fs.readFileSync(path.join(__dirname, '..', 'bot-bridge.js'), 'utf8');
  const answerStart = source.indexOf('async function answerWithDefaultCodexUnlocked');
  const answerEnd = source.indexOf('function taskLinkTargetAlias', answerStart);
  const answerBody = source.slice(answerStart, answerEnd);

  assert.ok(answerStart > 0 && answerEnd > answerStart);
  assert.doesNotMatch(answerBody, /readSessions\(|sessions\[defaultSessionName\]/);
  assert.match(answerBody, /sessionId: conversation\?\.threadId/);
  assert.match(source, /const defaultConversationChains = new Map\(\)/);
  assert.match(source, /resolveDefaultConversation\(message\)/);
  assert.match(source, /resolveDefaultConversation\(\{ message_id: event\.messageId \}\)/);
  assert.match(source, /state: 'promoted'/);
});
