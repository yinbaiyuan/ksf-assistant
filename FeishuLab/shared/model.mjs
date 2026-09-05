export const decisionLabels = { allowed: '直接放行', disabled: '禁止', confirm_each: '逐次确认', unknown: '尚未读取' };
export const riskLabels = { read: '只读', write: '写入', 'high-impact-write': '高影响写入', 'remote-operation': '远端操作', destructive: '破坏性' };
export const terminalStates = new Set(['succeeded', 'failed', 'expired', 'cancelled', 'outcome_unknown']);

export function decision(capability, policy) {
  if (capability.published === false) return { value: 'disabled', source: '未发布' };
  if (!policy) return { value: 'unknown', source: '请读取当前治理策略' };
  const override = policy.capabilityOverrides?.[capability.id];
  const value = override || policy.riskDefaults?.[capability.risk];
  return { value: ['allowed', 'disabled', 'confirm_each'].includes(value) ? value : 'disabled', source: override ? '能力覆盖' : '风险默认' };
}

export function filterCapabilities(catalog, filters, policy) {
  const query = (filters.query || '').toLowerCase().trim();
  return catalog.filter(item => (!query || `${item.id} ${item.domain} ${riskLabels[item.risk] || ''}`.toLowerCase().includes(query))
    && (!filters.domain || filters.domain === item.domain)
    && (!filters.identity || filters.identity === item.identity)
    && (!filters.risk || filters.risk === item.risk)
    && (!filters.permission || filters.permission === decision(item, policy).value));
}

export function parseFields(fields, values) {
  const input = {};
  for (const field of fields) {
    const value = values[field.name];
    if (value === undefined || value === '') continue;
    if (field.type === 'path') continue;
    if (field.type === 'integer') {
      const number = Number(value);
      if (!Number.isSafeInteger(number)) throw new Error(`${field.name} 必须是安全整数`);
      input[field.name] = number;
    } else if (field.type === 'boolean') input[field.name] = value === true || value === 'true';
    else if (field.type === 'json') input[field.name] = typeof value === 'string' ? JSON.parse(value) : value;
    else input[field.name] = value;
  }
  return input;
}

export function reportRows(history) {
  return history.map(row => ({ time: row.time, capabilityId: row.capabilityId, operationId: row.operationId || null, status: row.status, elapsedMs: row.elapsedMs, note: '不包含输入、正文、目标、结果或审批 challenge' }));
}
