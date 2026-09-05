import { randomBytes, createHash, timingSafeEqual } from 'node:crypto';
import { LabError, withInputFiles } from './bridge.mjs';
import { decision } from '../shared/model.mjs';

const maxBody = 6 * 1024 * 1024;
const operationPattern = /^OP-[0-9]{14}-[A-F0-9]{8}$/;
const object = value => value && typeof value === 'object' && !Array.isArray(value);
const fingerprint = value => createHash('sha256').update(JSON.stringify(value)).digest('hex');
const reject = (condition, message, code = 'invalid_request') => { if (condition) throw new LabError(code, message); };

function exact(value, keys) {
  reject(!object(value) || Object.keys(value).some(key => !keys.includes(key)), '请求字段不合法。');
}

function bound(value, fallback = 20) {
  const number = value ?? fallback;
  reject(!Number.isInteger(number) || number < 1 || number > 50, '查询数量必须为 1–50。');
  return String(number);
}

async function readBody(request) {
  reject(!String(request.headers['content-type']).startsWith('application/json'), '必须使用 JSON 请求。');
  let bytes = 0;
  const chunks = [];
  for await (const chunk of request) {
    bytes += chunk.length;
    reject(bytes > maxBody, '输入超过 6 MiB 上限。');
    chunks.push(chunk);
  }
  try { return JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { throw new LabError('invalid_json', 'JSON 格式无效。'); }
}

export function queryArguments(body) {
  exact(body, ['kind', 'limit', 'id', 'box', 'fingerprint']);
  const fixed = { catalog: ['capability', 'catalog'], capabilities: ['capabilities'], policy: ['policy', 'read'], status: ['status'], snapshot: ['snapshot'], doctor: ['doctor'], permissions: ['permissions'], targets: ['targets', 'list'], links: ['task-link', 'list'], eventCatalog: ['events', 'catalog'], eventStatus: ['events', 'status'] };
  if (fixed[body.kind]) return fixed[body.kind];
  if (body.kind === 'events') return ['events', 'recent', '--limit', bound(body.limit)];
  if (body.kind === 'event') {
    reject(typeof body.fingerprint !== 'string' || !/^[a-zA-Z0-9:_-]{8,160}$/.test(body.fingerprint), '事件指纹无效。');
    return ['events', 'get', '--fingerprint', body.fingerprint];
  }
  if (body.kind === 'operation') {
    reject(!operationPattern.test(body.id), 'Operation ID 无效。');
    return ['operation', 'status', '--id', body.id];
  }
  if (body.kind === 'recent' || body.kind === 'result') {
    reject(!['outbox', 'docbox', 'actionbox', ...(body.kind === 'recent' ? ['audit'] : [])].includes(body.box), '工作箱类型无效。');
    if (body.kind === 'recent') return ['recent', body.box, '--limit', bound(body.limit)];
    reject(typeof body.id !== 'string' || !/^(OUT|DOC|ACT|CAP)-[a-zA-Z0-9-]{1,100}$/.test(body.id), '结果 ID 无效。');
    return ['result', body.box, body.id];
  }
  throw new LabError('unsupported_query', '不支持此查询，不提供任意命令透传。');
}

export function validateInput(capability, input, files = {}) {
  reject(!object(input) || !object(files), '参数必须是 JSON 对象。');
  const fields = new Map(capability.inputFields.map(field => [field.name, field]));
  for (const [name, value] of Object.entries(input)) {
    const field = fields.get(name);
    reject(!field || name === '__proto__' || name === 'constructor' || name === 'prototype', `不支持输入字段：${name}`);
    reject(field.type === 'path', '禁止服务器路径。输入文件请使用上传控件；输出直接查看 JSON。');
    reject(value === null, `${name} 不能为 null。`);
    reject(field.type === 'integer' && !Number.isSafeInteger(value), `${name} 必须为整数。`);
    reject(field.type === 'boolean' && typeof value !== 'boolean', `${name} 必须为布尔值。`);
    reject(['string', 'enum', 'csv'].includes(field.type) && typeof value !== 'string', `${name} 必须为字符串。`);
  }
  let total = Buffer.byteLength(JSON.stringify(input));
  for (const [name, file] of Object.entries(files)) {
    reject(fields.get(name)?.type !== 'path' || /^(output|output-dir|output-file|out)$/.test(name), '此字段不支持上传。');
    exact(file, ['name', 'data']);
    reject(typeof file.name !== 'string' || file.name.length > 160 || typeof file.data !== 'string' || !/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(file.data), '文件数据无效。');
    total += Buffer.byteLength(file.data, 'base64');
  }
  reject(total > 4 * 1024 * 1024, '本测试台单次输入与附件合计最多 4 MiB。');
  for (const field of fields.values()) reject(field.required && input[field.name] === undefined && files[field.name] === undefined, `缺少必填字段：${field.name}`);
}

export function createApp({ bridge, staticHandler, now = Date.now } = {}) {
  const token = randomBytes(32).toString('hex');
  const proofs = new Map();
  const submissions = new Map();
  let active = 0;
  let catalog;
  const send = (response, status, data) => { response.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8' }); response.end(JSON.stringify(data)); };
  const call = (args, input, signal) => bridge.call(args, input, signal);
  const loadCatalog = async signal => {
    if (!catalog) catalog = (await call(['capability', 'catalog'], undefined, signal)).capabilities;
    if (!Array.isArray(catalog)) throw new LabError('catalog_unavailable', '能力目录不可用。', 503);
    return catalog;
  };
  const run = async (body, signal) => {
    exact(body, ['capabilityId', 'input', 'files', 'proof', 'requestId', 'acknowledge']);
    const capability = (await loadCatalog(signal)).find(item => item.id === body.capabilityId);
    reject(!capability || capability.published === false, '能力未发布或不存在。');
    validateInput(capability, body.input, body.files || {});
    return capability;
  };
  return async (request, response) => {
    const address = request.socket.localAddress;
    const origin = `http://127.0.0.1:${request.socket.localPort}`;
    const endpoint = request.url?.split('?')[0];
    response.setHeader('Cache-Control', 'no-store');
    response.setHeader('X-Content-Type-Options', 'nosniff');
    response.setHeader('Referrer-Policy', 'no-referrer');
    response.setHeader('X-Frame-Options', 'DENY');
    if (!['127.0.0.1', '::ffff:127.0.0.1'].includes(address) || request.headers.host !== new URL(origin).host || request.headers.origin && request.headers.origin !== origin || request.headers['sec-fetch-site'] === 'cross-site') return send(response, 403, { error: 'origin_denied', message: '只允许本机同源访问。' });
    if (!endpoint?.startsWith('/api/')) return staticHandler ? staticHandler(request, response) : send(response, 404, { error: 'not_found' });
    if (endpoint === '/api/session' && request.method === 'GET' && request.headers['x-lab-client'] === '1') return send(response, 200, { token, mode: 'local', maxInputBytes: 4 * 1024 * 1024 });
    const supplied = Buffer.from(String(request.headers['x-lab-token'] || ''));
    if (supplied.length !== token.length || !timingSafeEqual(supplied, Buffer.from(token))) return send(response, 403, { error: 'session_required', message: '会话无效，请刷新测试台。' });
    if (request.method !== 'POST') return send(response, 405, { error: 'method_not_allowed' });
    if (active >= 4) return send(response, 429, { error: 'busy', message: '本机请求已达并发上限，请等待。' });
    active++;
    const controller = new AbortController();
    const disconnect = () => { if (!response.writableEnded) controller.abort(); };
    response.on('close', disconnect);
    const deadline = setTimeout(() => controller.abort(), 120000);
    deadline.unref();
    try {
      const body = await readBody(request);
      let result;
      if (endpoint === '/api/query') result = await call(queryArguments(body), undefined, controller.signal);
      else if (endpoint === '/api/validate') {
        const capability = await run(body, controller.signal);
        result = await withInputFiles(capability, body.input, body.files || {}, payload => call(['capability', capability.risk === 'read' ? 'read' : 'write', capability.id, '--dry-run', '--payload-file', '-'], payload, controller.signal));
        reject(result.status !== 'dry_run', '服务未确认参数校验通过。');
        for (const [key, value] of proofs) if (value.expiresAt < now()) proofs.delete(key);
        reject(proofs.size >= 64, '校验会话已满，请稍后重试。');
        const proof = randomBytes(24).toString('hex');
        const expiresAt = now() + 120000;
        proofs.set(proof, { fingerprint: fingerprint([body.capabilityId, body.input, body.files || {}]), expiresAt });
        result = { ...result, proof, expiresAt, note: '仅结构校验，不代表权限、运行开关或真实预检已通过。' };
      } else if (endpoint === '/api/execute') {
        const capability = await run(body, controller.signal);
        reject(typeof body.requestId !== 'string' || !/^[a-zA-Z0-9-]{16,80}$/.test(body.requestId), '请求标识无效。');
        reject(body.acknowledge !== true, '必须明确确认执行意图。');
        const signature = fingerprint([body.capabilityId, body.input, body.files || {}]);
        const existing = submissions.get(body.requestId);
        if (existing) {
          reject(existing.signature !== signature, '请求标识与原参数不匹配。');
          result = await existing.promise;
        } else {
          const proof = proofs.get(body.proof);
          reject(!proof || proof.expiresAt < now() || proof.fingerprint !== signature, '参数已改变或校验已过期，请重新校验。', 'validation_required');
          reject(submissions.size >= 256, '本会话已达执行上限，请先核对已有结果后重新启动实验台。');
          proofs.delete(body.proof);
          const promise = (async () => {
            const current = await call(['policy', 'read'], undefined, controller.signal);
            reject(decision(capability, current.policy).value === 'disabled' || !current.policy, '当前治理策略禁止执行。', 'capability_disabled');
            return withInputFiles(capability, body.input, body.files || {}, payload => call(['operation', 'prepare', capability.id, '--source', 'feishu-lab', '--payload-file', '-'], payload, controller.signal));
          })();
          submissions.set(body.requestId, { signature, promise });
          result = await promise;
        }
      } else if (endpoint === '/api/operation') {
        exact(body, ['action', 'id', 'challenge', 'acknowledge']);
        reject(!operationPattern.test(body.id) || !['confirm', 'cancel'].includes(body.action), 'Operation 或动作无效。');
        reject(body.acknowledge !== true, '必须明确确认此操作。');
        if (body.action === 'confirm') {
          reject(typeof body.challenge !== 'string' || body.challenge.length < 8 || body.challenge.length > 256, '缺少原审批 challenge。');
          result = await call(['operation', 'confirm', '--id', body.id, '--challenge-file', '-'], body.challenge, controller.signal);
        } else result = await call(['operation', 'cancel', '--id', body.id], undefined, controller.signal);
      } else if (endpoint === '/api/policy') {
        exact(body, ['policy', 'expectedRevision', 'acknowledge']);
        reject(body.acknowledge !== true || !Number.isSafeInteger(body.expectedRevision) || body.expectedRevision < 1, '请明确确认策略变更并提供原 revision。');
        exact(body.policy, ['version', 'revision', 'riskDefaults', 'capabilityOverrides', 'updatedAt']);
        reject(!object(body.policy.riskDefaults) || !object(body.policy.capabilityOverrides), '策略结构无效。');
        const capabilities = await loadCatalog(controller.signal);
        reject(Object.keys(body.policy.capabilityOverrides).some(id => !capabilities.some(item => item.id === id)), '策略含未知能力。');
        reject(Object.values(body.policy.riskDefaults).concat(Object.values(body.policy.capabilityOverrides)).some(value => !['allowed', 'disabled', 'confirm_each'].includes(value)), '治理决策无效。');
        result = await call(['policy', 'update', '--expected-revision', String(body.expectedRevision), '--payload-file', '-'], JSON.stringify(body.policy), controller.signal);
      } else throw new LabError('not_found', '不支持此接口。', 404);
      if (!response.destroyed) send(response, 200, result);
    } catch (error) {
      if (!response.destroyed) send(response, error.status || 500, { error: error.code || 'internal_error', message: error instanceof LabError ? error.message : '请求失败，请检查服务状态。不会自动重试。' });
    } finally {
      active--; clearTimeout(deadline); response.off('close', disconnect);
    }
  };
}
