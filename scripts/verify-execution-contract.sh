#!/usr/bin/env bash
# Usage: bash scripts/verify-execution-contract.sh /absolute/path/to/pinned/lark-cli
# Requires preinstalled Go/Python and an already populated Go module cache.
set -euo pipefail

if [[ $# -ne 1 || -z "$1" ]]; then
  echo "usage: $0 /explicit/path/to/pinned/lark-cli" >&2
  exit 2
fi
if [[ ! -f "$1" || ! -x "$1" ]]; then
  echo "fixed CLI must be an existing executable regular file: $1" >&2
  exit 2
fi

contract_root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)"
contract_binary="$(python3 -c 'import pathlib,sys; print(pathlib.Path(sys.argv[1]).resolve(strict=True))' "$1")"

# Read only public repository manifests and the explicitly selected executable.
# Never search PATH for a CLI, download it, or accept a version string as proof.
python3 - "$contract_root" "$contract_binary" <<'PY'
import hashlib
import json
import pathlib
import sys

root, binary = map(pathlib.Path, sys.argv[1:])
runtime = json.loads((root / 'runtime/lark-cli-runtime.json').read_bytes())
manifest = json.loads((root / 'Core/internal/usercommand/execution-manifest.json').read_bytes())
skills_bytes = (root / 'runtime/lark-skills.json').read_bytes()
skills = json.loads(skills_bytes)
digest = hashlib.sha256(binary.read_bytes()).hexdigest()
provenance = manifest['provenance']
def require(condition, message):
    if not condition:
        raise SystemExit(message)
artifacts = {platform: item['executableSha256'] for platform, item in runtime['artifacts'].items()}
require(digest in set(artifacts.values()), 'fixed CLI SHA256 is absent from runtime manifest')
require(artifacts == provenance['binaryArtifacts'], 'execution provenance differs from the four pinned runtime artifacts')
require(digest in set(provenance['binaryArtifacts'].values()), 'fixed CLI SHA256 is absent from execution provenance')
require(len(provenance['schemaSha256']) == 64, 'missing canonical schema digest')
require(runtime['version'] == manifest['version'] == skills['version'] == '1.0.93', 'pinned version mismatch')
require(hashlib.sha256(skills_bytes).hexdigest() == provenance['skillsSha256'], 'Skills manifest digest mismatch')
require(skills['source']['sha256'] == provenance['sourceArchiveSha256'], 'Skills source archive provenance mismatch')
require(hashlib.sha256((root / 'Core/internal/usercommand/execution-overlay.json').read_bytes()).hexdigest() == provenance['overlaySha256'], 'execution overlay digest mismatch')
for name, key in [('Core/internal/feishucli/catalog.json', 'productCatalogSha256'), ('scripts/generate-execution-manifest.py', 'generatorSha256'), ('scripts/execution-source-ast.go', 'astExtractorSha256'), ('Core/internal/usercommand/catalog.go', 'reviewAdapterSha256')]:
    require(hashlib.sha256((root / name).read_bytes()).hexdigest() == provenance[key], 'generation provenance mismatch: ' + name)
require(manifest['counts'] == {'typed': 250, 'shortcuts': 526} and len(manifest['descriptors']) == 776, 'expected all 776 execution descriptors')
print('Verified fixed CLI SHA256: ' + digest)
PY

# An optional source checkout must be explicitly supplied. The generator verifies
# it against the cached pinned archive; --check never rewrites the manifest.
if [[ -n "${KSF_USERCOMMAND_PINNED_SOURCE:-}" ]]; then
  if [[ ! -d "$KSF_USERCOMMAND_PINNED_SOURCE" ]]; then
    echo "fixed source must be an existing directory: $KSF_USERCOMMAND_PINNED_SOURCE" >&2
    exit 2
  fi
  GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local \
    python3 "$contract_root/scripts/generate-execution-manifest.py" \
    --source "$KSF_USERCOMMAND_PINNED_SOURCE" --binary "$contract_binary" --check
fi

# Preserve only offline build locations, not arbitrary Go/CLI environment flags.
contract_go="$(command -v go)"
contract_gocache="$(GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOENV=off go env GOCACHE)"
contract_modcache="$(GOTOOLCHAIN=local GOPROXY=off GOSUMDB=off GOENV=off go env GOMODCACHE)"
contract_tmp="$(mktemp -d "${TMPDIR:-/tmp}/ksfas-execution-contract.XXXXXX")"
trap 'rm -rf -- "$contract_tmp"' EXIT
mkdir -p "$contract_tmp/home" "$contract_tmp/config" "$contract_tmp/tmp"
cd -- "$contract_root/Core"

# -count=1 forbids cached success. JSON auditing below rejects skipped/missing
# fixed tests, including a renamed test that would otherwise report no tests.
if ! env -i \
  PATH="$PATH" HOME="$contract_tmp/home" USERPROFILE="$contract_tmp/home" \
  XDG_CONFIG_HOME="$contract_tmp/config" XDG_CACHE_HOME="$contract_tmp/cache" \
  TMPDIR="$contract_tmp/tmp" GOCACHE="$contract_gocache" GOMODCACHE="$contract_modcache" \
  GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GOENV=off GOWORK=off \
  LARKSUITE_CLI_CONFIG_DIR="$contract_tmp/config" LARKSUITE_CLI_REMOTE_META=off \
  LARKSUITE_CLI_NO_UPDATE_NOTIFIER=1 LARKSUITE_CLI_NO_SKILLS_NOTIFIER=1 NO_COLOR=1 \
  KSF_USERCOMMAND_PINNED_CLI="$contract_binary" \
  "$contract_go" test -json -count=1 -parallel=8 -timeout=10m \
  -run '^(TestDescriptor.*Contract|TestFixedBinaryOfflineContract|TestExecutionEngineClosureAndLegacyAdapters)$' \
  ./internal/usercommand > "$contract_tmp/results.json"; then
  python3 - "$contract_tmp/results.json" <<'PY'
import json
import pathlib
import sys
for line in pathlib.Path(sys.argv[1]).read_text().splitlines():
    event = json.loads(line)
    if event.get('Action') == 'output':
        output = event.get('Output', '')
        if not output.startswith(('=== RUN', '=== PAUSE', '=== CONT', '--- PASS', '    --- PASS')):
            print(output, end='')
PY
  exit 1
fi

python3 - "$contract_tmp/results.json" <<'PY'
import json
import pathlib
import sys

events = [json.loads(line) for line in pathlib.Path(sys.argv[1]).read_text().splitlines() if line.strip()]
fixed = 'TestFixedBinaryOfflineContract'
prefix = fixed + '/descriptors/'
passed = {event.get('Test') for event in events if event.get('Action') == 'pass'}
skipped = {event.get('Test') for event in events if event.get('Action') == 'skip'}
required = {fixed, fixed + '/local_diagnostics', fixed + '/upstream_missing', 'TestDescriptorManifestContract', 'TestDescriptorSkillsAndProvenanceContract', 'TestDescriptorEffectsOfflineContract', 'TestDescriptorCompoundEffectsOfflineContract', 'TestExecutionEngineClosureAndLegacyAdapters'}
if not required <= passed or any(name and name.startswith(fixed) for name in skipped):
    raise SystemExit('execution contract did not actually pass every required test (missing/skipped fixed tests are forbidden)')
checked = {name for name in passed if name and name.startswith(prefix)}
if len(checked) != 776:
    raise SystemExit('fixed help contract checked %d descriptors; expected 776' % len(checked))
print('PASS: manifest, Skills provenance, engine closure and legacy adapters, offline effects, and all 776 fixed-binary help contracts.')
PY
