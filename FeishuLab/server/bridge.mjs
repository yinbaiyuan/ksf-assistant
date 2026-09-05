import { spawn } from 'node:child_process';
import { access, lstat, mkdtemp, writeFile, rm } from 'node:fs/promises';
import { constants } from 'node:fs';
import { homedir, tmpdir } from 'node:os';
import path from 'node:path';

export class LabError extends Error {
  constructor(code, message, status = 400) { super(message); this.code = code; this.status = status; }
}

export function defaultBinary() {
  const platform = process.platform;
  const architecture = process.arch === 'arm64' ? 'arm64' : 'x64';
  if (platform === 'darwin') return path.join(homedir(), 'Applications/KSFAssistant.app/Contents/Resources/runtime/feishu-bridge', `darwin-${architecture}`, 'ksf-assistant-feishu-bridge');
  throw new LabError('binary_required', '请通过 FEISHU_LAB_CLIENT 指定安装包内的原生飞书客户端。');
}

export async function createBridge(binary = process.env.FEISHU_LAB_CLIENT || defaultBinary()) {
  if (!path.isAbsolute(binary)) throw new LabError('invalid_binary', '客户端路径必须是绝对路径。');
  const info = await lstat(binary);
  if (!info.isFile() || info.isSymbolicLink()) throw new LabError('invalid_binary', '客户端必须是可执行普通文件。');
  await access(binary, constants.X_OK);
  return {
    call(args, input, signal) {
      return new Promise((resolve, reject) => {
        const child = spawn(binary, ['client', ...args], { stdio: ['pipe', 'pipe', 'pipe'], windowsHide: true });
        const buffers = [];
        let size = 0;
        let failure;
        let killTimer;
        const stop = (code, message) => {
          failure ||= new LabError(code, message, 504);
          child.kill('SIGTERM');
          killTimer ||= setTimeout(() => child.kill('SIGKILL'), 2000);
          killTimer.unref();
        };
        const cancel = () => stop('outcome_unknown', '连接已取消；已提交操作可能继续执行，请查询原 Operation，勿自动重放。');
        const timer = setTimeout(() => stop('outcome_unknown', '等待超时；远端结果可能未知，请查询原 Operation，勿重复提交。'), 90000);
        timer.unref();
        signal?.addEventListener('abort', cancel, { once: true });
        if (signal?.aborted) cancel();
        child.stdout.on('data', chunk => {
          size += chunk.length;
          if (size > 16 * 1024 * 1024) stop('response_too_large', '返回数据超过上限，请缩小查询范围。');
          else buffers.push(chunk);
        });
        child.stderr.resume();
        child.stdin.on('error', () => {});
        child.once('error', () => { failure = new LabError('client_unavailable', '原生客户端不可用，请检查安装路径。', 503); });
        child.once('close', code => {
          clearTimeout(timer); clearTimeout(killTimer);
          signal?.removeEventListener('abort', cancel);
          if (failure) return reject(failure);
          let result;
          try { result = JSON.parse(Buffer.concat(buffers).toString('utf8')); } catch {}
          if (code !== 0 && result?.status !== 'authorization_required') return reject(new LabError('service_unavailable', '客户端调用失败。请确认 KSFAssistant 与飞书服务已运行；不会自动启动或离线排队。', 503));
          if (result === undefined) return reject(new LabError('invalid_response', '客户端未返回有效 JSON。', 502));
          resolve(result);
        });
        child.stdin.end(input === undefined ? undefined : input);
      });
    },
  };
}

export async function withInputFiles(capability, input, files, run) {
  const directory = await mkdtemp(path.join(tmpdir(), 'ksfassistant-lab-'));
  try {
    const payload = { ...input };
    for (const [name, file] of Object.entries(files)) {
      const extension = /^\.[a-zA-Z0-9]{1,12}$/.test(path.extname(file.name)) ? path.extname(file.name) : '.bin';
      const destination = path.join(directory, `${Object.keys(payload).length}-${name.replace(/[^a-zA-Z0-9-]/g, '')}${extension}`);
      await writeFile(destination, Buffer.from(file.data, 'base64'), { mode: 0o600 });
      payload[name] = destination;
    }
    return await run(JSON.stringify(payload));
  } finally { await rm(directory, { recursive: true, force: true }); }
}
