import test from 'node:test';
import assert from 'node:assert/strict';
import { mkdtempSync, readFileSync, writeFileSync, rmSync, readdirSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { spawnSync } from 'node:child_process';
import { compareAdaptationReports } from './review-lark-skills-upgrade.mjs';

const script = fileURLToPath(new URL('./review-lark-skills-upgrade.mjs', import.meta.url));
const hash = character => character.repeat(64);
const file = (name, upstream = 'a', adapted = 'b') => ({ path: name, upstreamSha256: hash(upstream), adaptedSha256: hash(adapted), changed: upstream !== adapted });

function fixture() {
  return {
    schemaVersion: 1,
    revision: 'ksfas-entry-v1',
    upstreamVersion: '1.0.93',
    upstreamManifestSha256: hash('a'),
    adapterSources: { 'adapt-lark-skills.mjs': hash('b'), 'adapt-lark-skill-python.py': hash('c') },
    files: [file('lark-doc/SKILL.md'), file('lark-sheets/scripts/helper.py')],
    markdown: [{ path: 'lark-doc/SKILL.md', calls: [{ line: 9, state: 'entry-adapted-template', command: 'docs +fetch' }] }],
    python: { filesChanged: ['lark-sheets/scripts/helper.py'], callsites: [{ file: 'lark-sheets/scripts/helper.py', line: 20, kind: 'run_sheets', shortcut: '+cells-get' }] },
  };
}

test('reports added, removed and modified hashes without confusing adaptation changed markers with upgrade changes', () => {
  const before = fixture();
  before.files.push(file('lark-old/SKILL.md'));
  const after = fixture();
  after.files.push(file('lark-new/SKILL.md'));
  after.files[0] = file('lark-doc/SKILL.md', 'c', 'b');
  const result = compareAdaptationReports(before, after);
  assert.deepEqual(result.files.added, [file('lark-new/SKILL.md')]);
  assert.deepEqual(result.files.removed, [file('lark-old/SKILL.md')]);
  assert.deepEqual(result.files.modified, [{ before: before.files[0], after: after.files[0] }]);
  assert.deepEqual(result.commandEntries, { added: [], removed: [], modified: [] });
  assert.deepEqual(compareAdaptationReports(before, before).files, { added: [], removed: [], modified: [] });
  after.files[0] = file('lark-doc/SKILL.md', 'a', 'd');
  assert.equal(compareAdaptationReports(fixture(), after).files.modified.length, 1);
});

test('compares Markdown and Python command entries including duplicate calls on one line and line shifts', () => {
  const before = fixture();
  before.markdown[0].calls.push({ line: 9, state: 'diagnostic', command: 'schema docs' });
  const after = structuredClone(before);
  after.markdown[0].calls[0] = { line: 9, state: 'restricted-arguments', command: 'docs +fetch', unsupportedFlags: ['new-flag'] };
  after.python.callsites[0].shortcut = '+cells-set';
  const result = compareAdaptationReports(before, after);
  assert.equal(result.commandEntries.modified.length, 2);
  const markdown = result.commandEntries.modified.find(change => change.after.source === 'markdown');
  assert.equal(markdown.before.calls.length, 2);
  assert.equal(markdown.after.calls.length, 2);
  assert.equal(result.restricted.after[0].call.unsupportedFlags[0], 'new-flag');
  after.python.callsites[0].line = 21;
  const shifted = compareAdaptationReports(before, after);
  assert.equal(shifted.commandEntries.added[0].line, 21);
  assert.equal(shifted.commandEntries.removed[0].line, 20);
});

test('keeps unresolved and restricted calls on both sides, never promoting unknown labels to support', () => {
  const before = fixture();
  before.markdown[0].calls.push(
    { line: 10, state: 'unresolved-template', command: '<domain> +action', reason: 'dynamic input' },
    { line: 11, state: 'restricted', command: 'event consume', reasons: ['lifecycle boundary'] },
    { line: 12, state: 'desktop-control', command: 'auth login' },
    { line: 13, state: 'restricted-reference', command: 'skills read' },
    { line: 14, state: 'local-reference', command: 'skills read' },
    { line: 15, state: 'supported', command: 'future +command', futureDetail: 'not reviewed' },
  );
  before.python.callsites.push({ file: 'lark-sheets/scripts/helper.py', line: 30, kind: 'unknown-runner', shortcut: '+future' });
  const after = structuredClone(before);
  after.markdown[0].calls = after.markdown[0].calls.filter(call => call.line !== 11);
  const result = compareAdaptationReports(before, after);
  assert.equal(result.restricted.before.length, 4);
  assert.equal(result.restricted.after.length, 3);
  assert.equal(result.unresolved.before.length, 3);
  assert.equal(result.unresolved.after.length, 3);
  assert.equal(result.unresolved.after.find(entry => entry.call.state === 'supported').call.futureDetail, 'not reviewed');
  assert.ok(result.limitations.some(value => value.includes('not upgrade approved')));
  assert.equal('supported' in result, false);
  assert.equal('approved' in result, false);
});

test('canonical output is deterministic across inventory/object order and the pure comparator does not mutate inputs', () => {
  const before = fixture();
  before.markdown[0].calls.push({ line: 9, state: 'restricted-arguments', command: 'docs +fetch', unsupportedFlags: ['z', 'a'], reasons: ['z', 'a'] });
  const original = JSON.stringify(before);
  const reordered = JSON.parse(original);
  reordered.files.reverse();
  reordered.markdown[0].calls.reverse();
  reordered.markdown[0].calls[0].unsupportedFlags.reverse();
  reordered.markdown[0].calls[0].reasons.reverse();
  reordered.adapterSources = Object.fromEntries(Object.entries(reordered.adapterSources).reverse());
  const expected = JSON.stringify(compareAdaptationReports(before, before));
  assert.equal(JSON.stringify(compareAdaptationReports(reordered, before)), expected);
  assert.equal(JSON.stringify(compareAdaptationReports(before, reordered)), expected);
  assert.equal(JSON.stringify(before), original);
  const result = compareAdaptationReports(before, before);
  result.before.adapterSources['adapt-lark-skills.mjs'] = 'changed output';
  assert.equal(JSON.stringify(before), original);
});

test('adapter provenance changes remain visible without file or command changes', () => {
  const before = fixture();
  const after = fixture();
  after.revision = 'ksfas-entry-v2';
  after.upstreamVersion = '1.0.94';
  after.upstreamManifestSha256 = hash('e');
  after.adapterSources['adapt-lark-skills.mjs'] = hash('f');
  const result = compareAdaptationReports(before, after);
  assert.equal(result.before.revision, 'ksfas-entry-v1');
  assert.equal(result.after.revision, 'ksfas-entry-v2');
  assert.equal(result.after.adapterSources['adapt-lark-skills.mjs'], hash('f'));
  assert.deepEqual(result.files, { added: [], removed: [], modified: [] });
});

test('malformed, incomplete and ambiguous inventories fail closed', () => {
  const mutations = [
    report => { report.schemaVersion = 2; },
    report => { delete report.markdown; },
    report => { delete report.python.callsites; },
    report => { report.files[0].adaptedSha256 = 'invalid'; },
    report => { report.files[0].path = '../escape'; },
    report => { report.files[0].changed = false; },
    report => { report.files.push(report.files[0]); },
    report => { report.markdown.push(report.markdown[0]); },
    report => { report.markdown[0].calls[0].line = 0; },
    report => { report.markdown[0].calls[0].unsupportedFlags = 'unknown'; },
    report => { report.python.callsites[0].file = 'missing.py'; },
    report => { delete report.python.callsites[0].shortcut; },
    report => { report.python.filesChanged.push('missing.py'); },
    report => { report.adapterSources['adapter.js'] = 'invalid'; },
  ];
  for (const mutate of mutations) {
    const invalid = fixture();
    mutate(invalid);
    assert.throws(() => compareAdaptationReports(fixture(), invalid));
    assert.throws(() => compareAdaptationReports(invalid, fixture()));
  }
});

test('CLI reads only supplied reports, emits deterministic JSON and does not alter input or lock files', t => {
  const root = mkdtempSync(path.join(tmpdir(), 'ksfas-upgrade-review-'));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const before = path.join(root, 'before report.json');
  const after = path.join(root, 'after report.json');
  const locked = path.join(root, 'lark-skills.json');
  writeFileSync(before, JSON.stringify(fixture()));
  writeFileSync(after, JSON.stringify(fixture()));
  writeFileSync(locked, 'locked provenance');
  const snapshot = () => Object.fromEntries(readdirSync(root).sort().map(name => [name, readFileSync(path.join(root, name), 'utf8')]));
  const initial = snapshot();
  const run = args => spawnSync(process.execPath, [script, ...args], { cwd: root, encoding: 'utf8' });
  const first = run(['--before', before, '--after', after]);
  const second = run(['--after', after, '--before', before]);
  assert.equal(first.status, 0, first.stderr);
  assert.equal(first.stderr, '');
  assert.equal(first.stdout, second.stdout);
  assert.deepEqual(JSON.parse(first.stdout), compareAdaptationReports(fixture(), fixture()));
  assert.deepEqual(snapshot(), initial);
  for (const args of [[], ['--before', before], ['--before', before, '--before', after], ['--before', before, '--after', after, '--write-lock', locked]]) {
    const invalid = run(args);
    assert.equal(invalid.status, 2);
    assert.equal(invalid.stdout, '');
    assert.equal(JSON.parse(invalid.stderr).error.code, 'invalid_arguments');
  }
  for (const contents of ['{', '{}', JSON.stringify({ ...fixture(), schemaVersion: 2 })]) {
    writeFileSync(after, contents);
    const invalid = run(['--before', before, '--after', after]);
    assert.equal(invalid.status, 1);
    assert.equal(invalid.stdout, '');
    assert.equal(JSON.parse(invalid.stderr).error.code, 'invalid_report');
  }
  const missing = run(['--before', before, '--after', path.join(root, 'missing.json')]);
  assert.equal(missing.status, 1);
  assert.equal(readFileSync(locked, 'utf8'), initial['lark-skills.json']);
  assert.equal(readFileSync(before, 'utf8'), initial['before report.json']);
});
