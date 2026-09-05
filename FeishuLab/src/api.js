let session;

async function sessionToken() {
  session ||= fetch('/api/session', { headers: { 'X-Lab-Client': '1' }, cache: 'no-store' }).then(async response => {
    if (!response.ok) throw new Error('无法建立本机会话，请检查实验台服务。');
    return (await response.json()).token;
  }).catch(error => { session = undefined; throw error; });
  return session;
}

export async function api(endpoint, body) {
  const response = await fetch(`/api/${endpoint}`, {
    method: 'POST', cache: 'no-store',
    headers: { 'Content-Type': 'application/json', 'X-Lab-Token': await sessionToken() },
    body: JSON.stringify(body),
  });
  const data = await response.json();
  if (!response.ok) {
    if (response.status === 403) session = undefined;
    const error = new Error(data.message || `请求失败 (${response.status})`);
    error.code = data.error;
    throw error;
  }
  return data;
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
