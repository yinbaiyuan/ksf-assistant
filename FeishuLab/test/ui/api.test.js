import { afterEach, beforeEach, expect, it, vi } from 'vitest';

beforeEach(() => vi.resetModules());
afterEach(() => vi.unstubAllGlobals());
const session = () => ({ ok: true, json: async () => ({ token: 'isolated-session' }) });

it('lost mutation responses become outcome unknown rather than ordinary failure', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(session()).mockRejectedValueOnce(new TypeError('network failed')));
  const { api } = await import('../../src/api.js');
  await expect(api('execute', { requestId: 'isolated-request' })).rejects.toMatchObject({ code: 'outcome_unknown', requestId: 'isolated-request' });
  expect(fetch).toHaveBeenCalledTimes(2);
});

it('unreadable mutation responses remain unknown while explicit rejections preserve their code', async () => {
  vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(session()).mockResolvedValueOnce({ ok: true, json: async () => { throw new SyntaxError('partial response'); } }).mockResolvedValueOnce({ ok: false, status: 400, json: async () => ({ error: 'validation_required', message: 'revalidate' }) }));
  const { api } = await import('../../src/api.js');
  await expect(api('operation', { id: 'OP-fixture' })).rejects.toMatchObject({ code: 'outcome_unknown' });
  await expect(api('execute', {})).rejects.toMatchObject({ code: 'validation_required' });
});
