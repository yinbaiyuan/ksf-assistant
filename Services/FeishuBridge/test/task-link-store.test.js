const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const test = require('node:test');
const { assertPrivateMode } = require('./helpers/platform-private');

const originalHome = os.homedir;
const modulePath = require.resolve('../lib/task-link-store');

function isolatedStore() {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), 'task-link-store-'));
  os.homedir = () => root;
  delete require.cache[modulePath];
  const api = require('../lib/task-link-store');
  const cwd = path.join(root, 'repo');
  fs.mkdirSync(cwd);
  return { root, cwd, api };
}

test.afterEach(() => { os.homedir = originalHome; delete require.cache[modulePath]; });

test('stores private link state and exposes no thread id publicly', () => {
  const { root, cwd, api } = isolatedStore();
  const link = api.upsertLink({
    threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd, title: '测试任务', targetAlias: '我',
  });
  const file = api.defaultTaskLinkPath();
  assertPrivateMode(path.dirname(file), 0o700);
  assertPrivateMode(file, 0o600);
  assert.equal(api.listPublic()[0].taskKey, link.taskKey);
  assert.equal(JSON.stringify(api.listPublic()).includes(link.threadId), false);
  assert.equal(link.cardRevision, 0);
  assert.equal(link.nextTurnMode, 'default');
  assert.equal(link.activeTurnMode, '');
  assert.equal(Object.hasOwn(api.listPublic()[0], 'cardRevision'), false);
  assert.equal(api.listPublic()[0].nextTurnMode, 'default');
  assert.equal(api.listPublic()[0].controls.canSetMode, true);
  assert.equal(api.operatorMatches({ target: { type: 'open_id', id: 'ou_owner' } }, 'ou_owner'), true);
  assert.equal(api.operatorMatches({ target: { type: 'open_id', id: 'ou_owner' } }, 'ou_other'), false);
  fs.rmSync(root, { recursive: true, force: true });
});

test('matches replies, renews leases, and keeps only one pending message', () => {
  const { root, cwd, api } = isolatedStore();
  let link = api.upsertLink({ threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd, title: '测试', targetAlias: '我' });
  link = api.updateLink(link.id, { rootMessageId: 'om_root', messageIds: ['om_result'] });
  assert.equal(api.resolveReply({ parent_id: 'om_result' }).id, link.id);
  const before = link.expiresAt;
  link = api.updateLink(link.id, { state: 'queued', pendingMessageId: 'om_pending' }, undefined, { renew: true });
  assert.ok(Date.parse(link.expiresAt) >= Date.parse(before));
  assert.equal(link.pendingMessageId, 'om_pending');
  assert.equal(api.DEFAULT_LEASE_MS, 24 * 60 * 60 * 1000);
  fs.rmSync(root, { recursive: true, force: true });
});

test('reconnecting an active task refreshes authoritative turn timing', () => {
  const { root, cwd, api } = isolatedStore();
  const input = {
    threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd, title: '测试', targetAlias: '我',
  };
  let link = api.upsertLink({ ...input, progress: { phase: '完成', durationSeconds: 8 } });
  link = api.upsertLink({ ...input, progress: { phase: '完成', durationSeconds: 13 } });
  assert.equal(link.progress.durationSeconds, 13);
  fs.rmSync(root, { recursive: true, force: true });
});

test('migrates v1 links atomically into split link and turn state', () => {
  const { root, cwd, api } = isolatedStore();
  const file = api.defaultTaskLinkPath();
  fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  const now = new Date().toISOString();
  fs.writeFileSync(file, JSON.stringify({
    protocol: api.PROTOCOL,
    updatedAt: now,
    links: [{
      id: 'LINK-LEGACY', taskKey: '0123456789abcdef0123',
      threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd, title: '旧连接',
      targetAlias: '我', target: { type: 'open_id', id: 'ou_private' },
      state: 'waiting_current_turn', createdAt: now, updatedAt: now,
      lastInteractionAt: now, expiresAt: new Date(Date.now() + 1000).toISOString(),
      messageIds: [], progress: {},
    }],
  }), { mode: 0o600 });
  const migrated = api.migrateStore();
  assert.equal(migrated.schemaVersion, 2);
  assert.equal(migrated.links[0].linkState, 'active');
  assert.equal(migrated.links[0].turnState, 'running');
  assert.equal(migrated.links[0].turnOwner, 'desktop');
  assert.equal(migrated.links[0].nextTurnMode, 'default');
  assert.equal(migrated.links[0].activeTurnMode, '');
  assert.equal(Date.parse(migrated.links[0].expiresAt) - Date.parse(now), api.DEFAULT_LEASE_MS);
  assert.equal(JSON.parse(fs.readFileSync(file, 'utf8')).schemaVersion, 2);
  fs.rmSync(root, { recursive: true, force: true });
});

test('stores the next and active collaboration modes but rejects unsupported values', () => {
  const { root, cwd, api } = isolatedStore();
  let link = api.upsertLink({
    threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd, title: '测试', targetAlias: '我',
  });
  link = api.updateLink(link.id, { nextTurnMode: 'plan', activeTurnMode: 'plan' });
  assert.equal(link.nextTurnMode, 'plan');
  assert.equal(link.activeTurnMode, 'plan');
  assert.equal(api.publicLink(link).nextTurnMode, 'plan');
  assert.equal(api.publicLink(link).activeTurnMode, 'plan');
  assert.throws(() => api.updateLink(link.id, { nextTurnMode: 'unknown' }), /collaboration mode/);
  fs.rmSync(root, { recursive: true, force: true });
});

test('plan_ready stores only the plan turn and revision while exposing safe execution controls', () => {
  const { root, cwd, api } = isolatedStore();
  let link = api.upsertLink({
    threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd, title: '计划任务', targetAlias: '我',
  });
  link = api.updateLink(link.id, {
    turnState: 'plan_ready', turnOwner: 'none', actionRequired: 'feishu',
    pendingPlanTurnId: 'turn-plan', pendingPlanRevision: '0123456789abcdef0123',
    nextTurnMode: 'plan', activeTurnMode: 'plan',
  });
  const publicLink = api.publicLink(link);
  assert.equal(publicLink.hasPendingPlanImplementation, true);
  assert.equal(publicLink.planImplementationRevision, '0123456789abcdef0123');
  assert.equal(publicLink.controls.canImplementPlan, true);
  assert.equal(publicLink.controls.canSend, true);
  assert.equal(publicLink.controls.canSetMode, false);
  assert.equal(publicLink.controls.canInterrupt, false);
  assert.equal(JSON.stringify(publicLink).includes('turn-plan'), false);
  assert.equal(api.publicLink(link, Date.now() + 48 * 60 * 60 * 1000).linkState, 'active');

  link = api.updateLink(link.id, {
    turnState: 'running', turnOwner: 'desktop', actionRequired: 'none', activeTurnId: 'turn-next',
  });
  assert.equal(link.pendingPlanTurnId, '');
  assert.equal(link.pendingPlanRevision, '');
  assert.equal(api.publicLink(link).hasPendingPlanImplementation, false);
  fs.rmSync(root, { recursive: true, force: true });
});

test('running links do not expire mid-turn and released links cannot be resurrected', () => {
  const { root, cwd, api } = isolatedStore();
  let link = api.upsertLink({
    threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd, title: '测试', targetAlias: '我',
    turnState: 'running', turnOwner: 'desktop', actionRequired: 'none',
  });
  assert.equal(api.publicLink(link, Date.now() + 48 * 60 * 60 * 1000).linkState, 'active');
  link = api.updateLink(link.id, { linkState: 'released' });
  link = api.updateLink(link.id, {
    linkState: 'active', turnState: 'completed', turnOwner: 'none', actionRequired: 'none',
  }, undefined, { renew: true, terminalAt: new Date().toISOString() });
  assert.equal(link.linkState, 'released');
  assert.equal(api.publicLink(link).controls.canRelease, false);
  fs.rmSync(root, { recursive: true, force: true });
});

test('optimistic running links expose controls only after an authoritative turn id exists', () => {
  const { root, cwd, api } = isolatedStore();
  let link = api.upsertLink({
    threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd, title: '测试', targetAlias: '我',
    turnState: 'running', turnOwner: 'bridge', actionRequired: 'none', activeTurnId: '',
  });
  assert.equal(api.publicLink(link).controls.canSteer, false);
  assert.equal(api.publicLink(link).controls.canInterrupt, false);
  link = api.updateLink(link.id, { activeTurnId: 'turn-live' });
  assert.equal(api.publicLink(link).controls.canSteer, true);
  assert.equal(api.publicLink(link).controls.canInterrupt, true);
  fs.rmSync(root, { recursive: true, force: true });
});

test('input capture switches between task cards and consumes the next direct message once', () => {
  const { root, cwd, api } = isolatedStore();
  const target = { type: 'open_id', id: 'ou_owner' };
  let first = api.upsertLink({
    threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd, title: '任务一', targetAlias: '我',
    target, turnState: 'completed', turnOwner: 'none', actionRequired: 'none',
  });
  let second = api.upsertLink({
    threadId: '019c5678-abcd-7890-abcd-123456789abc', cwd, title: '任务二', targetAlias: '我',
    target, turnState: 'completed', turnOwner: 'none', actionRequired: 'none',
  });
  first = api.updateLink(first.id, { rootMessageId: 'om_first' });
  second = api.updateLink(second.id, { rootMessageId: 'om_second' });
  const startedAt = new Date('2026-09-02T10:00:00.000Z');

  let armed = api.armInputCapture(first.taskKey, {
    chatId: 'oc_direct', operatorId: 'ou_owner', messageId: 'om_first',
  }, undefined, startedAt);
  assert.equal(api.inputCaptureView(armed.link, startedAt).active, true);
  assert.equal(api.inputCaptureView(armed.link, startedAt).remainingSeconds, 120);
  assert.deepEqual(armed.replacedLinks, []);

  armed = api.armInputCapture(second.taskKey, {
    chatId: 'oc_direct', operatorId: 'ou_owner', messageId: 'om_second',
  }, undefined, new Date(startedAt.getTime() + 1000));
  assert.deepEqual(armed.replacedLinks.map((link) => link.taskKey), [first.taskKey]);
  assert.equal(api.inputCaptureView(api.findLinkByTaskKey(first.taskKey), startedAt).active, false);
  assert.equal(api.consumeInputCapture({ chatId: 'oc_direct', operatorId: 'ou_other' }, undefined, startedAt), null);

  const consumed = api.consumeInputCapture({
    chatId: 'oc_direct', operatorId: 'ou_owner',
  }, undefined, new Date(startedAt.getTime() + 2000));
  assert.equal(consumed.taskKey, second.taskKey);
  assert.equal(api.inputCaptureView(consumed, startedAt).active, false);
  assert.equal(api.consumeInputCapture({
    chatId: 'oc_direct', operatorId: 'ou_owner',
  }, undefined, new Date(startedAt.getTime() + 3000)), null);
  assert.equal(JSON.stringify(api.listPublic()).includes('oc_direct'), false);
  assert.equal(JSON.stringify(api.listPublic()).includes('ou_owner'), false);
  fs.rmSync(root, { recursive: true, force: true });
});

test('input capture expires, cancels safely, and rejects forged card bindings', () => {
  const { root, cwd, api } = isolatedStore();
  const target = { type: 'open_id', id: 'ou_owner' };
  let link = api.upsertLink({
    threadId: '019c1234-abcd-7890-abcd-123456789abc', cwd, title: '测试', targetAlias: '我',
    target, turnState: 'completed', turnOwner: 'none', actionRequired: 'none',
  });
  link = api.updateLink(link.id, { rootMessageId: 'om_card' });
  const startedAt = new Date('2026-09-02T10:00:00.000Z');

  assert.throws(() => api.armInputCapture(link.taskKey, {
    chatId: 'oc_direct', operatorId: 'ou_other', messageId: 'om_card',
  }, undefined, startedAt), /operator/);
  assert.throws(() => api.armInputCapture(link.taskKey, {
    chatId: 'oc_direct', operatorId: 'ou_owner', messageId: 'om_forged',
  }, undefined, startedAt), /card message/);

  api.armInputCapture(link.taskKey, {
    chatId: 'oc_direct', operatorId: 'ou_owner', messageId: 'om_card',
  }, undefined, startedAt);
  assert.equal(api.cancelInputCapture(link.taskKey, {
    chatId: 'oc_direct', operatorId: 'ou_owner',
  }, undefined, new Date(startedAt.getTime() + 1000)).canceled, true);
  assert.equal(api.cancelInputCapture(link.taskKey, {
    chatId: 'oc_direct', operatorId: 'ou_owner',
  }, undefined, new Date(startedAt.getTime() + 2000)).canceled, false);

  api.armInputCapture(link.taskKey, {
    chatId: 'oc_direct', operatorId: 'ou_owner', messageId: 'om_card',
  }, undefined, startedAt);
  const expired = api.expireInputCaptures(undefined, new Date(startedAt.getTime() + api.INPUT_CAPTURE_MS + 1));
  assert.deepEqual(expired.map((item) => item.taskKey), [link.taskKey]);
  assert.equal(api.consumeInputCapture({
    chatId: 'oc_direct', operatorId: 'ou_owner',
  }, undefined, new Date(startedAt.getTime() + api.INPUT_CAPTURE_MS + 2)), null);
  fs.rmSync(root, { recursive: true, force: true });
});

test('rejects symbolic-link stores and damaged protocols', () => {
  const { root, api } = isolatedStore();
  const file = api.defaultTaskLinkPath();
  fs.mkdirSync(path.dirname(file), { recursive: true, mode: 0o700 });
  const target = path.join(root, 'outside.json');
  fs.writeFileSync(target, '{}', { mode: 0o600 });
  if (process.platform !== 'win32') {
    fs.symlinkSync(target, file);
    assert.throws(() => api.readStore(), /symbolic link/);
    fs.unlinkSync(file);
  }
  fs.writeFileSync(file, '{"protocol":"wrong","links":[]}', { mode: 0o600 });
  assert.throws(() => api.readStore(), /unsupported or damaged/);
  fs.rmSync(root, { recursive: true, force: true });
});
