'use strict';

const { createHash } = require('node:crypto');
const { existsSync, readFileSync, writeFileSync } = require('node:fs');
const path = require('node:path');

module.exports = async function sealRuntime(context) {
  if (context.electronPlatformName !== 'win32') return;
  const root = path.join(context.appOutDir, 'resources', 'runtime');
  const filename = path.join(root, 'lark-cli-runtime.json');
  const manifest = JSON.parse(readFileSync(filename, 'utf8'));
  const digest = filename => createHash('sha256').update(readFileSync(filename)).digest('hex');
  for (const target of ['windows-x64', 'windows-arm64']) {
    const artifact = manifest.artifacts[target];
    const cli = path.join(root, 'lark-cli', target, 'lark-cli.exe');
    if (!existsSync(cli)) continue;
    artifact.upstreamExecutableSha256 ||= artifact.executableSha256;
    artifact.executableSha256 = digest(cli);
    artifact.taskExecutableSha256 = digest(path.join(root, 'task', target, 'ksf-assistant-task.exe'));
  }
  writeFileSync(filename, `${JSON.stringify(manifest, null, 2)}\n`);
};
