'use strict';

const { spawn, spawnSync } = require('node:child_process');
const { createInterface } = require('node:readline');
const path = require('node:path');

class CoreClient {
  constructor({ executablePath, env = {}, timeoutMs = 45_000, integrations = {} }) {
    this.executablePath = executablePath;
    this.env = env;
    this.timeoutMs = timeoutMs;
    this.integrations = integrations;
    this.process = null;
    this.sequence = 0;
    this.pending = new Map();
    this.startPromise = null;
    this.closing = false;
  }

  async start() {
    if (this.closing) throw new Error('核心服务正在退出');
    if (this.startPromise) return this.startPromise;
    if (this.process && !this.process.killed) return;
    this.startPromise = this.#start();
    try {
      await this.startPromise;
    } finally {
      this.startPromise = null;
    }
  }

  async #start() {
    const child = spawn(this.executablePath, [], {
      stdio: ['pipe', 'pipe', 'pipe'],
      windowsHide: true,
      cwd: path.dirname(this.executablePath),
      env: { ...process.env, ...this.env },
    });
    this.process = child;
    const reader = createInterface({ input: child.stdout });
    reader.on('line', (line) => { if (this.process === child) this.#handleLine(line); });
    reader.once('close', () => { if (this.process === child) this.#failAll(new Error('核心服务输出已关闭'), true); });
    child.stderr.on('data', () => {});
    child.once('exit', (_code, signal) => { if (this.process === child) this.#failAll(new Error(`核心服务已停止${signal ? `（${signal}）` : ''}`)); });
    child.once('error', (error) => { if (this.process === child) this.#failAll(error, true); });
    await this.request('initialize', {
      clientInfo: { name: 'ksf_assistant_windows', title: 'KSFAssistant for Windows', version: '0.11.0-preview.1' },
      integrations: this.integrations,
    }, { skipStart: true });
  }

  async request(method, params = {}, { skipStart = false, timeoutMs } = {}) {
    if (this.closing && method !== 'shutdown') throw new Error('核心服务正在退出');
    if (!skipStart) await this.start();
    if (!this.process?.stdin?.writable) throw new Error('核心服务未运行');
    const id = ++this.sequence;
    const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params });
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`${method} 等待核心服务响应超时`));
      }, timeoutMs ?? (['feishu/setup/activate', 'feishu/setup/verify', 'feishu/setup/continue'].includes(method) ? Math.max(this.timeoutMs, 125_000) : this.timeoutMs));
      this.pending.set(id, { resolve, reject, timeout });
      this.process.stdin.write(`${payload}\n`, (error) => {
        if (!error) return;
        const pending = this.pending.get(id);
        if (!pending) return;
        clearTimeout(pending.timeout);
        this.pending.delete(id);
        reject(error);
      });
    });
  }

  async close() {
    this.closing = true;
    const child = this.process;
    if (!child) return;
    let shutdownTimer;
    try {
      const timeout = new Promise((resolve) => { shutdownTimer = setTimeout(resolve, 8_000); });
      await Promise.race([
        this.request('shutdown', {}, { skipStart: true }).catch(() => {}),
        timeout,
      ]);
    } finally {
      clearTimeout(shutdownTimer);
    }
    child.stdin?.end();
    await this.#waitForExit(child, 2_000);
    if (child.exitCode === null && !child.killed) this.#forceStopTree(child);
    this.#failAll(new Error('核心服务已关闭'));
  }

  async #waitForExit(child, timeoutMs) {
    if (child.exitCode !== null) return;
    await new Promise((resolve) => {
      const done = () => {
        clearTimeout(timer);
        child.removeListener('exit', done);
        resolve();
      };
      const timer = setTimeout(done, timeoutMs);
      child.once('exit', done);
    });
  }

  #forceStopTree(child) {
    if (process.platform === 'win32' && child.pid) {
      spawnSync('taskkill.exe', ['/PID', String(child.pid), '/T', '/F'], {
        windowsHide: true,
        stdio: 'ignore',
        timeout: 5_000,
      });
      return;
    }
    child.kill('SIGTERM');
  }

  #handleLine(line) {
    let message;
    try {
      message = JSON.parse(line);
    } catch {
      return;
    }
    const pending = this.pending.get(message.id);
    if (!pending) return;
    clearTimeout(pending.timeout);
    this.pending.delete(message.id);
    if (message.error) pending.reject(new Error(message.error.message || '核心服务调用失败'));
    else pending.resolve(message.result);
  }

  #failAll(error, stopChild = false) {
    const child = this.process;
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timeout);
      pending.reject(error);
    }
    this.pending.clear();
    this.process = null;
    if (stopChild && child) {
      child.stdin?.end();
      void this.#waitForExit(child, 2000).then(() => {
        if (child.exitCode === null && !child.killed) this.#forceStopTree(child);
      });
    }
  }
}

module.exports = { CoreClient };
