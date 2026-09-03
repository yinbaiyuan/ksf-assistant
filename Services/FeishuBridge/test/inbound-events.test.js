const assert = require('node:assert/strict');
const test = require('node:test');
const {
  bridgeCardAction,
  normalizeCardAction,
  normalizeEventKey,
} = require('../lib/inbound-events');
const {
  CARD_REQUEST_SAFE_BYTES,
  TASK_LINK_CARD_REVISION,
  cardRequestBytes,
  fitProgressCardToRequestBudget,
  formatElapsedDuration,
  progressCard,
} = require('../lib/progress-card');
const taskLinkCardContract = require('./fixtures/task-link-card-interaction-contract-v1.json');

function cardElements(card, tag) {
  const matches = [];
  function visit(value) {
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    if (!value || typeof value !== 'object') return;
    if (value.tag === tag) matches.push(value);
    Object.values(value).forEach(visit);
  }
  visit(card);
  return matches;
}

test('language-neutral task-link card contract freezes revision 31 interactions', () => {
  assert.equal(taskLinkCardContract.schemaVersion, 1);
  assert.equal(taskLinkCardContract.cardRevision, TASK_LINK_CARD_REVISION);
  assert.deepEqual(taskLinkCardContract.states.running.topActions, ['task_link_release']);
  assert.deepEqual(taskLinkCardContract.states.running.inputRow, [
    'task_link_interrupt', 'followup', 'task_link_followup',
  ]);
  assert.deepEqual(taskLinkCardContract.states.completed.formFields, ['turnMode', 'followup']);
  assert.deepEqual(taskLinkCardContract.states.completed.turnModes, ['default', 'plan']);
  assert.equal(taskLinkCardContract.states.plan_ready.primaryAction, 'task_link_implement_plan');
  assert.equal(taskLinkCardContract.states.plan_ready.submitLabel, '提交修改');
  assert.deepEqual(taskLinkCardContract.states.queued.formFields, []);
  assert.deepEqual(taskLinkCardContract.callbacks.task_link_followup.intents, ['new_turn']);
  assert.equal(taskLinkCardContract.callbacks.task_link_release.transport, 'button');
  assert.equal(taskLinkCardContract.callbacks.dismiss.transport, 'overflow');
});

test('event routing accepts only the reviewed message and card event keys', () => {
  assert.equal(normalizeEventKey('im.message.receive_v1'), 'im.message.receive_v1');
  assert.equal(normalizeEventKey('card.action.trigger'), 'card.action.trigger');
  assert.equal(normalizeEventKey('approval.approval.updated_v4'), '');
});

test('card actions are namespaced, bounded, and reject arbitrary operations', () => {
  const normalized = normalizeCardAction({
    event_id: 'evt_one',
    operator_id: 'ou_operator',
    chat_id: 'oc_chat',
    message_id: 'om_card',
    token: 'private_token',
    action_value: JSON.stringify({ namespace: 'feishu_bridge', version: 1, action: 'task_status', taskId: 'TASK-20260901-ABCD' }),
  });
  assert.equal(normalized.eventId, 'evt_one');
  assert.deepEqual(bridgeCardAction(normalized), { action: 'task_status', taskId: 'TASK-20260901-ABCD' });
  assert.equal(bridgeCardAction({ actionValue: { namespace: 'feishu_bridge', version: 1, action: 'send_message' } }), null);
  assert.equal(bridgeCardAction({ actionValue: { namespace: 'other', version: 1, action: 'status' } }), null);
});

test('official SDK card callback shape normalizes into the existing bridge contract', () => {
  const normalized = normalizeCardAction({
    schema: '2.0',
    event_id: 'evt_official',
    token: 'callback_token',
    operator: { open_id: 'ou_operator' },
    context: { open_chat_id: 'oc_chat', open_message_id: 'om_card' },
    action: {
      tag: 'button',
      value: { namespace: 'feishu_bridge', version: 1, action: 'dismiss' },
      form_value: { choice: 'one' },
    },
  });
  assert.equal(normalized.eventId, 'evt_official');
  assert.equal(normalized.operatorId, 'ou_operator');
  assert.equal(normalized.chatId, 'oc_chat');
  assert.equal(normalized.messageId, 'om_card');
  assert.equal(normalized.token, 'callback_token');
  assert.deepEqual(normalized.formValue, { choice: 'one' });
  assert.deepEqual(bridgeCardAction(normalized), { action: 'dismiss', taskId: '' });
});

test('overflow options reuse existing namespaced actions and reject malformed values', () => {
  const normalized = normalizeCardAction({
    event_id: 'evt_overflow',
    action: {
      tag: 'overflow',
      option: JSON.stringify({
        namespace: 'feishu_bridge', version: 1, action: 'task_link_release',
        taskKey: '0123456789abcdef0123',
      }),
    },
  });
  assert.equal(normalized.actionTag, 'overflow');
  assert.deepEqual(bridgeCardAction(normalized), {
    action: 'task_link_release', taskId: '', taskKey: '0123456789abcdef0123',
  });
  assert.equal(bridgeCardAction(normalizeCardAction({
    action: { tag: 'overflow', option: 'not-json' },
  })), null);
  assert.equal(bridgeCardAction(normalizeCardAction({
    action: { tag: 'overflow', option: JSON.stringify({ action: 'task_link_release' }) },
  })), null);
});

test('progress cards expose refresh and dismiss actions without user prompt content', () => {
  const card = progressCard({ status: 'processing', taskId: 'TASK-1', detail: 'running' });
  const serialized = JSON.stringify(card);
  assert.match(serialized, /刷新状态/);
  assert.match(serialized, /task_status/);
  assert.match(serialized, /feishu_bridge/);
  assert.ok(Buffer.byteLength(serialized) < 30 * 1024);
});

test('ordinary processing cards expose no actions that can replace the active result view', () => {
  const card = progressCard({ status: 'processing', detail: 'running' });
  const actions = card.elements.flatMap((element) => element.actions || []);
  assert.deepEqual(actions, []);
  assert.doesNotMatch(JSON.stringify(card), /刷新状态|关闭卡片/);
});

test('elapsed durations are human-readable and keep bridge uptime distinct from task timing', () => {
  assert.equal(formatElapsedDuration(0), '0 秒');
  assert.equal(formatElapsedDuration(41), '41 秒');
  assert.equal(formatElapsedDuration(13_394), '3 小时 43 分 14 秒');
  assert.equal(formatElapsedDuration(90_061), '1 天 1 小时 1 分 1 秒');
});

test('legacy bridge status actions preserve bounded task duration across refreshes', () => {
  const card = progressCard({ status: 'completed', detail: 'done', taskDurationSeconds: 41 });
  assert.equal(card.elements.flatMap((element) => element.actions || [])
    .some((item) => item.value?.action === 'status'), false);
  const statusCard = progressCard({ status: 'status', detail: 'online', taskDurationSeconds: 41 });
  const refresh = statusCard.elements.flatMap((element) => element.actions || [])
    .find((item) => item.value?.action === 'status');
  assert.deepEqual(bridgeCardAction({ actionValue: refresh.value }), {
    action: 'status', taskId: '', taskDurationSeconds: 41,
  });
  assert.equal(bridgeCardAction({ actionValue: { ...refresh.value, taskDurationSeconds: -1 } }), null);
  assert.equal(bridgeCardAction({ actionValue: { ...refresh.value, taskDurationSeconds: '41' } }), null);
  assert.equal(bridgeCardAction({ actionValue: { ...refresh.value, taskDurationSeconds: 30 * 24 * 60 * 60 + 1 } }), null);
});

test('card-only results preserve multiline content within the Feishu card size budget', () => {
  const detail = `第一行\n\n- ${'验收内容'.repeat(1200)}`;
  const fitted = fitProgressCardToRequestBudget({ status: 'completed', detail });
  const { card } = fitted;
  const detailElements = card.elements.filter((element) => element.tag === 'div');
  assert.equal(fitted.complete, true);
  assert.ok(detailElements.length > 1);
  assert.match(detailElements[0].text.content, /第一行/);
  assert.ok(detailElements.every((element) => element.text.content.length <= 2400));
  assert.ok(cardRequestBytes(card) <= CARD_REQUEST_SAFE_BYTES);

  const formerlyClipped = fitProgressCardToRequestBudget({
    status: 'completed', detail: `${'x'.repeat(9000)}尾部`,
  });
  assert.equal(formerlyClipped.complete, true);
  assert.match(JSON.stringify(formerlyClipped.card), /尾部/);

  const oversized = fitProgressCardToRequestBudget({
    status: 'completed', detail: '很长的中文回复。'.repeat(4000),
  });
  assert.equal(oversized.complete, false);
  assert.ok(oversized.displayedCodePoints > 0);
  assert.ok(cardRequestBytes(oversized.card) <= CARD_REQUEST_SAFE_BYTES);
  assert.match(JSON.stringify(oversized.card), /完整回复见后续文字消息/);
});

test('completed chat cards mirror task-card hierarchy and accept a bounded follow-up form', () => {
  const card = progressCard({
    status: 'completed', detail: 'done', latestInput: '请解释第二点',
    taskDurationSeconds: 41, followupEnabled: true,
  });
  const form = card.body.elements.find((element) => element.tag === 'form');
  const input = cardElements(form, 'input').find((element) => element.name === 'followup');
  const [quickReplyLayout] = form.elements.filter((element) => element.tag === 'column_set');
  const quickReplyElements = quickReplyLayout.columns.flatMap((column) => column.elements || []);
  const submit = quickReplyElements.find((element) => element.name === 'submit_followup');
  const dismiss = cardElements(card, 'overflow')[0];
  assert.equal(card.schema, '2.0');
  assert.equal(card.config.width_mode, 'fill');
  assert.equal(card.header.title.content, 'Codex 对话');
  assert.deepEqual(card.header.text_tag_list, [{
    tag: 'text_tag', text: { tag: 'plain_text', content: '已完成' }, color: 'green',
  }]);
  assert.deepEqual(cardElements(card, 'markdown').slice(0, 3).map((element) => element.content), [
    '**耗时 41 秒**',
    '**你**\n请解释第二点',
    '**Codex**\ndone',
  ]);
  assert.deepEqual(card.body.elements.slice(0, 6).map((element) => element.tag), [
    'column_set', 'hr', 'markdown', 'hr', 'markdown', 'hr',
  ]);
  assert.deepEqual({
    width: input.width,
    maxLength: input.max_length,
    inputType: input.input_type,
    rows: input.rows,
    maxRows: input.max_rows,
  }, {
    width: 'fill', maxLength: 1000, inputType: 'text', rows: undefined, maxRows: undefined,
  });
  assert.equal(quickReplyLayout.flex_mode, 'none');
  assert.deepEqual(quickReplyLayout.columns.map((column) => column.width), ['weighted', 'auto']);
  assert.equal(form.vertical_spacing, '8px');
  assert.equal(form.elements.length, 1);
  assert.equal(submit.text.content, '发送');
  assert.equal(submit.form_action_type, 'submit');
  assert.equal(submit.type, 'primary_filled');
  assert.equal(dismiss.options[0].text.content, '关闭卡片');
  assert.equal(JSON.stringify(card).includes('刷新状态'), false);
  assert.deepEqual(bridgeCardAction({
    actionValue: submit.behaviors[0].value,
    formValue: { followup: '请继续解释第二点' },
  }), {
    action: 'chat_followup', taskId: '', followup: '请继续解释第二点',
  });
  assert.deepEqual(bridgeCardAction(normalizeCardAction({ action: {
    tag: 'overflow', option: dismiss.options[0].value,
  } })), {
    action: 'dismiss', taskId: '',
  });
  assert.equal(bridgeCardAction({ actionValue: submit.behaviors[0].value, formValue: { followup: '  ' } }), null);
  assert.equal(bridgeCardAction({ actionValue: submit.behaviors[0].value, formValue: { followup: 'x'.repeat(1001) } }), null);
});

test('JSON 2.0 chat cards keep one schema throughout follow-up and omit unsupported root fields', () => {
  const processing = progressCard({
    status: 'processing', schemaVersion: '2.0', detail: 'running', latestInput: '测试问题',
    openIds: ['ou_private'],
  });
  const completed = progressCard({
    status: 'completed', followupEnabled: true, detail: 'done', openIds: ['ou_private'],
  });
  const dismissed = progressCard({
    status: 'dismissed', schemaVersion: '2.0', detail: 'closed', openIds: ['ou_private'],
  });

  for (const card of [processing, completed, dismissed]) {
    assert.equal(card.schema, '2.0');
    assert.equal(Object.hasOwn(card, 'open_ids'), false);
    assert.ok(Array.isArray(card.body.elements));
  }
  assert.equal(processing.body.elements.some((element) => element.tag === 'form'), false);
  assert.deepEqual(cardElements(processing, 'markdown').map((element) => element.content), [
    '**你**\n测试问题', '**Codex**\nrunning',
  ]);
  assert.equal(processing.header.title.content, 'Codex 对话');
  assert.deepEqual(processing.header.text_tag_list, [{
    tag: 'text_tag', text: { tag: 'plain_text', content: '运行中' }, color: 'blue',
  }]);
  assert.equal(completed.body.elements.some((element) => element.tag === 'form'), true);
  assert.equal(dismissed.body.elements.some((element) => element.tag === 'form'), false);
});

test('task-link cards expose bounded answers and reject forged task-link actions', () => {
  const card = progressCard({
    status: 'waiting_input',
    taskLink: {
      taskKey: '0123456789abcdef0123', expiresAt: '2026-09-02T00:00:00Z',
      linkState: 'active', turnState: 'waiting_input', turnOwner: 'bridge',
      controls: { canInterrupt: true, canRelease: true },
    },
    questions: [{ id: 'choice', header: '选择', question: '下一步？', options: [{ label: '继续', description: '继续执行' }] }],
  });
  const answer = cardElements(card, 'button')
    .find((item) => item.behaviors?.[0]?.value?.action === 'task_link_answer');
  assert.equal(answer.text.content, '选择');
  assert.equal(answer.behaviors[0].value.answer, '继续');
  assert.equal(Object.hasOwn(answer.behaviors[0].value, 'description'), false);
  assert.deepEqual(bridgeCardAction({ actionValue: answer.behaviors[0].value }), {
    action: 'task_link_answer', taskId: '', taskKey: '0123456789abcdef0123', questionId: 'choice', answer: '继续',
  });
  const optionRow = cardElements(card, 'column_set').find((element) => (
    element.columns?.some((column) => column.elements?.some((item) => item.name === 'answer_option_1'))
  ));
  assert.equal(optionRow.flex_mode, 'none');
  assert.deepEqual(optionRow.columns.map((column) => column.width), ['weighted', 'auto']);
  assert.equal(optionRow.columns[0].weight, 1);
  assert.equal(
    optionRow.columns[0].elements[0].content,
    "**继续**\n<font color='grey'>继续执行</font>",
  );
  assert.equal(bridgeCardAction({ actionValue: { ...answer.behaviors[0].value, taskKey: '../../escape' } }), null);
  const actions = cardElements(card, 'button');
  const stop = actions.find((item) => item.behaviors?.[0]?.value?.action === 'task_link_interrupt');
  const refresh = actions.find((item) => item.behaviors?.[0]?.value?.action === 'task_link_refresh');
  assert.equal(bridgeCardAction({ actionValue: stop.behaviors[0].value }).action, 'task_link_interrupt');
  assert.equal(refresh, undefined);
  assert.equal(bridgeCardAction({
    actionValue: {
      namespace: 'feishu_bridge', version: 1, action: 'task_link_refresh', taskKey: '0123456789abcdef0123',
    },
  }).action, 'task_link_refresh');
  const { header } = card;
  assert.equal(card.body.elements.includes(header), false);
  assert.equal(header.template, 'orange');
  assert.equal(header.title.content, 'Codex 等待你的选择');
  assert.equal(header.padding, undefined);
  assert.deepEqual(header.text_tag_list, [{
    tag: 'text_tag', text: { tag: 'plain_text', content: '等待回答' }, color: 'orange',
  }]);
  assert.equal(card.config.style, undefined);
  assert.equal(
    cardElements(card, 'markdown').find((element) => element.content.includes('全权限')).content,
    '**全权限**',
  );
});

test('task-link answer options render as mobile-safe rows with optional bounded descriptions', () => {
  const longDescription = '减少安排，留出充分恢复时间。'.repeat(40);
  const card = progressCard({
    status: 'waiting_input',
    taskLink: {
      taskKey: '0123456789abcdef0123', linkState: 'active',
      turnState: 'waiting_input', turnOwner: 'bridge',
      controls: { canAnswer: true, canInterrupt: true, canRelease: true },
    },
    questions: [{
      id: 'weekend',
      header: '周末安排',
      question: '这个周末你最想做什么？',
      options: [
        { label: '在家休息 (Recommended)', description: '减少安排，留出充分恢复时间。' },
        { label: '户外活动', description: longDescription },
        { label: '朋友聚会' },
      ],
    }],
  });
  const optionRows = cardElements(card, 'column_set').filter((element) => (
    element.columns?.some((column) => column.elements?.some((item) => /^answer_option_/.test(item.name || '')))
  ));
  assert.equal(optionRows.length, 3);
  for (const row of optionRows) {
    assert.equal(row.flex_mode, 'none');
    assert.deepEqual(row.columns.map((column) => column.width), ['weighted', 'auto']);
    assert.equal(row.columns[1].elements[0].text.content, '选择');
    assert.equal(cardElements(row, 'button').length, 1);
  }
  assert.equal(
    optionRows[0].columns[0].elements[0].content,
    "**在家休息 \\(Recommended\\)**\n<font color='grey'>减少安排，留出充分恢复时间。</font>",
  );
  const boundedDescription = optionRows[1].columns[0].elements[0].content
    .match(/<font color='grey'>(.*)<\/font>/)?.[1];
  assert.equal(boundedDescription.length, 500);
  assert.match(boundedDescription, /…$/);
  assert.equal(optionRows[2].columns[0].elements[0].content, '**朋友聚会**');
  assert.doesNotMatch(optionRows[2].columns[0].elements[0].content, /<font/);
  assert.deepEqual(optionRows.map((row) => row.columns[1].elements[0].behaviors[0].value.answer), [
    '在家休息 (Recommended)', '户外活动', '朋友聚会',
  ]);
  assert.ok(cardRequestBytes(card) <= CARD_REQUEST_SAFE_BYTES);
});

test('project task cards use the verified project as the shared title and keep task identity secondary', () => {
  const card = progressCard({
    status: 'completed',
    title: 'CodexAssistant · 飞书任务',
    detail: '项目任务已完成。',
    taskLink: {
      taskKey: '0123456789abcdef0123',
      title: 'CodexAssistant · 飞书任务',
      projectName: 'CodexAssistant',
      linkState: 'active',
      turnState: 'completed',
      turnOwner: 'none',
      controls: {},
    },
    progress: { phase: '完成', detail: '项目任务已完成。' },
  });

  assert.equal(card.header.title.content, 'CodexAssistant');
  assert.equal(
    cardElements(card, 'markdown').some((element) => (
      element.content === "<font color='grey'>任务 · CodexAssistant · 飞书任务</font>"
    )),
    true,
  );
});

test('completed task-link cards keep one reply and offer quick text plus native rich input', () => {
  const finalReply = '收到。任务已经完成，并且结果只应该出现一次。';
  const card = progressCard({
    status: 'completed',
    title: 'CodexAssistant 到飞书',
    detail: finalReply,
    taskLink: {
      taskKey: '0123456789abcdef0123',
      linkState: 'active',
      turnState: 'completed',
      turnOwner: 'none',
      remainingSeconds: 23 * 60 * 60,
      controls: { canSend: true, canRelease: true, canSetMode: true },
    },
    progress: {
      phase: '完成', detail: finalReply, changedFiles: 0, testStatus: '未运行',
    },
  });
  const serialized = JSON.stringify(card);
  assert.equal(card.schema, '2.0');
  assert.equal(card.config.width_mode, 'fill');
  assert.equal(serialized.split(finalReply).length - 1, 1);
  assert.match(serialized, /全权限/);
  assert.match(serialized, /23 小时后失效/);
  assert.doesNotMatch(serialized, /Codex 回复|刷新任务|修改文件|验证|active|completed/);
  const { header } = card;
  assert.equal(card.body.elements.includes(header), false);
  assert.equal(header.template, 'green');
  assert.equal(header.title.content, 'CodexAssistant 到飞书');
  assert.equal(header.padding, undefined);
  assert.deepEqual(header.text_tag_list, [{
    tag: 'text_tag', text: { tag: 'plain_text', content: '已完成' }, color: 'green',
  }]);
  assert.deepEqual(
    card.body.elements.map((element) => element.tag),
    ['column_set', 'hr', 'markdown', 'hr', 'form'],
  );
  assert.equal(
    cardElements(card, 'markdown').find((element) => element.content.includes('全权限')).content,
    '**全权限**\n**23 小时后失效**',
  );

  const form = cardElements(card, 'form')[0];
  const input = cardElements(card, 'input')[0];
  assert.equal(form.name, 'codex_task_link_followup_form');
  assert.deepEqual({
    inputType: input.input_type,
    width: input.width,
    maxLength: input.max_length,
    rows: input.rows,
    autoResize: input.auto_resize,
    label: input.label?.content,
  }, {
    inputType: 'text', width: 'fill', maxLength: 1000,
    rows: undefined, autoResize: undefined, label: undefined,
  });
  const [newTurnLayout, quickReplyLayout] = form.elements
    .filter((element) => element.tag === 'column_set');
  const quickReplyElements = quickReplyLayout.columns.flatMap((column) => column.elements || []);
  const submit = quickReplyElements.find((element) => element.name === 'submit_task_link_followup');
  const mode = cardElements(newTurnLayout, 'select_static')[0];
  const statusLayout = card.body.elements[0];
  const release = cardElements(statusLayout, 'button')[0];
  assert.equal(newTurnLayout.columns[0].elements[0].content, '**开始新一轮**');
  assert.equal(mode.name, 'turnMode');
  assert.equal(mode.type, 'text');
  assert.equal(mode.initial_option, 'default');
  assert.deepEqual(mode.options.map((option) => [option.text.content, option.value]), [
    ['默认模式', 'default'], ['Plan 模式', 'plan'],
  ]);
  assert.equal(quickReplyLayout.flex_mode, 'none');
  assert.equal(quickReplyLayout.horizontal_spacing, '8px');
  assert.deepEqual(
    quickReplyLayout.columns.map((column) => ({
      width: column.width,
      weight: column.weight,
      verticalAlign: column.vertical_align,
    })),
    [
      { width: 'weighted', weight: 1, verticalAlign: undefined },
      { width: 'auto', weight: undefined, verticalAlign: 'bottom' },
    ],
  );
  assert.equal(quickReplyLayout.columns[0].elements[0], input);
  assert.equal(form.vertical_spacing, '8px');
  assert.equal(submit.text.content, '发送');
  assert.equal(submit.type, 'primary_filled');
  assert.equal(submit.form_action_type, 'submit');
  assert.equal(cardElements(form, 'button')
    .some((element) => element.name === 'capture_task_link_input'), false);
  assert.deepEqual(statusLayout.columns.map((column) => ({
    width: column.width,
    weight: column.weight,
    verticalAlign: column.vertical_align,
  })), [
    { width: 'weighted', weight: 1, verticalAlign: 'center' },
    { width: 'auto', weight: undefined, verticalAlign: 'center' },
  ]);
  assert.equal(release.name, 'release_task_link');
  assert.equal(release.text.content, '断连');
  assert.equal(release.type, 'default');
  assert.equal(cardElements(form, 'overflow').length, 0);
  assert.equal(cardElements(card, 'action').length, 0);
  assert.deepEqual(bridgeCardAction({
    actionValue: submit.behaviors[0].value,
    formValue: { followup: '快速补充一句', turnMode: 'plan' },
  }), {
    action: 'task_link_followup', taskId: '', taskKey: '0123456789abcdef0123',
    followup: '快速补充一句', intent: 'new_turn', turnMode: 'plan',
  });
  assert.equal(bridgeCardAction({ actionValue: release.behaviors[0].value }).action, 'task_link_release');
});

test('task-link reply controls and compact metadata follow authoritative turn controls', () => {
  const running = progressCard({
    status: 'processing',
    title: '运行中的任务',
    detail: '这段旧详情不应该覆盖权威进展。',
    taskLink: {
      taskKey: '0123456789abcdef0123', linkState: 'active', turnState: 'running', turnOwner: 'bridge',
      controls: { canSteer: true, canInterrupt: true, canRelease: true, canSetMode: true },
    },
    progress: { phase: '验证', detail: '正在运行完整测试。', changedFiles: 2, testStatus: '全部通过' },
  });
  const runningText = JSON.stringify(running);
  const runningHeader = running.header;
  assert.equal(running.body.elements.includes(runningHeader), false);
  assert.equal(runningHeader.template, 'blue');
  assert.equal(runningHeader.title.content, '运行中的任务');
  assert.equal(runningHeader.padding, undefined);
  assert.deepEqual(runningHeader.text_tag_list, [{
    tag: 'text_tag', text: { tag: 'plain_text', content: '运行中' }, color: 'blue',
  }]);
  assert.equal(
    cardElements(running, 'markdown').find((element) => element.content.includes('飞书控制')).content,
    '**飞书控制 · 全权限 · 验证**',
  );
  assert.match(runningText, /正在运行完整测试。/);
  assert.equal(runningText.split('验证').length - 1, 2);
  assert.match(runningText, /改动 2 个文件 · 验证：全部通过/);
  assert.doesNotMatch(runningText, /这段旧详情/);
  assert.equal(cardElements(running, 'form').length, 1);
  const [runningInputLayout] = cardElements(running, 'form')[0].elements
    .filter((element) => element.tag === 'column_set');
  const quickReplyButtons = runningInputLayout.columns
    .flatMap((column) => column.elements || [])
    .filter((element) => element.tag === 'button');
  assert.deepEqual(
    quickReplyButtons.map((button) => button.name),
    ['interrupt_task_link', 'submit_task_link_followup'],
  );
  assert.deepEqual(runningInputLayout.columns.map((column) => column.width), [
    'auto', 'weighted', 'auto',
  ]);
  const runningStop = quickReplyButtons[0];
  assert.equal(runningStop.text.content, '停止');
  assert.equal(runningStop.type, 'danger_text');
  assert.equal(runningStop.form_action_type, undefined);
  assert.deepEqual(runningStop.behaviors[0].value, {
    namespace: 'feishu_bridge', version: 1, action: 'task_link_interrupt',
    taskKey: '0123456789abcdef0123',
  });
  assert.equal(JSON.stringify(runningStop.behaviors[0].value).includes('followup'), false);
  assert.deepEqual(bridgeCardAction({
    actionValue: runningStop.behaviors[0].value,
    formValue: { followup: '不得随停止回调提交' },
  }), {
    action: 'task_link_interrupt', taskId: '', taskKey: '0123456789abcdef0123',
  });
  assert.equal(cardElements(running, 'select_static').length, 0);
  assert.equal(cardElements(running, 'input')[0].label.content, '补充当前轮');
  const runningTopControls = running.body.elements[0].columns[1].elements;
  assert.equal(runningTopControls.length, 1);
  assert.equal(runningTopControls[0].name, 'release_task_link');
  assert.equal(runningTopControls[0].text.content, '断连');
  assert.equal(runningTopControls[0].type, 'default');
  assert.equal(running.body.padding, '0px 0px 16px 0px');

  const waiting = progressCard({
    status: 'waiting_input',
    taskLink: {
      taskKey: '0123456789abcdef0123', linkState: 'active', turnState: 'waiting_input', turnOwner: 'bridge',
      controls: { canAnswer: true, canInterrupt: true, canRelease: true },
    },
    questions: [{ id: 'choice', header: '选择方式', question: '下一步怎么做？', options: [{ label: '继续' }] }],
  });
  assert.equal(cardElements(waiting, 'form').length, 1);
  assert.equal(cardElements(waiting, 'input')[0].label.content, '回答 Codex');
  assert.equal(cardElements(waiting, 'select_static').length, 0);
  assert.deepEqual(
    cardElements(waiting, 'form')[0].elements[0].columns.map((column) => (
      column.elements[0].name || column.elements[0].tag
    )),
    ['interrupt_task_link', 'followup', 'submit_task_link_followup'],
  );
  assert.equal(cardElements(waiting, 'button')
    .some((button) => button.name === 'capture_task_link_input'), false);

  for (const [turnState, linkState] of [
    ['queued', 'active'],
    ['desktop_action_required', 'active'],
    ['completed', 'expired'],
    ['completed', 'released'],
  ]) {
    const card = progressCard({
      status: turnState,
      taskLink: {
        taskKey: '0123456789abcdef0123', linkState, turnState, turnOwner: 'none',
        controls: { canSend: true, canSteer: true },
      },
    });
    assert.equal(cardElements(card, 'form').length, 0, `${linkState}/${turnState}`);
    assert.equal(cardElements(card, 'button')
      .some((button) => button.name === 'capture_task_link_input'), false, `${linkState}/${turnState}`);
  }
});

test('Plan mode task cards show the full plan separately and preserve it after completion', () => {
  const plan = '- [x] 检查现有链路\n- [ ] **实现模式切换**（进行中）\n- [ ] 验证飞书展示';
  const card = progressCard({
    status: 'processing',
    title: 'Plan 模式任务',
    detail: '正在实现模式切换。',
    latestInput: '请先制定计划，再开始开发。',
    taskLink: {
      taskKey: '0123456789abcdef0123', linkState: 'active', turnState: 'running', turnOwner: 'bridge',
      nextTurnMode: 'plan', activeTurnMode: 'plan',
      controls: { canSteer: true, canInterrupt: true, canRelease: true, canSetMode: true },
    },
    progress: { phase: '计划 2/3', detail: '正在实现模式切换。', plan },
  });
  const markdown = cardElements(card, 'markdown');
  assert.match(markdown[0].content, /Plan 模式/);
  const instruction = markdown.find((element) => element.content.startsWith('**你**'));
  const renderedPlan = markdown.find((element) => element.content.startsWith('**计划**'));
  const reply = markdown.find((element) => element.content.startsWith('**Codex**'));
  assert.match(renderedPlan.content, /检查现有链路/);
  assert.ok(markdown.indexOf(instruction) < markdown.indexOf(renderedPlan));
  assert.ok(markdown.indexOf(renderedPlan) < markdown.indexOf(reply));
  assert.equal(JSON.stringify(card).split('检查现有链路').length - 1, 1);
  assert.equal(cardElements(card, 'select_static').length, 0);
  assert.equal(cardElements(card, 'button')
    .some((button) => button.name === 'set_task_link_mode'), false);

  const completed = progressCard({
    status: 'completed',
    title: 'Plan 模式任务',
    detail: '计划已经生成，可以按此执行。',
    taskLink: {
      taskKey: '0123456789abcdef0123', linkState: 'active', turnState: 'completed', turnOwner: 'none',
      nextTurnMode: 'plan', activeTurnMode: 'plan', remainingSeconds: 23 * 60 * 60,
      controls: { canSend: true, canRelease: true, canSetMode: true },
    },
    progress: { phase: '完成', detail: '计划已经生成，可以按此执行。', plan },
  });
  assert.match(cardElements(completed, 'markdown')[0].content, /本轮 Plan/);
  assert.equal(JSON.stringify(completed).split('检查现有链路').length - 1, 1);
  assert.match(JSON.stringify(completed), /计划已经生成，可以按此执行/);
  assert.equal(cardElements(completed, 'select_static')[0].initial_option, 'plan');
});

test('plan_ready cards offer one safe start action and hide the mode toggle', () => {
  const plan = '# 测试计划\n\n1. 保持原任务\n2. 点击后开始执行';
  const card = progressCard({
    status: 'plan_ready',
    title: '等待执行的任务',
    taskLink: {
      taskKey: '0123456789abcdef0123', linkState: 'active', turnState: 'plan_ready',
      turnOwner: 'none', actionRequired: 'feishu', nextTurnMode: 'plan', activeTurnMode: 'plan',
      hasPendingPlanImplementation: true,
      planImplementationRevision: 'abcdef0123456789abcd',
      controls: {
        canSend: true, canImplementPlan: true, canRelease: true, canSetMode: false,
      },
    },
    progress: { phase: '等待开始执行', detail: '计划已生成。', plan },
  });
  assert.deepEqual(card.header.text_tag_list, [{
    tag: 'text_tag', text: { tag: 'plain_text', content: '等待开始执行' }, color: 'orange',
  }]);
  assert.match(JSON.stringify(card), /测试计划/);
  assert.equal(cardElements(card, 'input')[0].label.content, '修改计划');
  assert.equal(cardElements(card, 'input')[0].placeholder.content, '输入需要调整的内容');
  const buttons = cardElements(card, 'button');
  const form = cardElements(card, 'form')[0];
  const inputLayout = form.elements[0];
  const submit = buttons.find((button) => button.name === 'submit_task_link_followup');
  const implement = buttons.find((button) => button.name === 'implement_task_link_plan');
  assert.equal(form.vertical_spacing, '8px');
  assert.equal(form.elements.length, 1);
  assert.equal(inputLayout.horizontal_spacing, '8px');
  assert.equal(submit.text.content, '提交修改');
  assert.equal(submit.type, 'default');
  assert.equal(implement.text.content, '开始执行');
  assert.equal(implement.type, 'primary_filled');
  assert.equal(implement.width, 'fill');
  assert.deepEqual(
    buttons.filter((button) => button.type === 'primary_filled').map((button) => button.name),
    ['implement_task_link_plan'],
  );
  assert.equal(buttons.some((button) => button.name === 'set_task_link_mode'), false);
  assert.equal(cardElements(card, 'select_static').length, 0);
  assert.equal(buttons.some((button) => button.name === 'release_task_link'), true);
  const bodyTags = card.body.elements.map((element) => element.tag);
  const planIndex = card.body.elements.findIndex((element) => (
    element.tag === 'markdown' && element.content.startsWith('**计划**')
  ));
  assert.deepEqual(bodyTags.slice(planIndex, planIndex + 4), ['markdown', 'button', 'hr', 'form']);
  assert.doesNotMatch(JSON.stringify(card), /计划已生成。可直接开始执行/);
  assert.deepEqual(implement.behaviors[0].value, {
    namespace: 'feishu_bridge', version: 1, action: 'task_link_implement_plan',
    taskKey: '0123456789abcdef0123', planRevision: 'abcdef0123456789abcd',
  });
  assert.deepEqual(bridgeCardAction({ actionValue: implement.behaviors[0].value }), {
    action: 'task_link_implement_plan', taskId: '', taskKey: '0123456789abcdef0123',
    planRevision: 'abcdef0123456789abcd',
  });
  assert.equal(JSON.stringify(implement.behaviors[0].value).includes(plan), false);
  assert.equal(JSON.stringify(implement.behaviors[0].value).includes('thread'), false);
  const release = buttons.find((button) => button.name === 'release_task_link');
  assert.equal(release.text.content, '断连');
  assert.equal(bridgeCardAction({ actionValue: release.behaviors[0].value }).action, 'task_link_release');
});

test('task controls without an input form keep disconnect in status and stop below content', () => {
  const card = progressCard({
    status: 'desktop_action_required',
    title: '需要桌面操作',
    taskLink: {
      taskKey: '0123456789abcdef0123', linkState: 'active',
      turnState: 'desktop_action_required', turnOwner: 'desktop', actionRequired: 'desktop',
      controls: { canSetMode: true, canInterrupt: true, canRelease: true },
    },
  });
  assert.equal(cardElements(card, 'form').length, 0);
  assert.equal(card.body.elements.filter((element) => element.tag === 'column_set').length, 1);
  const topControls = card.body.elements[0].columns[1].elements;
  assert.deepEqual(topControls.map((control) => control.name), ['release_task_link']);
  assert.equal(topControls[0].text.content, '断连');
  const stop = card.body.elements.find((element) => element.name === 'interrupt_task_link');
  assert.equal(stop.text.content, '停止');
  assert.equal(stop.type, 'danger_text');
  assert.equal(stop.form_action_type, undefined);
  assert.deepEqual(card.body.elements.slice(-2).map((element) => element.tag), ['hr', 'button']);
});

test('legacy input capture state no longer changes task-card controls', () => {
  const card = progressCard({
    status: 'completed',
    title: '等待手机输入',
    detail: '上一轮已经完成。',
    taskLink: {
      taskKey: '0123456789abcdef0123', linkState: 'active', turnState: 'completed', turnOwner: 'none',
      remainingSeconds: 23 * 60 * 60, controls: { canSend: true, canRelease: true, acceptsAttachments: true },
    },
    inputCapture: { active: true, remainingSeconds: 120 },
  });
  const serialized = JSON.stringify(card);
  assert.doesNotMatch(serialized, /等待你的下一条消息或附件/);
  assert.doesNotMatch(serialized, /长文本 \/ 附件/);
  assert.equal(cardElements(card, 'form').length, 1);
  const buttons = cardElements(card, 'button');
  assert.equal(buttons.some((button) => button.name === 'capture_task_link_input'), false);
  assert.equal(buttons.some((button) => button.name === 'cancel_task_link_input'), false);
});

test('task-link status tags use semantic colors while supporting context stays bold', () => {
  for (const sample of [
    {
      status: 'failed', linkState: 'active', turnState: 'failed', template: 'red',
      label: '本轮失败', color: 'red', line: '**全权限**',
    },
    {
      status: 'interrupted', linkState: 'active', turnState: 'interrupted', template: 'grey',
      label: '本轮已停止', color: 'grey', line: '**全权限**',
    },
    {
      status: 'completed', linkState: 'active', turnState: 'completed', template: 'green',
      label: '已完成', color: 'green', progress: { durationSeconds: 83 },
      line: '**耗时 1 分 23 秒 · 全权限**',
    },
    {
      status: 'expired', linkState: 'expired', turnState: 'completed', template: 'grey',
      label: '连接已过期', color: 'grey', line: null,
    },
  ]) {
    const card = progressCard({
      status: sample.status,
      taskLink: {
        taskKey: '0123456789abcdef0123',
        linkState: sample.linkState,
        turnState: sample.turnState,
        turnOwner: 'none',
        controls: {},
      },
      progress: sample.progress,
    });
    assert.equal(card.header.template, sample.template);
    assert.deepEqual(card.header.text_tag_list, [{
      tag: 'text_tag', text: { tag: 'plain_text', content: sample.label }, color: sample.color,
    }]);
    if (sample.line) assert.equal(cardElements(card, 'markdown')[0].content, sample.line);
    else assert.equal(
      cardElements(card, 'markdown').some((element) => element.content === `**${sample.label}**`),
      false,
    );
  }
});

test('task-link cards bound the displayed instruction while preserving the reply budget', () => {
  const state = {
    status: 'completed',
    detail: `回复：${'验收内容'.repeat(1000)}`,
    latestInput: `指令：${'补充要求'.repeat(600)}`,
    taskLink: {
      taskKey: '0123456789abcdef0123', linkState: 'active', turnState: 'completed', turnOwner: 'none',
      remainingSeconds: 60 * 60, controls: { canSend: true, canRelease: true },
    },
  };
  const fitted = fitProgressCardToRequestBudget(state);
  const { card } = fitted;
  assert.equal(fitted.complete, true);
  const markdown = cardElements(card, 'markdown');
  const instruction = markdown
    .find((element) => element.content?.startsWith('**你**'));
  const reply = markdown.find((element) => element.content?.startsWith('**Codex**'));
  assert.ok(instruction.content.length <= 510);
  assert.ok(markdown.indexOf(instruction) < markdown.indexOf(reply));
  const instructionIndex = card.body.elements.indexOf(instruction);
  const replyIndex = card.body.elements.indexOf(reply);
  assert.equal(card.body.elements[instructionIndex + 1].tag, 'hr');
  assert.equal(replyIndex, instructionIndex + 2);
  assert.ok(cardRequestBytes(card) <= CARD_REQUEST_SAFE_BYTES);

  const oversized = fitProgressCardToRequestBudget({
    ...state,
    detail: '任务完整回复。'.repeat(5000),
  });
  assert.equal(oversized.complete, false);
  assert.ok(oversized.displayedCodePoints > 0);
  assert.ok(cardRequestBytes(oversized.card) <= CARD_REQUEST_SAFE_BYTES);
});

test('task-link follow-up actions reject empty, oversized, and forged submissions', () => {
  const actionValue = {
    namespace: 'feishu_bridge', version: 1, action: 'task_link_followup', taskKey: '0123456789abcdef0123',
  };
  assert.equal(bridgeCardAction({ actionValue, formValue: { followup: '  ' } }), null);
  assert.equal(bridgeCardAction({ actionValue, formValue: { followup: 'x'.repeat(1001) } }), null);
  assert.equal(bridgeCardAction({
    actionValue: { ...actionValue, taskKey: '../not-a-task' }, formValue: { followup: '继续' },
  }), null);
  assert.equal(bridgeCardAction({
    actionValue: { ...actionValue, action: 'task_link_shell' }, formValue: { followup: 'rm -rf' },
  }), null);
  assert.deepEqual(bridgeCardAction({
    actionValue, formValue: { followup: '先重新规划', turnMode: 'plan' },
  }), {
    action: 'task_link_followup', taskId: '', taskKey: '0123456789abcdef0123',
    followup: '先重新规划', turnMode: 'plan',
  });
  assert.equal(bridgeCardAction({
    actionValue, formValue: { followup: '执行命令', turnMode: 'unsafe' },
  }), null);
  assert.equal(bridgeCardAction({
    actionValue: { ...actionValue, intent: 'steer' }, formValue: { followup: '继续' },
  }), null);
  assert.equal(bridgeCardAction({
    actionValue: {
      namespace: 'feishu_bridge', version: 1, action: 'task_link_mode',
      taskKey: '0123456789abcdef0123', mode: 'unsafe',
    },
  }), null);
  assert.equal(bridgeCardAction({
    actionValue: {
      namespace: 'feishu_bridge', version: 1, action: 'task_link_implement_plan',
      taskKey: '0123456789abcdef0123', planRevision: 'forged',
    },
  }), null);
});
