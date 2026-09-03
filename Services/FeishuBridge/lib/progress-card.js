const CARD_ACTION_NAMESPACE = 'feishu_bridge';
const TASK_LINK_CARD_REVISION = 28;
const FEISHU_CARD_REQUEST_MAX_BYTES = 30 * 1024;
const CARD_REQUEST_RESERVE_BYTES = 512;
const CARD_REQUEST_SAFE_BYTES = FEISHU_CARD_REQUEST_MAX_BYTES - CARD_REQUEST_RESERVE_BYTES;
const CARD_DETAIL_CHUNK_CHARS = 2400;
const CARD_OVERFLOW_NOTICE = '[卡片空间有限，完整回复见后续文字消息。]';

function boundedText(value, maximum = 3000) {
  const text = String(value || '').trim();
  return text.length <= maximum ? text : `${text.slice(0, maximum - 1)}…`;
}

function cardDetailChunks(value, fallback) {
  let text = String(value || fallback || '').trim();
  const chunks = [];
  while (text.length) {
    if (text.length <= CARD_DETAIL_CHUNK_CHARS) {
      chunks.push(text);
      break;
    }
    const window = text.slice(0, CARD_DETAIL_CHUNK_CHARS);
    const newline = window.lastIndexOf('\n');
    const splitAt = newline >= Math.floor(CARD_DETAIL_CHUNK_CHARS * 0.6)
      ? newline + 1
      : CARD_DETAIL_CHUNK_CHARS;
    chunks.push(text.slice(0, splitAt).trimEnd());
    text = text.slice(splitAt).trimStart();
  }
  return chunks.length ? chunks : [String(fallback || '')];
}

function cardDetailElements(value, fallback) {
  return cardDetailChunks(value, fallback).map((content) => ({
    tag: 'div',
    text: { tag: 'lark_md', content },
  }));
}

function cardV2MarkdownElements(value, fallback, title = '') {
  const elements = cardDetailChunks(value, fallback)
    .map((content) => ({ tag: 'markdown', content }));
  if (title && elements[0]?.content) {
    elements[0].content = `**${boundedText(title, 80)}**\n${elements[0].content}`;
  }
  return elements;
}

function cardV2Button({ name, text, action, type = 'default', submit = false }) {
  return {
    tag: 'button',
    name,
    ...(submit ? { form_action_type: 'submit' } : {}),
    text: { tag: 'plain_text', content: text },
    type,
    size: 'medium',
    width: 'default',
    behaviors: [{ type: 'callback', value: action }],
  };
}

function cardV2ButtonRow(buttons) {
  return {
    tag: 'column_set',
    flex_mode: 'flow',
    horizontal_spacing: '8px',
    horizontal_align: 'left',
    columns: buttons.map((button) => ({
      tag: 'column',
      width: 'auto',
      elements: [button],
    })),
  };
}

function cardV2LiteralMarkdown(value, maximum) {
  return boundedText(value, maximum)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\\/g, '\\\\')
    .replace(/([`*_{}[\]()#+\-.!|~])/g, '\\$1');
}

function cardV2QuestionOptionRow({ option, index, taskLink, question }) {
  const label = cardV2LiteralMarkdown(option.label, 60);
  const description = cardV2LiteralMarkdown(option.description, 500);
  const content = [
    `**${label}**`,
    description ? `<font color='grey'>${description}</font>` : '',
  ].filter(Boolean).join('\n');
  return {
    tag: 'column_set',
    flex_mode: 'none',
    horizontal_spacing: '8px',
    columns: [{
      tag: 'column',
      width: 'weighted',
      weight: 1,
      vertical_align: 'center',
      elements: [{ tag: 'markdown', content }],
    }, {
      tag: 'column',
      width: 'auto',
      vertical_align: 'center',
      elements: [cardV2Button({
        name: `answer_option_${index + 1}`,
        text: '选择',
        action: bridgeAction('task_link_answer', {
          taskKey: taskLink.taskKey,
          questionId: question.id,
          answer: boundedText(option.label, 120),
        }),
      })],
    }],
  };
}

function cardV2QuickReplyForm({
  name,
  label,
  placeholder,
  submitName,
  submitLabel,
  submitAction,
  secondaryButtons = [],
}) {
  const input = {
    tag: 'input',
    name: 'followup',
    required: true,
    placeholder: { tag: 'plain_text', content: placeholder },
    width: 'fill',
    max_length: 1000,
    input_type: 'text',
    label: { tag: 'plain_text', content: label },
    label_position: 'top',
  };
  const submit = cardV2Button({
    name: submitName,
    text: submitLabel,
    action: submitAction,
    type: 'primary_filled',
    submit: true,
  });
  return {
    tag: 'form',
    name,
    direction: 'vertical',
    vertical_spacing: '12px',
    elements: [{
      tag: 'column_set',
      flex_mode: 'none',
      horizontal_spacing: '8px',
      columns: [{
        tag: 'column',
        width: 'weighted',
        weight: 1,
        elements: [input],
      }, {
        tag: 'column',
        width: 'auto',
        vertical_align: 'bottom',
        elements: [submit],
      }],
    }, ...(secondaryButtons.length ? [cardV2ButtonRow(secondaryButtons)] : [])],
  };
}

function chatCardContextLine(status, taskDurationSeconds) {
  const parts = [];
  if (['completed', 'failed'].includes(status) && Number.isInteger(taskDurationSeconds)) {
    parts.push(`耗时 ${formatElapsedDuration(taskDurationSeconds)}`);
  }
  return parts.join(' · ');
}

function chatStatusTag(status) {
  const labels = {
    processing: '运行中',
    running: '运行中',
    completed: '已完成',
    failed: '本轮失败',
    dismissed: '已关闭',
  };
  const colors = {
    processing: 'blue',
    running: 'blue',
    completed: 'green',
    failed: 'red',
    dismissed: 'grey',
  };
  return {
    tag: 'text_tag',
    text: { tag: 'plain_text', content: labels[status] || 'Codex 对话' },
    color: colors[status] || 'grey',
  };
}

function codexCardHeader(title, template, statusTag) {
  const color = [
    'blue', 'wathet', 'turquoise', 'green', 'yellow', 'orange', 'red',
    'carmine', 'violet', 'purple', 'indigo', 'grey', 'default',
  ].includes(template) ? template : 'default';
  return {
    template: color,
    title: { tag: 'plain_text', content: boundedText(title, 120) },
    ...(statusTag ? { text_tag_list: [statusTag] } : {}),
  };
}

function codexCardBody(contentElements) {
  return {
    direction: 'vertical',
    vertical_spacing: '0px',
    padding: '0px 0px 16px 0px',
    elements: contentElements.map((element) => ({
      ...element,
      margin: '12px 20px 0px 20px',
    })),
  };
}

function chatCardV2({
  state, status, title, detail, followupEnabled = false, latestInput, taskDurationSeconds,
}) {
  const contentElements = [];
  const contextLine = chatCardContextLine(status, taskDurationSeconds);
  if (contextLine) {
    contentElements.push({ tag: 'markdown', content: `**${contextLine}**` });
    contentElements.push({ tag: 'hr' });
  }
  if (latestInput) {
    contentElements.push(...cardV2MarkdownElements(boundedText(latestInput, 480), '', '你'));
    contentElements.push({ tag: 'hr' });
  }
  contentElements.push(...cardV2MarkdownElements(detail, state.detail, latestInput ? 'Codex' : ''));
  if (followupEnabled) {
    contentElements.push({ tag: 'hr' });
    contentElements.push(cardV2QuickReplyForm({
      name: 'codex_followup_form',
      label: '快速回复',
      placeholder: '输入下一步问题或要求',
      submitName: 'submit_followup',
      submitLabel: '发送',
      submitAction: bridgeAction('chat_followup'),
      secondaryButtons: [cardV2Button({
        name: 'dismiss_card',
        text: '关闭卡片',
        action: bridgeAction('dismiss'),
      })],
    }));
  }
  return {
    schema: '2.0',
    config: { width_mode: 'fill', update_multi: true },
    header: codexCardHeader(title || 'Codex 对话', state.template, chatStatusTag(status)),
    body: codexCardBody(contentElements),
  };
}

function formatElapsedDuration(seconds) {
  const totalSeconds = Math.max(0, Math.floor(Number(seconds) || 0));
  const units = [
    ['天', 24 * 60 * 60],
    ['小时', 60 * 60],
    ['分', 60],
    ['秒', 1],
  ];
  let remaining = totalSeconds;
  const parts = [];
  for (const [label, size] of units) {
    const value = Math.floor(remaining / size);
    remaining %= size;
    if (value > 0 || (label === '秒' && parts.length === 0)) parts.push(`${value} ${label}`);
  }
  return parts.join(' ');
}

function bridgeAction(action, input = {}) {
  return {
    namespace: CARD_ACTION_NAMESPACE,
    version: 1,
    action,
    ...input,
  };
}

function compactLease(seconds) {
  const totalSeconds = Math.max(0, Math.floor(Number(seconds) || 0));
  if (totalSeconds < 60) return '即将失效';
  if (totalSeconds < 60 * 60) return `${Math.floor(totalSeconds / 60)} 分后失效`;
  return `${Math.floor(totalSeconds / (60 * 60))} 小时后失效`;
}

function taskLinkStatus(taskLink, fallbackStatus) {
  const linkState = String(taskLink.linkState || 'active');
  if (linkState === 'released') return '连接已解除';
  if (linkState === 'expired') return '连接已过期';

  const turnState = String(taskLink.turnState || fallbackStatus || 'idle');
  const turnLabels = {
    idle: '已连接',
    running: '运行中',
    waiting_input: '等待回答',
    desktop_action_required: '等待桌面操作',
    queued: '已排队',
    plan_ready: '等待开始执行',
    completed: '已完成',
    failed: '本轮失败',
    interrupted: '本轮已停止',
  };
  return turnLabels[turnState] || '已连接';
}

function taskLinkContextLine(taskLink, fallbackStatus, progress) {
  const linkState = String(taskLink.linkState || 'active');
  const parts = [];
  if (linkState !== 'active') return '';

  const turnState = String(taskLink.turnState || fallbackStatus || 'idle');
  if (['completed', 'failed', 'interrupted'].includes(turnState)
    && Number.isInteger(progress?.durationSeconds)) {
    parts.push(`耗时 ${formatElapsedDuration(progress.durationSeconds)}`);
  }
  const ownerLabels = { bridge: '飞书控制', desktop: 'Codex Desktop 控制' };
  if (turnState === 'running' && ownerLabels[taskLink.turnOwner]) parts.push(ownerLabels[taskLink.turnOwner]);
  const activeMode = String(taskLink.activeTurnMode || '');
  const nextMode = String(taskLink.nextTurnMode || 'default');
  if (turnState === 'plan_ready') parts.push('Plan 已完成');
  if (activeMode === 'plan' && turnState !== 'plan_ready') {
    parts.push(['running', 'waiting_input', 'desktop_action_required', 'queued'].includes(turnState)
      ? 'Plan 模式' : '本轮 Plan');
  }
  if (turnState !== 'plan_ready'
    && nextMode !== activeMode && (nextMode === 'plan' || activeMode === 'plan')) {
    parts.push(nextMode === 'plan' ? '下轮 Plan' : '下轮默认');
  }
  parts.push('全权限');
  const phase = String(progress?.phase || '').trim();
  if (turnState === 'running' && phase && phase !== '运行中') parts.push(boundedText(phase, 40));
  return parts.join(' · ');
}

function taskLinkLeaseLine(taskLink, fallbackStatus) {
  if (String(taskLink.linkState || 'active') !== 'active') return '';
  const turnState = String(taskLink.turnState || fallbackStatus || 'idle');
  if (['running', 'waiting_input', 'desktop_action_required', 'queued'].includes(turnState)
    || !Number.isFinite(taskLink.remainingSeconds)) return '';
  return compactLease(taskLink.remainingSeconds);
}

function taskLinkContent(taskLink, status, detail, progress, hasQuestion) {
  const linkState = String(taskLink.linkState || 'active');
  const turnState = String(taskLink.turnState || status || 'idle');
  if (linkState === 'released') return { title: '连接状态', body: '任务连接已解除。当前 Codex 轮次不会因此停止。' };
  if (linkState === 'expired') return { title: '连接状态', body: '任务连接已过期，请回到 CodexAssistant 重新连接。' };
  if (hasQuestion && turnState === 'waiting_input') return null;

  const preferredDetail = String(detail || '').trim();
  const progressDetail = String(progress?.detail || '').trim();
  if (turnState === 'completed') {
    return { title: '', body: preferredDetail || taskLink.detailSummary || progressDetail || '本轮已完成。' };
  }
  if (turnState === 'plan_ready') {
    return { title: '', body: '计划已生成。可直接开始执行，或在下方提出修改意见。' };
  }
  if (turnState === 'running') {
    return { title: '', body: progressDetail || preferredDetail || 'Codex 正在处理。' };
  }
  if (turnState === 'failed') {
    return { title: '本轮失败', body: preferredDetail || progressDetail || '处理过程中发生错误。' };
  }
  if (turnState === 'interrupted') {
    return { title: '本轮已停止', body: preferredDetail || progressDetail || '任务连接仍然有效。' };
  }
  if (turnState === 'desktop_action_required') {
    return { title: '需要在 Codex Desktop 操作', body: preferredDetail || progressDetail || '当前输入或批准不能通过飞书安全转交。' };
  }
  if (turnState === 'queued') {
    return { title: '等待下一轮', body: preferredDetail || progressDetail || '当前轮结束后自动执行已排队的指令。' };
  }
  if (turnState === 'waiting_input') {
    return { title: '等待回答', body: preferredDetail || progressDetail || 'Codex 正在等待你的补充。' };
  }
  return { title: '可以继续', body: preferredDetail || progressDetail || '在下方输入内容，继续同一个 Codex 任务。' };
}

function taskLinkCanQuickReply(taskLink) {
  if (taskLink.linkState !== 'active'
    || ['queued', 'desktop_action_required'].includes(taskLink.turnState)) return false;
  const controls = taskLink.controls || {};
  return Boolean(controls.canAnswer || controls.canSteer || controls.canSend);
}

function taskLinkReleaseButton(taskLink) {
  if (!taskLink.controls?.canRelease) return null;
  return cardV2Button({
    name: 'release_task_link',
    text: '断连',
    action: bridgeAction('task_link_release', { taskKey: taskLink.taskKey }),
  });
}

function taskLinkContextElement(taskLink, status, progress) {
  const contextLine = taskLinkContextLine(taskLink, status, progress);
  const leaseLine = taskLinkLeaseLine(taskLink, status);
  if (!contextLine && !leaseLine) return null;
  const statusElement = {
    tag: 'markdown',
    content: [`**${contextLine}**`, leaseLine ? `**${leaseLine}**` : ''].filter(Boolean).join('\n'),
  };
  const release = taskLinkReleaseButton(taskLink);
  if (!release) return statusElement;
  return {
    tag: 'column_set',
    flex_mode: 'none',
    horizontal_spacing: '8px',
    columns: [{
      tag: 'column',
      width: 'weighted',
      weight: 1,
      vertical_align: 'top',
      elements: [statusElement],
    }, {
      tag: 'column',
      width: 'auto',
      vertical_align: 'top',
      elements: [release],
    }],
  };
}

function taskLinkControlButtons(taskLink) {
  const buttons = [];
  if (taskLink.controls?.canImplementPlan) {
    buttons.push(cardV2Button({
      name: 'implement_task_link_plan',
      text: '开始执行',
      type: 'primary_filled',
      action: bridgeAction('task_link_implement_plan', {
        taskKey: taskLink.taskKey,
        planRevision: taskLink.planImplementationRevision,
      }),
    }));
  }
  if (taskLink.controls?.canSetMode) {
    const nextMode = taskLink.nextTurnMode === 'plan' ? 'default' : 'plan';
    buttons.push(cardV2Button({
      name: 'set_task_link_mode',
      text: nextMode === 'plan' ? '下轮用 Plan' : '下轮用默认',
      action: bridgeAction('task_link_mode', { taskKey: taskLink.taskKey, mode: nextMode }),
    }));
  }
  if (taskLink.controls?.canInterrupt) buttons.push(cardV2Button({
    name: 'interrupt_task_link',
    text: '停止本轮',
    type: 'danger',
    action: bridgeAction('task_link_interrupt', { taskKey: taskLink.taskKey }),
  }));
  return buttons;
}

function taskLinkQuickReplyForm(taskLink) {
  if (!taskLinkCanQuickReply(taskLink)) return null;
  const controls = taskLink.controls || {};
  const label = taskLink.turnState === 'plan_ready'
    ? '修改计划'
    : controls.canAnswer
    ? '快速回答'
    : controls.canSteer ? '快速补充' : '快速回复';
  const placeholder = taskLink.turnState === 'plan_ready'
    ? '输入需要调整的内容'
    : controls.canAnswer
    ? '输入对当前问题的回答'
    : controls.canSteer ? '输入一句补充或修正' : '输入下一步问题或要求';
  return cardV2QuickReplyForm({
    name: 'codex_task_link_followup_form',
    label,
    placeholder,
    submitName: 'submit_task_link_followup',
    submitLabel: '发送',
    submitAction: bridgeAction('task_link_followup', { taskKey: taskLink.taskKey }),
    secondaryButtons: taskLinkControlButtons(taskLink),
  });
}

function taskLinkStatusTag(taskLink, fallbackStatus) {
  const turnState = String(taskLink.turnState || fallbackStatus || 'idle');
  const color = String(taskLink.linkState || 'active') !== 'active'
    ? 'grey'
    : ({
        idle: 'turquoise',
        running: 'blue',
        waiting_input: 'orange',
        desktop_action_required: 'orange',
        queued: 'orange',
        plan_ready: 'orange',
        completed: 'green',
        failed: 'red',
        interrupted: 'grey',
      }[turnState] || 'turquoise');
  return {
    tag: 'text_tag',
    text: { tag: 'plain_text', content: taskLinkStatus(taskLink, fallbackStatus) },
    color,
  };
}

function taskLinkCardTitle(taskLink, fallbackTitle) {
  return String(taskLink.projectName || fallbackTitle || 'Codex 任务').trim();
}

function taskLinkIdentity(taskLink, fallbackTitle) {
  const projectName = String(taskLink.projectName || '').trim();
  const title = String(taskLink.title || fallbackTitle || '').trim();
  if (!projectName || !title || title === projectName) return '';
  return title;
}

function taskLinkCardV2({
  state, status, title, detail, taskLink, progress, questions, latestInput,
}) {
  const contentElements = [];
  const contextElement = taskLinkContextElement(taskLink, status, progress);
  if (contextElement) contentElements.push(contextElement);
  const taskIdentity = taskLinkIdentity(taskLink, title);
  if (taskIdentity) {
    contentElements.push({
      tag: 'markdown',
      content: `<font color='grey'>任务 · ${boundedText(taskIdentity, 120)}</font>`,
    });
  }
  if (contextElement || taskIdentity) {
    contentElements.push({ tag: 'hr' });
  }
  const question = Array.isArray(questions) && questions.length === 1 && !questions[0].isSecret
    ? questions[0] : null;
  const planText = String(progress?.plan || '').trim();
  if (latestInput) {
    contentElements.push(...cardV2MarkdownElements(boundedText(latestInput, 480), '', '你'));
  }
  const content = taskLinkContent(taskLink, status, detail, progress, Boolean(question));
  if (latestInput && (planText || content || question)) contentElements.push({ tag: 'hr' });
  if (planText) {
    contentElements.push(...cardV2MarkdownElements(boundedText(planText, 3000), '', '计划'));
    if (content || question) contentElements.push({ tag: 'hr' });
  }
  if (content) {
    const contentTitle = latestInput
      ? ['Codex', content.title].filter(Boolean).join(' · ')
      : content.title;
    contentElements.push(...cardV2MarkdownElements(content.body, state.detail, contentTitle));
  }
  if (question) {
    const questionTitle = latestInput
      ? ['Codex', question.header || '需要选择'].join(' · ')
      : question.header || '需要选择';
    contentElements.push(...cardV2MarkdownElements(question.question, '', questionTitle));
    contentElements.push(...(question.options || []).slice(0, 3).map((option, index) => (
      cardV2QuestionOptionRow({ option, index, taskLink, question })
    )));
  }
  const metadata = [
    Number(progress?.changedFiles) > 0 ? `改动 ${progress.changedFiles} 个文件` : '',
    progress?.testStatus && progress.testStatus !== '未运行' ? `验证：${boundedText(progress.testStatus, 120)}` : '',
  ].filter(Boolean);
  if (metadata.length) contentElements.push({
    tag: 'markdown', content: `<font color='grey'>${metadata.join(' · ')}</font>`,
  });
  const quickReplyForm = taskLinkQuickReplyForm(taskLink);
  if (quickReplyForm) {
    contentElements.push({ tag: 'hr' });
    contentElements.push(quickReplyForm);
  } else {
    const taskActions = taskLinkControlButtons(taskLink);
    if (taskActions.length) {
      contentElements.push({ tag: 'hr' });
      contentElements.push(cardV2ButtonRow(taskActions));
    }
  }
  return {
    schema: '2.0',
    config: {
      width_mode: 'fill',
      update_multi: true,
    },
    header: codexCardHeader(
      taskLinkCardTitle(taskLink, title || state.title),
      state.template,
      taskLinkStatusTag(taskLink, status),
    ),
    body: codexCardBody(contentElements),
  };
}

function progressCard({
  status = 'processing', title, detail, taskId, taskLink, progress, questions, openIds,
  taskDurationSeconds, followupEnabled = false, hideActions = false, latestInput, schemaVersion,
} = {}) {
  const states = {
    processing: { title: 'Codex 正在处理', template: 'blue', detail: '请求已经进入本机 Codex。' },
    running: { title: 'Codex 正在处理', template: 'blue', detail: '请求正在原 Codex 任务中执行。' },
    completed: { title: 'Codex 已完成', template: 'green', detail: '处理已完成。' },
    failed: { title: 'Codex 处理失败', template: 'red', detail: '处理过程中发生错误。' },
    dismissed: { title: '卡片已关闭', template: 'grey', detail: '此状态卡不再更新。' },
    status: { title: '飞书桥状态', template: 'turquoise', detail: '状态已刷新。' },
    connected: { title: 'Codex 任务已连接', template: 'turquoise', detail: '回复本消息继续任务。' },
    waiting_current_turn: { title: '等待当前任务完成', template: 'orange', detail: '桌面端当前轮完成后即可从飞书继续。' },
    waiting_input: { title: 'Codex 等待你的选择', template: 'orange', detail: '请通过卡片选项或回复补充信息。' },
    desktop_action_required: { title: '需要 Codex Desktop 操作', template: 'orange', detail: '当前输入或批准不能通过飞书安全转交。' },
    queued: { title: '消息已排队', template: 'orange', detail: '当前轮完成后自动执行。' },
    interrupted: { title: 'Codex 本轮已停止', template: 'grey', detail: '任务连接仍然有效。' },
    idle: { title: 'Codex 任务已连接', template: 'turquoise', detail: '回复本消息继续任务。' },
    expired: { title: '任务连接已过期', template: 'grey', detail: '请回到 CodexAssistant 重新连接。' },
  };
  const state = states[status] || states.status;
  if (taskLink) {
    return taskLinkCardV2({
      state, status, title, detail, taskLink, progress, questions, latestInput,
    });
  }
  const canFollowup = followupEnabled && ['completed', 'failed'].includes(status) && !taskId && !taskLink;
  if (schemaVersion === '2.0' || canFollowup) {
    return chatCardV2({
      state, status, title, detail, followupEnabled: canFollowup, latestInput, taskDurationSeconds,
    });
  }
  const refreshInput = taskId
    ? { taskId }
    : (Number.isInteger(taskDurationSeconds) ? { taskDurationSeconds } : {});
  const isOrdinaryProcessing = ['processing', 'running'].includes(status) && !taskId && !taskLink;
  const showRefresh = Boolean(taskId) || status === 'status';
  const actions = status === 'dismissed' || taskLink || hideActions ? [] : [...(showRefresh ? [{
      tag: 'button',
      text: { tag: 'plain_text', content: '刷新状态' },
      type: 'default',
      value: bridgeAction(taskId ? 'task_status' : 'status', refreshInput),
    }] : []), ...(isOrdinaryProcessing ? [] : [{
    tag: 'button',
    text: { tag: 'plain_text', content: '关闭卡片' },
    type: 'default',
    value: bridgeAction('dismiss'),
  }])];
  const card = {
    config: { wide_screen_mode: true, update_multi: true },
    header: {
      template: state.template,
      title: { tag: 'plain_text', content: boundedText(title || state.title, 120) },
    },
    elements: cardDetailElements(detail, state.detail),
  };
  if (taskId) {
    card.elements.push({ tag: 'note', elements: [{ tag: 'plain_text', content: `任务：${boundedText(taskId, 100)}` }] });
  }
  if (actions.length) card.elements.push({ tag: 'action', actions });
  if (Array.isArray(openIds) && openIds.length) card.open_ids = [...new Set(openIds.filter(Boolean))];
  return card;
}

function cardRequestBytes(card) {
  if (!card) return Number.POSITIVE_INFINITY;
  return Buffer.byteLength(JSON.stringify({ content: JSON.stringify(card) }), 'utf8');
}

function fitProgressCardToRequestBudget(state = {}) {
  const fullCard = progressCard(state);
  const fullBytes = cardRequestBytes(fullCard);
  if (fullBytes <= CARD_REQUEST_SAFE_BYTES) {
    return { card: fullCard, complete: true, requestBytes: fullBytes };
  }

  const detail = String(state.detail || '').trim();
  const codePoints = Array.from(detail);
  const suffix = `\n\n${CARD_OVERFLOW_NOTICE}`;
  const variants = [
    state,
    {
      ...state,
      latestInput: '',
      progress: state.progress ? { ...state.progress, plan: '' } : state.progress,
    },
  ];

  for (const variant of variants) {
    let low = 0;
    let high = codePoints.length;
    let best = null;
    while (low <= high) {
      const middle = Math.floor((low + high) / 2);
      const prefix = codePoints.slice(0, middle).join('').trimEnd();
      const candidateDetail = prefix ? `${prefix}${suffix}` : CARD_OVERFLOW_NOTICE;
      const candidate = progressCard({ ...variant, detail: candidateDetail });
      const requestBytes = cardRequestBytes(candidate);
      if (requestBytes <= CARD_REQUEST_SAFE_BYTES) {
        best = { card: candidate, complete: false, requestBytes, displayedCodePoints: middle };
        low = middle + 1;
      } else {
        high = middle - 1;
      }
    }
    if (best) return best;
  }

  return { card: null, complete: false, requestBytes: fullBytes, displayedCodePoints: 0 };
}

module.exports = {
  CARD_REQUEST_SAFE_BYTES,
  CARD_ACTION_NAMESPACE,
  TASK_LINK_CARD_REVISION,
  bridgeAction,
  cardRequestBytes,
  fitProgressCardToRequestBudget,
  formatElapsedDuration,
  progressCard,
};
