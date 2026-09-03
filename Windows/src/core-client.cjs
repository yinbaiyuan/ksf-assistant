'use strict';

const { spawn } = require('node:child_process');
const { createInterface } = require('node:readline');
const path = require('node:path');

class CoreClient {
  constructor({ executablePath, timeoutMs = 45_000 }) {
    this.executablePath = executablePath;
    this.timeoutMs = timeoutMs;
    this.process = null;
    this.sequence = 0;
    this.pending = new Map();
    this.startPromise = null;
  }

  async start() {
    if (this.process && !this.process.killed) return;
    if (this.startPromise) return this.startPromise;
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
    });
    this.process = child;
    createInterface({ input: child.stdout }).on('line', (line) => this.#handleLine(line));
    child.stderr.on('data', () => {});
    child.once('exit', (_code, signal) => this.#failAll(new Error(`共享核心已停止${signal ? `（${signal}）` : ''}`)));
    child.once('error', (error) => this.#failAll(error));
    await this.request('initialize', {
      clientInfo: { name: 'codex_usage_bar_windows', title: 'Codex Usage Bar for Windows', version: '0.8.0' },
    }, { skipStart: true });
  }

  async request(method, params = {}, { skipStart = false } = {}) {
    if (!skipStart) await this.start();
    if (!this.process?.stdin?.writable) throw new Error('共享核心未运行');
    const id = ++this.sequence;
    const payload = JSON.stringify({ jsonrpc: '2.0', id, method, params });
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`${method} 等待共享核心响应超时`));
      }, this.timeoutMs);
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
    const child = this.process;
    if (!child) return;
    try {
      await this.request('shutdown', {}, { skipStart: true });
    } catch {}
    if (!child.killed) child.kill();
    this.process = null;
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
    if (message.error) pending.reject(new Error(message.error.message || '共享核心调用失败'));
    else pending.resolve(message.result);
  }

  #failAll(error) {
    for (const pending of this.pending.values()) {
      clearTimeout(pending.timeout);
      pending.reject(error);
    }
    this.pending.clear();
    this.process = null;
  }
}

module.exports = { CoreClient };
