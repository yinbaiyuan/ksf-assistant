#!/usr/bin/env node

const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');

const projectRoot = path.resolve(__dirname, '..');
const taskRunsRoot = path.join(projectRoot, 'logs', 'task-runs');
const inputPath = path.resolve(process.argv[2] || '');
const relative = path.relative(taskRunsRoot, inputPath);
if (!relative || relative.startsWith('..') || path.isAbsolute(relative)) {
  throw new Error('run log must be a file under logs/task-runs');
}
const stat = fs.lstatSync(inputPath);
if (!stat.isFile() || stat.isSymbolicLink()) throw new Error('run log must be a regular file');
const raw = fs.readFileSync(inputPath);
const eventCounts = {};
for (const line of raw.toString('utf8').split('\n')) {
  try {
    const record = JSON.parse(line);
    if (record.method) eventCounts[record.method] = (eventCounts[record.method] || 0) + 1;
  } catch {
    // Header lines are intentionally reduced to the aggregate below.
  }
}
const summary = {
  schemaVersion: 1,
  sanitized: true,
  sanitizedAt: new Date().toISOString(),
  originalBytes: raw.length,
  originalFingerprint: `sha256:${crypto.createHash('sha256').update(raw).digest('hex').slice(0, 16)}`,
  eventCounts,
  note: 'Raw Codex events, prompts, command output, Feishu identifiers, and document bodies were removed.',
};
const temporary = `${inputPath}.${process.pid}.tmp`;
fs.writeFileSync(temporary, `${JSON.stringify(summary, null, 2)}\n`, { encoding: 'utf8', mode: 0o600, flag: 'wx' });
fs.renameSync(temporary, inputPath);
fs.chmodSync(inputPath, 0o600);
process.stdout.write(`${JSON.stringify({ status: 'sanitized', path: relative, mode: '0600', originalBytes: raw.length })}\n`);
