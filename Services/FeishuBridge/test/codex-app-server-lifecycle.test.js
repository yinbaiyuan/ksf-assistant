const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const source = fs.readFileSync(path.join(__dirname, '..', 'bot-bridge.js'), 'utf8');

function functionBody(name, nextName) {
  const start = source.indexOf(`async function ${name}`);
  const end = source.indexOf(`async function ${nextName}`, start + 1);
  assert.ok(start >= 0, `${name} must exist`);
  assert.ok(end > start, `${nextName} must follow ${name}`);
  return source.slice(start, end);
}

test('default and background Codex turns use a transient app-server writer', () => {
  const body = functionBody('runCodex', 'answerWithDefaultCodexUnlocked');
  assert.match(body, /const turnRunner = new CodexAppServer\(\{/);
  assert.match(body, /transportPreference: bridgeOwnedTransportPreference/);
  assert.match(body, /return await turnRunner\.runTurn/);
  assert.match(body, /finally\s*{[\s\S]*await turnRunner\.close\(\)/);
  assert.doesNotMatch(body, /codexAppServer\.runTurn/);
});

test('managed bridge-owned turns bypass proxy and daemon with a five-second initialization bound', () => {
  assert.match(source, /bridgeOwnedTransportPreference = managedRuntime \? 'app-server'/);
  assert.match(source, /bridgeOwnedAutoStartDaemon = managedRuntime \? false/);
  assert.match(source, /codexAppServerRequestTimeoutMs = Number\([\s\S]*\|\| 60 \* 1000\)/);
  assert.match(source, /codexAppServerInitializeTimeoutMs = Number\([\s\S]*managedRuntime \? 5 \* 1000/);
  assert.match(source, /this\.requestTimeoutMs = requestTimeoutMs/);
  assert.match(source, /this\.initializeTimeoutMs = initializeTimeoutMs/);
  assert.match(source, /requestRaw\('initialize',[\s\S]*this\.initializeTimeoutMs\)/);
});

test('managed project promotion uses the host-owned support directory without inheriting user configuration', () => {
  const start = source.indexOf('function projectPromotionClientFor');
  const end = source.indexOf('\nlet taskLinkLeaseTimer', start);
  assert.ok(start >= 0 && end > start);
  const body = source.slice(start, end);
  assert.match(body, /env:\s*{}/);
  assert.match(body, /supportDirectory:\s*process\.env\.CODEX_USAGE_BAR_SUPPORT_DIR/);
  assert.match(body, /allowDiscovery:\s*false/);
});

test('new thread naming shares the transient writer and cannot block a turn for over one second', () => {
  const runTurnStart = source.indexOf('  async runTurn({');
  const runTurnEnd = source.indexOf('\n  answerUserInput(', runTurnStart);
  const body = source.slice(runTurnStart, runTurnEnd);
  assert.match(body, /!sessionId && threadName/);
  assert.match(body, /setThreadName\(thread\.id, threadName, 1000\)/);
  assert.doesNotMatch(functionBody('answerWithDefaultCodexUnlocked', 'promoteVerifiedDefaultConversation'), /await codexAppServer\.setThreadName/);
});
test('transient app-server releases its thread subscription before closing transport', () => {
  const closeStart = source.indexOf('  async close() {');
  const closeEnd = source.indexOf('\n  handleLine(', closeStart);
  const body = source.slice(closeStart, closeEnd);
  const unsubscribe = body.indexOf("requestRaw('thread/unsubscribe'");
  const teardown = body.indexOf('this.teardown(');
  assert.ok(unsubscribe >= 0);
  assert.ok(teardown > unsubscribe);
});

test('task-linked turns stay on Desktop IPC without initializing the shared app-server', () => {
  assert.equal((source.match(/codexAppServer\.collaborationModes\(/g) || []).length, 0);
  const start = source.indexOf('async function executeTaskLink');
  const end = source.indexOf('\nfunction cleanupTaskLinkAssetDirectories', start);
  assert.ok(start >= 0);
  assert.ok(end > start);
  const body = source.slice(start, end);
  assert.match(body, /taskLinkSubmittedTurnMode/);
  assert.match(body, /readTaskLinkSnapshot/);
  assert.match(body, /taskLink:\s*true/);
});

test('an already projected ordinary conversation upgrades before its next follow-up uses Desktop IPC', () => {
  const promotionStart = source.indexOf('async function promoteDefaultConversationBeforeFollowup');
  const detectionStart = source.indexOf('\nasync function detectVerifiedProject', promotionStart);
  assert.ok(promotionStart >= 0 && detectionStart > promotionStart);
  const promotion = source.slice(promotionStart, detectionStart);
  assert.match(promotion, /detectVerifiedProject\(threadId, context\)/);
  assert.match(promotion, /readTaskLinkSnapshot\(threadId\)/);
  assert.match(promotion, /promoteVerifiedDefaultConversation/);

  const followupStart = source.indexOf('async function handleChatFollowup');
  const taskFollowupStart = source.indexOf('\nasync function handleTaskLinkFollowup', followupStart);
  const followup = source.slice(followupStart, taskFollowupStart);
  const upgrade = followup.indexOf('promoteDefaultConversationBeforeFollowup(context)');
  const desktopContinuation = followup.indexOf('handleTaskLinkFollowup(event', upgrade);
  const appServerContinuation = followup.indexOf('answerWithDefaultCodex(action.followup', upgrade);
  assert.ok(upgrade >= 0 && desktopContinuation > upgrade && appServerContinuation > desktopContinuation);
});

test('active ordinary conversations reconcile authoritative project projections after bridge startup', () => {
  const reconcileStart = source.indexOf('async function reconcileDefaultConversationPromotions');
  const detectionStart = source.indexOf('\nasync function detectVerifiedProject', reconcileStart);
  assert.ok(reconcileStart >= 0 && detectionStart > reconcileStart);
  const reconcile = source.slice(reconcileStart, detectionStart);
  assert.match(reconcile, /item\.state === 'active'/);
  assert.match(reconcile, /withDefaultConversationLock/);
  assert.match(reconcile, /promoteDefaultConversationBeforeFollowup\(context\)/);
  assert.match(reconcile, /source: 'startup_reconciliation'/);

  const mainStart = source.indexOf('async function main()');
  assert.ok(mainStart >= 0);
  const main = source.slice(mainStart);
  const transportReady = main.indexOf('await startEventConsumerProfileController()');
  const reconcileAfterTransport = main.indexOf('reconcileDefaultConversationPromotions()', transportReady);
  assert.ok(transportReady >= 0 && reconcileAfterTransport > transportReady);
});

test('task cards recover the latest user input after a bridge restart', () => {
  const start = source.indexOf('function taskLinkDisplayInput');
  const end = source.indexOf('\nasync function patchTaskLinkCard', start);
  assert.ok(start >= 0);
  assert.ok(end > start);
  const body = source.slice(start, end);
  const explicit = body.indexOf('explicitInput');
  const cached = body.indexOf('cachedInput');
  const journal = body.indexOf('codexDesktopTurnJournal.snapshot');
  assert.ok(explicit >= 0 && cached > explicit && journal > cached);
  assert.match(body, /journalTurn\?\.lastUserMessage/);
  assert.equal((source.match(/latestInput: taskLinkDisplayInput\(link, latestInput\)/g) || []).length, 1);
  assert.match(source, /resolvedLatestInput = taskLinkDisplayInput\(link, latestInput\)/);
});
