const assert = require('node:assert/strict');
const test = require('node:test');

const {
  PLAN_COMPLETION_SNAPSHOT_DELAYS_MS,
  changedFilePathsFromEvent,
  changedFilePathsFromTurn,
  finalAnswerTextFromTurn,
  finalTextFromTurn,
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
  terminalTaskLinkDetail,
  taskLinkFollowupProjection,
  taskLinkPlanImplementationRequest,
  taskLinkProgressForTurn,
  taskLinkCollaborationMode,
  taskLinkSubmittedTurnMode,
  taskLinkSnapshotRequiresSync,
  taskInput,
  validateAuthoritativeThread,
} = require('../lib/codex-task-control');

test('Plan completion reconciliation recognizes an immediately available pending plan', async () => {
  const snapshot = {
    turnId: 'turn-plan',
    pendingPlanImplementation: { turnId: 'turn-plan', planContent: '# 实施计划' },
  };
  const waits = [];
  const result = await reconcilePlanTurnCompletion({
    resultTurnId: 'turn-plan',
    readSnapshot: async () => snapshot,
    wait: async (delayMs) => waits.push(delayMs),
  });
  assert.deepEqual(PLAN_COMPLETION_SNAPSHOT_DELAYS_MS, [0, 250, 750]);
  assert.equal(result.kind, 'plan_ready');
  assert.equal(result.snapshot, snapshot);
  assert.equal(result.attempts, 1);
  assert.equal(result.readFailures, 0);
  assert.deepEqual(waits, []);
});

test('Plan completion reconciliation waits for delayed Desktop plan projection', async () => {
  const snapshots = [
    { turnId: 'turn-plan', pendingPlanImplementation: null },
    { turnId: 'turn-plan', pendingPlanImplementation: null },
    {
      turnId: 'turn-plan',
      pendingPlanImplementation: { turnId: 'turn-plan', planContent: '延迟出现的计划' },
    },
  ];
  const waits = [];
  const result = await reconcilePlanTurnCompletion({
    resultTurnId: 'turn-plan',
    readSnapshot: async () => snapshots.shift(),
    wait: async (delayMs) => waits.push(delayMs),
  });
  assert.equal(result.kind, 'plan_ready');
  assert.equal(result.attempts, 3);
  assert.deepEqual(waits, [250, 750]);
});

test('Plan completion reconciliation preserves a newer authoritative turn', async () => {
  const snapshot = {
    turnId: 'turn-new',
    publicState: { turnState: 'running', turnOwner: 'desktop', actionRequired: 'none' },
    pendingPlanImplementation: null,
  };
  const result = await reconcilePlanTurnCompletion({
    resultTurnId: 'turn-plan',
    readSnapshot: async () => snapshot,
    wait: async () => {},
  });
  assert.equal(result.kind, 'superseded');
  assert.equal(result.snapshot, snapshot);
  assert.equal(result.attempts, 1);
});

test('Plan completion reconciliation safely completes when no pending plan appears', async () => {
  let reads = 0;
  const result = await reconcilePlanTurnCompletion({
    resultTurnId: 'turn-plan',
    readSnapshot: async () => {
      reads += 1;
      return { turnId: 'turn-plan', pendingPlanImplementation: null };
    },
    wait: async () => {},
  });
  assert.equal(result.kind, 'completed');
  assert.equal(result.attempts, 3);
  assert.equal(result.readFailures, 0);
  assert.equal(reads, 3);
});

test('Plan completion reconciliation fails closed when Desktop stays unavailable', async () => {
  const result = await reconcilePlanTurnCompletion({
    resultTurnId: 'turn-plan',
    readSnapshot: async () => { throw new Error('Desktop unavailable'); },
    wait: async () => {},
  });
  assert.equal(result.kind, 'completed');
  assert.equal(result.snapshot, null);
  assert.equal(result.attempts, 3);
  assert.equal(result.readFailures, 3);
});

test('changed file metadata counts unique paths instead of file-change events', () => {
  assert.deepEqual(changedFilePathsFromEvent({
    method: 'item/completed',
    params: {
      item: {
        type: 'fileChange',
        changes: [
          { path: 'lib/progress-card.js', kind: 'update' },
          { path: 'lib/progress-card.js', kind: 'update' },
          { path: 'test\\progress-card.test.js', kind: 'update' },
        ],
      },
    },
  }), ['lib/progress-card.js', 'test/progress-card.test.js']);
  assert.deepEqual(changedFilePathsFromEvent({
    method: 'item/completed',
    params: { item: { type: 'commandExecution', command: 'edit README.md' } },
  }), []);
  assert.deepEqual(changedFilePathsFromEvent({
    method: 'item/completed',
    params: { item: { type: 'fileChange', changes: [] } },
  }), []);
  assert.deepEqual(changedFilePathsFromTurn({
    items: [
      { type: 'fileChange', changes: [{ path: 'lib/a.js' }, { path: 'lib/b.js' }] },
      { type: 'fileChange', changes: [{ path: 'lib/a.js' }] },
    ],
  }), ['lib/a.js', 'lib/b.js']);
});

test('per-turn file metadata resets when the authoritative turn changes', () => {
  const previous = {
    phase: '完成', changedFiles: 11, changedFilesTurnId: 'turn-old', testStatus: '全部通过',
  };
  assert.deepEqual(taskLinkProgressForTurn(previous, 'turn-old'), previous);
  assert.deepEqual(taskLinkProgressForTurn(previous, 'turn-new'), {
    phase: '完成', changedFiles: 0, changedFilesTurnId: 'turn-new', testStatus: '未运行',
  });
  assert.deepEqual(taskLinkProgressForTurn(previous, 'turn-new', ['a.js', 'a.js', 'b.js']), {
    phase: '完成', changedFiles: 2, changedFilesTurnId: 'turn-new', testStatus: '未运行',
  });
});

test('terminal task cards recover the full authoritative reply instead of a stored summary', () => {
  const link = {
    turnState: 'completed',
    detailSummary: '摘要…',
    progress: { detail: '前 500 字…' },
  };
  const fullReply = '完整回答'.repeat(1200);
  assert.equal(terminalTaskLinkDetail(link, {
    publicState: { turnState: 'completed' },
    finalText: fullReply,
  }, link.progress.detail), fullReply);
  assert.equal(terminalTaskLinkDetail(
    { ...link, turnState: 'running' },
    { publicState: { turnState: 'running' }, finalText: fullReply },
    '当前进展',
  ), '当前进展');
});

test('task-link follow-ups leave a terminal card immediately and preserve active-turn context', () => {
  const starting = taskLinkFollowupProjection({
    turnState: 'completed', turnOwner: 'none', actionRequired: 'none', activeTurnId: '',
    progress: { phase: '完成', detail: '上一轮已完成。', durationSeconds: 18 },
  }, new Date('2026-09-02T13:00:37.000Z'));
  assert.deepEqual(starting, {
    turnState: 'running',
    turnOwner: 'bridge',
    actionRequired: 'none',
    activeTurnId: '',
    pendingMessageId: '',
    detailSummary: '已收到追问，正在启动同一 Codex 任务的新轮次。',
    progress: {
      phase: '启动中',
      detail: '已收到追问，正在启动同一 Codex 任务的新轮次。',
      changedFiles: 0,
      testStatus: '未运行',
      plan: '',
      startedAt: '2026-09-02T13:00:37.000Z',
      durationSeconds: undefined,
    },
  });

  const steering = taskLinkFollowupProjection({
    turnState: 'running', turnOwner: 'desktop', actionRequired: 'none', activeTurnId: 'turn-live',
    progress: { phase: '当前进展', detail: '正在检查。', startedAt: '2026-09-02T12:59:00.000Z' },
  }, new Date('2026-09-02T13:00:37.000Z'));
  assert.equal(steering.turnOwner, 'desktop');
  assert.equal(steering.activeTurnId, 'turn-live');
  assert.equal(steering.progress.phase, '正在提交补充');
  assert.equal(steering.progress.startedAt, '2026-09-02T12:59:00.000Z');
});

test('new-turn mode submissions fail closed when the authoritative task state changes', () => {
  const link = { turnState: 'completed', nextTurnMode: 'default' };
  assert.equal(taskLinkSubmittedTurnMode(
    link,
    { publicState: { turnState: 'completed' } },
    'plan',
  ), 'plan');
  assert.equal(taskLinkSubmittedTurnMode(
    link,
    { publicState: { turnState: 'completed' } },
  ), 'default');
  assert.throws(() => taskLinkSubmittedTurnMode(
    link,
    { publicState: { turnState: 'running' } },
    'plan',
  ), /任务状态已变化/);
  assert.throws(() => taskLinkSubmittedTurnMode(
    link,
    { publicState: { turnState: 'running' } },
  ), /任务状态已变化/);
  assert.throws(() => taskLinkSubmittedTurnMode(
    { ...link, turnState: 'plan_ready' },
    { publicState: { turnState: 'plan_ready' } },
    'default',
  ), /任务状态已变化/);
  assert.throws(() => taskLinkSubmittedTurnMode(
    link,
    { publicState: { turnState: 'completed' } },
    'unsafe',
  ), /unsupported task collaboration mode/);
});

test('thread projection distinguishes running, desktop-required, and terminal states', () => {
  assert.deepEqual(
    publicTurnState({ status: { type: 'active' } }, { id: 'turn-1', status: 'inProgress' }),
    { turnState: 'running', turnOwner: 'desktop', actionRequired: 'none' },
  );
  assert.deepEqual(
    publicTurnState({ status: { type: 'active', activeFlags: { waitingOnUserInput: true } } }, { id: 'turn-1', status: 'inProgress' }),
    { turnState: 'desktop_action_required', turnOwner: 'desktop', actionRequired: 'desktop' },
  );
  assert.equal(publicTurnState({ status: { type: 'idle' } }, { status: 'completed' }).turnState, 'completed');
});

test('turn timing prefers the Desktop journal and exposes bounded elapsed seconds', () => {
  assert.deepEqual(publicTurnTiming({
    startedAt: '2026-09-02T08:00:00Z', completedAt: '2026-09-02T08:00:45Z',
  }), {
    startedAt: '2026-09-02T08:00:00.000Z',
    completedAt: '2026-09-02T08:00:45.000Z',
    durationSeconds: 45,
  });
  assert.equal(publicTurnTiming(null, {
    startedAt: '2026-09-02T08:45:50Z', completedAt: '2026-09-02T08:46:01Z',
  }).durationSeconds, 11);
  assert.equal(publicTurnTiming({ startedAt: 'invalid', completedAt: 'invalid' }).durationSeconds, undefined);
});

test('final extraction prefers final_answer and falls back to the last agent message', () => {
  const turn = { items: [
    { type: 'agentMessage', phase: 'commentary', text: '处理中' },
    { type: 'agentMessage', text: '兼容终态' },
    { type: 'agentMessage', phase: 'final_answer', text: '最终结果' },
  ] };
  assert.equal(finalTextFromTurn(turn), '最终结果');
  assert.equal(finalAnswerTextFromTurn(turn), '最终结果');
  assert.equal(finalTextFromTurn({ items: turn.items.slice(0, 2) }), '兼容终态');
  assert.equal(finalAnswerTextFromTurn({ items: turn.items.slice(0, 2) }), '');
});

test('Desktop journal state overrides the provisional interrupted app-server projection', () => {
  const snapshot = {
    turn: {
      id: 'turn-2',
      status: 'interrupted',
      items: [{ type: 'agentMessage', phase: 'commentary', text: '仍在处理' }],
    },
    publicState: { turnState: 'interrupted', turnOwner: 'none', actionRequired: 'none' },
  };
  const running = projectDesktopTaskSnapshot(snapshot, {
    turnId: 'turn-2', state: 'running', finalText: '', lastMessage: '仍在处理',
  });
  assert.deepEqual(running.publicState, {
    turnState: 'running', turnOwner: 'desktop', actionRequired: 'none',
  });
  assert.equal(running.turnId, 'turn-2');

  const completed = projectDesktopTaskSnapshot(snapshot, {
    turnId: 'turn-2', state: 'completed', finalText: '最终结果', lastMessage: '最终结果',
    startedAt: '2026-09-02T08:45:50Z', completedAt: '2026-09-02T08:46:01Z',
  });
  assert.equal(completed.publicState.turnState, 'completed');
  assert.equal(completed.finalText, '最终结果');
  assert.equal(completed.turnDurationSeconds, 11);
});

test('a delivered completed turn cannot regress to interrupted during Desktop recovery', () => {
  const link = {
    turnState: 'completed', turnOwner: 'none', actionRequired: 'none',
    lastDeliveredTurnId: 'turn-external', activeTurnId: '',
    pendingPlanRevision: '', pendingPlanTurnId: '',
    progress: { durationSeconds: 2, plan: '', changedFiles: 0 },
  };
  const interrupted = {
    publicState: { turnState: 'interrupted', turnOwner: 'none', actionRequired: 'none' },
    turnId: 'turn-external', turnDurationSeconds: 2, finalText: '', planText: '',
    pendingPlanImplementation: null,
  };
  assert.deepEqual(recoveredTaskLinkPublicState(link, interrupted), {
    turnState: 'completed', turnOwner: 'none', actionRequired: 'none',
  });
  assert.equal(taskLinkSnapshotRequiresSync(link, interrupted), false);
  assert.equal(recoveredTaskLinkPublicState({ ...link, turnState: 'interrupted' }, interrupted).turnState, 'completed');
  assert.equal(recoveredTaskLinkPublicState(link, {
    ...interrupted,
    turnId: 'turn-new',
  }).turnState, 'interrupted');
});

test('Desktop journal projects a pending Plan question as Feishu-answerable', () => {
  const snapshot = projectDesktopTaskSnapshot({
    turn: { id: 'turn-plan', status: 'inProgress', items: [] },
    publicState: { turnState: 'desktop_action_required', turnOwner: 'desktop', actionRequired: 'desktop' },
  }, {
    turnId: 'turn-plan',
    state: 'waiting_input',
    pendingInput: {
      requestId: 'call-choice-1',
      questions: [{ id: 'choice', question: '请选择', options: [{ label: 'A' }] }],
    },
  });

  assert.deepEqual(snapshot.publicState, {
    turnState: 'waiting_input', turnOwner: 'desktop', actionRequired: 'feishu',
  });
  assert.equal(snapshot.journalTurn.pendingInput.requestId, 'call-choice-1');
});

test('running task recovery preserves the persisted owner only for the same turn', () => {
  const bridgeLink = {
    turnState: 'running', turnOwner: 'bridge', actionRequired: 'none', activeTurnId: 'turn-feishu',
  };
  const observed = {
    turnState: 'running', turnOwner: 'desktop', actionRequired: 'none',
  };

  assert.equal(recoveredRunningTurnOwner(bridgeLink, observed, 'turn-feishu'), 'bridge');
  assert.equal(taskLinkSnapshotRequiresSync(bridgeLink, {
    publicState: observed,
    turnId: 'turn-feishu',
  }), false);
  assert.equal(recoveredRunningTurnOwner(bridgeLink, observed, 'turn-desktop'), 'desktop');
  assert.equal(recoveredRunningTurnOwner(bridgeLink, {
    turnState: 'waiting_input', turnOwner: 'desktop', actionRequired: 'feishu',
  }, 'turn-feishu'), 'bridge');
  assert.equal(recoveredRunningTurnOwner(bridgeLink, {
    turnState: 'completed', turnOwner: 'none', actionRequired: 'none',
  }, 'turn-feishu'), 'none');
});

test('Desktop task projection carries the generated plan independently from replies', () => {
  const plan = '- [x] 读取上下文\n- [ ] 实现飞书模式切换';
  const snapshot = projectDesktopTaskSnapshot({
    turn: { id: 'turn-plan', status: 'completed', items: [] },
    publicState: { turnState: 'completed', turnOwner: 'none', actionRequired: 'none' },
  }, {
    turnId: 'turn-plan', state: 'completed', finalText: '计划已经完成。',
    lastMessage: '计划已经完成。', planText: plan,
  });
  assert.equal(snapshot.planText, plan);
  assert.equal(snapshot.finalText, '计划已经完成。');
});

test('pending plan implementation projects plan_ready with a stable non-secret revision', () => {
  const pendingPlanImplementation = {
    turnId: 'turn-plan',
    planContent: '# 实施计划\n\n1. 修改协议\n2. 运行测试',
  };
  const snapshot = projectDesktopTaskSnapshot({
    thread: { pendingPlanImplementation },
    turn: { id: 'turn-plan', status: 'completed', items: [] },
    publicState: { turnState: 'completed', turnOwner: 'none', actionRequired: 'none' },
  }, {
    turnId: 'turn-plan', state: 'completed', finalText: '计划已完成。',
    collaborationMode: { mode: 'plan', settings: { model: 'gpt-5.6-sol' } },
  });
  assert.deepEqual(snapshot.publicState, {
    turnState: 'plan_ready', turnOwner: 'none', actionRequired: 'feishu',
  });
  assert.equal(snapshot.planText, pendingPlanImplementation.planContent);
  assert.equal(planImplementationRevision(pendingPlanImplementation).length, 20);
  assert.equal(planImplementationRevision(pendingPlanImplementation).includes('实施计划'), false);
  assert.equal(
    planImplementationPrompt(pendingPlanImplementation.planContent),
    `PLEASE IMPLEMENT THIS PLAN:\n${pendingPlanImplementation.planContent}`,
  );
  assert.throws(() => planImplementationPrompt(''), /没有可执行计划/);
});

test('pending plan revisions force task-card synchronization and default execution preserves model settings', () => {
  const pendingPlanImplementation = { turnId: 'turn-plan', planContent: '执行这份计划' };
  const revision = planImplementationRevision(pendingPlanImplementation);
  const snapshot = {
    publicState: { turnState: 'plan_ready', turnOwner: 'none', actionRequired: 'feishu' },
    pendingPlanImplementation,
    journalTurn: {
      collaborationMode: {
        mode: 'plan', settings: { model: 'gpt-5.6-sol', reasoning_effort: 'high' },
      },
    },
  };
  const link = {
    turnState: 'plan_ready', turnOwner: 'none', actionRequired: 'feishu',
    pendingPlanTurnId: 'turn-plan', pendingPlanRevision: revision,
    activeTurnMode: 'plan', nextTurnMode: 'plan',
  };
  assert.equal(taskLinkSnapshotRequiresSync(link, snapshot), false);
  assert.equal(taskLinkSnapshotRequiresSync({ ...link, pendingPlanRevision: '0'.repeat(20) }, snapshot), true);
  assert.deepEqual(taskLinkCollaborationMode({ nextTurnMode: 'plan' }, snapshot, 'default'), {
    mode: 'default',
    settings: {
      model: 'gpt-5.6-sol', reasoning_effort: 'high', developer_instructions: null,
    },
  });
  assert.deepEqual(taskLinkPlanImplementationRequest({
    threadId: 'thread-original', cwd: '/project/original', nextTurnMode: 'plan',
  }, snapshot), {
    threadId: 'thread-original',
    cwd: '/project/original',
    input: [{ type: 'text', text: 'PLEASE IMPLEMENT THIS PLAN:\n执行这份计划' }],
    collaborationMode: {
      mode: 'default',
      settings: {
        model: 'gpt-5.6-sol', reasoning_effort: 'high', developer_instructions: null,
      },
    },
  });
});

test('unchanged terminal task snapshots do not replace a card while the user is typing', () => {
  const completedLink = {
    turnState: 'completed',
    turnOwner: 'none',
    actionRequired: 'none',
    activeTurnId: '',
    lastDeliveredTurnId: 'turn-2',
    rootMessageId: 'message-card',
  };
  const completedSnapshot = {
    publicState: { turnState: 'completed', turnOwner: 'none', actionRequired: 'none' },
    turnId: 'turn-2',
    finalText: '最终结果',
  };

  assert.equal(taskLinkSnapshotRequiresSync(completedLink, completedSnapshot), false);
  assert.equal(taskLinkSnapshotRequiresSync({
    ...completedLink,
    turnState: 'running',
    turnOwner: 'desktop',
    activeTurnId: 'turn-2',
    lastDeliveredTurnId: '',
  }, completedSnapshot), true);
  assert.equal(taskLinkSnapshotRequiresSync({
    ...completedLink,
    lastDeliveredTurnId: 'turn-1',
  }, completedSnapshot), true);
  assert.equal(taskLinkSnapshotRequiresSync({
    ...completedLink,
    turnState: 'running',
    turnOwner: 'bridge',
    activeTurnId: 'turn-3',
    lastDeliveredTurnId: 'turn-2',
  }, {
    publicState: { turnState: 'running', turnOwner: 'bridge', actionRequired: 'none' },
    turnId: 'turn-3',
    lastMessage: '仅进展内容发生变化',
  }), false);
  assert.equal(taskLinkSnapshotRequiresSync({
    ...completedLink,
    progress: { durationSeconds: 10 },
  }, {
    ...completedSnapshot,
    turnDurationSeconds: 11,
  }), true);
  assert.equal(taskLinkSnapshotRequiresSync({
    ...completedLink,
    progress: { plan: '' },
  }, {
    ...completedSnapshot,
    planText: '- [ ] 最终计划',
  }), true);
});

test('running snapshots only request a card update for new public commentary', () => {
  const link = {
    turnState: 'running',
    turnOwner: 'desktop',
    actionRequired: 'none',
    activeTurnId: 'turn-3',
    detailSummary: '正在检查事件链路。',
  };
  const base = {
    publicState: { turnState: 'running', turnOwner: 'desktop', actionRequired: 'none' },
    turnId: 'turn-3',
    lastMessagePhase: 'commentary',
  };
  assert.equal(taskLinkSnapshotRequiresSync(link, {
    ...base,
    lastMessage: '正在检查事件链路。',
  }), false);
  assert.equal(taskLinkSnapshotRequiresSync(link, {
    ...base,
    lastMessage: '已经定位到卡片更新入口。',
  }), true);
  assert.equal(taskLinkSnapshotRequiresSync(link, {
    ...base,
    lastMessagePhase: 'final_answer',
    lastMessage: '最终结果不作为运行中进展。',
  }), false);
  assert.equal(taskLinkSnapshotRequiresSync({
    ...link,
    progress: { plan: '- [ ] 旧计划' },
  }, {
    ...base,
    lastMessage: '正在检查事件链路。',
    planText: '- [ ] 新计划',
  }), true);
});

test('public progress projection keeps commentary and plan updates but hides raw activity detail', () => {
  assert.deepEqual(publicProgressFromEvent({
    method: 'item/completed',
    params: { item: { type: 'agentMessage', phase: 'commentary', text: '我正在检查桥接事件。' } },
  }), {
    phase: '当前进展', detail: '我正在检查桥接事件。', kind: 'commentary',
  });
  assert.deepEqual(publicProgressFromEvent({
    method: 'turn/plan/updated',
    params: { plan: [
      { step: '读取事件', status: 'completed' },
      { step: '更新卡片', status: 'inProgress' },
    ] },
  }), {
    phase: '计划 2/2', detail: '更新卡片', kind: 'plan',
    plan: '- [x] 读取事件\n- [ ] **更新卡片**（进行中）',
  });
  assert.deepEqual(publicProgressFromEvent({
    method: 'item/completed',
    params: { item: { type: 'plan', text: '- [ ] 调研现状\n- [ ] 提交实施计划' } },
  }), {
    phase: '计划已生成', detail: '计划已同步到飞书。', kind: 'plan',
    plan: '- [ ] 调研现状\n- [ ] 提交实施计划',
  });
  assert.equal(publicPlanText([
    { step: '读取事件', status: 'completed' },
    { step: '生成计划', status: 'inProgress' },
  ]), '- [x] 读取事件\n- [ ] **生成计划**（进行中）');
  assert.equal(publicPlanTextFromItem({ type: 'plan', text: '实施计划正文' }), '实施计划正文');
  assert.deepEqual(publicProgressFromEvent({
    method: 'item/started',
    params: { item: { type: 'commandExecution', command: 'cat /private/secret' } },
  }), {
    phase: '执行中', detail: '正在执行任务所需操作。', kind: 'activity',
  });
  assert.equal(publicProgressText('第一行\n\n\n第二行'), '第一行\n\n第二行');
});

test('task collaboration modes inherit the authoritative model and reasoning effort', () => {
  assert.deepEqual(taskLinkCollaborationMode({ nextTurnMode: 'plan' }, {
    journalTurn: { model: 'gpt-5.5', effort: 'high' },
  }), {
    mode: 'plan',
    settings: {
      model: 'gpt-5.5', reasoning_effort: 'high', developer_instructions: null,
    },
  });
  assert.throws(() => taskLinkCollaborationMode({ nextTurnMode: 'plan' }, {
    journalTurn: {},
  }), /当前模型/);
});

test('Desktop user messages expose only the explicit request and redact likely secrets', () => {
  assert.equal(publicUserMessageText({
    type: 'message',
    role: 'user',
    content: [
      { type: 'input_text', text: '# Files mentioned by the user:\n\n附件路径\n\n## My request:\n请把我的消息显示在进展之前。' },
      { type: 'input_text', text: '<image name="one">' },
      { type: 'input_text', text: '</image>' },
    ],
  }), '请把我的消息显示在进展之前。');
  assert.equal(publicUserMessageText({
    type: 'message',
    role: 'user',
    content: [{ type: 'input_text', text: 'access_token: very-private-value' }],
  }), '本轮消息包含可能的敏感信息，请在 Codex Desktop 查看。');
});

test('task input uses native local images and bounded read-only paths for other attachments', () => {
  const input = taskInput('检查附件', { assets: [
    { resourceType: 'image', localPath: '/tmp/a.png', displayName: 'a.png', messageType: 'image', sizeBytes: 10 },
    { resourceType: 'file', localPath: '/tmp/a.pdf', displayName: 'a.pdf', messageType: 'file', sizeBytes: 20 },
  ] });
  assert.deepEqual(input[1], { type: 'localImage', path: '/tmp/a.png' });
  assert.match(input[0].text, /本机只读输入/);
  assert.match(input[0].text, /\/tmp\/a\.pdf/);
});

test('authoritative thread validation rejects child threads and missing local cwd', () => {
  assert.throws(() => validateAuthoritativeThread({ thread: { id: 'thread', cwd: '/tmp', parentThreadId: 'parent' } }), /child/);
  assert.throws(() => validateAuthoritativeThread({ thread: { id: 'thread', cwd: 'relative' } }), /working directory/);
});
