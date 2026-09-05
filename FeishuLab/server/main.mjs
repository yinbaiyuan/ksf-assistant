import { createServer } from 'node:http';
import { readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createApp } from './app.mjs';
import { createBridge } from './bridge.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const port = Number(process.env.FEISHU_LAB_PORT || 4318);
if (!Number.isInteger(port) || port < 1024 || port > 65535) throw new Error('FEISHU_LAB_PORT must be 1024–65535');
const development = process.argv.includes('--dev');
let vite;
const server = createServer();
if (development) {
  const { createServer: createVite } = await import('vite');
  vite = await createVite({ root, server: { middlewareMode: true, hmr: { server }, allowedHosts: ['127.0.0.1'] }, appType: 'spa' });
}
const contentTypes = { '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.css': 'text/css; charset=utf-8', '.svg': 'image/svg+xml' };
const staticHandler = async (request, response) => {
  if (vite) return vite.middlewares(request, response);
  response.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'");
  try {
    const pathname = decodeURIComponent(new URL(request.url, 'http://127.0.0.1').pathname);
    const target = path.resolve(root, 'dist', `.${pathname === '/' ? '/index.html' : pathname}`);
    if (!target.startsWith(path.join(root, 'dist') + path.sep) || !(await stat(target)).isFile()) throw new Error('not found');
    response.setHeader('Content-Type', contentTypes[path.extname(target)] || 'application/octet-stream');
    response.end(await readFile(target));
  } catch { response.writeHead(404); response.end('Not found. Run npm run build first.'); }
};
const bridge = await createBridge();
server.on('request', createApp({ bridge, staticHandler }));
server.requestTimeout = 125000;
server.headersTimeout = 10000;
server.listen(port, '127.0.0.1', () => console.log(`Feishu Lab: http://127.0.0.1:${port} (local client only; no service autostart)`));
for (const signal of ['SIGINT', 'SIGTERM']) process.once(signal, async () => {
  server.close(); server.closeAllConnections();
  await vite?.close();
});
