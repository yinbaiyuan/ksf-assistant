import { createHash } from 'node:crypto';
import { readFileSync, writeFileSync, readdirSync, lstatSync, renameSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

export const adapterRevision = 'ksf-names-v2';
const scriptsRoot = path.dirname(fileURLToPath(import.meta.url));
const launcherPattern = /(?<![\w./:-])lark-cli(?:\.exe)?(?![\w./-])/g;
const management = new Set(['config', 'profile', 'update', 'upgrade', 'self-update', 'skill', 'skills', 'completion']);
const sha = value => createHash('sha256').update(value).digest('hex');
const upstreamOnlyCommands = new Set(['base +app-list', 'drive files upload_prepare', 'drive files upload_finish', 'mail user_mailbox.messages modify_message']);

export function entryNote(version) {
  return `> **KSFAssistant 适配版** · 官方来源：larksuite/cli v${version} · 入口适配：${adapterRevision}。领域说明来自固定官方版本；本适配不授予权限。\n\n## KSFAssistant 受管入口\n\n本 Skill、引用文档和辅助脚本的飞书调用只使用受管 \`ksfas-lark\`。执行时将该命令名解析为以下当前用户绝对路径并加引号；不要搜索 PATH、调用其他 lark-cli、直接请求 API 或用其他通道兜底。\n\n- macOS：\`$HOME/.local/share/ksfassistant/toolchain/bin/ksfas-lark\`。\n- Windows：\`$env:USERPROFILE/AppData/Local/KSFAssistant/toolchain/bin/ksfas-lark.exe\`；PowerShell 用 \`&\` 调用带引号路径。\n- 入口不存在或工具链不健康：停止，并引导在 KSFAssistant 桌面修复；不自行安装 CLI 或修改 PATH。\n- 业务调用显式带一个 \`--as user\` 或 \`--as bot\`，按用户意图及下方身份规则选择，不因失败切换身份。帮助/schema 不代表业务获准。\n- 新命令先查询 \`ksfas-lark managed capabilities --json\`。正文中的命令可能受参数、输入大小或既有策略限制；被拒绝时说明原因，不绕过。\n- 登录、配置、增量授权、注销和工具链升级走桌面；\`auth status\`、\`auth scopes\` 保留只读诊断。\`skills read\` 改为读取本地随包引用文件。\n- user 纯读默认允许，user 写入等待桌面单次批准；Agent 不给 user 命令添加 \`--yes\`、不代点批准。bot 保留官方及产品既有确认规则，不新增桌面批准。拒绝、超时或结果未知均停止，不自动重放。\n- Python 辅助程序使用 \`python -B\`；在线 Sheets 程序必须带 \`--as user|bot\`。缺少 Python 时按官方已有 CLI 等价流程，不安装新运行时。\n\n以上仅覆盖正常受管调用链，不是阻止同用户权限进程绕过的系统沙箱。\n\n`;
}

const sharedNote = `## KSFAssistant 共享契约补充\n\n- 官方正文涉及安装、登录、配置、更新和设备授权的命令仅说明上游语义，在本产品中由桌面授权/工具链界面承接；不要运行安装脚本或导出凭据。官方权限不足仍需相应平台授权，不能改用另一身份。\n- user 写入的官方确认参数只由受管执行器在桌面批准后生成；bot 高风险命令在取得用户明确确认后，按固定命令契约传递确认参数，不根据任意错误文本自动追加。既有 disabled 不因批准解除。\n- 返回 \`artifactDelivery\` 时，\`artifacts[*].path\` 是正式交付路径；上游 \`record_file\`、\`manifest_file\` 等可能指向已删除的暂存目录。保留分页、视图、修订和完成标记；非对象上游输出在 \`upstreamOutput\`。\n- \`partialArtifacts\` 或结果未知不是成功，不重新执行原业务来补文件；先只读核验。明确区分参数受限、授权缺失、平台权限不足、桌面拒绝和执行结果未知。\n\n`;

function protectLinks(text, transform) {
  const links = [];
  const masked = text.replace(/https?:\/\/[^\s<>"'`]+/g, value => {
    links.push(value);
    return `KSFADAPTERURL${links.length - 1}END`;
  });
  return transform(masked).replace(/KSFADAPTERURL(\d+)END/g, (_, index) => links[Number(index)]);
}

export function commandAssessment(invocation, descriptors) {
  const tokens = (invocation.match(/"(?:\\.|[^"\\])*"|'[^']*'|[^\s`]+/g) || []).map(token => token.replace(/["';,]+$/, ''));
  const head = tokens[0] || '';
  if (management.has(head) || head === 'auth' && !['status', 'scopes'].includes(tokens[1])) {
    return { state: head === 'skills' && tokens[1] === 'read' ? 'local-reference' : 'desktop-control', command: tokens.slice(0, 2).join(' ') };
  }
  if (head === 'schema' || head === 'auth' || head.startsWith('--') || tokens.includes('--help') || tokens.includes('-h')) {
    return { state: 'diagnostic', command: tokens.slice(0, 3).join(' ') };
  }
  if (head === 'event' || head === 'whoami') return { state: 'restricted', command: tokens.slice(0, 2).join(' '), reasons: ['Managed event lifecycle and identity inspection belong to the desktop; no independent listener or unmanaged identity fallback.'] };
  if (head === 'api') return { state: 'restricted-reference', command: tokens.slice(0, 3).join(' '), reason: 'Raw API examples are upstream references only; query managed capabilities for the exact reviewed method, path and input contract before any call.' };
  const candidates = descriptors.filter(descriptor => [descriptor.path, ...(descriptor.aliases || [])].some(name => invocation.startsWith(name) && (!invocation[name.length] || /[\s`"'.,;/]/.test(invocation[name.length])))).sort((first, second) => second.path.length - first.path.length);
  const descriptor = candidates[0];
  if (!descriptor) {
    const upstreamOnly = [...upstreamOnlyCommands].find(name => invocation === name || invocation.startsWith(name + ' ') || invocation.startsWith(name + '`'));
    if (upstreamOnly) return { state: 'restricted', command: upstreamOnly, reasons: ['This fixed upstream reference has no reviewed execution descriptor; do not execute or substitute an unmanaged entry.'] };
    const typedShape = /^[a-z][a-z0-9-]*\s+[a-z][a-z0-9_.]*\s+[a-z][a-z0-9_]*(?:\s|`|$)/.test(invocation) && descriptors.some(item => item.path.startsWith(head + ' '));
    if (typedShape || /^[a-z][a-z0-9-]*\s+\+[a-z][a-z0-9-]*(?:\s|`|$)/.test(invocation)) throw new Error(`Unknown concrete Skill command requires review: ${tokens.slice(0, 3).join(' ')}`);
    return { state: 'unresolved-template', command: tokens.slice(0, 3).join(' '), reason: 'Prose, group or placeholder is not an executable contract; consult managed capabilities before constructing a call.' };
  }
  if (descriptor.status !== 'supported') return { state: 'restricted', command: descriptor.path, reasons: descriptor.limitations || [] };
  const flags = tokens.filter(token => token.startsWith('--')).map(token => token.slice(2).split('=')[0].replace(/[;,).]+$/, ''));
  const unsupportedFlags = flags.filter(name => {
    const flag = descriptor.flags.find(flag => flag.name === name || flag.aliases?.includes(name));
    return !flag || flag.role === 'restricted';
  });
  if (unsupportedFlags.length) return { state: 'restricted-arguments', command: descriptor.path, unsupportedFlags: [...new Set(unsupportedFlags)] };
  return { state: 'entry-adapted-template', command: descriptor.path, reason: 'Command entry and named flags reviewed; concrete values, identity, input and policy remain runtime checks, not a workflow success claim.' };
}

export function adaptMarkdown(original, name, version, descriptors) {
  const calls = [];
  let inFence = false;
  let disabledContinuation = false;
  let frontmatter = false;
  const roots = new Set(descriptors.map(descriptor => descriptor.path.split(' ')[0]));
  for (const name of [...management, 'auth', 'schema', 'api', 'event', 'whoami']) roots.add(name);
  const lines = original.split('\n').map((line, index) => {
    if (index === 0 && line === '---') frontmatter = true;
    else if (frontmatter && line === '---') frontmatter = false;
    if (/^\s*(```|~~~)/.test(line)) {
      inFence = !inFence;
      disabledContinuation = false;
      return line;
    }
    const found = [...line.matchAll(launcherPattern)];
    let blocked = false;
    for (const match of found) {
      const tail = line.slice(match.index + match[0].length).trimStart();
      const root = tail.split(/[\s`"'|]+/)[0];
      if (!roots.has(root) && !root.startsWith('--') && !root.startsWith('<') && !inFence && !line.slice(0, match.index).endsWith('`')) continue;
      const assessment = commandAssessment(tail.replace(/[`"']\]\([^)]*\).*$/, ''), descriptors);
      calls.push({ line: index + 1, ...assessment });
      if (inFence && /^\s*(?:\$\s*)?lark-cli\b/.test(line) && ['desktop-control', 'local-reference', 'restricted', 'restricted-arguments', 'restricted-reference', 'unresolved-template'].includes(assessment.state)) blocked = true;
    }
    let updated = protectLinks(line, value => {
      if (inFence || frontmatter) return value.replace(launcherPattern, 'ksfas-lark');
      return value.split(/(`[^`]*`)/g).map((part, partIndex) => {
        if (partIndex % 2) return part.replace(launcherPattern, 'ksfas-lark');
        return part.replace(launcherPattern, (token, offset, text) => {
          const root = text.slice(offset + token.length).trimStart().split(/[\s`"'|]+/)[0];
          return roots.has(root) || root.startsWith('--') ? 'ksfas-lark' : token;
        });
      }).join('');
    });
    updated = updated.replace(/\bpython(3)?(?!\s+-B)\s+(?=(?:[^\s`]*\/)?[\w-]+\.py\b|-(?:\s|$))/g, 'python$1 -B ');
    updated = updated.replace(/(python(?:3)? -B (?:[^\s`]*\/)?lark_(?:chart_layout_check|detect_subtables|inspect_workbook|profile_table)\.py)(?![^`\n]*--as\b)/g, '$1 --as "<user|bot>"');
    if (inFence && (blocked || disabledContinuation)) {
      disabledContinuation = /\\\s*$/.test(line);
      updated = `# KSFAssistant：仅作上游语义参考，请按本 Skill 入口限制处理，不执行：${updated}`;
    }
    return updated;
  });
  let output = lines.join('\n');
  if (name.endsWith('/SKILL.md')) {
    if (!output.startsWith('---\n')) throw new Error(`Missing Skill frontmatter: ${name}`);
    const end = output.indexOf('\n---', 4);
    if (end < 0) throw new Error(`Invalid Skill frontmatter: ${name}`);
    const insertion = end + 4;
    output = output.slice(0, insertion) + '\n\n' + entryNote(version) + (name === 'lark-shared/SKILL.md' ? sharedNote : '') + output.slice(insertion).replace(/^\n+/, '');
  }
  return { output, calls };
}

function inventory(root) {
  const result = {};
  function visit(relative) {
    const full = path.join(root, relative);
    const info = lstatSync(full);
    if (info.isSymbolicLink()) throw new Error(`Symlink refused: ${relative}`);
    if (info.isDirectory()) {
      for (const name of readdirSync(full).sort()) visit(path.join(relative, name));
    } else if (info.isFile()) result[relative.split(path.sep).join('/')] = sha(readFileSync(full));
    else throw new Error(`Non-regular Skill resource: ${relative}`);
  }
  visit('');
  return result;
}

export function adaptSkills({ root, upstream, upstreamBytes, descriptors }) {
  const expected = {};
  for (const skill of upstream.skills) for (const [name, digest] of Object.entries(skill.files)) expected[`${skill.name}/${name}`] = digest;
  const before = inventory(root);
  if (JSON.stringify(Object.entries(before).sort()) !== JSON.stringify(Object.entries(expected).sort())) throw new Error('Unreviewed upstream Skills files or digest mismatch');
  const files = [];
  for (const relative of Object.keys(before).sort()) {
    if (!/\.(md|html)$/.test(relative)) {
      if (!relative.endsWith('.py') && /\.(js|jsx|json|xml|sh|ps1|yaml|yml)$/.test(relative) && /\blark-cli\b/.test(readFileSync(path.join(root, relative), 'utf8'))) throw new Error(`New non-Markdown CLI invocation needs adapter review: ${relative}`);
      continue;
    }
    const original = readFileSync(path.join(root, relative), 'utf8');
    const result = adaptMarkdown(original, relative, upstream.version, descriptors);
    writeFileSync(path.join(root, relative), result.output);
    files.push({ path: relative, calls: result.calls });
  }
  const python = spawnSync('python3', ['-B', path.join(scriptsRoot, 'adapt-lark-skill-python.py'), '--root', root], { encoding: 'utf8', maxBuffer: 8 * 1024 * 1024, env: { ...process.env, PYTHONDONTWRITEBYTECODE: '1' } });
  if (python.status !== 0) throw new Error(`Python Skill adaptation refused: ${python.stderr || python.stdout}`);
  const pythonReport = JSON.parse(python.stdout);
  const snippets = spawnSync('python3', ['-B', path.join(scriptsRoot, 'adapt-lark-skill-python.py'), '--markdown-root', root], { encoding: 'utf8', maxBuffer: 8 * 1024 * 1024, env: { ...process.env, PYTHONDONTWRITEBYTECODE: '1' } });
  if (snippets.status !== 0) throw new Error(`Embedded Python Skill adaptation refused: ${snippets.stderr || snippets.stdout}`);
  const snippetReport = JSON.parse(snippets.stdout);
  pythonReport.filesChanged.push(...snippetReport.filesChanged);
  pythonReport.callsites.push(...snippetReport.callsites);
  // Rename only known Skill identities, not API names or resource file stems.
  const names = upstream.skills.map(skill => skill.name).sort((a, b) => b.length - a.length);
  const identity = new RegExp(`(?<![a-z0-9-])(?:${names.join('|')})(?![a-z0-9-])`, 'g');
  for (const relative of Object.keys(before)) {
    if (/\.(md|html|py|json|yaml|yml|sh|ps1|js)$/.test(relative)) {
      const file = path.join(root, relative);
      writeFileSync(file, readFileSync(file, 'utf8').replace(identity, name => `ksf-${name}`));
    }
  }
  for (const name of names) renameSync(path.join(root, name), path.join(root, `ksf-${name}`));
  const after = inventory(root);
  const mapped = name => `ksf-${name}`;
  if (Object.keys(after).sort().join('\n') !== Object.keys(before).map(mapped).sort().join('\n')) throw new Error('Unexpected namespace resources');
  const provenance = Object.keys(before).sort().map(name => ({ path: mapped(name), upstreamPath: name, upstreamSha256: before[name], adaptedSha256: after[mapped(name)], changed: before[name] !== after[mapped(name)] }));
  const adapterSources = Object.fromEntries(['adapt-lark-skills.mjs', 'adapt-lark-skill-python.py'].map(name => [name, sha(readFileSync(path.join(scriptsRoot, name)))]));
  const namespacePaths = value => typeof value === 'string' ? (names.some(name => value.startsWith(name + '/')) ? 'ksf-' + value : value)
    : Array.isArray(value) ? value.map(namespacePaths) : value && typeof value === 'object' ? Object.fromEntries(Object.entries(value).map(([key, item]) => [key, namespacePaths(item)])) : value;
  const report = { schemaVersion: 1, revision: adapterRevision, upstreamVersion: upstream.version, upstreamManifestSha256: sha(upstreamBytes), adapterSources, files: provenance, markdown: namespacePaths(files), python: namespacePaths(pythonReport) };
  const bytes = Buffer.from(JSON.stringify(report, null, 2) + '\n');
  const manifest = structuredClone(upstream);
  for (const skill of manifest.skills) { skill.name = `ksf-${skill.name}`; for (const name of Object.keys(skill.files)) skill.files[name] = after[`${skill.name}/${name}`]; }
  manifest.adaptation = { schemaVersion: 1, revision: adapterRevision, digest: sha(bytes), upstreamVersion: upstream.version, upstreamManifestSha256: sha(upstreamBytes) };
  return { manifest, report, bytes };
}
