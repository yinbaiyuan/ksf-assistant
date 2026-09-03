const { CARD_ACTION_NAMESPACE } = require('./progress-card');
const { FIXED_EVENT_KEYS } = require('./event-inbox');

const SUPPORTED_EVENT_KEYS = new Set(FIXED_EVENT_KEYS);

function normalizeEventKey(value, fallback = '') {
  const key = String(value || fallback || '').trim();
  return SUPPORTED_EVENT_KEYS.has(key) ? key : '';
}

function parseJsonObject(value) {
  if (value && typeof value === 'object' && !Array.isArray(value)) return value;
  try {
    const parsed = JSON.parse(String(value || ''));
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

function normalizeCardAction(raw) {
  const value = raw?.event || raw?.data?.event || raw?.data || raw || {};
  const context = value.context || {};
  const operator = value.operator || {};
  const action = value.action || {};
  return {
    type: 'card.action.trigger',
    eventId: String(value.event_id || value.eventId || value.header?.event_id || ''),
    operatorId: String(
      value.operator_id?.open_id
      || (typeof value.operator_id === 'string' ? value.operator_id : '')
      || value.operatorId
      || operator.open_id
      || operator.operator_id?.open_id
      || '',
    ),
    chatId: String(
      value.chat_id
      || value.chatId
      || value.open_chat_id
      || context.open_chat_id
      || '',
    ),
    messageId: String(
      value.message_id
      || value.messageId
      || value.open_message_id
      || context.open_message_id
      || '',
    ),
    actionTag: String(value.action_tag || value.actionTag || action.tag || ''),
    actionValue: parseJsonObject(value.action_value || value.actionValue || action.value),
    formValue: parseJsonObject(value.form_value || value.formValue || action.form_value),
    token: String(value.token || ''),
    cardContent: String(value.card_content || value.cardContent || context.card_content || ''),
  };
}

function bridgeCardAction(event) {
  const value = event?.actionValue || {};
  if (value.namespace !== CARD_ACTION_NAMESPACE || value.version !== 1) return null;
  if (![
    'status', 'task_status', 'task_link_detail', 'task_link_refresh',
    'task_link_interrupt', 'task_link_release', 'task_link_answer', 'task_link_followup',
    'task_link_capture', 'task_link_capture_cancel', 'task_link_mode',
    'task_link_implement_plan',
    'chat_followup', 'dismiss',
  ].includes(value.action)) return null;
  if (value.action === 'task_status' && !/^TASK-[A-Za-z0-9-]+$/.test(String(value.taskId || ''))) return null;
  if (value.action.startsWith('task_link_') && !/^[a-f0-9]{20}$/.test(String(value.taskKey || ''))) return null;
  const result = {
    action: value.action,
    taskId: value.taskId ? String(value.taskId) : '',
  };
  if (value.taskDurationSeconds !== undefined) {
    if (
      value.action !== 'status'
      || !Number.isInteger(value.taskDurationSeconds)
      || value.taskDurationSeconds < 0
      || value.taskDurationSeconds > 30 * 24 * 60 * 60
    ) return null;
    result.taskDurationSeconds = value.taskDurationSeconds;
  }
  if (value.taskKey) result.taskKey = String(value.taskKey);
  if (['chat_followup', 'task_link_followup'].includes(value.action)) {
    const followup = String(event?.formValue?.followup || '').replace(/\u0000/g, '').trim();
    const maximum = 1000;
    if (!followup || followup.length > maximum) return null;
    result.followup = followup;
  }
  if (value.action === 'task_link_answer') {
    if (!/^[A-Za-z0-9_-]{1,80}$/.test(String(value.questionId || '')) || !String(value.answer || '').trim()) return null;
    result.questionId = String(value.questionId);
    result.answer = String(value.answer).slice(0, 160);
  }
  if (value.action === 'task_link_mode') {
    if (!['default', 'plan'].includes(value.mode)) return null;
    result.mode = value.mode;
  }
  if (value.action === 'task_link_implement_plan') {
    if (!/^[a-f0-9]{20}$/.test(String(value.planRevision || ''))) return null;
    result.planRevision = String(value.planRevision);
  }
  return result;
}

module.exports = {
  SUPPORTED_EVENT_KEYS,
  bridgeCardAction,
  normalizeCardAction,
  normalizeEventKey,
  parseJsonObject,
};
