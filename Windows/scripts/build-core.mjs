import { mkdirSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const windowsRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = path.resolve(windowsRoot, '..');
const coreRoot = path.join(repoRoot, 'Core');

for (const arch of ['amd64', 'arm64']) {
  const outputArch = arch === 'amd64' ? 'windows-x64' : 'windows-arm64';
  const outputDir = path.join(repoRoot, 'dist', 'core', outputArch);
  mkdirSync(outputDir, { recursive: true });
  const result = spawnSync('go', [
    'build', '-trimpath', '-ldflags=-s -w',
    '-o', path.join(outputDir, 'codex-usage-core.exe'),
    './cmd/codex-usage-core',
  ], {
    cwd: coreRoot,
    stdio: 'inherit',
    env: { ...process.env, GOOS: 'windows', GOARCH: arch },
  });
  if (result.status !== 0) process.exit(result.status ?? 1);
}
