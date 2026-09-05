import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import Workbench from '../../src/components/Workbench.vue';
import Policy from '../../src/components/Policy.vue';
import Records from '../../src/components/Records.vue';
import App from '../../src/App.vue';
import Permissions from '../../src/components/Permissions.vue';
import { api, query } from '../../src/api.js';

vi.mock('../../src/api.js', () => ({ api: vi.fn(), query: vi.fn(), pretty: value => JSON.stringify(value, null, 2), timeLabel: value => String(value || '未知'), downloadReport: vi.fn() }));

const capability = { id: 'im.fixture.read', identity: 'bot', risk: 'read', queue: 'direct', inputFields: [{ name: 'page-size', type: 'integer' }] };
const policy = { version: 1, revision: 7, riskDefaults: { read: 'allowed', write: 'allowed' }, capabilityOverrides: {} };
const operation = { id: 'OP-20260906000000-ABCDEF12', summary: '隔离目标', status: 'awaiting_confirmation', challengeExpiresAt: 'fixture expiry' };
const wrappers = [];
const render = (component, props) => { const wrapper = mount(component, { props }); wrappers.push(wrapper); return wrapper; };
const button = (wrapper, text) => wrapper.findAll('button').find(item => item.text() === text);
async function click(wrapper, text) { await button(wrapper, text).trigger('click'); await flushPromises(); }
async function validate(wrapper) { await click(wrapper, '校验参数'); await wrapper.get('.check-line input').setValue(true); }
beforeEach(() => { vi.clearAllMocks(); api.mockResolvedValue({ status: 'dry_run', proof: 'fixture-proof', expiresAt: Date.now() + 120000 }); });
afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); });

describe('capability workbench', () => {
  it('loads a saved case on mount and requires new validation after parameters or policy change', async () => {
    const wrapper = render(Workbench, { capability, policy, seed: { capabilityId: capability.id, input: { 'page-size': 3 } } });
    expect(JSON.parse(wrapper.get('#request-json').element.value)).toEqual({ 'page-size': 3 });
    await click(wrapper, 'JSON');
    expect(JSON.parse(wrapper.get('#request-json').element.value)).toEqual({ 'page-size': 3 });
    await validate(wrapper);
    expect(button(wrapper, '执行能力').attributes('disabled')).toBeUndefined();
    await wrapper.get('#request-json').setValue('{"page-size":4}');
    expect(button(wrapper, '执行能力').attributes('disabled')).toBeDefined();
    await validate(wrapper);
    await wrapper.setProps({ policy: { ...policy, revision: 8 } });
    expect(button(wrapper, '执行能力').attributes('disabled')).toBeDefined();
  });

  it('file selection immediately invalidates proofs and never retains an old file after replacement fails', async () => {
    const wrapper = render(Workbench, { capability: { ...capability, inputFields: [{ name: 'file', type: 'path' }] }, policy });
    const picker = wrapper.get('input[type=file]');
    Object.defineProperty(picker.element, 'files', { configurable: true, value: [{ name: 'first.txt', size: 1, arrayBuffer: async () => new Uint8Array([65]).buffer }] });
    await picker.trigger('change'); await flushPromises();
    await validate(wrapper);
    let finish;
    Object.defineProperty(picker.element, 'files', { configurable: true, value: [{ name: 'second.txt', size: 1, arrayBuffer: () => new Promise(resolve => { finish = resolve; }) }] });
    await picker.trigger('change');
    expect(button(wrapper, '执行能力').attributes('disabled')).toBeDefined();
    expect(button(wrapper, '校验参数').attributes('disabled')).toBeDefined();
    finish(new Uint8Array([66]).buffer); await flushPromises();
    await validate(wrapper);
    Object.defineProperty(picker.element, 'files', { configurable: true, value: [{ name: 'oversize.txt', size: 5 * 1024 * 1024 }] });
    await picker.trigger('change'); await flushPromises();
    expect(button(wrapper, '执行能力').attributes('disabled')).toBeDefined();
    await click(wrapper, '校验参数');
    expect(api.mock.calls.at(-1)[1].files).toEqual({});
  });

  it('late file reads cannot replace newer selections and failed reads stay empty', async () => {
    const wrapper = render(Workbench, { capability: { ...capability, inputFields: [{ name: 'file', type: 'path' }] }, policy });
    const picker = wrapper.get('input[type=file]');
    let finishOld;
    Object.defineProperty(picker.element, 'files', { configurable: true, value: [{ name: 'old.txt', size: 1, arrayBuffer: () => new Promise(resolve => { finishOld = resolve; }) }] });
    await picker.trigger('change');
    Object.defineProperty(picker.element, 'files', { configurable: true, value: [{ name: 'new.txt', size: 1, arrayBuffer: async () => new Uint8Array([66]).buffer }] });
    await picker.trigger('change'); await flushPromises();
    finishOld(new Uint8Array([65]).buffer); await flushPromises();
    await validate(wrapper);
    expect(api.mock.calls.at(-1)[1].files.file.name).toBe('new.txt');
    Object.defineProperty(picker.element, 'files', { configurable: true, value: [{ name: 'failed.txt', size: 1, arrayBuffer: async () => { throw new Error('fixture'); } }] });
    await picker.trigger('change'); await flushPromises();
    expect(wrapper.get('[role="alert"]').text()).toContain('旧附件已清除');
    expect(button(wrapper, '执行能力').attributes('disabled')).toBeDefined();
    await click(wrapper, '校验参数');
    expect(api.mock.calls.at(-1)[1].files).toEqual({});
  });

  it('a lost response isolates the previous success and records the attempted request ID', async () => {
    const wrapper = render(Workbench, { capability, policy });
    await validate(wrapper);
    api.mockResolvedValueOnce({ status: 'ok', operation: { ...operation, status: 'succeeded' } });
    await click(wrapper, '执行能力');
    api.mockResolvedValueOnce({ status: 'dry_run', proof: 'new-proof', expiresAt: Date.now() + 120000 });
    await validate(wrapper);
    api.mockRejectedValueOnce(Object.assign(new Error('结果未知'), { code: 'outcome_unknown' }));
    await click(wrapper, '执行能力');
    expect(wrapper.find('.result-section').text()).not.toContain('succeeded');
    expect(wrapper.emitted('result').at(-1)[0].requestId).toBeTruthy();
    expect(wrapper.emitted('result').at(-1)[0].status).toBe('outcome_unknown');
  });

  it('disabled governance cannot execute even after valid parameters', async () => {
    const wrapper = render(Workbench, { capability, policy: { ...policy, capabilityOverrides: { [capability.id]: 'disabled' } } });
    await validate(wrapper);
    expect(button(wrapper, '执行能力').attributes('disabled')).toBeDefined();
    expect(api.mock.calls.every(([endpoint]) => endpoint === 'validate')).toBe(true);
  });

  it('creates and confirms the original operation only after two deliberate actions', async () => {
    const wrapper = render(Workbench, { capability, policy: { ...policy, capabilityOverrides: { [capability.id]: 'confirm_each' } } });
    await validate(wrapper);
    api.mockResolvedValueOnce({ status: 'ok', operation, challenge: 'original-challenge' });
    await click(wrapper, '创建待确认操作');
    expect(wrapper.text()).toContain(operation.id);
    await click(wrapper, '确认并执行');
    expect(api.mock.calls.filter(([endpoint]) => endpoint === 'operation')).toHaveLength(0);
    api.mockResolvedValueOnce({ status: 'ok', operation: { ...operation, status: 'succeeded' } });
    await click(wrapper, '已核对，继续');
    expect(api).toHaveBeenLastCalledWith('operation', { action: 'confirm', id: operation.id, challenge: 'original-challenge', acknowledge: true });
    expect(wrapper.emitted('result').at(-1)[0].status).toBe('succeeded');
    expect(wrapper.text()).not.toContain('等待本机逐次确认');
  });

  it('shows unknown outcomes without retrying and labels required file output limitations', async () => {
    const wrapper = render(Workbench, { capability, policy });
    await validate(wrapper);
    api.mockRejectedValueOnce(Object.assign(new Error('结果未知，请核对原操作'), { code: 'outcome_unknown' }));
    await click(wrapper, '执行能力');
    expect(wrapper.get('[role="alert"]').text()).toContain('结果未知');
    expect(api.mock.calls.filter(([endpoint]) => endpoint === 'execute')).toHaveLength(1);
    expect(button(wrapper, '执行能力').attributes('disabled')).toBeDefined();
    await wrapper.setProps({ capability: { ...capability, id: 'docs.fixture.output', inputFields: [{ name: 'output', type: 'path', required: true }] } });
    expect(wrapper.text()).toContain('请使用原生 CLI');
    expect(button(wrapper, '校验参数').attributes('disabled')).toBeDefined();
  });
});

describe('policy and operation records', () => {
  it('discarded drafts never appear as saved policy; saving requires explicit confirmation', async () => {
    const wrapper = render(Policy, { policy, catalog: [capability] });
    await click(wrapper, '编辑策略');
    await wrapper.get('[aria-label="read 默认决策"]').setValue('disabled');
    await click(wrapper, '放弃草稿');
    expect(wrapper.text()).not.toContain('禁止');
    expect(api).not.toHaveBeenCalled();
    await click(wrapper, '编辑策略');
    await wrapper.get('[aria-label="read 默认决策"]').setValue('confirm_each');
    await click(wrapper, '确认并保存策略');
    expect(api).not.toHaveBeenCalled();
    api.mockResolvedValueOnce({ status: 'updated', policy: { ...policy, revision: 8, riskDefaults: { ...policy.riskDefaults, read: 'confirm_each' } } });
    await click(wrapper, '已核对，继续');
    expect(api.mock.calls[0][1]).toMatchObject({ expectedRevision: 7, acknowledge: true });
    expect(wrapper.emitted('updated')[0][0].revision).toBe(8);
  });

  it('policy conflicts retain the draft and do not emit a successful update', async () => {
    const wrapper = render(Policy, { policy, catalog: [capability] });
    await click(wrapper, '编辑策略');
    await wrapper.get('[aria-label="read 默认决策"]').setValue('disabled');
    await click(wrapper, '确认并保存策略');
    api.mockRejectedValueOnce(new Error('策略 revision 冲突'));
    await click(wrapper, '已核对，继续');
    expect(wrapper.emitted('updated')).toBeUndefined();
    expect(wrapper.get('[aria-label="read 默认决策"]').element.value).toBe('disabled');
    expect(wrapper.get('[role="alert"]').text()).toContain('revision');
  });

  it('refresh preserves local edits and explicitly rebases only those edits onto the latest policy', async () => {
    const wrapper = render(Policy, { policy, catalog: [capability] });
    await click(wrapper, '编辑策略');
    await wrapper.get('[aria-label="read 默认决策"]').setValue('disabled');
    await wrapper.setProps({ policy: null });
    expect(wrapper.get('[aria-label="read 默认决策"]').element.value).toBe('disabled');
    await wrapper.setProps({ policy: { ...policy, revision: 8, riskDefaults: { read: 'allowed', write: 'confirm_each' } } });
    expect(wrapper.get('[aria-label="read 默认决策"]').element.value).toBe('disabled');
    await click(wrapper, '保留差异，重新核对最新版本');
    expect(wrapper.get('[aria-label="write 默认决策"]').element.value).toBe('confirm_each');
    expect(wrapper.get('[aria-label="read 默认决策"]').element.value).toBe('disabled');
    expect(api).not.toHaveBeenCalled();
    await click(wrapper, '确认并保存策略');
    api.mockResolvedValueOnce({ status: 'updated', policy: { ...policy, revision: 9 } });
    await click(wrapper, '已核对，继续');
    expect(api.mock.calls.at(-1)[1].expectedRevision).toBe(8);
  });

  it('refreshing an operation updates stale pending records without replaying', async () => {
    const history = [{ time: 1, capabilityId: capability.id, operationId: operation.id, status: operation.status, response: { operation, challenge: 'original-challenge' } }];
    const wrapper = render(Records, { history });
    query.mockResolvedValueOnce({ status: 'ok', operation: { ...operation, status: 'expired' } });
    await click(wrapper, '查询');
    expect(query).toHaveBeenCalledWith('operation', { id: operation.id });
    expect(wrapper.emitted('result')[0][0].status).toBe('expired');
    expect(api).not.toHaveBeenCalled();
  });
});

describe('workspace integration', () => {
  it('shows identity scope gaps without treating online identity as full authorization', async () => {
    const wrapper = render(Permissions, {});
    query.mockResolvedValueOnce({ permissions: { identities: { bot: { ready: true }, user: { ready: true, complete: false, missing: ['fixture:missing'], excess: ['fixture:extra'], requiredCount: 3, grantedCount: 2 } } } });
    await click(wrapper, '核验当前授权');
    expect(wrapper.text()).toContain('存在缺项');
    expect(wrapper.text()).toContain('fixture:missing');
    expect(wrapper.text()).toContain('平台未暴露逐项 bot scope 校验');
    await wrapper.get('select').setValue('excess');
    expect(wrapper.text()).toContain('fixture:extra');
  });

  it.each(['succeeded', 'outcome_unknown'])('a confirmation from records synchronizes %s to the original request inspector', async status => {
    query.mockImplementation(async kind => kind === 'catalog' ? { capabilities: [{ ...capability, domain: 'im' }] } : kind === 'policy' ? { policy: { ...policy, capabilityOverrides: { [capability.id]: 'confirm_each' } } } : { processRunning: true, readinessBlockers: ['fixture-degraded'] });
    const wrapper = render(App, {});
    await flushPromises();
    expect(wrapper.text()).toContain('fixture-degraded');
    await validate(wrapper);
    api.mockResolvedValueOnce({ status: 'ok', operation, challenge: 'original-challenge' });
    await click(wrapper, '创建待确认操作');
    await click(wrapper, '运行记录1');
    await click(wrapper, '确认并执行此操作');
    if (status === 'succeeded') api.mockResolvedValueOnce({ status: 'ok', operation: { ...operation, status } });
    else api.mockRejectedValueOnce(Object.assign(new Error('响应丢失'), { code: 'outcome_unknown' }));
    await click(wrapper, '已核对，继续');
    await click(wrapper, '能力实验');
    expect(wrapper.get('.workbench').text()).toContain(status);
    expect(wrapper.get('.workbench').text()).not.toContain('等待本机逐次确认');
    if (status === 'outcome_unknown') {
      query.mockResolvedValueOnce({ status: 'ok', operation: { ...operation, status: 'succeeded' } });
      await click(wrapper, '查询原操作状态');
      expect(query).toHaveBeenLastCalledWith('operation', { id: operation.id });
      expect(wrapper.get('.workbench').text()).toContain('succeeded');
    }
  });
});
