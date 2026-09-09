import { existsSync, mkdirSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const windowsRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const repoRoot = path.resolve(windowsRoot, '..');
const coreRoot = path.join(repoRoot, 'Core');

function locateGo() {
  if (process.env.KSF_ASSISTANT_GO) return process.env.KSF_ASSISTANT_GO;
  if (process.platform === 'win32') {
    const programFiles = process.env.ProgramFiles || 'C:\\Program Files';
    const standardInstall = path.join(programFiles, 'Go', 'bin', 'go.exe');
    if (existsSync(standardInstall)) return standardInstall;
  }
  return 'go';
}

const go = locateGo();

for (const arch of ['amd64', 'arm64']) {
  const outputArch = arch === 'amd64' ? 'windows-x64' : 'windows-arm64';
  const outputDir = path.join(repoRoot, 'dist', 'core', outputArch);
  mkdirSync(outputDir, { recursive: true });
	const result = spawnSync(go, [
    'build', '-buildvcs=false', '-trimpath', '-ldflags=-s -w',
    '-o', path.join(outputDir, 'ksf-assistant-core.exe'),
    './cmd/ksf-assistant-core',
  ], {
    cwd: coreRoot,
    stdio: 'inherit',
    env: { ...process.env, GOOS: 'windows', GOARCH: arch },
  });
  if (result.error) {
    if (result.error.code === 'ENOENT') {
      console.error('Go toolchain not found: install Go 1.23+ and ensure go is available on PATH.');
    } else {
      console.error(`Failed to start Go toolchain: ${result.error.message}`);
    }
    process.exit(1);
  }
	if (result.status !== 0) process.exit(result.status ?? 1);
	const bridgeOutputDir = path.join(repoRoot, 'dist', 'runtime', 'feishu-bridge', outputArch);
	mkdirSync(bridgeOutputDir, { recursive: true });
	const bridgeResult = spawnSync(go, [
		'build', '-buildvcs=false', '-trimpath', '-ldflags=-s -w',
		'-o', path.join(bridgeOutputDir, 'ksf-assistant-feishu-bridge.exe'),
		'./cmd/ksf-assistant-feishu-bridge',
	], {
		cwd: coreRoot,
		stdio: 'inherit',
		env: { ...process.env, GOOS: 'windows', GOARCH: arch },
	});
	if (bridgeResult.error) {
		console.error(`Failed to build Go Feishu service: ${bridgeResult.error.message}`);
		process.exit(1);
	}
	if (bridgeResult.status !== 0) process.exit(bridgeResult.status ?? 1);
  for (const component of ['toolchain', 'task']) {
    const componentDir = path.join(repoRoot, 'dist', 'runtime', component, outputArch);
    mkdirSync(componentDir, { recursive: true });
    const componentResult = spawnSync(go, [
      'build', '-buildvcs=false', '-trimpath', '-ldflags=-s -w',
      '-o', path.join(componentDir, `ksf-assistant-${component}.exe`),
      `./cmd/ksf-assistant-${component}`,
    ], {
      cwd: coreRoot,
      stdio: 'inherit',
      env: { ...process.env, CGO_ENABLED: '0', GOOS: 'windows', GOARCH: arch },
    });
    if (componentResult.error || componentResult.status !== 0) {
      console.error(`Failed to build ${component} runtime.`);
      process.exit(componentResult.status || 1);
    }
  }
}
