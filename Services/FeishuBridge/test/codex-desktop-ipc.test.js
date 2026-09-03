const test = require('node:test');
const assert = require('node:assert/strict');

const {
  CodexDesktopTaskController,
  DesktopIPCFrameDecoder,
  desktopThreadURL,
  encodeFrame,
  extractTurnId,
  matchingDesktopUserInputRequest,
  normalizeCollaborationMode,
  normalizeDesktopInput,
  defaultOpenTask,
  desktopEndpointReady,
  validateDesktopEndpoint,
} = require('../lib/codex-desktop-ipc');

test('Desktop IPC frames survive fragmented reads', () => {
  const frame = encodeFrame({ type: 'response', requestId: 'r1', result: { ok: true } });
  const decoder = new DesktopIPCFrameDecoder();
  assert.deepEqual(decoder.append(frame.subarray(0, 3)), []);
  assert.deepEqual(decoder.append(frame.subarray(3)), [
    { type: 'response', requestId: 'r1', result: { ok: true } },
  ]);
});

test('Desktop task deep links encode the exact task id', () => {
  assert.equal(desktopThreadURL('task/中文'), 'codex://threads/task%2F%E4%B8%AD%E6%96%87');
  assert.throws(() => desktopThreadURL('bad\nid'), /invalid Codex task id/);
});

test('Windows Desktop IPC accepts only an explicit named pipe', () => {
  const endpoint = String.raw`\\.\pipe\codex-desktop`;
  assert.deepEqual(validateDesktopEndpoint(endpoint, 'win32'), { type: 'named-pipe', path: endpoint });
  assert.throws(() => validateDesktopEndpoint('C:\\temp\\ipc.sock', 'win32'), /named pipe/);
  assert.throws(() => validateDesktopEndpoint('', 'win32'), /not configured/);
});

test('Windows task opening keeps the task URL out of PowerShell arguments', () => {
  let captured;
  defaultOpenTask('thread/private', {
    platform: 'win32', projectRoot: 'C:\\bridge', env: {},
    spawnSyncFn(command, args, options) {
      captured = { command, args, options };
      return { status: 0, stdout: '' };
    },
  });
  assert.equal(captured.command, 'powershell.exe');
  assert.equal(captured.args.some((item) => item.includes('thread')), false);
  assert.equal(captured.options.env.CODEX_TASK_URL, 'codex://threads/thread%2Fprivate');
});

test('Windows Desktop readiness checks the configured pipe through a fixed adapter', () => {
  const endpoint = String.raw`\\.\pipe\codex-desktop`;
  let captured;
  const ready = desktopEndpointReady(endpoint, {
    platform: 'win32', projectRoot: 'C:\\bridge', env: {},
    spawnSyncFn(command, args, options) {
      captured = { command, args, options };
      return { status: 0, stdout: '{"ready":true}' };
    },
  });
  assert.equal(ready, true);
  assert.equal(captured.command, 'powershell.exe');
  assert.equal(captured.args.some((item) => item.includes('codex-desktop')), false);
  assert.equal(captured.options.env.CODEX_DESKTOP_IPC_PATH, endpoint);
});

test('Desktop text input carries the native text element field', () => {
  assert.deepEqual(normalizeDesktopInput([
    { type: 'text', text: '继续' },
    { type: 'localImage', path: '/tmp/example.png' },
  ]), [
    { type: 'text', text: '继续', text_elements: [] },
    { type: 'localImage', path: '/tmp/example.png' },
  ]);
});

test('Desktop collaboration modes require a model and keep only supported settings', () => {
  assert.deepEqual(normalizeCollaborationMode({
    mode: 'plan',
    settings: { model: 'gpt-5.5', reasoning_effort: 'high', developer_instructions: 'ignored' },
  }), {
    mode: 'plan',
    settings: { model: 'gpt-5.5', reasoning_effort: 'high', developer_instructions: null },
  });
  assert.throws(() => normalizeCollaborationMode({ mode: 'plan', settings: {} }), /invalid/);
  assert.throws(() => normalizeCollaborationMode({ mode: 'unknown', settings: { model: 'gpt-5.5' } }), /invalid/);
});

test('turn ids are recovered from nested follower responses', () => {
  assert.equal(extractTurnId({ result: { result: { turn: { id: 'turn-1' } } } }), 'turn-1');
});

function controllerHarness(response = { result: { result: { turn: { id: 'turn-1' } } } }) {
  const requests = [];
  const resolutions = [];
  let closed = false;
  const session = {
    async connect() {},
    async resolveUserInputRequest(request) {
      resolutions.push(request);
      return { ownerClientId: 'desktop-owner-1', requestId: 'server-request-7' };
    },
    async requestFollower(request) {
      requests.push(request);
      return response;
    },
    close() { closed = true; },
  };
  const controller = new CodexDesktopTaskController({
    socketPath: '/private/socket',
    sessionFactory: () => session,
    openTask() {},
  });
  return { controller, requests, resolutions, wasClosed: () => closed };
}

test('Desktop snapshots map a rollout question to the live server request id', () => {
  const expected = [{ id: 'choice' }];
  const request = matchingDesktopUserInputRequest({
    requests: [
      { id: 'approval-1', method: 'item/fileChange/requestApproval', params: { turnId: 'turn-1' } },
      {
        id: 'server-request-7', method: 'item/tool/requestUserInput',
        params: { turnId: 'turn-1', questions: [{ id: 'choice' }] },
      },
    ],
  }, { turnId: 'turn-1', questions: expected });

  assert.equal(request.id, 'server-request-7');
  assert.equal(matchingDesktopUserInputRequest({
    requests: [{
      id: 'other-request', method: 'item/tool/requestUserInput',
      params: { turnId: 'turn-1', questions: [{ id: 'different' }] },
    }],
  }, { turnId: 'turn-1', questions: expected }), null);
});

test('Feishu starts the linked task through its Desktop owner with full access', async () => {
  const harness = controllerHarness();
  const result = await harness.controller.startTurn({
    threadId: 'thread-1',
    cwd: '/project',
    input: [{ type: 'text', text: '继续任务' }],
    collaborationMode: {
      mode: 'plan',
      settings: { model: 'gpt-5.5', reasoning_effort: 'high' },
    },
  });

  assert.equal(result.turnId, 'turn-1');
  assert.equal(harness.requests[0].method, 'thread-follower-start-turn');
  assert.equal(harness.requests[0].version, 2);
  assert.deepEqual(harness.requests[0].params.turnStart.request.sandboxPolicy, {
    type: 'dangerFullAccess',
  });
  assert.equal(harness.requests[0].params.turnStart.request.approvalPolicy, 'never');
  assert.deepEqual(harness.requests[0].params.turnStart.request.collaborationMode, {
    mode: 'plan',
    settings: { model: 'gpt-5.5', reasoning_effort: 'high', developer_instructions: null },
  });
  assert.equal(harness.requests[0].params.turnStart.context.inheritThreadSettings, true);
  assert.equal(harness.wasClosed(), true);
});

test('Feishu changes the linked task collaboration mode through its Desktop owner', async () => {
  const harness = controllerHarness({ result: { ok: true } });
  await harness.controller.updateCollaborationMode({
    threadId: 'thread-1',
    collaborationMode: {
      mode: 'plan',
      settings: { model: 'gpt-5.5', reasoning_effort: 'medium' },
    },
  });

  assert.equal(harness.requests[0].method, 'thread-follower-update-thread-settings');
  assert.equal(harness.requests[0].version, 1);
  assert.deepEqual(harness.requests[0].params, {
    conversationId: 'thread-1',
    threadSettings: {
      collaborationMode: {
        mode: 'plan',
        settings: {
          model: 'gpt-5.5', reasoning_effort: 'medium', developer_instructions: null,
        },
      },
    },
  });
});

test('Feishu steering and interruption target the Desktop task owner', async () => {
  const harness = controllerHarness({ result: { ok: true } });
  const turnId = await harness.controller.steer({
    threadId: 'thread-1',
    cwd: '/project',
    input: [{ type: 'text', text: '先修测试' }],
    turnId: 'turn-active',
  });
  await harness.controller.interrupt({ threadId: 'thread-1', turnId: 'turn-active' });

  assert.equal(turnId, 'turn-active');
  assert.equal(harness.requests[0].method, 'thread-follower-steer-turn');
  assert.equal(harness.requests[0].version, 1);
  assert.deepEqual(harness.requests[0].params.restoreMessage.context.workspaceRoots, ['/project']);
  assert.equal(harness.requests[1].method, 'thread-follower-interrupt-turn');
  assert.equal(harness.requests[1].version, 4);
  assert.deepEqual(harness.requests[1].params, {
    conversationId: 'thread-1',
    mode: 'user-stop',
    expectedTurnId: 'turn-active',
  });
});

test('Feishu submits a user-input answer to the Desktop-owned request', async () => {
  const harness = controllerHarness({ result: { ok: true } });
  const answered = await harness.controller.submitUserInput({
    threadId: 'thread-1',
    turnId: 'turn-plan',
    questions: [{ id: 'card_test_choice' }],
    response: {
      answers: { card_test_choice: { answers: ['方案 A (Recommended)'] } },
    },
  });

  assert.equal(answered, true);
  assert.equal(harness.resolutions.length, 1);
  assert.equal(harness.resolutions[0].turnId, 'turn-plan');
  assert.deepEqual(harness.resolutions[0].questions, [{ id: 'card_test_choice' }]);
  assert.equal(harness.requests[0].method, 'thread-follower-submit-user-input');
  assert.equal(harness.requests[0].version, 1);
  assert.equal(harness.requests[0].targetClientId, 'desktop-owner-1');
  assert.deepEqual(harness.requests[0].params, {
    conversationId: 'thread-1',
    requestId: 'server-request-7',
    response: {
      answers: { card_test_choice: { answers: ['方案 A (Recommended)'] } },
    },
  });
  assert.equal(harness.wasClosed(), true);
});
