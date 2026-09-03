const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { spawn } = require('node:child_process');

const CATALOG_PROTOCOL = 'ksf-panel-catalog-v1';
const RESOLUTION_PROTOCOL = 'ksf-task-project-resolution-v1';
const PANEL_BRIDGE_RELATIVE_PATH = path.join(
  '.agents', 'skills', 'ksf-load-route-context', 'scripts', 'ksf_panel_bridge.rb',
);
const DEFAULT_TIMEOUT_MS = 10 * 1000;
const MAX_OUTPUT_BYTES = 4 * 1024 * 1024;

class ProjectPromotionError extends Error {
  constructor(code, message, details = {}) {
    super(message);
    this.name = 'ProjectPromotionError';
    this.code = code;
    this.details = details;
  }
}

function normalizedProjectText(value) {
  return String(value || '')
    .normalize('NFKC')
    .trim()
    .replace(/^[\s\u3000「『“\"']+|[\s\u3000」』”\"'。！？!?，,；;：:]+$/g, '')
    .replace(/[\s\u3000]+/g, '')
    .toLocaleLowerCase('zh-CN');
}

function projectNameVariants(name) {
  const value = String(name || '').trim();
  if (!value) return [];
  const variants = [value];
  if (value.endsWith('项目')) variants.push(value.slice(0, -2));
  else variants.push(`${value}项目`);
  return [...new Set(variants.map(normalizedProjectText).filter(Boolean))];
}

function parseProjectContinuationIntent(text) {
  const value = String(text || '').normalize('NFKC').trim();
  const match = value.match(
    /^(?:我\s*)?(?:(?:现在|接下来)\s*)?(?:需要|想要|想|要)\s*继续(?:完成|推进|处理)\s*(.+?)\s*[。！？!?]*$/u,
  );
  if (!match) return null;
  const projectQuery = String(match[1] || '').trim();
  return projectQuery ? { projectQuery, originalText: value } : null;
}

function selectProject(projects, projectQuery) {
  const query = normalizedProjectText(projectQuery);
  if (!query) return { status: 'not_found', candidates: [] };
  const matches = (Array.isArray(projects) ? projects : []).filter((project) => (
    projectNameVariants(project?.name).includes(query)
  ));
  if (matches.length === 1) return { status: 'matched', project: matches[0] };
  if (matches.length > 1) return { status: 'ambiguous', candidates: matches };
  return { status: 'not_found', candidates: [] };
}

function availableProjectNames(projects, maximum = 8) {
  const names = (Array.isArray(projects) ? projects : [])
    .map((project) => String(project?.name || '').trim())
    .filter(Boolean);
  if (names.length <= maximum) return names;
  return [...names.slice(0, maximum), `其余 ${names.length - maximum} 个项目`];
}

function defaultRootCandidates(env = process.env, homeDir = os.homedir()) {
  return [...new Set([
    env.KSF_PROJECT_ROOT,
    env.KMS_ROOT,
    homeDir ? path.join(homeDir, 'Documents', 'KSF') : '',
  ].map((item) => String(item || '').trim()).filter(Boolean))];
}

function validatePanelBridgeRoot(rootPath, { platform = process.platform } = {}) {
  let root;
  try {
    root = fs.realpathSync(path.resolve(rootPath));
  } catch {
    return null;
  }
  const scriptPath = path.join(root, PANEL_BRIDGE_RELATIVE_PATH);
  try {
    const lstat = fs.lstatSync(scriptPath);
    const stat = fs.statSync(scriptPath);
    if (!lstat.isFile() || lstat.isSymbolicLink() || !stat.isFile()) return null;
    if (platform !== 'win32') {
      if (typeof process.getuid === 'function' && stat.uid !== process.getuid()) return null;
      if ((stat.mode & 0o022) !== 0) return null;
    }
    return { root, scriptPath };
  } catch {
    return null;
  }
}

function discoverPanelBridgeRoot({ env = process.env, homeDir = os.homedir() } = {}) {
  for (const candidate of defaultRootCandidates(env, homeDir)) {
    const resolved = validatePanelBridgeRoot(candidate);
    if (resolved) return resolved;
  }
  throw new ProjectPromotionError(
    'project_catalog_unavailable',
    '没有找到可用的 KSF 项目目录。请配置 KSF_PROJECT_ROOT，或先在 Codex Usage Bar 中启用 KSF。',
  );
}

function runProcess({
  command,
  args,
  cwd,
  env = process.env,
  input = '',
  timeoutMs = DEFAULT_TIMEOUT_MS,
}) {
  return new Promise((resolve, reject) => {
    const child = spawn(command, args, {
      cwd,
      env,
      stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true,
    });
    const stdout = [];
    const stderr = [];
    let outputBytes = 0;
    let settled = false;
    let timer = null;
    const finish = (callback) => {
      if (settled) return;
      settled = true;
      if (timer) clearTimeout(timer);
      callback();
    };
    const collect = (bucket) => (chunk) => {
      outputBytes += chunk.length;
      if (outputBytes > MAX_OUTPUT_BYTES) {
        child.kill();
        finish(() => reject(new ProjectPromotionError(
          'project_catalog_invalid', 'KSF 项目协议返回内容过大。',
        )));
        return;
      }
      bucket.push(chunk);
    };
    child.stdout.on('data', collect(stdout));
    child.stderr.on('data', collect(stderr));
    child.on('error', (error) => finish(() => reject(error)));
    child.on('close', (code) => finish(() => {
      const output = Buffer.concat(stdout).toString('utf8').trim();
      const errorText = Buffer.concat(stderr).toString('utf8').trim();
      if (code !== 0) {
        reject(new ProjectPromotionError(
          'project_catalog_failed', errorText || `KSF 项目协议执行失败（状态 ${code}）。`,
        ));
        return;
      }
      resolve(output);
    }));
    timer = setTimeout(() => {
      child.kill();
      finish(() => reject(new ProjectPromotionError(
        'project_catalog_timeout', '读取 KSF 项目归属超时，请稍后重试。',
      )));
    }, timeoutMs);
    child.stdin.end(input);
  });
}

class KSFProjectPromotionClient {
  constructor({
    env = process.env,
    homeDir = os.homedir(),
    platform = process.platform,
    rootPath = '',
    runner = runProcess,
    timeoutMs = DEFAULT_TIMEOUT_MS,
  } = {}) {
    this.env = env;
    this.homeDir = homeDir;
    this.platform = platform;
    this.rootPath = rootPath;
    this.runner = runner;
    this.timeoutMs = timeoutMs;
  }

  bridgeLocation() {
    if (this.rootPath) {
      const resolved = validatePanelBridgeRoot(this.rootPath);
      if (!resolved) {
        throw new ProjectPromotionError(
          'project_catalog_unavailable', '配置的 KSF_PROJECT_ROOT 不可用或项目桥接脚本不安全。',
        );
      }
      return resolved;
    }
    return discoverPanelBridgeRoot({ env: this.env, homeDir: this.homeDir });
  }

  async request(mode, input = '') {
    const location = this.bridgeLocation();
    const ruby = this.env.KSF_PROJECT_RUBY
      || (this.platform === 'win32' ? 'ruby' : '/usr/bin/ruby');
    const output = await this.runner({
      command: ruby,
      args: [location.scriptPath, '--root', location.root, mode],
      cwd: location.root,
      env: this.env,
      input,
      timeoutMs: this.timeoutMs,
    });
    try {
      return JSON.parse(output);
    } catch {
      throw new ProjectPromotionError('project_catalog_invalid', 'KSF 项目协议返回了无法识别的数据。');
    }
  }

  async catalog() {
    const response = await this.request('--export-catalog');
    if (response?.protocol !== CATALOG_PROTOCOL || !Array.isArray(response.projects)) {
      throw new ProjectPromotionError('project_catalog_invalid', 'KSF 项目目录协议不兼容。');
    }
    return response.projects.filter((project) => (
      project && project.status === 'active' && project.id && project.name && project.cardPath
    ));
  }

  async resolveIntent(text) {
    const intent = parseProjectContinuationIntent(text);
    if (!intent) return null;
    const projects = await this.catalog();
    const selection = selectProject(projects, intent.projectQuery);
    if (selection.status === 'matched') return { ...intent, project: selection.project };
    if (selection.status === 'ambiguous') {
      throw new ProjectPromotionError(
        'project_ambiguous',
        `“${intent.projectQuery}”对应多个项目，请使用完整项目名：${availableProjectNames(selection.candidates).join('、')}`,
      );
    }
    const available = availableProjectNames(projects);
    throw new ProjectPromotionError(
      'project_not_found',
      `没有找到“${intent.projectQuery}”。请使用 Codex Usage Bar 中的完整项目名${available.length ? `，例如：${available.join('、')}` : ''}。`,
    );
  }

  async currentBinding(threadId) {
    const normalizedThreadId = String(threadId || '').trim();
    if (!normalizedThreadId) return null;
    const response = await this.request(
      '--resolve-projections',
      JSON.stringify({ threadIds: [normalizedThreadId] }),
    );
    if (response?.protocol !== RESOLUTION_PROTOCOL || !Array.isArray(response.projections)) {
      throw new ProjectPromotionError('project_binding_invalid', 'KSF 项目归属协议不兼容。');
    }
    const projection = response.projections
      .find((item) => item?.threadId === normalizedThreadId)?.projection;
    return Array.isArray(projection?.bindings) ? projection.bindings.at(-1) || null : null;
  }

  async currentProject(threadId) {
    const binding = await this.currentBinding(threadId);
    if (!binding?.projectCard) return null;
    const projects = await this.catalog();
    const project = projects.find((item) => item.id === binding.projectCard);
    if (!project) return null;
    return { binding, project };
  }

  async verifyBinding(threadId, projectId) {
    const current = await this.currentBinding(threadId);
    if (current?.projectCard !== projectId) {
      throw new ProjectPromotionError(
        'project_binding_missing',
        'Codex 已完成本轮，但尚未形成可验证的项目归属。当前对话仍保留为默认对话，请重试或在 Codex Usage Bar 中检查项目。',
      );
    }
    return current;
  }
}

function projectContinuationPrompt(project, originalInput = '') {
  const prompt = [
    `这是 KSF 项目「${project.name}」的延续任务。`,
    `项目记忆卡：${project.cardPath}`,
    '',
    '用户从飞书默认对话明确要求继续这个项目。请按 KSF 规范加载该项目的基础上下文，并把当前 Codex 对话作为该项目的延续任务。',
    '本轮只完成项目上下文加载与任务身份切换：不要修改文件，不要生成实施方案。完成后简短说明已进入该项目，并等待用户下一步指令。',
  ];
  const userText = String(originalInput || '').trim();
  if (userText) prompt.push('', '## My request:', userText);
  return prompt.join('\n');
}

function promotedTaskTitle(projectName) {
  const suffix = ' · 飞书任务';
  const available = Math.max(1, 64 - suffix.length);
  return `${String(projectName || '').trim().slice(0, available)}${suffix}`;
}

module.exports = {
  CATALOG_PROTOCOL,
  KSFProjectPromotionClient,
  ProjectPromotionError,
  RESOLUTION_PROTOCOL,
  availableProjectNames,
  defaultRootCandidates,
  discoverPanelBridgeRoot,
  normalizedProjectText,
  parseProjectContinuationIntent,
  projectContinuationPrompt,
  projectNameVariants,
  promotedTaskTitle,
  selectProject,
  validatePanelBridgeRoot,
};
