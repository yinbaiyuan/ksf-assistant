const crypto = require('node:crypto');
const path = require('node:path');

const PLAN_IMPLEMENTATION_PREFIX = 'PLEASE IMPLEMENT THIS PLAN:';
const PLAN_COMPLETION_SNAPSHOT_DELAYS_MS = Object.freeze([0, 250, 750]);

function threadFromResult(result) {
  return result?.thread || result;
}

function turnsFromResult(result) {
  const direct = result?.turns || result?.items || result?.data;
  return Array.isArray(direct) ? direct : [];
}

function turnTime(turn) {
  return Date.parse(turn?.completedAt || turn?.startedAt || turn?.createdAt || '') || 0;
}

function publicTurnTiming(turn, journalTurn) {
  const startedAt = String(
    journalTurn?.startedAt || turn?.startedAt || turn?.createdAt || '',
  );
  const completedAt = String(
    journalTurn?.completedAt || turn?.completedAt || '',
  );
  const startedMs = Date.parse(startedAt);
  const completedMs = Date.parse(completedAt);
  return {
    startedAt: Number.isFinite(startedMs) ? new Date(startedMs).toISOString() : '',
    completedAt: Number.isFinite(completedMs) ? new Date(completedMs).toISOString() : '',
    durationSeconds: Number.isFinite(startedMs) && Number.isFinite(completedMs) && completedMs >= startedMs
      ? Math.max(0, Math.round((completedMs - startedMs) / 1000))
      : undefined,
  };
}

function latestTurn(thread, turns = []) {
  const candidates = [...(Array.isArray(thread?.turns) ? thread.turns : []), ...turns];
  const unique = new Map(candidates.filter(Boolean).map((turn) => [turn.id || JSON.stringify(turn), turn]));
  return [...unique.values()].sort((left, right) => turnTime(left) - turnTime(right)).at(-1) || null;
}

function activeFlags(thread) {
  const status = thread?.status || {};
  return status.activeFlags || status.flags || status;
}

function threadStatusType(thread) {
  const status = thread?.status;
  return String(typeof status === 'string' ? status : status?.type || '').toLowerCase();
}

function turnStatusType(turn) {
  return String(turn?.status?.type || turn?.status || '').toLowerCase();
}

function isTurnRunning(thread, turn) {
  const threadStatus = threadStatusType(thread);
  const turnStatus = turnStatusType(turn);
  return ['active', 'running', 'inprogress', 'in_progress'].includes(threadStatus)
    || ['active', 'running', 'inprogress', 'in_progress'].includes(turnStatus);
}

function publicTurnState(thread, turn, owner = 'desktop') {
  const flags = activeFlags(thread);
  if (isTurnRunning(thread, turn)) {
    if (flags.waitingOnApproval || flags.waiting_on_approval || flags.waitingOnUserInput || flags.waiting_on_user_input) {
      return {
        turnState: 'desktop_action_required',
        turnOwner: owner,
        actionRequired: 'desktop',
      };
    }
    return { turnState: 'running', turnOwner: owner, actionRequired: 'none' };
  }
  switch (turnStatusType(turn)) {
    case 'failed':
      return { turnState: 'failed', turnOwner: 'none', actionRequired: 'none' };
    case 'interrupted':
    case 'cancelled':
    case 'canceled':
      return { turnState: 'interrupted', turnOwner: 'none', actionRequired: 'none' };
    case 'completed':
      return { turnState: 'completed', turnOwner: 'none', actionRequired: 'none' };
    default:
      return { turnState: 'idle', turnOwner: 'none', actionRequired: 'none' };
  }
}

function agentMessageText(item) {
  if (item?.type !== 'agentMessage') return '';
  return String(item.text || item.content || '').trim();
}

function latestAgentMessageFromTurn(turn) {
  const items = Array.isArray(turn?.items) ? turn.items : [];
  return [...items].reverse().find((item) => agentMessageText(item)) || null;
}

function publicProgressText(value, maximum = 900) {
  const text = String(value || '')
    .replace(/\r\n?/g, '\n')
    .replace(/[ \t]+/g, ' ')
    .replace(/\n{3,}/g, '\n\n')
    .trim();
  if (text.length <= maximum) return text;
  return `${text.slice(0, Math.max(0, maximum - 1)).trimEnd()}…`;
}

function publicPlanText(plan, explanation = '', maximum = 3000) {
  const entries = Array.isArray(plan) ? plan : [];
  const lines = entries.slice(0, 20).map((entry) => {
    const step = publicProgressText(entry?.step || entry?.text, 240);
    if (!step) return '';
    const status = String(entry?.status || '').toLowerCase();
    if (/completed|complete|done/.test(status)) return `- [x] ${step}`;
    if (/in.?progress|running|active/.test(status)) return `- [ ] **${step}**（进行中）`;
    return `- [ ] ${step}`;
  }).filter(Boolean);
  if (entries.length > 20) lines.push(`- 其余 ${entries.length - 20} 项请在 Codex Desktop 查看`);
  const lead = publicProgressText(explanation, 500);
  return publicProgressText([lead, lines.join('\n')].filter(Boolean).join('\n\n'), maximum);
}

function publicPlanTextFromItem(item, maximum = 3000) {
  if (!item || typeof item !== 'object') return '';
  if (item.type === 'plan') return publicProgressText(item.text || item.content, maximum);
  if (item.type === 'todo-list' || item.type === 'todoList') {
    return publicPlanText(item.plan || item.items, item.explanation, maximum);
  }
  return '';
}

function publicUserMessageText(payload, maximum = 1000) {
  if (payload?.type !== 'message' || payload?.role !== 'user') return '';
  const texts = (Array.isArray(payload.content) ? payload.content : [])
    .filter((item) => item?.type === 'input_text' && item.text)
    .map((item) => String(item.text || '').trim())
    .filter((text) => text && !/^<\/?image\b/i.test(text));
  if (!texts.length) return '';
  let message = texts.join('\n\n');
  const requestMarker = message.match(/(?:^|\n)## My request:\s*\n/i);
  if (requestMarker) message = message.slice(requestMarker.index + requestMarker[0].length);
  message = message.replace(/\n?<\/?image\b[^>]*>\s*$/gi, '').trim();
  if (/(?:authorization\s*:\s*bearer\s+\S+|(?:api[_ -]?key|access[_ -]?token|password|secret)\s*[:=：]\s*\S+|\bsk-[A-Za-z0-9_-]{16,})/i.test(message)) {
    return '本轮消息包含可能的敏感信息，请在 Codex Desktop 查看。';
  }
  return publicProgressText(message, maximum);
}

function publicProgressFromEvent(event) {
  const method = String(event?.method || '');
  const params = event?.params || {};
  const item = params.item || {};

  if (method === 'item/completed') {
    const planText = publicPlanTextFromItem(item);
    if (planText) {
      return {
        phase: item.type === 'plan' ? '计划已生成' : '计划更新',
        detail: '计划已同步到飞书。',
        kind: 'plan',
        plan: planText,
      };
    }
  }

  if (method === 'item/completed'
    && item.type === 'agentMessage'
    && String(item.phase || '').toLowerCase() === 'commentary') {
    const detail = publicProgressText(item.text || item.content);
    return detail ? { phase: '当前进展', detail, kind: 'commentary' } : null;
  }

  if (method === 'turn/plan/updated') {
    const plan = Array.isArray(params.plan) ? params.plan : [];
    const active = plan.find((entry) => /in.?progress/i.test(String(entry?.status || '')))
      || plan.find((entry) => !/completed/i.test(String(entry?.status || '')));
    const completed = plan.filter((entry) => /completed/i.test(String(entry?.status || ''))).length;
    const detail = publicProgressText(active?.step || params.explanation || '正在按计划推进。');
    return detail ? {
      phase: plan.length ? `计划 ${Math.min(completed + 1, plan.length)}/${plan.length}` : '计划更新',
      detail,
      kind: 'plan',
      plan: publicPlanText(plan, params.explanation),
    } : null;
  }

  if (method === 'turn/started') {
    return { phase: '当前进展', detail: 'Codex 已开始处理。', kind: 'state' };
  }
  if (method === 'item/started') {
    if (/command|shell/i.test(item.type || '')) {
      return { phase: '执行中', detail: '正在执行任务所需操作。', kind: 'activity' };
    }
    if (/file|patch|edit/i.test(item.type || '')) {
      return { phase: '处理中', detail: '正在处理工作区文件。', kind: 'activity' };
    }
    if (/tool|mcp/i.test(item.type || '')) {
      return { phase: '处理中', detail: '正在调用任务所需工具。', kind: 'activity' };
    }
  }
  return null;
}

function changedFilePathsFromEvent(event) {
  if (String(event?.method || '') !== 'item/completed') return [];
  const params = event?.params || {};
  const item = params.item || {};
  if (!/file|patch|edit/i.test(String(item.type || ''))) return [];
  const changes = Array.isArray(item.changes)
    ? item.changes
    : Array.isArray(params.changes) ? params.changes : [];
  const candidates = [
    ...changes.map((change) => change?.path || change?.filePath || change?.file_path),
    item.path,
    item.filePath,
    item.file_path,
  ];
  return [...new Set(candidates.map((value) => {
    const filePath = String(value || '').trim();
    return filePath ? path.normalize(filePath).replaceAll('\\', '/') : '';
  }).filter(Boolean))];
}

function changedFilePathsFromTurn(turn) {
  const paths = new Set();
  for (const item of Array.isArray(turn?.items) ? turn.items : []) {
    for (const filePath of changedFilePathsFromEvent({
      method: 'item/completed',
      params: { item },
    })) paths.add(filePath);
  }
  return [...paths];
}

function taskLinkProgressForTurn(progress, turnId, changedFilePaths = null) {
  const next = { ...(progress || {}) };
  const normalizedTurnId = String(turnId || '');
  if (String(next.changedFilesTurnId || '') !== normalizedTurnId) {
    next.changedFiles = 0;
    next.testStatus = '未运行';
  }
  if (Array.isArray(changedFilePaths)) next.changedFiles = new Set(changedFilePaths).size;
  next.changedFilesTurnId = normalizedTurnId;
  return next;
}

function finalAnswerTextFromTurn(turn) {
  const items = Array.isArray(turn?.items) ? turn.items : [];
  return agentMessageText([...items].reverse().find((item) => (
    agentMessageText(item) && String(item.phase || '').toLowerCase() === 'final_answer'
  )));
}

function finalTextFromTurn(turn) {
  const items = Array.isArray(turn?.items) ? turn.items : [];
  const final = finalAnswerTextFromTurn(turn);
  if (final) return final;
  return agentMessageText([...items].reverse().find((item) => agentMessageText(item)));
}

function terminalTaskLinkDetail(link, snapshot, fallback = '') {
  const detail = String(fallback || link?.progress?.detail || link?.detailSummary || '').trim();
  const turnState = String(snapshot?.publicState?.turnState || link?.turnState || '');
  if (turnState !== 'completed') return detail;
  return String(
    snapshot?.finalText
      || finalTextFromTurn(snapshot?.turn)
      || snapshot?.lastMessage
      || detail,
  ).trim();
}

function planTextFromTurn(turn) {
  const items = Array.isArray(turn?.items) ? turn.items : [];
  for (const item of [...items].reverse()) {
    const text = publicPlanTextFromItem(item);
    if (text) return text;
  }
  return '';
}

function planImplementationRevision(pendingPlan) {
  const turnId = String(pendingPlan?.turnId || '').trim();
  const planContent = String(pendingPlan?.planContent || '').trim();
  if (!turnId || !planContent) return '';
  return crypto.createHash('sha256')
    .update(`${turnId}\u0000${planContent}`, 'utf8')
    .digest('hex')
    .slice(0, 20);
}

async function reconcilePlanTurnCompletion({
  resultTurnId,
  readSnapshot,
  wait = (delayMs) => new Promise((resolve) => setTimeout(resolve, delayMs)),
  delaysMs = PLAN_COMPLETION_SNAPSHOT_DELAYS_MS,
}) {
  if (typeof readSnapshot !== 'function') {
    throw new TypeError('readSnapshot must be a function');
  }
  const completedTurnId = String(resultTurnId || '').trim();
  let lastSnapshot = null;
  let attempts = 0;
  let readFailures = 0;
  for (const delayMs of delaysMs) {
    if (delayMs > 0) await wait(delayMs);
    attempts += 1;
    let snapshot;
    try {
      snapshot = await readSnapshot();
    } catch {
      readFailures += 1;
      continue;
    }
    lastSnapshot = snapshot;
    if (snapshot?.pendingPlanImplementation
      && planImplementationRevision(snapshot.pendingPlanImplementation)) {
      return { kind: 'plan_ready', snapshot, attempts, readFailures };
    }
    const observedTurnId = String(snapshot?.turnId || '').trim();
    if (completedTurnId && observedTurnId && observedTurnId !== completedTurnId) {
      return { kind: 'superseded', snapshot, attempts, readFailures };
    }
  }
  return { kind: 'completed', snapshot: lastSnapshot, attempts, readFailures };
}

function planImplementationPrompt(planContent) {
  const plan = String(planContent || '').trim();
  if (!plan) throw new Error('Codex Desktop 当前没有可执行计划');
  return `${PLAN_IMPLEMENTATION_PREFIX}\n${plan}`;
}

function withPendingPlanImplementation(projection, snapshot) {
  const pendingPlanImplementation = snapshot?.thread?.pendingPlanImplementation
    || snapshot?.pendingPlanImplementation
    || null;
  if (!pendingPlanImplementation) return { ...projection, pendingPlanImplementation: null };
  return {
    ...projection,
    publicState: { turnState: 'plan_ready', turnOwner: 'none', actionRequired: 'feishu' },
    pendingPlanImplementation,
    planText: pendingPlanImplementation.planContent,
  };
}

function projectDesktopTaskSnapshot(snapshot, journalTurn) {
  const appTurnId = String(snapshot?.turn?.id || '');
  const appFinalText = finalAnswerTextFromTurn(snapshot?.turn);
  const appLastMessage = latestAgentMessageFromTurn(snapshot?.turn);
  const appChangedFilePaths = changedFilePathsFromTurn(snapshot?.turn);
  if (!journalTurn?.turnId) {
    const timing = publicTurnTiming(snapshot?.turn);
    return withPendingPlanImplementation({
      ...snapshot,
      turnId: appTurnId,
      finalText: appFinalText || (snapshot?.publicState?.turnState === 'completed'
        ? finalTextFromTurn(snapshot?.turn) : ''),
      lastMessage: agentMessageText(appLastMessage),
      lastMessagePhase: String(appLastMessage?.phase || '').toLowerCase(),
      lastUserMessage: '',
      turnStartedAt: timing.startedAt,
      turnCompletedAt: timing.completedAt,
      turnDurationSeconds: timing.durationSeconds,
      planText: planTextFromTurn(snapshot?.turn),
      changedFilePaths: appChangedFilePaths,
      journalTurn: null,
    }, snapshot);
  }

  const appMatchesJournal = appTurnId === journalTurn.turnId;
  let publicState;
  switch (journalTurn.state) {
    case 'running':
      publicState = appMatchesJournal && snapshot?.publicState?.turnState === 'desktop_action_required'
        ? snapshot.publicState
        : { turnState: 'running', turnOwner: 'desktop', actionRequired: 'none' };
      break;
    case 'waiting_input':
      publicState = { turnState: 'waiting_input', turnOwner: 'desktop', actionRequired: 'feishu' };
      break;
    case 'completed':
      publicState = { turnState: 'completed', turnOwner: 'none', actionRequired: 'none' };
      break;
    case 'failed':
      publicState = { turnState: 'failed', turnOwner: 'none', actionRequired: 'none' };
      break;
    case 'interrupted':
    default:
      publicState = { turnState: 'interrupted', turnOwner: 'none', actionRequired: 'none' };
      break;
  }
  const matchedFallback = appMatchesJournal ? finalTextFromTurn(snapshot?.turn) : '';
  const timing = publicTurnTiming(appMatchesJournal ? snapshot?.turn : null, journalTurn);
  return withPendingPlanImplementation({
    ...snapshot,
    publicState,
    turnId: journalTurn.turnId,
    finalText: journalTurn.finalText || (journalTurn.state === 'completed' ? matchedFallback : ''),
    lastMessage: journalTurn.lastMessage || matchedFallback,
    lastMessagePhase: String(journalTurn.lastMessagePhase || (appMatchesJournal ? appLastMessage?.phase : '') || '').toLowerCase(),
    lastUserMessage: journalTurn.lastUserMessage || '',
    turnStartedAt: timing.startedAt,
    turnCompletedAt: timing.completedAt,
    turnDurationSeconds: timing.durationSeconds,
    planText: journalTurn.planText || (appMatchesJournal ? planTextFromTurn(snapshot?.turn) : ''),
    changedFilePaths: appMatchesJournal ? appChangedFilePaths : null,
    journalTurn,
  }, snapshot);
}

function recoveredRunningTurnOwner(link, observedState, observedTurnId) {
  const fallbackOwner = String(observedState?.turnOwner || 'none');
  if (!['running', 'waiting_input', 'desktop_action_required'].includes(observedState?.turnState)) {
    return fallbackOwner;
  }
  const activeTurnId = String(link?.activeTurnId || '');
  const nextTurnId = String(observedTurnId || '');
  const storedOwner = String(link?.turnOwner || '');
  if (activeTurnId && activeTurnId === nextTurnId
    && ['bridge', 'desktop'].includes(storedOwner)) return storedOwner;
  return fallbackOwner;
}

function recoveredTaskLinkPublicState(link, snapshot) {
  const observed = snapshot?.publicState || {};
  const observedTurnId = String(snapshot?.turnId || '');
  const deliveredTurnId = String(link?.lastDeliveredTurnId || '');
  if (['completed', 'interrupted'].includes(link?.turnState)
    && observed.turnState === 'interrupted'
    && observedTurnId
    && observedTurnId === deliveredTurnId) {
    return { turnState: 'completed', turnOwner: 'none', actionRequired: 'none' };
  }
  return observed;
}

function taskLinkSnapshotRequiresSync(link, snapshot) {
  const next = recoveredTaskLinkPublicState(link, snapshot);
  const pendingRevision = planImplementationRevision(snapshot?.pendingPlanImplementation);
  if (String(link.pendingPlanRevision || '') !== pendingRevision
    || String(link.pendingPlanTurnId || '') !== String(snapshot?.pendingPlanImplementation?.turnId || '')) {
    return true;
  }
  const nextOwner = recoveredRunningTurnOwner(link, next, snapshot?.turnId);
  if (link.turnState !== next.turnState
    || link.turnOwner !== nextOwner
    || link.actionRequired !== next.actionRequired) return true;
  const observedMode = String(snapshot?.journalTurn?.collaborationMode?.mode || '');
  if (observedMode && observedMode !== String(link.activeTurnMode || '')) return true;

  const observedTurnId = String(snapshot?.turnId || link.activeTurnId || '');
  if (Array.isArray(snapshot?.changedFilePaths)) {
    const progress = taskLinkProgressForTurn(link.progress, observedTurnId);
    if (Number(progress.changedFiles || 0) !== new Set(snapshot.changedFilePaths).size) return true;
  }
  if (['running', 'waiting_input', 'desktop_action_required'].includes(next.turnState)) {
    if (String(link.activeTurnId || '') !== observedTurnId) return true;
    const planText = publicProgressText(snapshot?.planText, 3000);
    if (planText && planText !== publicProgressText(link.progress?.plan, 3000)) return true;
    const lastMessage = snapshot?.lastMessagePhase === 'commentary'
      ? publicProgressText(snapshot.lastMessage) : '';
    return Boolean(lastMessage && lastMessage !== publicProgressText(link.detailSummary));
  }
  if (link.activeTurnId) return true;

  const planText = publicProgressText(snapshot?.planText, 3000);
  if (planText && planText !== publicProgressText(link.progress?.plan, 3000)) return true;
  const finalText = String(snapshot?.finalText || '').trim();
  if (Number.isInteger(snapshot?.turnDurationSeconds)
    && snapshot.turnDurationSeconds !== Number(link.progress?.durationSeconds)) return true;
  return Boolean(link.rootMessageId
    && observedTurnId
    && finalText
    && observedTurnId !== String(link.lastDeliveredTurnId || ''));
}

function taskLinkFollowupProjection(link, now = new Date()) {
  const turnState = String(link?.turnState || 'idle');
  const continuingTurn = ['running', 'waiting_input', 'desktop_action_required'].includes(turnState);
  const answering = turnState === 'waiting_input';
  const detail = answering
    ? '已收到回答，Codex 正在继续。'
    : continuingTurn
      ? '已收到补充，正在送入当前 Codex 轮次。'
      : '已收到追问，正在启动同一 Codex 任务的新轮次。';
  const phase = answering ? '正在提交回答' : continuingTurn ? '正在提交补充' : '启动中';
  const startedAt = continuingTurn
    ? String(link?.progress?.startedAt || '')
    : now.toISOString();
  return {
    turnState: 'running',
    turnOwner: continuingTurn && link?.turnOwner && link.turnOwner !== 'none'
      ? link.turnOwner : 'bridge',
    actionRequired: 'none',
    activeTurnId: continuingTurn ? String(link?.activeTurnId || '') : '',
    pendingMessageId: '',
    detailSummary: detail,
    progress: continuingTurn ? {
      ...link?.progress,
      phase,
      detail,
      startedAt,
    } : {
      phase,
      detail,
      changedFiles: 0,
      testStatus: '未运行',
      plan: '',
      startedAt,
      durationSeconds: undefined,
    },
  };
}

function taskLinkSubmittedTurnMode(link, snapshot, requestedMode = '') {
  const mode = String(requestedMode || link?.nextTurnMode || 'default');
  if (!['default', 'plan'].includes(mode)) throw new Error('unsupported task collaboration mode');
  const startableStates = new Set(['idle', 'completed', 'failed', 'interrupted']);
  const storedState = String(link?.turnState || '');
  const observedState = String(snapshot?.publicState?.turnState || '');
  if (!startableStates.has(storedState) || !startableStates.has(observedState)) {
    throw new Error('任务状态已变化，请在最新卡片重新提交');
  }
  return mode;
}

function taskLinkCollaborationMode(link, snapshot, modeOverride = '') {
  const mode = String(modeOverride || link?.nextTurnMode || 'default');
  if (!['default', 'plan'].includes(mode)) throw new Error('unsupported task collaboration mode');
  const journal = snapshot?.journalTurn || {};
  const current = journal.collaborationMode || {};
  const model = String(current.settings?.model || journal.model || '').trim();
  if (!model) throw new Error('无法读取该 Codex 任务当前模型，不能安全切换协作模式');
  const reasoningEffort = journal.effort ?? current.settings?.reasoning_effort ?? null;
  return {
    mode,
    settings: {
      model,
      reasoning_effort: reasoningEffort == null ? null : String(reasoningEffort),
      developer_instructions: null,
    },
  };
}

function taskLinkPlanImplementationRequest(link, snapshot) {
  const threadId = String(link?.threadId || '').trim();
  const cwd = String(link?.cwd || '').trim();
  const pendingPlan = snapshot?.pendingPlanImplementation;
  if (!threadId || !path.isAbsolute(cwd) || !pendingPlan) {
    throw new Error('无法构造待执行计划的 Codex 请求');
  }
  return {
    threadId,
    cwd,
    input: [{ type: 'text', text: planImplementationPrompt(pendingPlan.planContent) }],
    collaborationMode: taskLinkCollaborationMode(link, snapshot, 'default'),
  };
}

function validateAuthoritativeThread(result) {
  const thread = threadFromResult(result);
  const threadId = String(thread?.id || '').trim();
  const cwd = String(thread?.cwd || '').trim();
  if (!threadId) throw new Error('Codex thread was not found');
  if (thread.parentThreadId || thread.parent_thread_id) throw new Error('child Codex threads cannot be linked directly');
  if (!path.isAbsolute(cwd)) throw new Error('Codex thread has no authoritative local working directory');
  return thread;
}

function taskInput(text, inbound) {
  const assets = Array.isArray(inbound?.assets) ? inbound.assets : [];
  const images = assets.filter((asset) => asset.resourceType === 'image').map((asset) => ({
    type: 'localImage',
    path: asset.localPath,
  }));
  const files = assets.filter((asset) => asset.resourceType !== 'image');
  const fileContext = files.length ? [
    '',
    '飞书桥已将本轮附件暂存为以下本机只读输入。请按用户意图读取这些文件；不要移动、改写或删除它们：',
    ...files.map((asset, index) => (
      `${index + 1}. ${asset.displayName} (${asset.messageType}/${asset.resourceType}, ${asset.sizeBytes} bytes): ${asset.localPath}`
    )),
  ].join('\n') : '';
  const prompt = `${String(text || '').trim() || '请分析我发送的附件并给出有用的回复。'}${fileContext}`;
  return [{ type: 'text', text: prompt }, ...images];
}

function safeQuestionSummary(questions) {
  return (Array.isArray(questions) ? questions : []).filter((question) => !question.isSecret).map((question) => [
    `${question.header || '需要输入'}：${question.question || ''}`,
    ...(question.options || []).map((option) => `• ${option.label}`),
    '回复格式：' + `${question.id}: 你的回答`,
  ].join('\n')).join('\n\n');
}

module.exports = {
  PLAN_COMPLETION_SNAPSHOT_DELAYS_MS,
  PLAN_IMPLEMENTATION_PREFIX,
  changedFilePathsFromEvent,
  changedFilePathsFromTurn,
  finalAnswerTextFromTurn,
  finalTextFromTurn,
  latestTurn,
  planImplementationPrompt,
  planImplementationRevision,
  projectDesktopTaskSnapshot,
  publicPlanText,
  publicPlanTextFromItem,
  publicProgressFromEvent,
  publicProgressText,
  publicUserMessageText,
  publicTurnTiming,
  publicTurnState,
  reconcilePlanTurnCompletion,
  recoveredRunningTurnOwner,
  recoveredTaskLinkPublicState,
  safeQuestionSummary,
  terminalTaskLinkDetail,
  taskLinkCollaborationMode,
  taskLinkSubmittedTurnMode,
  taskLinkPlanImplementationRequest,
  taskLinkFollowupProjection,
  taskLinkProgressForTurn,
  taskLinkSnapshotRequiresSync,
  taskInput,
  threadFromResult,
  turnsFromResult,
  validateAuthoritativeThread,
};
