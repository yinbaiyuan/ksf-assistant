import { createServer } from 'node:http';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createApp } from '../server/app.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const bundle = JSON.parse(await readFile(path.join(root, '../Core/internal/feishucli/catalog.json'), 'utf8'));
let policy = { version: 1, revision: 7, riskDefaults: { read: 'allowed', write: 'allowed', 'high-impact-write': 'confirm_each', 'remote-operation': 'confirm_each', destructive: 'disabled' }, capabilityOverrides: {} };
const operations = new Map();
let sequence = 0;
const bridge = { async call(args, input) {
  const command = args.slice(0, 2).join(' ');
  if (command === 'capability catalog') return { capabilities: bundle.catalog };
  if (command === 'policy read') return { policy };
  if (command === 'policy update') { policy = { ...JSON.parse(input), revision: policy.revision + 1 }; return { policy, status: 'updated' }; }
  if (args.includes('--dry-run')) return { status: 'dry_run', submitted: false };
  if (command === 'operation prepare') {
    const capability = bundle.catalog.find(item => item.id === args[2]);
    const permission = policy.capabilityOverrides[capability.id] || policy.riskDefaults[capability.risk];
    const id = `OP-20260906000000-${(++sequence).toString(16).toUpperCase().padStart(8, '0')}`;
    const operation = { id, capabilityId: capability.id, status: permission === 'confirm_each' ? 'awaiting_confirmation' : 'succeeded', summary: '隔离测试操作；没有真实飞书副作用', challengeExpiresAt: new Date(Date.now() + 300000).toISOString(), result: { fixture: true } };
    operations.set(id, operation);
    return { status: 'ok', operation, challenge: permission === 'confirm_each' ? 'fixture-original-challenge' : undefined };
  }
  if (args[0] === 'operation') {
    const operation = operations.get(args[args.indexOf('--id') + 1]);
    if (!operation) return { status: 'rejected', errorCode: 'not_found' };
    if (args[1] === 'confirm') operation.status = 'succeeded';
    if (args[1] === 'cancel') operation.status = 'cancelled';
    return { status: 'ok', operation };
  }
  if (args[0] === 'snapshot') return { processRunning: true, configured: true, availability: 'ready', capabilities: { feishuInbound: { state: 'ready', detail: '隔离夹具，不连接飞书' }, businessIntegration: { state: 'ready' }, outbox: { state: 'ready' }, actionbox: { state: 'ready' } } };
  if (args[0] === 'permissions') return { permissions: { identities: { bot: { ready: true }, user: { ready: true, requiredCount: 3, grantedCount: 2, missing: ['fixture:missing'], excess: ['fixture:extra'], application: { complete: false }, oauth: { complete: false }, complete: false } } } };
  if (command === 'events catalog') return bundle.events;
  if (args[0] === 'capabilities') return bundle.capabilities;
  return { status: 'ok', fixture: true, records: [], health: 'healthy' };
} };
const contentTypes = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css' };
const server = createServer(createApp({ bridge, staticHandler: async (request, response) => {
  try {
    const pathname = new URL(request.url, 'http://127.0.0.1').pathname;
    const filename = path.resolve(root, 'dist', `.${pathname === '/' ? '/index.html' : pathname}`);
    if (!filename.startsWith(path.join(root, 'dist') + path.sep)) throw new Error('invalid path');
    response.setHeader('Content-Type', contentTypes[path.extname(filename)] || 'text/plain');
    response.end(await readFile(filename));
  } catch { response.writeHead(404); response.end(); }
} }));
server.listen(4319, '127.0.0.1', () => console.log('ISOLATED FIXTURE http://127.0.0.1:4319 — no Feishu calls'));
process.once('SIGTERM', () => { server.close(); server.closeAllConnections(); });
