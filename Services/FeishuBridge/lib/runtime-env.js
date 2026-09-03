const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

function readEnvironmentFile(filePath) {
  if (!fs.existsSync(filePath)) return {};
  const values = {};
  for (const line of fs.readFileSync(filePath, 'utf8').split(/\r?\n/)) {
    const trimmed = line.trim();
    if (!trimmed || trimmed.startsWith('#')) continue;
    const index = trimmed.indexOf('=');
    if (index <= 0) continue;
    const key = trimmed.slice(0, index).trim();
    if (!/^[A-Z][A-Z0-9_]*$/.test(key)) continue;
    let value = trimmed.slice(index + 1).trim();
    if ((value.startsWith('"') && value.endsWith('"')) || (value.startsWith("'") && value.endsWith("'"))) {
      value = value.slice(1, -1);
    }
    values[key] = value;
  }
  return values;
}

function loadRuntimeEnvironment(projectRoot, { env = process.env, homeDir = os.homedir() } = {}) {
  const local = readEnvironmentFile(path.join(projectRoot, '.env.local'));
  const initial = { ...local, ...env };
  if (initial.CODEX_USAGE_BAR_MANAGED !== '1') return initial;
  const dataRoot = initial.FEISHU_BRIDGE_DATA_DIR
    ? path.resolve(initial.FEISHU_BRIDGE_DATA_DIR)
    : path.join(homeDir, '.config', 'feishu-bridge');
  const privateValues = readEnvironmentFile(path.join(dataRoot, 'runtime.env'));
  return { ...local, ...privateValues, ...env };
}

module.exports = { loadRuntimeEnvironment, readEnvironmentFile };
