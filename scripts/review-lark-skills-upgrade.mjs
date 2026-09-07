#!/usr/bin/env node
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const hashPattern = /^[a-f0-9]{64}$/;
const informationalStates = new Set(['diagnostic', 'entry-adapted-template']);
const restrictedStates = new Set(['desktop-control', 'local-reference']);
const pythonKinds = new Set(['managed-cli', 'run_sheets', 'local-python-test', 'local-python-exec', 'markdown-managed-cli']);

function requireValue(condition, message) {
  if (!condition) throw new Error(message);
}

function object(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function nonempty(value) {
  return typeof value === 'string' && value.trim().length > 0;
}

function relativePath(value) {
  return nonempty(value) && !/[\\:\x00-\x1f]/.test(value) && value.split('/').every(part => part && part !== '.' && part !== '..');
}

function canonical(value) {
  if (Array.isArray(value)) return value.map(canonical);
  if (object(value)) return Object.fromEntries(Object.keys(value).sort().map(key => [key, canonical(value[key])]));
  requireValue(value === null || typeof value === 'string' || typeof value === 'boolean' || typeof value === 'number' && Number.isFinite(value), 'Expected JSON values');
  return value;
}

function serialized(value) {
  return JSON.stringify(canonical(value));
}

function compareText(left, right) {
  return left < right ? -1 : left > right ? 1 : 0;
}

function sorted(values) {
  return [...values].sort((left, right) => compareText(serialized(left), serialized(right)));
}

function normalize(report) {
  requireValue(object(report) && report.schemaVersion === 1, 'Expected adaptation report schemaVersion 1');
  requireValue(nonempty(report.revision) && nonempty(report.upstreamVersion) && hashPattern.test(report.upstreamManifestSha256), 'Invalid adaptation provenance');
  requireValue(object(report.adapterSources) && Object.keys(report.adapterSources).length > 0, 'Missing adapter source hashes');
  for (const [name, hash] of Object.entries(report.adapterSources)) {
    requireValue(relativePath(name) && typeof hash === 'string' && hashPattern.test(hash), 'Invalid adapter source hash');
  }
  requireValue(Array.isArray(report.files) && Array.isArray(report.markdown) && object(report.python) && Array.isArray(report.python.callsites) && Array.isArray(report.python.filesChanged), 'Incomplete report inventories');
  const files = new Map();
  for (const file of report.files) {
    requireValue(object(file) && relativePath(file.path) && !files.has(file.path), 'Invalid or duplicate file path');
    requireValue(typeof file.upstreamSha256 === 'string' && hashPattern.test(file.upstreamSha256) && typeof file.adaptedSha256 === 'string' && hashPattern.test(file.adaptedSha256), 'Invalid file hashes');
    requireValue(file.changed === (file.upstreamSha256 !== file.adaptedSha256), 'Inconsistent file changed marker');
    files.set(file.path, canonical(file));
  }
  const sites = new Map();
  const unresolved = [];
  const restricted = [];
  const recordCall = (source, name, raw) => {
    requireValue(object(raw) && Number.isSafeInteger(raw.line) && raw.line > 0, 'Invalid callsite line');
    requireValue(files.has(name), 'Callsite file missing from inventory');
    if (source === 'markdown') {
      requireValue(nonempty(raw.state) && nonempty(raw.command), 'Invalid Markdown command assessment');
    } else {
      requireValue(nonempty(raw.kind), 'Invalid Python callsite kind');
      if (raw.kind === 'run_sheets') requireValue(nonempty(raw.shortcut), 'Missing Python shortcut');
    }
    const { line, file, ...payload } = raw;
    for (const field of ['reasons', 'unsupportedFlags']) {
      if (field in payload) {
        requireValue(Array.isArray(payload[field]) && payload[field].every(value => typeof value === 'string'), 'Invalid command restriction details');
        payload[field] = [...payload[field]].sort();
      }
    }
    const call = canonical(payload);
    const key = JSON.stringify([name, source, line]);
    if (!sites.has(key)) sites.set(key, { path: name, source, line, calls: [] });
    sites.get(key).calls.push(call);
    const entry = { path: name, source, line, call };
    if (source === 'markdown') {
      if (raw.state.startsWith('restricted') || restrictedStates.has(raw.state)) restricted.push(entry);
      else if (!informationalStates.has(raw.state)) unresolved.push(entry);
    } else if (!pythonKinds.has(raw.kind)) unresolved.push(entry);
  };
  const markdownPaths = new Set();
  for (const section of report.markdown) {
    requireValue(object(section) && files.has(section.path) && !markdownPaths.has(section.path) && Array.isArray(section.calls), 'Invalid or duplicate Markdown inventory');
    requireValue(Object.keys(section).every(key => ['path', 'calls'].includes(key)), 'Unknown Markdown inventory fields require review');
    markdownPaths.add(section.path);
    for (const call of section.calls) recordCall('markdown', section.path, call);
  }
  for (const call of report.python.callsites) {
    requireValue(object(call) && relativePath(call.file), 'Invalid Python callsite file');
    recordCall('python', call.file, call);
  }
  requireValue(new Set(report.python.filesChanged).size === report.python.filesChanged.length && report.python.filesChanged.every(name => files.has(name)), 'Invalid Python changed files');
  for (const site of sites.values()) site.calls = sorted(site.calls);
  const { files: ignoredFiles, markdown: ignoredMarkdown, python, ...metadata } = report;
  const { callsites: ignoredCallsites, ...pythonMetadata } = python;
  metadata.python = { ...pythonMetadata, filesChanged: [...python.filesChanged].sort() };
  return { files, sites, metadata: canonical(metadata), unresolved: sorted(unresolved), restricted: sorted(restricted) };
}

function changes(before, after) {
  const added = [];
  const removed = [];
  const modified = [];
  for (const key of [...new Set([...before.keys(), ...after.keys()])].sort()) {
    if (!before.has(key)) added.push(after.get(key));
    else if (!after.has(key)) removed.push(before.get(key));
    else if (serialized(before.get(key)) !== serialized(after.get(key))) modified.push({ before: before.get(key), after: after.get(key) });
  }
  return { added, removed, modified };
}

export function compareAdaptationReports(beforeReport, afterReport) {
  const before = normalize(beforeReport);
  const after = normalize(afterReport);
  return canonical({
    schemaVersion: 1,
    before: before.metadata,
    after: after.metadata,
    files: changes(before.files, after.files),
    commandEntries: changes(before.sites, after.sites),
    unresolved: { before: before.unresolved, after: after.unresolved },
    restricted: { before: before.restricted, after: after.restricted },
    limitations: [
      'This is a comparison of supplied reports, not verification of source bytes, report digests, signatures or runtime support.',
      'Only recorded callsites are compared. Missing records do not prove the absence of invocations, restrictions or unknown behavior.',
      'Command entries are grouped by source, file and reported line; line shifts appear as removals/additions. Full argv and dynamic command expansion are not reconstructed.',
      'Entry-adapted templates, diagnostics and recognized Python callsite kinds are observations, not permission grants or successful workflow evidence.',
      'Restrictions and unresolved or unknown states remain review items; their removal does not establish support. Exit code 0 means comparison completed, not upgrade approved.',
    ],
  });
}

function main(args) {
  const inputs = new Map();
  for (let index = 0; index < args.length; index += 2) {
    const flag = args[index];
    if (!['--before', '--after'].includes(flag) || inputs.has(flag) || !args[index + 1] || args[index + 1].startsWith('--')) {
      process.stderr.write(`${serialized({ schemaVersion: 1, error: { code: 'invalid_arguments', message: 'Usage: review-lark-skills-upgrade.mjs --before REPORT --after REPORT' } })}\n`);
      return 2;
    }
    inputs.set(flag, args[index + 1]);
  }
  if (inputs.size !== 2) return main(['--invalid']);
  try {
    const before = JSON.parse(readFileSync(inputs.get('--before'), 'utf8'));
    const after = JSON.parse(readFileSync(inputs.get('--after'), 'utf8'));
    process.stdout.write(`${JSON.stringify(compareAdaptationReports(before, after), null, 2)}\n`);
    return 0;
  } catch {
    process.stderr.write(`${serialized({ schemaVersion: 1, error: { code: 'invalid_report', message: 'Both inputs must be readable, valid adaptation reports with complete inventories; no support conclusion was produced.' } })}\n`);
    return 1;
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exitCode = main(process.argv.slice(2));
}
