import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const revision = execFileSync('git', ['rev-parse', '--verify', 'HEAD'], { cwd: root, encoding: 'utf8' }).trim();
const timestamp = new Date(Number(process.env.SOURCE_DATE_EPOCH || execFileSync(
  'git', ['show', '-s', '--format=%ct', 'HEAD'], { cwd: root, encoding: 'utf8' },
).trim()) * 1000).toISOString();

const goOutput = execFileSync('go', ['list', '-m', '-json', 'all'], { cwd: path.join(root, 'Core'), encoding: 'utf8' });
const goModules = [...goOutput.matchAll(/\{(?:[^{}]|\{[^{}]*\})*\}/gs)].map((match) => JSON.parse(match[0]));
const lock = JSON.parse(readFileSync(path.join(root, 'Windows', 'package-lock.json'), 'utf8'));
const packages = [{
  SPDXID: 'SPDXRef-Application', name: 'KSFAssistant', versionInfo: '0.10.0-preview.1',
  downloadLocation: 'NOASSERTION', filesAnalyzed: false, licenseConcluded: 'MIT', licenseDeclared: 'MIT',
}];
for (const module of goModules.filter((item) => item.Path !== 'ksfassistant/core')) {
  packages.push({
    SPDXID: `SPDXRef-Go-${stableID(module.Path)}`, name: module.Path, versionInfo: module.Version || 'unknown',
    downloadLocation: module.Version ? `https://proxy.golang.org/${module.Path}/@v/${module.Version}.zip` : 'NOASSERTION',
    filesAnalyzed: false, licenseConcluded: 'NOASSERTION', licenseDeclared: 'NOASSERTION',
  });
}
for (const [location, metadata] of Object.entries(lock.packages || {})) {
  if (!location.startsWith('node_modules/') || !metadata.version) continue;
  const name = location.slice('node_modules/'.length);
  packages.push({
    SPDXID: `SPDXRef-Npm-${stableID(`${name}@${metadata.version}`)}`, name, versionInfo: metadata.version,
    downloadLocation: metadata.resolved || 'NOASSERTION', filesAnalyzed: false,
    licenseConcluded: metadata.license || 'NOASSERTION', licenseDeclared: metadata.license || 'NOASSERTION',
  });
}
packages.sort((left, right) => left.SPDXID.localeCompare(right.SPDXID));
const document = {
  spdxVersion: 'SPDX-2.3', dataLicense: 'CC0-1.0', SPDXID: 'SPDXRef-DOCUMENT',
  name: `KSFAssistant-${revision.slice(0, 12)}`,
  documentNamespace: `https://ksfassistant.invalid/spdx/${revision}`,
  creationInfo: { created: timestamp, creators: ['Tool: scripts/generate-sbom.mjs'] },
  packages,
  relationships: packages.slice(1).map((item) => ({
    spdxElementId: 'SPDXRef-Application', relationshipType: 'DEPENDS_ON', relatedSpdxElement: item.SPDXID,
  })),
};
const output = path.join(root, 'dist', 'sbom');
mkdirSync(output, { recursive: true });
writeFileSync(path.join(output, 'KSFAssistant.spdx.json'), `${JSON.stringify(document, null, 2)}\n`);
console.log(`Generated SPDX 2.3 SBOM with ${packages.length} packages.`);

function stableID(value) {
  return createHash('sha256').update(value).digest('hex').slice(0, 20);
}
