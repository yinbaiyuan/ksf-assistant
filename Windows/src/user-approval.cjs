'use strict';

function parsePoll(value, now = Date.now()) {
  const exact = (object, keys) => object && typeof object === 'object' && !Array.isArray(object) && Object.keys(object).sort().join('|') === keys.sort().join('|');
  if (!exact(value, ['schemaVersion', 'request']) || value.schemaVersion !== 1) throw new Error('批准协议无效');
  if (value.request === null) return null;
  const request = value.request;
  if (!exact(request, ['id', 'title', 'user', 'application', 'action', 'target', 'content', 'attachments', 'source', 'expiresAt', ...(Object.hasOwn(request, 'preview') ? ['preview'] : [])])) throw new Error('批准请求无效');
  for (const key of ['id', 'title', 'user', 'application', 'action', 'target', 'content', 'source', 'expiresAt']) {
    if (typeof request[key] !== 'string' || request[key].includes('\0') || (key !== 'content' && !request[key].trim())) throw new Error('批准字段无效');
    if (Buffer.byteLength(request[key]) > (key === 'content' ? 1_048_576 : key === 'id' ? 256 : 65_536)) throw new Error('批准字段过长');
  }
  const expiration = Date.parse(request.expiresAt);
  if (!/^\d{4}-\d{2}-\d{2}T/.test(request.expiresAt) || !Number.isFinite(expiration) || expiration <= now || expiration > now + 301_000) throw new Error('批准已过期');
  if (!Array.isArray(request.attachments) || request.attachments.length > 1000) throw new Error('附件无效');
  for (const attachment of request.attachments) {
    if (!exact(attachment, ['name', 'size', 'sha256']) || typeof attachment.name !== 'string' || !attachment.name || attachment.name.includes('\0') || Buffer.byteLength(attachment.name) > 4096 || !Number.isSafeInteger(attachment.size) || attachment.size < 0 || typeof attachment.sha256 !== 'string' || !/^[a-fA-F0-9]{64}$/.test(attachment.sha256)) throw new Error('附件无效');
  }
  if (Object.hasOwn(request, 'preview')) {
    const preview = request.preview;
    if (!exact(preview, ['content', 'confirmLabel', 'destructive']) || typeof preview.content !== 'string' || preview.content.includes('\0') || Buffer.byteLength(preview.content) > 262144 || typeof preview.confirmLabel !== 'string' || !preview.confirmLabel.trim() || [...preview.confirmLabel].length > 8 || /[\0\r\n]/.test(preview.confirmLabel) || typeof preview.destructive !== 'boolean') throw new Error('批准预览无效');
  }
  return structuredClone(request);
}

function detailPages(request) {
  const files = request.attachments.length ? request.attachments.map((file) => `${file.name} · ${file.size} 字节\nSHA-256：${file.sha256}`).join('\n\n') : '无';
  const details = `请求\n${request.title}\n\n用户身份\n${request.user}\n\n应用\n${request.application}\n\n操作\n${request.action}\n\n目标\n${request.target}\n\n内容 / 修改摘要\n${request.content}\n\n附件\n${files}\n\n来源（由 Core 提供）\n${request.source}\n\n失效时间\n${request.expiresAt}`;
  const pages = [];
  let page = '';
  let lines = 0;
  let column = 0;
  for (const character of details) {
    if (page.length >= 900 || lines >= 16) {
      pages.push(page);
      page = '';
      lines = 0;
      column = 0;
    }
    page += character;
    column += 1;
    if (character === '\n' || column >= 48) { lines += 1; column = 0; }
  }
  if (page) pages.push(page);
  return pages;
}

class UserApprovalController {
  constructor({ core, dialog, isInteractive, now = Date.now, intervalMs = 500 }) {
    this.core = core;
    this.dialog = dialog;
    this.isInteractive = isInteractive;
    this.now = now;
    this.intervalMs = intervalMs;
    this.running = false;
    this.polling = false;
    this.active = null;
    this.retired = new Map();
    this.lastHeartbeat = 0;
    this.epoch = 0;
  }

  start() {
    if (this.running) return;
    this.running = true;
    this.timer = setInterval(() => { void this.tick(); }, this.intervalMs);
    void this.tick();
  }

  async stop() {
    this.running = false;
    clearInterval(this.timer);
    await this.unavailable();
  }

  async unavailable() {
    this.epoch += 1;
    this.lastHeartbeat = 0;
    this.dismiss();
    try { await this.core.request('userApproval/poll', { interactive: false }, { skipStart: true, timeoutMs: 2000 }); } catch {}
  }

  dismiss() {
    if (this.active) {
      this.retired.set(this.active.request.id, Date.parse(this.active.request.expiresAt));
      this.active.abort.abort();
    }
  }

  async tick() {
    if (!this.running || this.polling) return;
    this.polling = true;
    const interactive = this.canInteract();
    const epoch = this.epoch;
    if (!interactive) this.dismiss();
    try {
      const result = await this.core.request('userApproval/poll', { interactive }, { skipStart: true, timeoutMs: 2000 });
      if (!this.running || epoch !== this.epoch || !interactive || !this.canInteract()) { this.dismiss(); return; }
      const request = parsePoll(result, this.now());
      this.lastHeartbeat = this.now();
      for (const [id, expiration] of this.retired) if (expiration <= this.now()) this.retired.delete(id);
      if (this.active) {
        if (JSON.stringify(request) !== JSON.stringify(this.active.request)) this.dismiss();
        return;
      }
      if (request && !this.retired.has(request.id)) void this.present(request);
    } catch {
      this.lastHeartbeat = 0;
      this.dismiss();
    } finally {
      this.polling = false;
    }
  }

  async present(request) {
    if (this.active || !this.running || !this.canInteract()) return;
    const state = { request, abort: new AbortController() };
    this.active = state;
    const expirationTimer = setTimeout(() => state.abort.abort(), Math.max(0, Date.parse(request.expiresAt) - this.now()));
    const pages = detailPages(request);
    let page = 0;
    try {
      while (!state.abort.signal.aborted && this.running) {
        const last = page === pages.length - 1;
        const buttons = ['拒绝', last ? '批准本次操作' : '下一页'];
        if (page > 0) buttons.push('上一页');
        const result = await this.dialog.showMessageBox({
          type: 'warning',
          title: 'KSFAssistant · 用户身份操作批准',
          message: `本机受管工具请求以你的身份操作飞书 · ${page + 1}/${pages.length} 页`,
          detail: pages[page],
          buttons,
          defaultId: 0,
          cancelId: 0,
          noLink: true,
          normalizeAccessKeys: false,
          signal: state.abort.signal,
        });
        if (state.abort.signal.aborted || !this.running || !this.canInteract() || this.now() - this.lastHeartbeat >= 2000 || Date.parse(request.expiresAt) <= this.now()) return;
        if (result.response === 2 && page > 0) { page -= 1; continue; }
        if (result.response === 1 && !last) { page += 1; continue; }
        if (![0, 1].includes(result.response)) return;
        this.retired.set(request.id, Date.parse(request.expiresAt));
        const decision = await this.core.request('userApproval/decide', { id: request.id, approve: result.response === 1 }, { skipStart: true, timeoutMs: 2000 });
        if (decision?.schemaVersion !== 1 || typeof decision.accepted !== 'boolean' || !decision.accepted) this.lastHeartbeat = 0;
        return;
      }
    } catch {
      this.lastHeartbeat = 0;
    } finally {
      clearTimeout(expirationTimer);
      this.retired.set(request.id, Date.parse(request.expiresAt));
      if (this.active === state) this.active = null;
    }
  }

  canInteract() {
    try { return this.isInteractive() === true; } catch { return false; }
  }
}

module.exports = { UserApprovalController, parsePoll, detailPages };
