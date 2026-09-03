const fs = require('node:fs');
const net = require('node:net');
const path = require('node:path');
const { spawnSync } = require('node:child_process');
const { randomUUID } = require('node:crypto');

const MAX_FRAME_BYTES = 64 * 1024 * 1024;

function encodeFrame(message) {
  const payload = Buffer.from(JSON.stringify(message), 'utf8');
  const frame = Buffer.allocUnsafe(payload.length + 4);
  frame.writeUInt32LE(payload.length, 0);
  payload.copy(frame, 4);
  return frame;
}

class DesktopIPCFrameDecoder {
  constructor(maxFrameBytes = MAX_FRAME_BYTES) {
    this.maxFrameBytes = maxFrameBytes;
    this.buffer = Buffer.alloc(0);
  }

  append(chunk) {
    this.buffer = Buffer.concat([this.buffer, Buffer.from(chunk)]);
    const messages = [];
    while (this.buffer.length >= 4) {
      const length = this.buffer.readUInt32LE(0);
      if (!length || length > this.maxFrameBytes) {
        throw new Error(`invalid Codex Desktop IPC frame length: ${length}`);
      }
      if (this.buffer.length < length + 4) break;
      const payload = this.buffer.subarray(4, length + 4);
      this.buffer = this.buffer.subarray(length + 4);
      messages.push(JSON.parse(payload.toString('utf8')));
    }
    return messages;
  }
}

function desktopThreadURL(threadId) {
  const value = String(threadId || '');
  if (!value || /[\u0000-\u001f\u007f]/.test(value)) {
    throw new Error('invalid Codex task id');
  }
  return `codex://threads/${encodeURIComponent(value)}`;
}

function normalizeDesktopInput(input) {
  return (Array.isArray(input) ? input : []).map((item) => (
    item?.type === 'text'
      ? { ...item, text_elements: Array.isArray(item.text_elements) ? item.text_elements : [] }
      : item
  ));
}

function normalizeCollaborationMode(value) {
  if (value == null) return null;
  const mode = String(value.mode || '');
  const model = String(value.settings?.model || '').trim();
  if (!['default', 'plan'].includes(mode) || !model) {
    throw new Error('invalid Codex collaboration mode');
  }
  return {
    mode,
    settings: {
      model,
      reasoning_effort: value.settings?.reasoning_effort == null
        ? null : String(value.settings.reasoning_effort),
      developer_instructions: null,
    },
  };
}

function followerRequest({ requestId, sourceClientId, targetClientId, method, version, params, timeoutMs }) {
  return {
    type: 'request',
    requestId,
    sourceClientId,
    targetClientId,
    timeoutMs,
    method,
    version,
    params,
  };
}

function extractTurnId(value, seen = new Set()) {
  if (!value || typeof value !== 'object' || seen.has(value)) return '';
  seen.add(value);
  if (typeof value.turnId === 'string' && value.turnId) return value.turnId;
  if (typeof value.turn?.id === 'string' && value.turn.id) return value.turn.id;
  for (const nested of Object.values(value)) {
    const turnId = extractTurnId(nested, seen);
    if (turnId) return turnId;
  }
  return '';
}

function questionIds(questions) {
  return (Array.isArray(questions) ? questions : [])
    .map((question) => String(question?.id || '').trim())
    .filter(Boolean)
    .sort();
}

function matchingDesktopUserInputRequest(conversationState, { turnId, questions } = {}) {
  const expectedQuestionIds = questionIds(questions);
  const requests = (Array.isArray(conversationState?.requests) ? conversationState.requests : [])
    .filter((request) => request?.method === 'item/tool/requestUserInput');
  const sameTurn = turnId
    ? requests.filter((request) => !request.params?.turnId || String(request.params.turnId) === String(turnId))
    : requests;
  const sameQuestions = expectedQuestionIds.length
    ? sameTurn.filter((request) => {
      const actualQuestionIds = questionIds(request.params?.questions);
      return actualQuestionIds.length === expectedQuestionIds.length
        && actualQuestionIds.every((id, index) => id === expectedQuestionIds[index]);
    })
    : sameTurn;
  const candidates = expectedQuestionIds.length ? sameQuestions : sameTurn;
  if (candidates.length !== 1 || candidates[0].id == null) return null;
  return candidates[0];
}

function desktopTurnTimestamp(turn) {
  const milliseconds = Number(turn?.turnStartedAtMs);
  if (Number.isFinite(milliseconds) && milliseconds > 0) return milliseconds;
  return Date.parse(turn?.startedAt || turn?.createdAt || '') || 0;
}

function normalizeDesktopTurn(turn) {
  const id = String(turn?.id || turn?.turnId || '').trim();
  if (!id) return null;
  const startedAtMs = desktopTurnTimestamp(turn);
  const durationMs = Number(turn?.durationMs);
  const hasDuration = turn?.durationMs !== null
    && turn?.durationMs !== undefined
    && Number.isFinite(durationMs)
    && durationMs >= 0;
  const status = turn?.status || { type: 'unknown' };
  return {
    ...turn,
    id,
    status,
    startedAt: startedAtMs > 0 ? new Date(startedAtMs).toISOString() : String(turn?.startedAt || ''),
    completedAt: hasDuration && startedAtMs > 0
      ? new Date(startedAtMs + durationMs).toISOString()
      : String(turn?.completedAt || ''),
    items: Array.isArray(turn?.items) ? turn.items : [],
  };
}

function desktopConversationTurns(conversationState) {
  const candidates = Array.isArray(conversationState?.turns) ? [...conversationState.turns] : [];
  const history = conversationState?.turnHistory?.history;
  if (history?.entitiesByKey && typeof history.entitiesByKey === 'object') {
    candidates.push(...Object.values(history.entitiesByKey));
  }
  const unique = new Map();
  for (const candidate of candidates) {
    const turn = normalizeDesktopTurn(candidate);
    if (turn) unique.set(turn.id, turn);
  }
  return [...unique.values()].sort((left, right) => (
    desktopTurnTimestamp(left) - desktopTurnTimestamp(right)
  ));
}

function normalizePlanImplementation(value) {
  const turnId = String(value?.turnId || value?.params?.turnId || '').trim();
  const planContent = String(value?.planContent || value?.params?.planContent || '').trim();
  if (!turnId || !planContent) return null;
  return { turnId, planContent };
}

function pendingPlanImplementation(conversationState) {
  const turns = desktopConversationTurns(conversationState);
  let item = null;
  const latest = turns.at(-1);
  const implementation = [...(latest?.items || [])].reverse()
    .find((candidate) => candidate?.type === 'planImplementation');
  if (implementation && implementation.isCompleted !== true) {
    item = normalizePlanImplementation({ ...implementation, turnId: implementation.turnId || latest.id });
    if (!item) throw new Error('Codex Desktop returned an invalid plan implementation state');
  }

  const requestCandidates = (Array.isArray(conversationState?.requests)
    ? conversationState.requests : Object.values(conversationState?.requests || {}))
    .filter((request) => request?.method === 'item/plan/requestImplementation')
    .map(normalizePlanImplementation)
    .filter(Boolean);
  const uniqueRequests = new Map(requestCandidates.map((request) => (
    [`${request.turnId}\u0000${request.planContent}`, request]
  )));
  if (uniqueRequests.size > 1) {
    throw new Error('Codex Desktop returned conflicting plan implementation state');
  }
  const request = [...uniqueRequests.values()][0] || null;
  if (implementation?.isCompleted === true) {
    if (request) throw new Error('Codex Desktop returned conflicting plan implementation state');
    return null;
  }
  if (!item && request && latest && request.turnId !== latest.id) return null;
  if (item && request
    && (item.turnId !== request.turnId || item.planContent !== request.planContent)) {
    throw new Error('Codex Desktop returned conflicting plan implementation state');
  }
  return item || request;
}

function desktopThreadSnapshot(conversationState, expectedThreadId = '') {
  const id = String(conversationState?.id || conversationState?.sessionId || '').trim();
  if (!id || (expectedThreadId && id !== expectedThreadId)) {
    throw new Error('Codex Desktop returned a different task');
  }
  return {
    id,
    cwd: String(conversationState?.cwd || '').trim(),
    name: String(conversationState?.title || conversationState?.name || '').trim(),
    parentThreadId: String(conversationState?.parentThreadId || '').trim(),
    status: conversationState?.threadRuntimeStatus || conversationState?.status || { type: 'notLoaded' },
    turns: desktopConversationTurns(conversationState),
    pendingPlanImplementation: pendingPlanImplementation(conversationState),
  };
}

function validateDesktopEndpoint(socketPath, platform = process.platform) {
  if (!socketPath) throw new Error('Codex Desktop IPC endpoint is not configured');
  if (platform === 'win32') {
    if (!/^\\\\\.\\pipe\\[A-Za-z0-9._-]{1,200}$/.test(socketPath)) {
      throw new Error('Codex Desktop IPC path must be a Windows named pipe');
    }
    return { type: 'named-pipe', path: socketPath };
  }
  const stat = fs.lstatSync(socketPath);
  if (!stat.isSocket()) throw new Error('Codex Desktop IPC path is not a Unix socket');
  if (typeof process.getuid === 'function' && stat.uid !== process.getuid()) {
    throw new Error('Codex Desktop IPC socket belongs to another user');
  }
  if ((stat.mode & 0o077) !== 0) {
    throw new Error('Codex Desktop IPC socket permissions are not private');
  }
  return { type: 'unix-socket', path: socketPath };
}

function defaultOpenTask(threadId, {
  platform = process.platform,
  projectRoot = path.resolve(__dirname, '..'),
  spawnSyncFn = spawnSync,
  env = process.env,
} = {}) {
  const url = desktopThreadURL(threadId);
  const command = platform === 'win32' ? 'powershell.exe' : '/usr/bin/open';
  const args = platform === 'win32'
    ? [
      '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass',
      '-File', path.join(projectRoot, 'windows', 'Open-CodexTask.ps1'),
    ]
    : [url];
  const result = spawnSyncFn(command, args, {
    encoding: 'utf8',
    timeout: 5000,
    env: platform === 'win32' ? { ...env, CODEX_TASK_URL: url } : env,
  });
  if (result.error || result.status !== 0) {
    throw new Error('Codex Desktop could not open the linked task');
  }
}

function desktopEndpointReady(socketPath, {
  platform = process.platform,
  projectRoot = path.resolve(__dirname, '..'),
  spawnSyncFn = spawnSync,
  env = process.env,
} = {}) {
  try {
    validateDesktopEndpoint(socketPath, platform);
  } catch {
    return false;
  }
  if (platform !== 'win32') return true;
  const result = spawnSyncFn('powershell.exe', [
    '-NoLogo', '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass',
    '-File', path.join(projectRoot, 'windows', 'Test-CodexDesktopPipe.ps1'),
  ], {
    encoding: 'utf8', timeout: 5000,
    env: { ...env, CODEX_DESKTOP_IPC_PATH: socketPath },
  });
  if (result.status !== 0) return false;
  try {
    return Boolean(JSON.parse(String(result.stdout || '').replace(/^\uFEFF/, '').trim()).ready);
  } catch {
    return false;
  }
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

class DesktopIPCSession {
  constructor({ socketPath, requestTimeoutMs, socketFactory = net.createConnection, platform = process.platform }) {
    this.socketPath = socketPath;
    this.requestTimeoutMs = requestTimeoutMs;
    this.socketFactory = socketFactory;
    this.platform = platform;
    this.socket = null;
    this.decoder = new DesktopIPCFrameDecoder();
    this.pending = new Map();
    this.snapshotWaiters = new Map();
    this.followedThreadIds = new Set();
    this.clientId = '';
    this.closed = false;
  }

  async connect() {
    validateDesktopEndpoint(this.socketPath, this.platform);
    this.socket = this.socketFactory({ path: this.socketPath });
    this.socket.on('data', (chunk) => this.handleData(chunk));
    this.socket.on('error', (error) => this.failAll(error));
    this.socket.on('close', () => this.failAll(new Error('Codex Desktop IPC disconnected')));
    await new Promise((resolve, reject) => {
      const onConnect = () => { cleanup(); resolve(); };
      const onError = (error) => { cleanup(); reject(error); };
      const cleanup = () => {
        this.socket.off('connect', onConnect);
        this.socket.off('error', onError);
      };
      this.socket.once('connect', onConnect);
      this.socket.once('error', onError);
    });
    const response = await this.request({
      type: 'request',
      requestId: `feishu-bridge-initialize-${randomUUID()}`,
      method: 'initialize',
      params: { clientType: 'feishu-bridge' },
    });
    this.clientId = response?.result?.clientId || response?.result?.clientID
      || response?.clientId || response?.clientID || '';
    if (!this.clientId) throw new Error('Codex Desktop IPC did not return a client id');
  }

  handleData(chunk) {
    let messages;
    try {
      messages = this.decoder.append(chunk);
    } catch (error) {
      this.failAll(error);
      return;
    }
    for (const message of messages) {
      if (message.type === 'client-discovery-request' && message.requestId) {
        this.send({
          type: 'client-discovery-response',
          requestId: message.requestId,
          response: { canHandle: false },
        });
        continue;
      }
      if (message.type === 'broadcast' && message.method === 'thread-stream-state-changed') {
        const conversationId = String(message.params?.conversationId || '');
        const change = message.params?.change;
        if (conversationId && change?.type === 'snapshot' && change.conversationState) {
          const waiters = this.snapshotWaiters.get(conversationId);
          if (waiters?.length) {
            this.snapshotWaiters.delete(conversationId);
            for (const waiter of waiters) {
              clearTimeout(waiter.timer);
              waiter.resolve(change.conversationState);
            }
          }
        }
        continue;
      }
      if (message.type !== 'response' || !message.requestId) continue;
      const pending = this.pending.get(message.requestId);
      if (!pending) continue;
      this.pending.delete(message.requestId);
      clearTimeout(pending.timer);
      if (message.error || message.resultType === 'error') {
        const detail = message.error?.message || message.error?.code || message.error || 'Codex Desktop IPC request failed';
        pending.reject(new Error(String(detail)));
      } else {
        pending.resolve(message);
      }
    }
  }

  request(message, timeoutMs = this.requestTimeoutMs) {
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(message.requestId);
        reject(new Error(`Codex Desktop IPC request timed out: ${message.method}`));
      }, timeoutMs);
      this.pending.set(message.requestId, { resolve, reject, timer });
      try {
        this.send(message);
      } catch (error) {
        clearTimeout(timer);
        this.pending.delete(message.requestId);
        reject(error);
      }
    });
  }

  send(message) {
    if (!this.socket?.writable) throw new Error('Codex Desktop IPC is not writable');
    this.socket.write(encodeFrame(message));
  }

  async discoverOwner(threadId) {
    try {
      const response = await this.request({
        type: 'request',
        requestId: `feishu-bridge-owner-${randomUUID()}`,
        sourceClientId: this.clientId,
        method: 'thread-owner-discovery',
        version: 1,
        params: { conversationId: threadId, hostId: 'local' },
      }, Math.min(this.requestTimeoutMs, 3000));
      return response?.result?.handledByClientId || response?.result?.handledByClientID
        || response?.handledByClientId || response?.handledByClientID || '';
    } catch (error) {
      if (/no-client-found|no client|not handled|timed out/i.test(error.message || '')) return '';
      throw error;
    }
  }

  async ensureOwner({ threadId, openTask, ownerAttempts, ownerRetryMs }) {
    let owner = await this.discoverOwner(threadId);
    if (!owner) {
      openTask(threadId);
      for (let attempt = 0; attempt < ownerAttempts && !owner; attempt += 1) {
        if (attempt) await delay(ownerRetryMs);
        owner = await this.discoverOwner(threadId);
      }
    }
    if (!owner) throw new Error('Codex Desktop did not take ownership of the linked task');
    return owner;
  }

  nextConversationSnapshot(threadId, timeoutMs = Math.min(this.requestTimeoutMs, 5000)) {
    return new Promise((resolve, reject) => {
      const waiter = {
        resolve,
        reject,
        timer: setTimeout(() => {
          const waiters = this.snapshotWaiters.get(threadId) || [];
          this.snapshotWaiters.set(threadId, waiters.filter((item) => item !== waiter));
          reject(new Error('Codex Desktop task snapshot timed out'));
        }, timeoutMs),
      };
      const waiters = this.snapshotWaiters.get(threadId) || [];
      waiters.push(waiter);
      this.snapshotWaiters.set(threadId, waiters);
    });
  }

  async loadConversationState({ threadId, ownerClientId }) {
    const snapshot = this.nextConversationSnapshot(threadId);
    this.followedThreadIds.add(threadId);
    this.send({
      type: 'broadcast',
      sourceClientId: this.clientId,
      method: 'thread-stream-following-changed',
      version: 1,
      params: { conversationId: threadId, hostId: 'local', following: true },
    });
    const history = this.request(followerRequest({
      requestId: `feishu-bridge-history-${randomUUID()}`,
      sourceClientId: this.clientId,
      targetClientId: ownerClientId,
      method: 'thread-follower-load-complete-history',
      version: 1,
      params: { conversationId: threadId },
      timeoutMs: this.requestTimeoutMs,
    }));
    const [conversationState] = await Promise.all([snapshot, history]);
    return conversationState;
  }

  async resolveUserInputRequest({ threadId, turnId, questions, openTask, ownerAttempts, ownerRetryMs }) {
    const ownerClientId = await this.ensureOwner({ threadId, openTask, ownerAttempts, ownerRetryMs });
    const conversationState = await this.loadConversationState({ threadId, ownerClientId });
    const request = matchingDesktopUserInputRequest(conversationState, { turnId, questions });
    if (!request) throw new Error('Codex Desktop no longer has the expected user-input request');
    return { ownerClientId, requestId: request.id };
  }

  async requestFollower({
    threadId, method, version, params, openTask, ownerAttempts, ownerRetryMs, targetClientId,
  }) {
    const owner = targetClientId || await this.ensureOwner({
      threadId, openTask, ownerAttempts, ownerRetryMs,
    });
    return this.request(followerRequest({
      requestId: `feishu-bridge-follower-${randomUUID()}`,
      sourceClientId: this.clientId,
      targetClientId: owner,
      method,
      version,
      params,
      timeoutMs: this.requestTimeoutMs,
    }));
  }

  failAll(error) {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timer);
      pending.reject(error);
    }
    this.pending.clear();
    for (const waiters of this.snapshotWaiters.values()) {
      for (const waiter of waiters) {
        clearTimeout(waiter.timer);
        waiter.reject(error);
      }
    }
    this.snapshotWaiters.clear();
  }

  close() {
    this.failAll(new Error('Codex Desktop IPC closed'));
    this.closed = true;
    if (this.socket?.writable && this.clientId) {
      for (const threadId of this.followedThreadIds) {
        this.send({
          type: 'broadcast',
          sourceClientId: this.clientId,
          method: 'thread-stream-following-changed',
          version: 1,
          params: { conversationId: threadId, hostId: 'local', following: false },
        });
      }
    }
    if (this.socket && !this.socket.destroyed) this.socket.destroy();
    this.socket = null;
  }
}

class CodexDesktopTaskController {
  constructor({
    socketPath,
    requestTimeoutMs = 20000,
    ownerAttempts = 40,
    ownerRetryMs = 250,
    openTask,
    sessionFactory,
    platform = process.platform,
    projectRoot = path.resolve(__dirname, '..'),
  }) {
    this.socketPath = socketPath;
    this.requestTimeoutMs = requestTimeoutMs;
    this.ownerAttempts = ownerAttempts;
    this.ownerRetryMs = ownerRetryMs;
    this.platform = platform;
    this.openTask = openTask || ((threadId) => defaultOpenTask(threadId, { platform, projectRoot }));
    this.sessionFactory = sessionFactory || ((options) => new DesktopIPCSession(options));
  }

  async withSession(operation) {
    const session = this.sessionFactory({
      socketPath: this.socketPath,
      requestTimeoutMs: this.requestTimeoutMs,
      platform: this.platform,
    });
    try {
      await session.connect();
      return await operation(session);
    } finally {
      session.close();
    }
  }

  async discoverOwner(threadId) {
    return this.withSession((session) => session.discoverOwner(threadId));
  }

  async readThreadSnapshot(threadId, { openIfNeeded = true } = {}) {
    return this.withSession(async (session) => {
      const ownerClientId = openIfNeeded
        ? await session.ensureOwner({
            threadId,
            openTask: this.openTask,
            ownerAttempts: this.ownerAttempts,
            ownerRetryMs: this.ownerRetryMs,
          })
        : await session.discoverOwner(threadId);
      if (!ownerClientId) throw new Error('Codex Desktop does not currently own the linked task');
      const conversationState = await session.loadConversationState({ threadId, ownerClientId });
      return desktopThreadSnapshot(conversationState, threadId);
    });
  }

  async startTurn({ threadId, cwd, input, collaborationMode = null }) {
    return this.withSession(async (session) => {
      const normalizedMode = normalizeCollaborationMode(collaborationMode);
      const response = await session.requestFollower({
        threadId,
        method: 'thread-follower-start-turn',
        version: 2,
        params: {
          conversationId: threadId,
          turnStart: {
            request: {
              threadId,
              input: normalizeDesktopInput(input),
              cwd,
              approvalPolicy: 'never',
              sandboxPolicy: { type: 'dangerFullAccess' },
              ...(normalizedMode ? { collaborationMode: normalizedMode } : {}),
            },
            context: { inheritThreadSettings: true },
          },
        },
        openTask: this.openTask,
        ownerAttempts: this.ownerAttempts,
        ownerRetryMs: this.ownerRetryMs,
      });
      return { response, turnId: extractTurnId(response) };
    });
  }

  async updateCollaborationMode({ threadId, collaborationMode }) {
    return this.withSession(async (session) => {
      const normalizedMode = normalizeCollaborationMode(collaborationMode);
      await session.requestFollower({
        threadId,
        method: 'thread-follower-update-thread-settings',
        version: 1,
        params: {
          conversationId: threadId,
          threadSettings: { collaborationMode: normalizedMode },
        },
        openTask: this.openTask,
        ownerAttempts: this.ownerAttempts,
        ownerRetryMs: this.ownerRetryMs,
      });
      return true;
    });
  }

  async steer({ threadId, cwd, input, turnId }) {
    return this.withSession(async (session) => {
      const response = await session.requestFollower({
        threadId,
        method: 'thread-follower-steer-turn',
        version: 1,
        params: {
          conversationId: threadId,
          input: normalizeDesktopInput(input),
          restoreMessage: {
            cwd,
            context: { workspaceRoots: [cwd], collaborationMode: null },
            responsesapiClientMetadata: {},
          },
          attachments: [],
          clientUserMessageId: randomUUID(),
        },
        openTask: this.openTask,
        ownerAttempts: this.ownerAttempts,
        ownerRetryMs: this.ownerRetryMs,
      });
      return extractTurnId(response) || turnId;
    });
  }

  async submitUserInput({ threadId, turnId, questions, response }) {
    if (!threadId || !turnId || !Array.isArray(questions) || !questions.length
      || !response || typeof response !== 'object') {
      throw new Error('invalid Codex Desktop user input response');
    }
    return this.withSession(async (session) => {
      const pending = await session.resolveUserInputRequest({
        threadId,
        turnId,
        questions,
        openTask: this.openTask,
        ownerAttempts: this.ownerAttempts,
        ownerRetryMs: this.ownerRetryMs,
      });
      await session.requestFollower({
        threadId,
        targetClientId: pending.ownerClientId,
        method: 'thread-follower-submit-user-input',
        version: 1,
        params: {
          conversationId: threadId,
          requestId: pending.requestId,
          response,
        },
        openTask: this.openTask,
        ownerAttempts: this.ownerAttempts,
        ownerRetryMs: this.ownerRetryMs,
      });
      return true;
    });
  }

  async interrupt({ threadId, turnId }) {
    return this.withSession(async (session) => {
      await session.requestFollower({
        threadId,
        method: 'thread-follower-interrupt-turn',
        version: 4,
        params: { conversationId: threadId, mode: 'user-stop', expectedTurnId: turnId },
        openTask: this.openTask,
        ownerAttempts: this.ownerAttempts,
        ownerRetryMs: this.ownerRetryMs,
      });
      return true;
    });
  }
}

module.exports = {
  CodexDesktopTaskController,
  DesktopIPCFrameDecoder,
  desktopThreadURL,
  encodeFrame,
  extractTurnId,
  followerRequest,
  matchingDesktopUserInputRequest,
  pendingPlanImplementation,
  desktopConversationTurns,
  desktopThreadSnapshot,
  normalizeCollaborationMode,
  normalizeDesktopInput,
  defaultOpenTask,
  desktopEndpointReady,
  validateDesktopEndpoint,
};
