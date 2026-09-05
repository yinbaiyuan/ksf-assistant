let session;

async function sessionToken() {
  session ||= fetch('/api/session', { headers: { 'X-Lab-Client': '1' }, cache: 'no-store' }).then(async response => {
    if (!response.ok) throw new Error('无法建立本机会话，请检查实验台服务。');
    return (await response.json()).token;
  }).catch(error => { session = undefined; throw error; });
  return session;
}

export async function api(endpoint, body) {
  const token = await sessionToken();
  try {
    const response = await fetch(`/api/${endpoint}`, {
      method: 'POST', cache: 'no-store',
      headers: { 'Content-Type': 'application/json', 'X-Lab-Token': token },
      body: JSON.stringify(body),
    });
    const data = await response.json();
    if (!response.ok) {
      if (response.status === 403) session = undefined;
      const error = new Error(data.message || `请求失败 (${response.status})`);
      error.code = data.error || 'request_rejected';
      throw error;
    }
    return data;
  } catch (issue) {
    if (['execute', 'operation', 'policy'].includes(endpoint) && !issue.code) {
      const error = new Error(endpoint === 'policy' ? '未能确认策略保存结果。请先重新读取并核对，不要直接重试。' : '调用响应未能确认，结果未知。请核对原 Operation 或审计，不要重复执行。');
      error.code = 'outcome_unknown'; error.requestId = body.requestId || body.id;
      throw error;
    }
    throw issue;
  }
}

export const query = (kind, options = {}) => api('query', { kind, ...options });
export const pretty = value => JSON.stringify(value, null, 2);
export const timeLabel = value => value ? new Date(value).toLocaleTimeString('zh-CN', { hour12: false }) : '尚未读取';

export function downloadReport(data) {
  const url = URL.createObjectURL(new Blob([pretty(data)], { type: 'application/json' }));
  const anchor = document.createElement('a');
  anchor.href = url; anchor.download = 'feishu-lab-report.json'; anchor.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
