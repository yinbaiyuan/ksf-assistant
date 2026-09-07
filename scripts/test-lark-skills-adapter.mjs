import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, mkdtempSync, mkdirSync, cpSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { adaptMarkdown, adaptSkills, commandAssessment } from './adapt-lark-skills.mjs';
import { compareAdaptationReports } from './review-lark-skills-upgrade.mjs';

const repo = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const execution = JSON.parse(readFileSync(path.join(repo, 'Core/internal/usercommand/execution-manifest.json')));
const descriptors = execution.descriptors;

test('entry-only adaptation preserves names, URLs and non-command facts', () => {
  const input = '---\nname: lark-doc\ndescription: docs\nmetadata:\n  requires:\n    bins: ["lark-cli"]\n---\n# Docs\nOfficial lark-cli version 1.0.93.\nhttps://example.com/lark-cli/install\n`lark-cli docs +fetch --doc fixture`\n[guide](references/guide.md)\n';
  const result = adaptMarkdown(input, 'lark-doc/SKILL.md', '1.0.93', descriptors);
  assert.match(result.output, /name: lark-doc/);
  assert.match(result.output, /bins: \["ksfas-lark"\]/);
  assert.match(result.output, /Official lark-cli version 1.0.93/);
  assert.match(result.output, /https:\/\/example.com\/lark-cli\/install/);
  assert.match(result.output, /ksfas-lark docs \+fetch --doc fixture/);
  assert.match(result.output, /\[guide\]\(references\/guide.md\)/);
  assert.match(result.output, /KSFAssistant 适配版/);
  assert.equal(result.output, adaptMarkdown(input, 'lark-doc/SKILL.md', '1.0.93', descriptors).output);
});

test('configuration examples are not executable product instructions', () => {
  const input = '```bash\nlark-cli auth login \\\n --domain docs\nlark-cli auth status --json\n```\n';
  const result = adaptMarkdown(input, 'lark-shared/references/auth.md', '1.0.93', descriptors);
  assert.match(result.output, /# KSFAssistant.*ksfas-lark auth login/);
  assert.match(result.output, /# KSFAssistant.*--domain docs/);
  assert.match(result.output, /\nksfas-lark auth status --json/);
});

test('assessment never promotes command-level support to concrete workflow success', () => {
  assert.equal(commandAssessment('mail +template-update --as user --template-id 712345 --inspect', descriptors).state, 'restricted-arguments');
  assert.equal(commandAssessment('docs +fetch --doc fixture --as user', descriptors).state, 'entry-adapted-template');
  assert.throws(() => commandAssessment('new +undocumented --as user', descriptors), /requires review/);
  assert.equal(commandAssessment('config init', descriptors).state, 'desktop-control');
  assert.equal(commandAssessment('skills read lark-im', descriptors).state, 'local-reference');
});

test('Python examples use no-cache mode and online helpers require explicit identity', () => {
  const result = adaptMarkdown('```bash\npython3 "<lark-slides-skill-dir>/scripts/xml_lint.py" --input page.xml\npython scripts/lark_inspect_workbook.py --url fixture\n```', 'lark-sheets/references/example.md', '1.0.93', descriptors);
  assert.match(result.output, /python3 -B/);
  assert.match(result.output, /lark_inspect_workbook.py --as "<user\|bot>"/);
  assert.throws(() => adaptMarkdown('```bash\nlark-cli future +execute --scope all\n```', 'lark-new/SKILL.md', '2.0.0', descriptors), /requires review/);
});

test('new Skill gets common entry rules without a copied per-version patch', () => {
  const result = adaptMarkdown('---\nname: lark-new\ndescription: new\n---\n# New\n`lark-cli docs +fetch --doc sample`', 'lark-new/SKILL.md', '2.0.0', descriptors);
  assert.match(result.output, /larksuite\/cli v2.0.0/);
  assert.match(result.output, /name: lark-new/);
  assert.match(result.output, /ksfas-lark docs/);
});

test('fixed official tree adapts deterministically and records full original/final provenance', () => {
  const source = process.env.KSF_USERCOMMAND_PINNED_SOURCE;
  if (!source) throw new Error('KSF_USERCOMMAND_PINNED_SOURCE is mandatory; fixed source tests must not silently skip');
  const upstreamBytes = readFileSync(path.join(repo, 'runtime/lark-skills.json'));
  const upstream = JSON.parse(upstreamBytes);
  const temporary = mkdtempSync(path.join(tmpdir(), 'ksfas-adapter-test-'));
  try {
    const results = [];
    for (const iteration of ['first', 'second']) {
      const root = path.join(temporary, iteration);
      mkdirSync(root);
      for (const skill of upstream.skills) {
        for (const name of Object.keys(skill.files)) {
          const target = path.join(root, skill.name, name);
          mkdirSync(path.dirname(target), { recursive: true });
          cpSync(path.join(source, 'skills', skill.name, name), target);
        }
      }
      results.push(adaptSkills({ root, upstream, upstreamBytes, descriptors }));
    }
    assert.deepEqual(results[0].bytes, results[1].bytes);
    const comparison = compareAdaptationReports(results[0].report, results[1].report);
    assert.deepEqual(comparison.files, { added: [], removed: [], modified: [] });
    assert.deepEqual(comparison.commandEntries, { added: [], removed: [], modified: [] });
    assert.equal(results[0].report.python.callsites.filter(site => site.kind === 'markdown-managed-cli').length, 2);
    assert.equal(results[0].manifest.skills.length, upstream.skills.length);
    for (const skill of upstream.skills) {
      assert.ok(results[0].manifest.skills.some(item => item.name === 'ksf-' + skill.name));
      const text = readFileSync(path.join(temporary, 'first', 'ksf-' + skill.name, 'SKILL.md'), 'utf8');
      assert.match(text, new RegExp('name: ksf-' + skill.name));
    }
    for (const file of results[0].report.files) {
      assert.equal(file.path, 'ksf-' + file.upstreamPath);
    }
    assert.equal(results[0].manifest.skills.some(skill => skill.name === 'ksfas'), false);
    assert.equal(results[0].report.files.length, upstream.skills.reduce((sum, skill) => sum + Object.keys(skill.files).length, 0));
    const template = results[0].report.markdown.flatMap(file => file.calls).find(call => call.command === 'mail +template-update' && call.state === 'restricted-arguments');
    assert.ok(template);
    assert.throws(() => adaptSkills({ root: path.join(temporary, 'first'), upstream, upstreamBytes, descriptors }), /digest mismatch/);
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
});
