import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const allowed = new Set(['MIT', 'ISC', 'BSD-2-Clause', 'BSD-3-Clause', 'Apache-2.0', '0BSD', 'BlueOak-1.0.0', 'CC0-1.0', 'Python-2.0', 'WTFPL']);
const lock = JSON.parse(readFileSync(path.join(root, 'Windows', 'package-lock.json'), 'utf8'));
const failures = [];
for (const [location, metadata] of Object.entries(lock.packages || {})) {
  if (!location.startsWith('node_modules/')) continue;
  const licenses = String(metadata.license || '').split(/\s+OR\s+|\s+AND\s+/).map((item) => item.replace(/[()]/g, ''));
  if (!licenses.length || licenses.some((license) => !allowed.has(license))) {
    failures.push(`${location}: ${metadata.license || 'missing license metadata'}`);
  }
}

const goModules = execFileSync('go', ['list', '-m', '-f', '{{if not .Main}}{{.Path}} {{.Version}}{{end}}', 'all'], {
  cwd: path.join(root, 'Core'), encoding: 'utf8',
}).trim().split('\n').filter(Boolean);
const reviewedGo = new Set([
  'github.com/Microsoft/go-winio v0.6.2',
  'github.com/gogo/protobuf v1.3.2',
  'github.com/gorilla/websocket v1.5.0',
  'github.com/larksuite/oapi-sdk-go/v3 v3.11.0',
  'github.com/kisielk/errcheck v1.5.0',
  'github.com/kisielk/gotool v1.0.0',
  'github.com/sirupsen/logrus v1.9.3',
  'github.com/yuin/goldmark v1.2.1',
  'golang.org/x/crypto v0.0.0-20200622213623-75b288015ac9',
  'golang.org/x/mod v0.12.0',
  'golang.org/x/net v0.0.0-20201021035429-f5854403a974',
  'golang.org/x/sync v0.0.0-20201020160332-67f06af15bc9',
  'golang.org/x/sys v0.10.0',
  'golang.org/x/text v0.3.3',
  'golang.org/x/tools v0.11.0',
  'golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1',
]);
for (const module of goModules) if (!reviewedGo.has(module)) failures.push(`unreviewed Go module: ${module}`);

const notices = readFileSync(path.join(root, 'THIRD_PARTY_NOTICES.md'), 'utf8');
for (const marker of ['oapi-sdk-go', 'lark-cli', 'go-winio', 'gorilla/websocket', 'gogo/protobuf', 'x/sys', 'Node.js 24.20.0']) {
  if (!notices.includes(marker)) failures.push(`third-party notice missing: ${marker}`);
}
if (failures.length) {
  console.error(failures.join('\n'));
  process.exit(1);
}
console.log(`PASS dependency license policy (${goModules.length} Go modules, ${Object.keys(lock.packages || {}).length - 1} npm packages)`);
