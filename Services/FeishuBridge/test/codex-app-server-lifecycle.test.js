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
  assert.match(body, /const turnRunner = new CodexAppServer\(\)/);
  assert.match(body, /return await turnRunner\.runTurn/);
  assert.match(body, /finally\s*{[\s\S]*await turnRunner\.close\(\)/);
  assert.doesNotMatch(body, /codexAppServer\.runTurn/);
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
