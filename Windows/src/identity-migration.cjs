'use strict';

const fs = require('node:fs');
const path = require('node:path');

function readSettings(filePath) {
  let metadata;
  try {
    metadata = fs.lstatSync(filePath);
  } catch (error) {
    if (error.code === 'ENOENT') return null;
    throw new Error(`无法检查设置文件 ${filePath}（${error.code || '读取失败'}）`, { cause: error });
  }
  try {
    if (!metadata.isFile()) throw new Error('Settings must be a regular file');
    const value = JSON.parse(fs.readFileSync(filePath, 'utf8'));
    if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('Settings must be a JSON object');
    return value;
  } catch (error) {
    throw new Error(`无法读取设置文件 ${filePath}（${error.code || '无效 JSON 对象'}）`, { cause: error });
  }
}

function remapProjectPins(value) {
  if (!Array.isArray(value.pinnedProjectIds)) return value;
  return {
    ...value,
    pinnedProjectIds: [...new Set(value.pinnedProjectIds.map((projectId) => projectId === '10项目/Codex Usage Bar/项目记忆卡.md'
      ? '10项目/KSFAssistant/项目记忆卡.md'
      : projectId))],
  };
}

function writeSettingsAtomic(filePath, value, { overwrite = false } = {}) {
  fs.mkdirSync(path.dirname(filePath), { recursive: true });
  const temporaryDirectory = fs.mkdtempSync(path.join(path.dirname(filePath), '.settings-'));
  const temporary = path.join(temporaryDirectory, 'settings.json');
  try {
    fs.writeFileSync(temporary, `${JSON.stringify(value, null, 2)}\n`, { encoding: 'utf8', mode: 0o600, flag: 'wx', flush: true });
    if (overwrite) {
      fs.renameSync(temporary, filePath);
    } else {
      try {
        fs.linkSync(temporary, filePath);
      } catch (error) {
        if (error.code === 'EEXIST') return false;
        throw error;
      }
    }
    return true;
  } finally {
    fs.rmSync(temporaryDirectory, { recursive: true, force: true });
  }
}

function migrateLegacySettings(currentSettingsPath, appDataPath) {
  if (readSettings(currentSettingsPath) !== null) return;
  for (const productName of ['CodexAssistant', 'Codex Usage Bar']) {
    const sourcePath = path.join(appDataPath, productName, 'settings.json');
    const value = readSettings(sourcePath);
    if (value === null) continue;
    try {
      writeSettingsAtomic(currentSettingsPath, remapProjectPins(value));
    } catch (error) {
      throw new Error(`设置迁移失败：${sourcePath} → ${currentSettingsPath}（${error.code || '写入失败'}）`, { cause: error });
    }
    return;
  }
}

module.exports = { migrateLegacySettings, readSettings, remapProjectPins, writeSettingsAtomic };
