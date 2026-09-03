const { spawn, spawnSync } = require('node:child_process');
const crypto = require('node:crypto');
const fs = require('node:fs');
const path = require('node:path');
const { chmodPrivate, commandForNodeScript } = require('./platform-runtime');

function safeOneLine(text, limit = 160) {
  const value = String(text || '').replace(/\s+/g, ' ').trim();
  if (value.length <= limit) return value;
  return `${value.slice(0, Math.max(0, limit - 20))}...`;
}

function safeAuthStatusDetail(text) {
  const value = String(text || '').replace(/^\uFEFF/, '').trim();
  try {
    const parsed = JSON.parse(value);
    const type = [parsed?.error?.type, parsed?.error?.subtype].filter(Boolean).join('/');
    const message = safeOneLine(parsed?.error?.message || parsed?.message || 'not configured', 180);
    return `${type || 'lark-cli'}: ${message}`;
  } catch {
    return safeOneLine(value, 900);
  }
}

function createLarkCliRunner(options = {}) {
  const {
    bin,
    cwd,
    env = process.env,
    profile = '',
    as = 'bot',
    privateDir = '',
    mediaRoot = '',
  } = options;

  if (!bin) throw new Error('lark-cli bin is required');

  function profileArgs(args) {
    return profile ? ['--profile', profile, ...args] : args;
  }

  function identityArgs() {
    return as ? ['--as', as] : [];
  }

  function runLarkCli(args, runOptions = {}) {
    return new Promise((resolve, reject) => {
      const invocation = commandForNodeScript(bin, profileArgs(args));
      const child = spawn(invocation.command, invocation.args, {
        cwd,
        env,
        stdio: ['pipe', 'pipe', 'pipe'],
      });
      let stdout = '';
      let stderr = '';
      let finished = false;
      const timeoutMs = runOptions.timeoutMs || 60 * 1000;
      const timer = setTimeout(() => {
        if (finished) return;
        finished = true;
        child.kill('SIGTERM');
        reject(new Error(`lark-cli timeout: ${args.join(' ')}`));
      }, timeoutMs);

      child.stdout.on('data', (chunk) => {
        stdout += chunk.toString();
      });
      child.stderr.on('data', (chunk) => {
        stderr += chunk.toString();
      });
      child.on('error', (error) => {
        if (finished) return;
        finished = true;
        clearTimeout(timer);
        reject(error);
      });
      child.on('exit', (code, signal) => {
        if (finished) return;
        finished = true;
        clearTimeout(timer);
        if (code === 0) {
          resolve({ stdout, stderr, code, signal });
          return;
        }
        const detail = safeOneLine(stderr || stdout || `exit ${code || signal}`, 900);
        reject(new Error(`lark-cli failed: ${detail}`));
      });

      if (runOptions.input !== undefined) {
        child.stdin.end(runOptions.input);
      } else {
        child.stdin.end();
      }
    });
  }

  async function runLarkCliJson(args, runOptions = {}) {
    const result = await runLarkCli(args, runOptions);
    const text = result.stdout.trim();
    if (!text) return {};
    try {
      return JSON.parse(text);
    } catch {
      throw new Error(`lark-cli returned non-json: ${safeOneLine(text, 900)}`);
    }
  }

  async function runLarkCliJsonWithPayloadFile(args, {
    payload,
    placeholder = '__PRIVATE_JSON_PAYLOAD__',
    timeoutMs,
  } = {}) {
    const payloadDir = privateDir || path.join(cwd, 'node_modules', '.cache', 'feishu-bridge', 'action-payloads');
    fs.mkdirSync(payloadDir, { recursive: true, mode: 0o700 });
    const dirStat = fs.lstatSync(payloadDir);
    if (dirStat.isSymbolicLink() || !dirStat.isDirectory()) throw new Error('unsafe action payload directory');
    chmodPrivate(payloadDir, 0o700);
    const payloadPath = path.join(payloadDir, `${process.pid}-${crypto.randomBytes(8).toString('hex')}.json`);
    const value = typeof payload === 'string' ? payload : JSON.stringify(payload);
    fs.writeFileSync(payloadPath, value, { encoding: 'utf8', mode: 0o600, flag: 'wx' });
    const payloadArgument = path.relative(cwd, payloadPath);
    const fileArgument = payloadArgument.startsWith('..') || path.isAbsolute(payloadArgument)
      ? payloadPath : payloadArgument;
    const commandArgs = args.map((item) => item === placeholder ? `@${fileArgument.split(path.sep).join('/')}` : item);
    try {
      return await runLarkCliJson(commandArgs, { timeoutMs });
    } finally {
      try {
        fs.unlinkSync(payloadPath);
      } catch (error) {
        if (error.code !== 'ENOENT') throw error;
      }
    }
  }

  async function runLarkCliJsonWithPrivateFiles(args, {
    files = {},
    input,
    timeoutMs,
  } = {}) {
    const entries = Object.entries(files);
    if (!entries.length) return runLarkCliJson(args, { input, timeoutMs });
    const payloadDir = privateDir || path.join(cwd, 'node_modules', '.cache', 'feishu-bridge', 'action-payloads');
    fs.mkdirSync(payloadDir, { recursive: true, mode: 0o700 });
    const dirStat = fs.lstatSync(payloadDir);
    if (dirStat.isSymbolicLink() || !dirStat.isDirectory()) throw new Error('unsafe action payload directory');
    chmodPrivate(payloadDir, 0o700);
    const created = [];
    const replacements = new Map();
    try {
      for (const [placeholder, payload] of entries) {
        if (!/^__PRIVATE_[A-Z0-9_]+__$/.test(placeholder)) throw new Error('invalid private payload placeholder');
        const payloadPath = path.join(payloadDir, `${process.pid}-${crypto.randomBytes(8).toString('hex')}.json`);
        const value = typeof payload === 'string' ? payload : JSON.stringify(payload);
        fs.writeFileSync(payloadPath, value, { encoding: 'utf8', mode: 0o600, flag: 'wx' });
        created.push(payloadPath);
        const relative = path.relative(cwd, payloadPath);
        const fileArgument = relative.startsWith('..') || path.isAbsolute(relative) ? payloadPath : relative;
        replacements.set(placeholder, `@${fileArgument.split(path.sep).join('/')}`);
      }
      const commandArgs = args.map((item) => replacements.get(item) || item);
      return await runLarkCliJson(commandArgs, { input, timeoutMs });
    } finally {
      for (const payloadPath of created) {
        try {
          fs.unlinkSync(payloadPath);
        } catch (error) {
          if (error.code !== 'ENOENT') throw error;
        }
      }
    }
  }

  function larkApiData(resp) {
    return resp?.data?.data || resp?.data || resp;
  }

  function ensureReady() {
    const versionInvocation = commandForNodeScript(bin, profileArgs(['--version']));
    const version = spawnSync(versionInvocation.command, versionInvocation.args, {
      cwd,
      env,
      encoding: 'utf8',
      timeout: 10 * 1000,
    });
    if (version.status !== 0) {
      throw new Error(`lark-cli unavailable: ${safeOneLine(version.stderr || version.stdout || version.error?.message, 900)}`);
    }

    const authInvocation = commandForNodeScript(bin, profileArgs(['auth', 'status']));
    const auth = spawnSync(authInvocation.command, authInvocation.args, {
      cwd,
      env,
      encoding: 'utf8',
      timeout: 10 * 1000,
    });
    if (auth.status !== 0) {
      throw new Error([
        'lark-cli is not configured for this workspace.',
        `Detail: ${safeAuthStatusDetail(auth.stderr || auth.stdout || auth.error?.message)}`,
        'Run: npm run bridge -- auth configure-existing --payload-file -',
        'Use auth start-config --create-new only when the user explicitly wants a new Feishu CLI application.',
        'Then verify: npx lark-cli auth status',
      ].join('\n'));
    }
    return (version.stdout || version.stderr || '').trim();
  }

  async function larkApi(method, apiPath, { data, params, timeoutMs } = {}) {
    const args = [
      'api',
      method,
      apiPath,
      ...identityArgs(),
      '--format',
      'json',
    ];
    if (data !== undefined) args.push('--data', JSON.stringify(data));
    if (params !== undefined) args.push('--params', JSON.stringify(params));
    return runLarkCliJson(args, { timeoutMs });
  }

  async function larkApiPrivate(method, apiPath, { data, params, timeoutMs } = {}) {
    const args = [
      'api',
      method,
      apiPath,
      ...identityArgs(),
      '--format',
      'json',
    ];
    const files = {};
    if (data !== undefined) {
      args.push('--data', '__PRIVATE_API_DATA__');
      files.__PRIVATE_API_DATA__ = data;
    }
    if (params !== undefined) {
      args.push('--params', '__PRIVATE_API_PARAMS__');
      files.__PRIVATE_API_PARAMS__ = params;
    }
    return runLarkCliJsonWithPrivateFiles(args, { files, timeoutMs });
  }

  function sentMessageId(resp) {
    return resp?.data?.message_id
      || resp?.data?.message?.message_id
      || resp?.data?.data?.message_id
      || resp?.data?.data?.message?.message_id
      || resp?.message_id
      || resp?.message?.message_id
      || '';
  }

  function localMediaArgument(filePath) {
    const absolute = path.resolve(String(filePath || ''));
    const relative = path.relative(cwd, absolute);
    const inWorkingTree = relative && !relative.startsWith('..') && !path.isAbsolute(relative);
    const privateRelative = mediaRoot ? path.relative(path.resolve(mediaRoot), absolute) : '';
    const inPrivateMediaRoot = mediaRoot && privateRelative
      && !privateRelative.startsWith('..') && !path.isAbsolute(privateRelative);
    if (!inWorkingTree && !inPrivateMediaRoot) throw new Error('media file must be staged inside a bridge private root');
    const stat = fs.lstatSync(absolute);
    if (stat.isSymbolicLink() || !stat.isFile()) throw new Error('media file must be a regular file');
    return (inWorkingTree ? relative : absolute).split(path.sep).join('/');
  }

  async function larkImSend({ target, text, format = 'text', content, filePath, idempotencyKey }) {
    const targetArgs = target.type === 'chat_id'
      ? ['--chat-id', target.id]
      : ['--user-id', target.id];
    const value = content === undefined ? text : content;
    let contentArgs;
    switch (format) {
      case 'text': contentArgs = ['--text', String(value || '')]; break;
      case 'markdown': contentArgs = ['--markdown', String(value || '')]; break;
      case 'card': contentArgs = ['--content', String(value || ''), '--msg-type', 'interactive']; break;
      case 'image': contentArgs = ['--image', localMediaArgument(filePath)]; break;
      case 'file': contentArgs = ['--file', localMediaArgument(filePath)]; break;
      default: throw new Error(`unsupported message format: ${format}`);
    }
    const resp = await runLarkCliJson([
      'im',
      '+messages-send',
      ...identityArgs(),
      ...targetArgs,
      ...contentArgs,
      '--idempotency-key',
      idempotencyKey,
    ]);
    return { response: resp, messageId: sentMessageId(resp) };
  }

  async function larkImReply({ messageId, text, idempotencyKey }) {
    const resp = await runLarkCliJson([
      'im',
      '+messages-reply',
      ...identityArgs(),
      '--message-id',
      messageId,
      '--text',
      String(text || ''),
      '--idempotency-key',
      idempotencyKey,
    ]);
    return { response: resp, messageId: sentMessageId(resp) };
  }

  async function larkImReplyCard({ messageId, card, idempotencyKey }) {
    const content = typeof card === 'string' ? card : JSON.stringify(card);
    const resp = await runLarkCliJson([
      'im',
      '+messages-reply',
      ...identityArgs(),
      '--message-id',
      messageId,
      '--content',
      content,
      '--msg-type',
      'interactive',
      '--idempotency-key',
      idempotencyKey,
    ]);
    return { response: resp, messageId: sentMessageId(resp) };
  }

  async function larkImPatchCard({ messageId, card, timeoutMs }) {
    return runLarkCliJsonWithPayloadFile([
      'im',
      'messages',
      'patch',
      ...identityArgs(),
      '--message-id',
      messageId,
      '--data',
      '__PRIVATE_JSON_PAYLOAD__',
      '--format',
      'json',
    ], {
      payload: { content: JSON.stringify(card) },
      timeoutMs,
    });
  }

  async function larkCardUpdateByToken({ token, card, timeoutMs }) {
    return runLarkCliJsonWithPayloadFile([
      'api',
      'POST',
      '/open-apis/interactive/v1/card/update',
      ...identityArgs(),
      '--format',
      'json',
      '--data',
      '__PRIVATE_JSON_PAYLOAD__',
    ], {
      payload: { token, card },
      timeoutMs,
    });
  }

  async function larkImDownloadMessageResource({
    messageId,
    fileKey,
    resourceType,
    output,
    timeoutMs,
  }) {
    const outputPath = String(output || '').replace(/\\/g, '/');
    if (!outputPath || path.posix.isAbsolute(outputPath) || outputPath.split('/').includes('..')) {
      throw new Error('resource output must be a safe project-relative path');
    }
    if (!['image', 'file'].includes(resourceType)) throw new Error('resource type must be image or file');
    return runLarkCliJson([
      'im',
      '+messages-resources-download',
      ...identityArgs(),
      '--message-id',
      messageId,
      '--file-key',
      fileKey,
      '--type',
      resourceType,
      '--output',
      outputPath,
      '--format',
      'json',
    ], { timeoutMs });
  }

  async function larkDocFetch(doc, {
    scope = 'full',
    keyword,
    startBlockId,
    endBlockId,
    contextBefore,
    contextAfter,
    maxDepth,
    detail = 'simple',
    docFormat = 'markdown',
    timeoutMs,
  } = {}) {
    const args = [
      'docs',
      '+fetch',
      ...identityArgs(),
      '--api-version',
      'v2',
      '--doc',
      doc,
      '--scope',
      scope,
      '--detail',
      detail,
      '--doc-format',
      docFormat,
      '--format',
      'json',
    ];
    if (keyword) args.push('--keyword', keyword);
    if (startBlockId) args.push('--start-block-id', startBlockId);
    if (endBlockId) args.push('--end-block-id', endBlockId);
    if (contextBefore !== undefined) args.push('--context-before', String(contextBefore));
    if (contextAfter !== undefined) args.push('--context-after', String(contextAfter));
    if (maxDepth !== undefined) args.push('--max-depth', String(maxDepth));
    return runLarkCliJson(args, { timeoutMs });
  }

  async function larkDocCreate({ content, docFormat = 'markdown', title, parentToken, timeoutMs } = {}) {
    if (!['markdown', 'xml'].includes(docFormat)) throw new Error('unsupported document create format');
    const args = [
      'docs',
      '+create',
      ...identityArgs(),
      '--doc-format',
      docFormat,
      '--content',
      '-',
      '--format',
      'json',
    ];
    if (title) args.push('--title', title);
    if (parentToken) args.push('--parent-token', parentToken);
    return runLarkCliJson(args, { input: String(content || ''), timeoutMs });
  }

  async function larkDocUpdate(doc, {
    content,
    docFormat = 'markdown',
    command = 'append',
    newTitle,
    selectionByTitle,
    selectionWithEllipsis,
    timeoutMs,
  } = {}) {
    if (newTitle) throw new Error('document title updates are not supported by lark-cli 1.0.92');
    if (selectionByTitle) throw new Error('selection-by-title is not supported by lark-cli 1.0.92');
    const args = [
      'docs',
      '+update',
      ...identityArgs(),
      '--api-version',
      'v2',
      '--doc',
      doc,
      '--command',
      command,
      '--content',
      '-',
      '--doc-format',
      docFormat,
    ];
    if (command === 'str_replace') args.push('--pattern', String(selectionWithEllipsis || ''));
    return runLarkCliJson(args, { timeoutMs, input: String(content || '') });
  }

  async function larkDocCreateVersion(fileToken, { name, objType = 'docx', timeoutMs } = {}) {
    return larkApi('POST', `/open-apis/drive/v1/files/${fileToken}/versions`, {
      data: {
        name,
        obj_type: objType,
      },
      timeoutMs,
    });
  }

  async function larkDocListVersions(fileToken, { objType = 'docx', pageSize = 10, timeoutMs } = {}) {
    return larkApi('GET', `/open-apis/drive/v1/files/${fileToken}/versions`, {
      params: {
        obj_type: objType,
        page_size: pageSize,
      },
      timeoutMs,
    });
  }

  async function larkContactListDepartments({
    departmentId = '0',
    fetchChild = true,
    pageSize = 50,
    pageToken = '',
    timeoutMs,
  } = {}) {
    const params = {
      department_id_type: 'open_department_id',
      fetch_child: Boolean(fetchChild),
      page_size: pageSize,
    };
    if (pageToken) params.page_token = pageToken;
    return larkApi('GET', `/open-apis/contact/v3/departments/${encodeURIComponent(departmentId)}/children`, {
      params,
      timeoutMs,
    });
  }

  async function larkContactListUsers({
    departmentId = '0',
    pageSize = 50,
    pageToken = '',
    timeoutMs,
  } = {}) {
    const params = {
      department_id: departmentId,
      department_id_type: 'open_department_id',
      user_id_type: 'open_id',
      page_size: pageSize,
    };
    if (pageToken) params.page_token = pageToken;
    return larkApi('GET', '/open-apis/contact/v3/users', { params, timeoutMs });
  }

  async function larkImListChats({
    pageSize = 100,
    pageToken = '',
    timeoutMs,
  } = {}) {
    const params = {
      page_size: pageSize,
      sort_type: 'ByCreateTimeAsc',
      user_id_type: 'open_id',
    };
    if (pageToken) params.page_token = pageToken;
    return larkApi('GET', '/open-apis/im/v1/chats', { params, timeoutMs });
  }

  async function larkImListMessages({
    chatId,
    start,
    end,
    pageSize = 50,
    order = 'desc',
    timeoutMs,
  } = {}) {
    return runLarkCliJson([
      'im',
      '+chat-messages-list',
      ...identityArgs(),
      '--chat-id',
      chatId,
      '--start',
      start,
      '--end',
      end,
      '--page-size',
      String(pageSize),
      '--order',
      order,
      '--no-reactions',
      '--format',
      'json',
    ], { timeoutMs });
  }

  async function larkImSearchMessages({
    chatId,
    query,
    start,
    end,
    pageSize = 20,
    timeoutMs,
  } = {}) {
    const args = [
      'im',
      '+messages-search',
      ...identityArgs(),
      '--chat-id',
      chatId,
      '--query',
      query,
      '--start',
      start,
      '--end',
      end,
      '--page-size',
      String(pageSize),
      '--no-reactions',
      '--format',
      'json',
    ];
    return runLarkCliJson(args, { timeoutMs });
  }

  async function larkImListThread({ threadId, pageSize = 50, order = 'asc', timeoutMs } = {}) {
    return runLarkCliJson([
      'im',
      '+threads-messages-list',
      ...identityArgs(),
      '--thread',
      threadId,
      '--page-size',
      String(pageSize),
      '--order',
      order,
      '--no-reactions',
      '--format',
      'json',
    ], { timeoutMs });
  }

  async function larkKnowledgeSearch({ query, docTypes, pageSize = 15, sort = 'default', timeoutMs } = {}) {
    const args = [
      'drive',
      '+search',
      ...identityArgs(),
      '--query',
      String(query || ''),
      '--page-size',
      String(pageSize),
      '--sort',
      sort,
      '--format',
      'json',
    ];
    if (docTypes) args.push('--doc-types', docTypes);
    return runLarkCliJson(args, { timeoutMs });
  }

  async function larkDriveInspect({ url, timeoutMs } = {}) {
    return runLarkCliJson([
      'drive',
      '+inspect',
      ...identityArgs(),
      '--url',
      url,
      '--format',
      'json',
    ], { timeoutMs });
  }

  async function larkDriveListComments({
    url,
    pageSize = 50,
    solvedStatus = 'false',
    commentScope = 'all',
    timeoutMs,
  } = {}) {
    return runLarkCliJson([
      'drive',
      '+list-comments',
      ...identityArgs(),
      '--url',
      url,
      '--page-size',
      String(pageSize),
      '--solved-status',
      solvedStatus,
      '--comment-scope',
      commentScope,
      '--format',
      'json',
    ], { timeoutMs });
  }

  async function larkDriveAddComment({ doc, comment, blockId, timeoutMs } = {}) {
    const args = [
      'drive',
      '+add-comment',
      ...identityArgs(),
      '--doc',
      doc,
      '--content',
      '-',
      '--format',
      'json',
    ];
    if (blockId) args.push('--block-id', blockId);
    else args.push('--full-comment');
    return runLarkCliJson(args, {
      timeoutMs,
      input: JSON.stringify([{ type: 'text', text: String(comment || '') }]),
    });
  }

  return {
    cwd,
    identityArgs,
    profileArgs,
    runLarkCli,
    runLarkCliJson,
    runLarkCliJsonWithPayloadFile,
    runLarkCliJsonWithPrivateFiles,
    larkApi,
    larkApiPrivate,
    larkApiData,
    larkImSend,
    larkImReply,
    larkImReplyCard,
    larkImPatchCard,
    larkCardUpdateByToken,
    larkImDownloadMessageResource,
    larkDocCreate,
    larkDocFetch,
    larkDocUpdate,
    larkDocCreateVersion,
    larkDocListVersions,
    larkContactListDepartments,
    larkContactListUsers,
    larkImListChats,
    larkImListMessages,
    larkImSearchMessages,
    larkImListThread,
    larkKnowledgeSearch,
    larkDriveInspect,
    larkDriveListComments,
    larkDriveAddComment,
    ensureReady,
  };
}

module.exports = {
  createLarkCliRunner,
};
