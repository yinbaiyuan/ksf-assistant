const { fingerprint } = require('./event-inbox');

const EVENT_WATCHES = Object.freeze({
  task: Object.freeze({
    eventKeys: ['task.task.update_user_access_v2'],
    identity: 'user',
    add: { method: 'POST', path: '/open-apis/task/v2/task_v2/task_subscription?user_id_type=open_id' },
    remove: null,
    targetRequired: false,
  }),
  whiteboard: Object.freeze({
    eventKeys: ['board.whiteboard.updated_v1'],
    identity: 'user',
    add: { method: 'POST', path: '/open-apis/board/v1/whiteboards/{target}/subscribe' },
    remove: { method: 'POST', path: '/open-apis/board/v1/whiteboards/{target}/unsubscribe' },
    targetRequired: true,
  }),
  meeting: Object.freeze({
    eventKeys: [
      'vc.meeting.participant_meeting_started_v1',
      'vc.meeting.participant_meeting_joined_v1',
      'vc.meeting.participant_meeting_ended_v1',
    ],
    identity: 'user',
    add: { method: 'POST', path: '/open-apis/vc/v1/meetings/subscription' },
    remove: { method: 'POST', path: '/open-apis/vc/v1/meetings/unsubscription' },
    targetRequired: false,
  }),
  note: Object.freeze({
    eventKeys: ['vc.note.generated_v1'],
    identity: 'user',
    add: { method: 'POST', path: '/open-apis/vc/v1/notes/subscription' },
    remove: { method: 'POST', path: '/open-apis/vc/v1/notes/unsubscription' },
    targetRequired: false,
  }),
  recording: Object.freeze({
    eventKeys: [
      'vc.recording.recording_started_v1',
      'vc.recording.recording_ended_v1',
      'vc.recording.recording_transcript_generated_v1',
    ],
    identity: 'user',
    add: { method: 'POST', path: '/open-apis/vc/v1/recordings/subscription' },
    remove: { method: 'POST', path: '/open-apis/vc/v1/recordings/unsubscription' },
    targetRequired: false,
  }),
  minutes: Object.freeze({
    eventKeys: ['minutes.minute.generated_v1'],
    identity: 'user',
    add: { method: 'POST', path: '/open-apis/minutes/v1/minutes/subscription' },
    remove: { method: 'POST', path: '/open-apis/minutes/v1/minutes/unsubscription' },
    targetRequired: false,
  }),
});

function watchDefinition(type) {
  return EVENT_WATCHES[String(type || '')];
}

function validateTarget(definition, target) {
  if (definition.targetRequired) {
    if (!target || !/^[A-Za-z0-9_-]{6,400}$/.test(String(target))) throw new Error('event_watch_target_required');
  } else if (target) {
    throw new Error('event_watch_target_not_supported');
  }
}

function resolvedOperation(definition, action, target) {
  const operation = definition[action];
  if (!operation) throw new Error(action === 'remove' ? 'event_watch_cannot_be_removed' : 'event_watch_operation_not_supported');
  return {
    ...operation,
    path: operation.path.replace('{target}', encodeURIComponent(String(target || ''))),
  };
}

async function updateEventWatch(lark, { action, type, target }) {
  if (!['add', 'remove'].includes(action)) throw new Error('event_watch_action_invalid');
  const definition = watchDefinition(type);
  if (!definition) throw new Error('event_watch_type_invalid');
  validateTarget(definition, target);
  const operation = resolvedOperation(definition, action, target);
  const response = await lark.larkApiPrivate(operation.method, operation.path);
  return {
    type,
    action,
    identity: definition.identity,
    eventKeys: [...definition.eventKeys],
    targetFingerprint: fingerprint(target),
    response,
  };
}

function publicWatchCatalog() {
  return Object.entries(EVENT_WATCHES).map(([type, definition]) => ({
    type,
    identity: definition.identity,
    eventKeys: [...definition.eventKeys],
    targetRequired: definition.targetRequired,
    removable: Boolean(definition.remove),
  }));
}

module.exports = {
  EVENT_WATCHES,
  publicWatchCatalog,
  resolvedOperation,
  updateEventWatch,
  validateTarget,
  watchDefinition,
};
