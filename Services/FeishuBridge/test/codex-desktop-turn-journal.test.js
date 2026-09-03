const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');

const {
  CodexDesktopTurnJournal,
  applyJournalRecord,
  findRolloutPath,
} = require('../lib/codex-desktop-turn-journal');

function record(timestamp, payload, type = 'event_msg') {
  return { timestamp, type, payload };
}

test('Desktop journal distinguishes live, completed, and explicitly interrupted turns', () => {
  let latest = applyJournalRecord(null, record('2026-09-02T08:45:50Z', {
    type: 'task_started', turn_id: 'turn-1',
  }));
  assert.equal(latest.state, 'running');

  latest = applyJournalRecord(latest, record('2026-09-02T08:45:51Z', {
    turn_id: 'turn-1',
    model: 'gpt-5.5',
    effort: 'high',
    collaboration_mode: {
      mode: 'plan',
      settings: { model: 'gpt-5.5', reasoning_effort: 'high', developer_instructions: null },
    },
  }, 'turn_context'));
  assert.equal(latest.model, 'gpt-5.5');
  assert.equal(latest.effort, 'high');
  assert.equal(latest.collaborationMode.mode, 'plan');

  latest = applyJournalRecord(latest, record('2026-09-02T08:45:52Z', {
    type: 'message',
    role: 'user',
    content: [{ type: 'input_text', text: '显示我本轮发出的消息。' }],
    internal_chat_message_metadata_passthrough: { turn_id: 'turn-1' },
  }, 'response_item'));
  assert.equal(latest.lastUserMessage, '显示我本轮发出的消息。');

  latest = applyJournalRecord(latest, record('2026-09-02T08:45:55Z', {
    type: 'item_completed', turn_id: 'turn-1',
    item: { type: 'plan', text: '- [x] 读取上下文\n- [ ] 输出计划' },
  }));
  assert.equal(latest.planText, '- [x] 读取上下文\n- [ ] 输出计划');

  latest = applyJournalRecord(latest, record('2026-09-02T08:45:59Z', {
    type: 'agent_message', turn_id: 'turn-1', phase: 'final_answer', message: '最终结果',
  }));
  assert.equal(latest.lastMessagePhase, 'final_answer');
  latest = applyJournalRecord(latest, record('2026-09-02T08:46:01Z', {
    type: 'task_complete', turn_id: 'turn-1', last_agent_message: '最终结果',
  }));
  assert.deepEqual(
    {
      state: latest.state, finalText: latest.finalText,
      startedAt: latest.startedAt, completedAt: latest.completedAt,
    },
    {
      state: 'completed', finalText: '最终结果',
      startedAt: '2026-09-02T08:45:50Z', completedAt: '2026-09-02T08:46:01Z',
    },
  );

  latest = applyJournalRecord(latest, record('2026-09-02T08:47:00Z', {
    type: 'task_started', turn_id: 'turn-2',
  }));
  latest = applyJournalRecord(latest, record('2026-09-02T08:47:02Z', {
    type: 'turn_aborted', turn_id: 'turn-2', reason: 'interrupted',
  }));
  assert.equal(latest.state, 'interrupted');
  assert.equal(latest.completedAt, '2026-09-02T08:47:02Z');
});

test('Desktop journal exposes request_user_input choices and clears them after an answer', () => {
  let latest = applyJournalRecord(null, record('2026-09-03T02:00:00Z', {
    type: 'task_started', turn_id: 'turn-plan',
  }));
  latest = applyJournalRecord(latest, record('2026-09-03T02:00:03Z', {
    type: 'function_call',
    name: 'request_user_input',
    call_id: 'call-choice-1',
    arguments: JSON.stringify({
      questions: [{
        header: '测试选择',
        id: 'card_test_choice',
        question: '请选择一个测试方案：',
        options: [
          { label: '方案 A (Recommended)', description: '推荐选项' },
          { label: '方案 B', description: '普通选项' },
        ],
      }],
    }),
    internal_chat_message_metadata_passthrough: { turn_id: 'turn-plan' },
  }, 'response_item'));

  assert.equal(latest.state, 'waiting_input');
  assert.equal(latest.pendingInput.requestId, 'call-choice-1');
  assert.deepEqual(latest.pendingInput.questions[0], {
    id: 'card_test_choice',
    header: '测试选择',
    question: '请选择一个测试方案：',
    options: [
      { label: '方案 A (Recommended)', description: '推荐选项' },
      { label: '方案 B', description: '普通选项' },
    ],
    isOther: false,
    isSecret: false,
  });

  latest = applyJournalRecord(latest, record('2026-09-03T02:00:08Z', {
    type: 'function_call_output',
    call_id: 'call-choice-1',
    output: JSON.stringify({ answers: { card_test_choice: { answers: ['方案 A (Recommended)'] } } }),
    internal_chat_message_metadata_passthrough: { turn_id: 'turn-plan' },
  }, 'response_item'));
  assert.equal(latest.state, 'running');
  assert.equal(latest.pendingInput, null);
});

test('Desktop journal never copies a secret question into bridge-visible state', () => {
  let latest = applyJournalRecord(null, record('2026-09-03T02:10:00Z', {
    type: 'task_started', turn_id: 'turn-secret',
  }));
  latest = applyJournalRecord(latest, record('2026-09-03T02:10:03Z', {
    type: 'function_call', name: 'request_user_input', call_id: 'call-secret',
    arguments: JSON.stringify({ questions: [{
      header: 'Token', id: 'secret_token', question: '请输入密钥', isSecret: true,
    }] }),
    internal_chat_message_metadata_passthrough: { turn_id: 'turn-secret' },
  }, 'response_item'));
  assert.deepEqual(latest.pendingInput.questions[0], {
    id: 'secret_token', header: '', question: '', options: [], isSecret: true,
  });
});

test('Desktop journal tails the exact rollout and tolerates partial appends', (t) => {
  const codexHome = fs.mkdtempSync(path.join(os.tmpdir(), 'codex-journal-'));
  t.after(() => fs.rmSync(codexHome, { recursive: true, force: true }));
  const sessions = path.join(codexHome, 'sessions', '2026', '09', '02');
  fs.mkdirSync(sessions, { recursive: true });
  const rollout = path.join(sessions, 'rollout-2026-09-02T08-00-00-thread-1.jsonl');
  fs.writeFileSync(rollout, `${JSON.stringify(record('2026-09-02T08:45:50Z', {
    type: 'task_started', turn_id: 'turn-1',
  }))}\n`);

  assert.equal(findRolloutPath(codexHome, 'thread-1'), rollout);
  const journal = new CodexDesktopTurnJournal({ codexHome });
  assert.equal(journal.snapshot('thread-1').state, 'running');

  const completed = JSON.stringify(record('2026-09-02T08:46:01Z', {
    type: 'task_complete', turn_id: 'turn-1', last_agent_message: '完成',
  }));
  fs.appendFileSync(rollout, completed.slice(0, 20));
  assert.equal(journal.snapshot('thread-1').state, 'running');
  fs.appendFileSync(rollout, `${completed.slice(20)}\n`);
  assert.deepEqual(
    { state: journal.snapshot('thread-1').state, finalText: journal.snapshot('thread-1').finalText },
    { state: 'completed', finalText: '完成' },
  );
});

test('Desktop journal watcher pushes completed public commentary changes', async (t) => {
  const codexHome = fs.mkdtempSync(path.join(os.tmpdir(), 'codex-journal-watch-'));
  t.after(() => fs.rmSync(codexHome, { recursive: true, force: true }));
  const sessions = path.join(codexHome, 'sessions', '2026', '09', '02');
  fs.mkdirSync(sessions, { recursive: true });
  const rollout = path.join(sessions, 'rollout-2026-09-02T08-00-00-thread-watch.jsonl');
  fs.writeFileSync(rollout, `${JSON.stringify(record('2026-09-02T08:45:50Z', {
    type: 'task_started', turn_id: 'turn-watch',
  }))}\n`);

  const journal = new CodexDesktopTurnJournal({ codexHome });
  assert.equal(journal.snapshot('thread-watch').state, 'running');
  const changed = new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('journal watcher timed out')), 2000);
    const stop = journal.watch('thread-watch', (snapshot) => {
      if (snapshot?.lastMessage !== '正在检查卡片推送。') return;
      clearTimeout(timeout);
      stop();
      resolve(snapshot);
    });
    t.after(stop);
  });
  await new Promise((resolve) => setTimeout(resolve, 50));
  fs.appendFileSync(rollout, `${JSON.stringify(record('2026-09-02T08:45:55Z', {
    type: 'agent_message', turn_id: 'turn-watch', phase: 'commentary', message: '正在检查卡片推送。',
  }))}\n`);

  const snapshot = await changed;
  assert.equal(snapshot.lastMessagePhase, 'commentary');
});
