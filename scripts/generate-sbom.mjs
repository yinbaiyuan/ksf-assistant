import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync, existsSync, readdirSync, lstatSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const revision = process.env.PREVIEW_SOURCE_SHA256 || currentSourceHash();
const timestamp = new Date(Number(process.env.SOURCE_DATE_EPOCH || Math.floor(Date.now() / 1000)) * 1000).toISOString();

const goOutput = execFileSync('go', ['list', '-m', '-json', 'all'], { cwd: path.join(root, 'Core'), encoding: 'utf8' });
const goModules = [...goOutput.matchAll(/\{(?:[^{}]|\{[^{}]*\})*\}/gs)].map((match) => JSON.parse(match[0]));
const lock = JSON.parse(readFileSync(path.join(root, 'Windows', 'package-lock.json'), 'utf8'));
const packages = [{
  SPDXID: 'SPDXRef-Application', name: 'KSFAssistant', versionInfo: lock.version,
  downloadLocation: 'NOASSERTION', filesAnalyzed: false, licenseConcluded: 'MIT', licenseDeclared: 'MIT',
}];
for (const module of goModules.filter((item) => item.Path !== 'ksfassistant/core' && !item.Main)) {
  packages.push({
    SPDXID: `SPDXRef-Go-${stableID(module.Path)}`, name: module.Path, versionInfo: module.Version || 'unknown',
    downloadLocation: module.Version ? `https://proxy.golang.org/${module.Path}/@v/${module.Version}.zip` : 'NOASSERTION',
    filesAnalyzed: false, licenseConcluded: 'NOASSERTION', licenseDeclared: 'NOASSERTION',
  });
}
const cli = JSON.parse(readFileSync(path.join(root, 'runtime/lark-cli-runtime.json')));
const skills = JSON.parse(readFileSync(path.join(root, 'runtime/lark-skills.json')));
const embeddedIDs = new Set();
const embeddedRelationships = [];
packages.push({
  SPDXID: 'SPDXRef-Lark-Skills', name: 'larksuite-cli-skills', versionInfo: skills.version,
  downloadLocation: skills.source.url, filesAnalyzed: false, licenseConcluded: skills.license, licenseDeclared: skills.license,
  checksums: [{ algorithm: 'SHA256', checksumValue: skills.source.sha256 }],
  sourceInfo: `Official source tag ${skills.source.tag}; ${skills.skills.length} Skills; LICENSE SHA256 ${skills.licenseSha256}`,
});
packages.push({
  SPDXID: 'SPDXRef-Ksfas-Integration', name: 'ksfas-integration-skill', versionInfo: lock.version,
  downloadLocation: 'NOASSERTION', filesAnalyzed: false, licenseConcluded: 'MIT', licenseDeclared: 'MIT',
});
packages.push({
  SPDXID: 'SPDXRef-Execution-Manifest', name: 'ksfassistant-feishu-execution-contract', versionInfo: lock.version,
  downloadLocation: 'NOASSERTION', filesAnalyzed: false, licenseConcluded: 'MIT', licenseDeclared: 'MIT',
  checksums: [{ algorithm: 'SHA256', checksumValue: createHash('sha256').update(readFileSync(path.join(root, 'Core/internal/usercommand/execution-manifest.json'))).digest('hex') }],
  sourceInfo: `Compiled execution descriptors for fixed official CLI ${cli.version}; provenance and restrictions are embedded in the manifest.`,
});
for (const [target, artifact] of Object.entries(cli.artifacts)) {
  packages.push({
    SPDXID: `SPDXRef-Lark-CLI-${target}`, name: `lark-cli-${target}`, versionInfo: cli.version,
    downloadLocation: `${cli.baseUrl}/${artifact.archive}`, filesAnalyzed: false,
    licenseConcluded: cli.license, licenseDeclared: cli.license,
    checksums: [{ algorithm: 'SHA256', checksumValue: artifact.sha256 }],
  });
  for (const module of artifact.embeddedGoModules || []) {
    const identifier = `SPDXRef-Lark-Embedded-${stableID(`${module.name}@${module.version}`)}`;
    if (!embeddedIDs.has(identifier)) {
      embeddedIDs.add(identifier);
      packages.push({
        SPDXID: identifier, name: module.name, versionInfo: module.version,
        downloadLocation: 'NOASSERTION', filesAnalyzed: false,
        licenseConcluded: 'NOASSERTION', licenseDeclared: 'NOASSERTION',
        sourceInfo: `Embedded in official lark-cli ${cli.version}, verified with go version -m; Go module sum ${module.sum}. Not a KSFAssistant Core dependency.`,
      });
    }
    embeddedRelationships.push({ spdxElementId: `SPDXRef-Lark-CLI-${target}`, relationshipType: 'CONTAINS', relatedSpdxElement: identifier });
  }
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
  name: `KSFAssistant-worktree-${revision.slice(0, 12)}`,
  documentNamespace: `https://ksfassistant.invalid/spdx/${revision}`,
  creationInfo: { created: timestamp, creators: ['Tool: scripts/generate-sbom.mjs'] },
  packages,
  relationships: [...packages.filter(item => item.SPDXID !== 'SPDXRef-Application' && !embeddedIDs.has(item.SPDXID)).map((item) => ({
    spdxElementId: 'SPDXRef-Application', relationshipType: 'DEPENDS_ON', relatedSpdxElement: item.SPDXID,
  })), ...embeddedRelationships],
};
const output = path.join(root, 'dist', 'sbom');
mkdirSync(output, { recursive: true });
writeFileSync(path.join(output, 'KSFAssistant.spdx.json'), `${JSON.stringify(document, null, 2)}\n`);
console.log(`Generated SPDX 2.3 SBOM with ${packages.length} packages.`);

function stableID(value) {
  return createHash('sha256').update(value).digest('hex').slice(0, 20);
}

function currentSourceHash() {
  const digest = createHash('sha256');
  const roots = ['Core', 'Sources', 'Resources', 'runtime', 'scripts', 'Windows/src', 'Windows/scripts', 'Windows/package.json', 'Windows/package-lock.json', 'Package.swift', 'LICENSE'];
  function walk(relative) {
    const full = path.join(root, relative);
    for (const entry of readdirSync(full, { withFileTypes: true }).sort((left, right) => left.name.localeCompare(right.name))) {
      if (['.git', '.build', 'dist', 'node_modules'].includes(entry.name)) continue;
      const name = `${relative}/${entry.name}`;
      if (entry.isDirectory()) walk(name);
      else if (entry.isFile()) digest.update(name).update('\0').update(readFileSync(path.join(root, name))).update('\0');
      else throw new Error('Symlink source refused');
    }
  }
  for (const relative of roots) {
    if (!existsSync(path.join(root, relative))) throw new Error(`Missing source input: ${relative}`);
    if (lstatSync(path.join(root, relative)).isFile()) digest.update(relative).update('\0').update(readFileSync(path.join(root, relative))).update('\0');
    else walk(relative);
  }
  return digest.digest('hex');
}
