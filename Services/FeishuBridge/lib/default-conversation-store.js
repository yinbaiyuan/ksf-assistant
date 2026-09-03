const crypto = require('node:crypto');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { chmodPrivate, defaultDataRoot } = require('./platform-runtime');

const PROTOCOL = 'codex-feishu-default-conversation-v1';
const SCHEMA_VERSION = 1;
const STATES = new Set(['active', 'promoted', 'closed']);

function defaultConversationPath(homeDir = os.homedir()) {
  return path.join(defaultDataRoot({ homeDir }), 'default-conversations-v1.json');
}

function assertSafePath(filePath) {
  const expectedRoot = defaultDataRoot({ homeDir: os.homedir() });
  const expectedPath = path.join(expectedRoot, 'default-conversations-v1.json');
  const absolute = path.resolve(filePath);
  if (absolute !== expectedPath) {
    throw new Error('default conversation store must use the configured Feishu Bridge private path');
  }
  for (const candidate of [expectedRoot, absolute]) {
    if (!fs.existsSync(candidate)) continue;
    const stat = fs.lstatSync(candidate);
    if (stat.isSymbolicLink()) throw new Error(`unsafe symbolic link: ${candidate}`);
  }
  return absolute;
}

function emptyStore() {
  return {
    protocol: PROTOCOL,
    schemaVersion: SCHEMA_VERSION,
    updatedAt: new Date(0).toISOString(),
    conversations: [],
  };
}

function normalizedMessages(values) {
  return [...new Set((Array.isArray(values) ? values : [])
    .map((value) => String(value || '').trim())
    .filter(Boolean))];
}

function normalizeConversation(value) {
  return {
    ...value,
    id: String(value.id || ''),
    threadId: String(value.threadId || ''),
    cwd: String(value.cwd || ''),
    title: String(value.title || '').slice(0, 160),
    chatId: String(value.chatId || ''),
    operatorId: String(value.operatorId || ''),
    rootMessageId: String(value.rootMessageId || ''),
    messageIds: normalizedMessages(value.messageIds),
    state: STATES.has(value.state) ? value.state : 'active',
    lastTurnId: String(value.lastTurnId || ''),
    createdAt: String(value.createdAt || ''),
    updatedAt: String(value.updatedAt || ''),
  };
}

function normalizeStore(value) {
  if (value?.protocol !== PROTOCOL || !Array.isArray(value.conversations)) {
    throw new Error('unsupported or damaged default conversation store');
  }
  return {
    ...value,
    protocol: PROTOCOL,
    schemaVersion: SCHEMA_VERSION,
    conversations: value.conversations.map(normalizeConversation),
  };
}

function readStore(filePath = defaultConversationPath()) {
  const safePath = assertSafePath(filePath);
  if (!fs.existsSync(safePath)) return emptyStore();
  const stat = fs.lstatSync(safePath);
  if (!stat.isFile() || stat.isSymbolicLink()
    || (process.platform !== 'win32' && (stat.mode & 0o077) !== 0)) {
    throw new Error('unsafe default conversation store permissions');
  }
  return normalizeStore(JSON.parse(fs.readFileSync(safePath, 'utf8')));
}

function writeStore(store, filePath = defaultConversationPath()) {
  const safePath = assertSafePath(filePath);
  const directory = path.dirname(safePath);
  fs.mkdirSync(directory, { recursive: true, mode: 0o700 });
  chmodPrivate(directory, 0o700);
  const temporary = path.join(
    directory,
    `.default-conversations-${process.pid}-${crypto.randomBytes(5).toString('hex')}.tmp`,
  );
  const next = {
    ...normalizeStore(store),
    protocol: PROTOCOL,
    schemaVersion: SCHEMA_VERSION,
    updatedAt: new Date().toISOString(),
  };
  const fd = fs.openSync(temporary, 'wx', 0o600);
  try {
    fs.writeFileSync(fd, `${JSON.stringify(next, null, 2)}\n`, 'utf8');
    fs.fsyncSync(fd);
  } finally {
    fs.closeSync(fd);
  }
  fs.renameSync(temporary, safePath);
  chmodPrivate(safePath, 0o600);
  return next;
}

function validateBinding(input) {
  const threadId = String(input.threadId || '').trim();
  const cwdInput = String(input.cwd || '').trim();
  const cwd = cwdInput ? path.resolve(cwdInput) : '';
  const chatId = String(input.chatId || '').trim();
  const operatorId = String(input.operatorId || '').trim();
  const rootMessageId = String(input.rootMessageId || '').trim();
  const messageIds = normalizedMessages(input.messageIds);
  if (!/^[0-9a-f-]{20,}$/i.test(threadId)) throw new Error('invalid Codex thread id');
  if (!path.isAbsolute(cwd) || !fs.existsSync(cwd) || !fs.statSync(cwd).isDirectory()) {
    throw new Error('invalid default conversation working directory');
  }
  if (!chatId || !operatorId || (!rootMessageId && !messageIds.length)) {
    throw new Error('default conversation message binding is incomplete');
  }
  return { threadId, cwd, chatId, operatorId, rootMessageId, messageIds };
}

function upsertConversation(input, filePath = defaultConversationPath()) {
  const binding = validateBinding(input);
  const store = readStore(filePath);
  const now = new Date().toISOString();
  const existing = store.conversations.find((item) => item.threadId === binding.threadId);
  const conversation = normalizeConversation({
    ...(existing || {}),
    id: existing?.id || `CHAT-${crypto.randomBytes(8).toString('hex').toUpperCase()}`,
    ...binding,
    title: String(input.title || existing?.title || '飞书对话').trim().slice(0, 160),
    rootMessageId: binding.rootMessageId || existing?.rootMessageId || '',
    messageIds: normalizedMessages([
      ...(existing?.messageIds || []),
      ...binding.messageIds,
      binding.rootMessageId,
    ]),
    state: existing && existing.state !== 'active' ? existing.state : 'active',
    lastTurnId: String(input.lastTurnId || existing?.lastTurnId || ''),
    createdAt: existing?.createdAt || now,
    updatedAt: now,
  });
  writeStore({
    ...store,
    conversations: [...store.conversations.filter((item) => item.id !== conversation.id), conversation],
  }, filePath);
  return conversation;
}

function updateConversation(id, patch, filePath = defaultConversationPath()) {
  const store = readStore(filePath);
  const index = store.conversations.findIndex((item) => item.id === id);
  if (index < 0) throw new Error('default conversation not found');
  if (patch.state && !STATES.has(patch.state)) throw new Error('invalid default conversation state');
  const previous = store.conversations[index];
  const requestedState = patch.state && previous.state !== 'active' && patch.state === 'active'
    ? previous.state
    : patch.state;
  store.conversations[index] = normalizeConversation({
    ...previous,
    ...patch,
    ...(requestedState ? { state: requestedState } : {}),
    messageIds: normalizedMessages([
      ...(previous.messageIds || []),
      ...(patch.messageIds || []),
      patch.rootMessageId || '',
    ]),
    updatedAt: new Date().toISOString(),
  });
  writeStore(store, filePath);
  return store.conversations[index];
}

function messageIdsFor(message) {
  return new Set([
    message?.message_id,
    message?.messageId,
    message?.root_id,
    message?.rootId,
    message?.parent_id,
    message?.parentId,
  ].map((value) => String(value || '').trim()).filter(Boolean));
}

function resolveConversation(message, filePath = defaultConversationPath()) {
  const ids = messageIdsFor(message);
  if (!ids.size) return null;
  return [...readStore(filePath).conversations].reverse().find((conversation) => (
    conversation.state === 'active'
      && [conversation.rootMessageId, ...(conversation.messageIds || [])]
        .some((id) => ids.has(id))
  )) || null;
}

function operatorMatches(conversation, { chatId, operatorId }) {
  return Boolean(conversation
    && conversation.chatId === String(chatId || '')
    && conversation.operatorId === String(operatorId || ''));
}

module.exports = {
  PROTOCOL,
  SCHEMA_VERSION,
  defaultConversationPath,
  emptyStore,
  operatorMatches,
  readStore,
  resolveConversation,
  updateConversation,
  upsertConversation,
  writeStore,
};
