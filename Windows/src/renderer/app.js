'use strict';

const api = window.ksfAssistant;
const root = document.querySelector('#app');
const toast = document.querySelector('#toast');

const state = {
  page: 'home',
  selectedProjectId: null,
  selectedTaskId: null,
  query: '',
  dashboard: null,
  settings: null,
  loading: true,
  refreshing: false,
  error: '',
  history: [],
  historyLoading: false,
  historyError: '',
  historyServerError: '',
  historySummary: null,
  selectedHistoryDate: null,
  pricingCatalog: { defaultPlanId: 'openai:gpt-5.6-sol', plans: [] },
  pricingDraft: null,
  feishuSetup: { stage: 'not_started' },
  feishuSetupPayload: null,
  feishuSetupMode: 'new',
  feishuPermissions: '',
  feishuOverview: null,
  staticDataLoaded: false,
  timer: null,
};

const icons = {
  settings: '<path d="M12 15.5a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7Z"/><path d="M19.4 15a1.7 1.7 0 0 0 .34 1.88l.06.06-2.12 2.12-.06-.06a1.7 1.7 0 0 0-1.88-.34 1.7 1.7 0 0 0-1.03 1.55V20.3h-3v-.09a1.7 1.7 0 0 0-1.03-1.55 1.7 1.7 0 0 0-1.88.34l-.06.06-2.12-2.12.06-.06A1.7 1.7 0 0 0 7.02 15a1.7 1.7 0 0 0-1.55-1.03H5.4v-3h.08A1.7 1.7 0 0 0 7.02 9.94a1.7 1.7 0 0 0-.34-1.88L6.62 8l2.12-2.12.06.06a1.7 1.7 0 0 0 1.88.34 1.7 1.7 0 0 0 1.03-1.55V4.7h3v.08a1.7 1.7 0 0 0 1.03 1.55 1.7 1.7 0 0 0 1.88-.34l.06-.06L19.8 8l-.06.06a1.7 1.7 0 0 0-.34 1.88 1.7 1.7 0 0 0 1.55 1.03h.08v3h-.08A1.7 1.7 0 0 0 19.4 15Z"/>',
  refresh: '<path d="M20 11a8.1 8.1 0 1 0 2.2 5.5"/><path d="M20 4v7h-7"/>',
  chart: '<path d="M4 19V9M10 19V5M16 19v-7M22 19V3"/><path d="M2 19h22"/>',
  back: '<path d="m15 18-6-6 6-6"/>',
  grid: '<rect width="7" height="7" x="3" y="3" rx="1"/><rect width="7" height="7" x="14" y="3" rx="1"/><rect width="7" height="7" x="14" y="14" rx="1"/><rect width="7" height="7" x="3" y="14" rx="1"/>',
  pin: '<path d="M12 17v5"/><path d="M5 17h14l-2-5V5l2-2H5l2 2v7l-2 5Z"/>',
  folder: '<path d="M3 6h6l2 2h10v10a2 2 0 0 1-2 2H3a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2Z"/>',
  add: '<path d="M12 5v14M5 12h14"/>',
  play: '<path d="M7 4.8v14.4L19 12 7 4.8Z"/>',
  archive: '<path d="M4 7h16"/><path d="M5 7v13h14V7"/><path d="M3 3h18v4H3z"/><path d="M9 11h6"/>',
  send: '<path d="m22 2-7 20-4-9-9-4Z"/><path d="M22 2 11 13"/>',
  open: '<path d="M15 3h6v6"/><path d="m10 14 11-11"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>',
  info: '<circle cx="12" cy="12" r="9"/><path d="M12 11v5M12 8h.01"/>',
  stop: '<rect width="14" height="14" x="5" y="5" rx="2"/>',
  close: '<path d="m6 6 12 12M18 6 6 18"/>',
};

function icon(name, className = '') {
  return `<svg class="${className}" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${icons[name]}</svg>`;
}

function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>'"]/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' })[character]);
}

function buttonIcon(action, name, label, extra = '') {
  return `<button class="icon-button ${extra}" type="button" data-action="${action}" title="${escapeHTML(label)}" aria-label="${escapeHTML(label)}">${icon(name)}</button>`;
}

function header(title) {
  const secondary = state.page !== 'home';
  return `<header class="app-header">
    ${secondary ? buttonIcon('back', 'back', '返回') : ''}
    <h1 class="app-title">${escapeHTML(title)}</h1>
    <div class="header-actions">
      ${state.page === 'home' ? buttonIcon('projects', 'grid', '项目列表') : ''}
      ${state.page === 'home' ? buttonIcon('settings', 'settings', '设置') : ''}
      ${buttonIcon('refresh', 'refresh', '刷新', state.refreshing ? 'spin' : '')}
    </div>
  </header>`;
}

function render() {
  if (state.loading && !state.dashboard) {
    root.innerHTML = `${header('Codex 用量')}<section class="card skeleton" aria-label="正在读取额度"></section><div class="section-header"><h2 class="section-title">Token 活动</h2></div><section class="card skeleton"></section>`;
    reportHeight();
    return;
  }
  if (state.page === 'projects') root.innerHTML = renderProjectsPage();
  else if (state.page === 'settings') root.innerHTML = renderSettingsPage();
  else if (state.page === 'project') root.innerHTML = renderProjectPage();
  else if (state.page === 'task') root.innerHTML = renderTaskPage();
  else if (state.page === 'history') root.innerHTML = renderHistoryPage();
  else if (state.page === 'pricing') root.innerHTML = renderPricingPage();
  else if (state.page === 'feishu') root.innerHTML = renderFeishuPage();
  else root.innerHTML = renderHome();
  root.setAttribute('aria-busy', state.refreshing ? 'true' : 'false');
  reportHeight();
}

function renderHome() {
  const dashboard = state.dashboard;
  if (!dashboard) return `${header('Codex 用量')}<section class="card callout error"><strong>核心服务暂不可用</strong>${escapeHTML(state.error)}</section>`;
  const bucket = dashboard.usage.buckets.find((item) => item.limitId === 'codex') || dashboard.usage.buckets[0];
  const remaining = headlineRemaining(bucket);
  const window = shortestWindow(bucket);
  const projects = selectHomeProjects(dashboard.projects.projects);
  return `${header('Codex 用量')}
    ${renderQuota(bucket, remaining, window)}
    <div class="section-header"><h2 class="section-title">Token 活动</h2><span class="section-action">${buttonIcon('history', 'chart', '查看每日 Token 历史')}</span></div>
    ${renderTokens(dashboard.usage)}
    ${state.settings?.ksfRoot ? `<div class="section-header"><h2 class="section-title">KSF 项目</h2><span class="section-meta">${projects.length}</span><span class="section-action">${buttonIcon('projects', 'grid', '查看全部项目')}</span></div>${renderProjectSection(projects, dashboard.projects)}` : ''}
    ${state.error ? `<p class="support-copy">${escapeHTML(state.error)}</p>` : ''}`;
}

function renderQuota(bucket, remaining, window) {
  if (!bucket || remaining == null) {
    return `<section class="card callout error"><strong>额度信息暂不可用</strong>${escapeHTML(state.dashboard?.usage?.rateError || '请确认 Codex 已登录并支持账户额度接口。')}</section>`;
  }
  return `<section class="card quota-card">
    <div class="quota-head"><span class="quota-value">${remaining}%</span><span class="quota-unit">剩余</span><span class="plan-badge">${escapeHTML((bucket.planType || 'Codex').toUpperCase())}</span></div>
    <div class="quota-updated">${relativeTime(state.dashboard.usage.rateUpdatedAt)}</div>
    <progress class="progress" value="${remaining}" max="100" aria-label="通用额度剩余 ${remaining}%"></progress>
    <div class="quota-meta"><span>${window?.windowDurationMins ? durationWindow(window.windowDurationMins) : '额度窗口'}</span><strong>${window?.resetsAt ? `${remaining}% · ${formatReset(window.resetsAt)}` : `${remaining}%`}</strong></div>
  </section>`;
}

function renderTokens(usage) {
  const latest = usage.dailyUsageBuckets?.at(-1);
  const local = usage.localDailyUsage;
  const localBreakdown = local?.breakdown;
  const metrics = [
    ['模型普通输入', localBreakdown ? formatTokens(localBreakdown.regularInputTokens) : '—'],
    ['模型缓存输入', localBreakdown ? formatTokens(localBreakdown.cachedInputTokens) : '—'],
    ['模型输出', localBreakdown ? formatTokens(localBreakdown.outputTokens) : '—'],
    [accountLatestLabel(latest?.startDate), latest ? formatTokens(latest.tokens) : '未同步'],
    ['本机昨日', usage.localPreviousDailyUsage ? formatTokens(usage.localPreviousDailyUsage.tokens) : '—'],
    ['本机今日', local ? formatTokens(local.tokens) : '—'],
  ];
  const plan = selectedPricingPlan();
  return `<section class="card token-card">${metrics.map(([label, value]) => `<div class="metric"><div class="metric-label">${label}</div><div class="metric-value">${value}</div></div>`).join('')}<div class="token-cost-row"><span><small>API 估算</small><strong>${escapeHTML(compactPricingName(plan))}</strong></span><b>今日 ${formatCostEstimate(usage.localDailyCost)}</b></div></section>
    <p class="support-copy">账号数据可能延迟；本机统计覆盖此电脑的 Codex 会话。</p>`;
}

function renderHistoryPage() {
  if (state.historyLoading && !state.history.length) {
    return `${header('每日 Token')}<section class="card skeleton" aria-label="正在读取每日 Token 历史"></section>`;
  }
  if (!state.history.length) {
    return `${header('每日 Token')}<section class="card empty"><strong>没有每日 Token 历史</strong>${escapeHTML(state.historyError || '本机 Codex 会话中尚未发现可统计的 Token 记录。')}</section>`;
  }
  const total = state.history.reduce((sum, day) => sum + Math.max(0, day.localTokens), 0);
  const activeDays = state.history.filter((day) => day.localTokens > 0).length;
  const average = Math.round(total / state.history.length);
  const maximum = Math.max(1, ...state.history.flatMap((day) => [day.localTokens, day.serverTokens ?? 0]));
  const selected = state.history.find((day) => day.startDate === state.selectedHistoryDate) || state.history.at(-1);
  const breakdown = selected?.localBreakdown;
  const cost = selected?.localCost;
  const share = selected?.serverTokens > 0 ? selected.localTokens / selected.serverTokens : null;
  const bars = state.history.map((day) => {
    const selectedClass = day.startDate === selected.startDate ? ' selected' : '';
    const serverHeight = day.serverTokens == null ? 0 : Math.max(2, day.serverTokens / maximum * 100);
    const localHeight = Math.max(2, day.localTokens / maximum * 100);
    const serverLabel = day.serverTokens == null ? '未同步' : formatTokens(day.serverTokens);
    const shareLabel = day.serverTokens > 0 ? formatShare(day.localTokens / day.serverTokens) : '—';
    const serverBar = day.serverTokens == null ? '' : `<span class="history-server" style="height:${serverHeight}%"></span>`;
    return `<button class="history-bar${selectedClass}" type="button" data-action="history-day" data-date="${escapeHTML(day.startDate)}" title="${escapeHTML(historyDateLabel(day.startDate))} · 服务器 ${escapeHTML(serverLabel)} · 本机 ${escapeHTML(formatTokens(day.localTokens))}" aria-label="${escapeHTML(historyDateLabel(day.startDate))}，服务器 ${escapeHTML(serverLabel)}，本机 ${escapeHTML(formatTokens(day.localTokens))}，本机占比 ${escapeHTML(shareLabel)}">${serverBar}<span class="history-local" style="height:${localHeight}%"></span></button>`;
  }).join('');
  return `${header('每日 Token')}
    <div class="pricing-toolbar"><select data-field="pricing-plan" aria-label="API 价格方案">${renderPricingOptions()}</select></div>
    <div class="history-scope"><span>最近 30 个自然日</span><span class="history-legend server">服务器</span><span class="history-legend local">本机</span></div>
    <section class="card history-card">
      <div class="history-summary"><div class="metric"><div class="metric-label">本机 30 日</div><div class="metric-value">${formatTokens(total)}</div></div><div class="metric"><div class="metric-label">本机日均</div><div class="metric-value">${formatTokens(average)}</div></div><div class="metric"><div class="metric-label">本机活跃</div><div class="metric-value">${activeDays} 天</div></div></div>
      <div class="cost-summary"><span>30 日 API 估算</span><strong>${formatCostEstimate(state.historySummary)}</strong>${state.historySummary?.incompleteDayCount ? `<small>缺 ${state.historySummary.incompleteDayCount} 天</small>` : ''}</div>
      <div class="history-current"><span>${escapeHTML(historyDateLabel(selected.startDate))}</span><strong>本机 ${formatTokens(selected.localTokens)} · ${formatCostEstimate(cost)}</strong></div>
      <div class="history-bars" role="list" aria-label="最近 30 天服务器与本机 Token 趋势">${bars}</div>
      <div class="history-axis"><span>${escapeHTML(shortDate(state.history[0].startDate))}</span><span>${escapeHTML(shortDate(state.history[Math.floor(state.history.length / 2)].startDate))}</span><span>${escapeHTML(shortDate(state.history.at(-1).startDate))}</span></div>
      <div class="history-comparison"><div class="metric"><div class="metric-label with-key server">服务器当日</div><div class="metric-value">${selected.serverTokens == null ? '未同步' : formatTokens(selected.serverTokens)}</div></div><div class="metric"><div class="metric-label with-key local">本机当日</div><div class="metric-value">${formatTokens(selected.localTokens)}</div></div><div class="metric"><div class="metric-label">本机占比</div><div class="metric-value">${formatShare(share)}</div></div></div>
      <div class="history-breakdown"><div class="metric"><div class="metric-label">普通输入</div><div class="metric-value">${breakdown ? formatTokens(breakdown.regularInputTokens) : '—'}</div><div class="metric-cost">${cost ? formatMicroUSD(cost.regularInputMicroUsd) : '—'}</div></div><div class="metric"><div class="metric-label">缓存输入</div><div class="metric-value">${breakdown ? formatTokens(breakdown.cachedInputTokens) : '—'}</div><div class="metric-cost">${cost ? formatMicroUSD(cost.cachedInputMicroUsd) : '—'}</div></div><div class="metric"><div class="metric-label">输出</div><div class="metric-value">${breakdown ? formatTokens(breakdown.outputTokens) : '—'}</div><div class="metric-cost">${cost ? formatMicroUSD(cost.outputMicroUsd) : '—'}</div></div></div>
      <p class="history-note">按当前所选 API 价格估算，历史金额会随方案或价格变化，不代表实际账单。</p>
    </section>
    ${state.historyError || state.historyServerError ? `<p class="support-copy">${escapeHTML(state.historyError || state.historyServerError)}</p>` : ''}`;
}

function renderPricingPage() {
  const plans = state.pricingCatalog?.plans || [];
  const builtIns = plans.filter((plan) => plan.builtIn);
  const custom = plans.filter((plan) => !plan.builtIn);
  const draft = state.pricingDraft;
  return `${header('API 估算价格')}
    <section class="card setting pricing-current"><div class="setting-head"><div class="setting-copy"><label class="setting-title" for="pricing-current">当前方案</label><div class="setting-description">统一套用于本机 Token 历史。</div></div><select id="pricing-current" data-field="pricing-plan">${renderPricingOptions()}</select></div></section>
    <div class="section-header"><h2 class="section-title">内置方案</h2><span class="section-meta">美元 / 百万 Token</span></div>
    <section class="card pricing-list">${builtIns.map((plan) => renderPricingRow(plan, false)).join('')}</section>
    <div class="section-header"><h2 class="section-title">自定义方案</h2><span class="section-meta">${custom.length}/20</span><span class="section-action"><button class="button" type="button" data-action="pricing-add" ${custom.length >= 20 ? 'disabled' : ''}>添加</button></span></div>
    ${custom.length ? `<section class="card pricing-list">${custom.map((plan) => renderPricingRow(plan, true)).join('')}</section>` : '<p class="support-copy">自定义方案只保存在当前设备。</p>'}
    ${draft ? renderPricingEditor(draft) : ''}
    <p class="pricing-sources"><button type="button" data-action="pricing-source" data-url="https://developers.openai.com/api/docs/models/compare">OpenAI 官方价格</button><button type="button" data-action="pricing-source" data-url="https://api-docs.deepseek.com/quick_start/pricing/">DeepSeek 官方价格</button></p>
    <p class="history-note">按当前所选 API 价格估算，历史金额会随方案或价格变化，不代表实际账单。</p>`;
}

function renderPricingRow(plan, editable) {
  return `<div class="pricing-row"><div><strong>${escapeHTML(plan.displayName)}</strong><small>入 ${formatRate(plan.regularInputMicroUsdPerMillion)} · 缓 ${formatRate(plan.cachedInputMicroUsdPerMillion)} · 出 ${formatRate(plan.outputMicroUsdPerMillion)}</small></div>${editable ? `<span><button class="button" type="button" data-action="pricing-edit" data-id="${escapeHTML(plan.id)}">编辑</button><button class="button danger" type="button" data-action="pricing-delete" data-id="${escapeHTML(plan.id)}">删除</button></span>` : ''}</div>`;
}

function renderPricingEditor(draft) {
  return `<form class="card pricing-editor" data-form="pricing"><strong>${draft.id ? '编辑价格方案' : '新建价格方案'}</strong><div class="pricing-fields"><label>厂商<input data-draft="provider" value="${escapeHTML(draft.provider)}" maxlength="60" required></label><label>模型<input data-draft="model" value="${escapeHTML(draft.model)}" maxlength="60" required></label><label>方案名称<input data-draft="variant" value="${escapeHTML(draft.variant)}" maxlength="40" placeholder="可选"></label></div><div class="pricing-fields rates"><label>普通输入<input data-draft="regular" value="${escapeHTML(draft.regular)}" inputmode="decimal" required></label><label>缓存输入<input data-draft="cached" value="${escapeHTML(draft.cached)}" inputmode="decimal" required></label><label>输出<input data-draft="output" value="${escapeHTML(draft.output)}" inputmode="decimal" required></label></div><div class="detail-actions"><button class="button" type="button" data-action="pricing-cancel">取消</button><button class="button primary" type="submit">保存</button></div></form>`;
}

function formatShare(value) {
  return Number.isFinite(value) ? `${(value * 100).toFixed(1)}%` : '—';
}

function formatMicroUSD(value) {
  if (!Number.isSafeInteger(value) || value < 0) return '—';
  const amount = value / 1_000_000;
  if (amount === 0) return '$0.00';
  if (amount < 0.01) return `$${amount.toFixed(6)}`;
  if (amount < 1) return `$${amount.toFixed(4)}`;
  return `$${amount.toFixed(2)}`;
}

function formatCostEstimate(estimate) {
  return !estimate || estimate.status === 'unavailable' ? '—' : formatMicroUSD(estimate.totalMicroUsd);
}

function formatRate(value) {
  if (!Number.isSafeInteger(value)) return '—';
  return (value / 1_000_000).toFixed(6).replace(/\.?0+$/, '');
}

function selectedPricingPlan() {
  return state.pricingCatalog?.plans?.find((plan) => plan.id === state.settings?.selectedPricingPlanId)
    || state.pricingCatalog?.plans?.find((plan) => plan.id === state.pricingCatalog.defaultPlanId);
}

function compactPricingName(plan) {
  return plan ? [plan.model, plan.variant].filter(Boolean).join(' · ') : 'GPT-5.6 Sol';
}

function renderPricingOptions() {
  return (state.pricingCatalog?.plans || []).map((plan) => `<option value="${escapeHTML(plan.id)}" ${plan.id === state.settings?.selectedPricingPlanId ? 'selected' : ''}>${escapeHTML(plan.displayName)}</option>`).join('');
}

function pricingDraft(plan = null) {
  return {
    id: plan?.id || null,
    provider: plan?.provider || '',
    model: plan?.model || '',
    variant: plan?.variant || '',
    regular: plan ? formatRate(plan.regularInputMicroUsdPerMillion) : '',
    cached: plan ? formatRate(plan.cachedInputMicroUsdPerMillion) : '',
    output: plan ? formatRate(plan.outputMicroUsdPerMillion) : '',
  };
}

function parseRate(value) {
  const normalized = String(value ?? '').trim();
  if (!/^\d{1,4}(?:\.\d{1,6})?$/.test(normalized)) return null;
  const number = Number(normalized);
  if (!Number.isFinite(number) || number < 0 || number > 1000) return null;
  return Math.round(number * 1_000_000);
}

function applyHistoryComparison(comparison, resetSelection = false) {
  state.history = comparison.days || [];
  state.historySummary = comparison.localCostSummary || null;
  state.historyServerError = comparison.serverError || '';
  if (comparison.selectedPlan?.id && comparison.pricingFallback) state.settings.selectedPricingPlanId = comparison.selectedPlan.id;
  if (resetSelection || !state.history.some((day) => day.startDate === state.selectedHistoryDate)) {
    state.selectedHistoryDate = state.history.at(-1)?.startDate || null;
  }
}

function accountLatestLabel(dateString) {
  if (!dateString) return '账号最新';
  const today = new Date();
  const localDate = (offset) => {
    const value = new Date(today.getFullYear(), today.getMonth(), today.getDate() + offset);
    return `${value.getFullYear()}-${String(value.getMonth() + 1).padStart(2, '0')}-${String(value.getDate()).padStart(2, '0')}`;
  };
  if (dateString === localDate(0)) return '账号最新 · 今日';
  if (dateString === localDate(-1)) return '账号最新 · 昨日';
  const parsed = new Date(`${dateString}T00:00:00`);
  return Number.isNaN(parsed.getTime()) ? '账号最新' : `账号最新 · ${parsed.getMonth() + 1}月${parsed.getDate()}日`;
}

function historyDateLabel(value) {
  const parsed = new Date(`${value}T00:00:00`);
  if (Number.isNaN(parsed.getTime())) return value;
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: 'long', day: 'numeric' }).format(parsed);
}

function shortDate(value) {
  const parsed = new Date(`${value}T00:00:00`);
  return Number.isNaN(parsed.getTime()) ? value : `${parsed.getMonth() + 1}/${parsed.getDate()}`;
}

function renderProjectSection(projects, snapshot) {
  if (snapshot.availability !== 'available') {
    return `<section class="card callout"><strong>项目暂不可用</strong>${escapeHTML(snapshot.message || '请在设置中选择 KSF 根目录。')}</section>`;
  }
  if (!projects.length) {
    return '<section class="card empty"><strong>没有固定或活跃项目</strong>从项目列表固定常用项目；运行中的任务也会自动出现在这里。</section>';
  }
  return `<section class="project-stack">${projects.map(renderProjectCard).join('')}</section>`;
}

function renderProjectCard(item) {
  const project = item.project;
  const title = item.kind === 'unassigned' ? '无项目' : project?.name || '项目不可用';
  const activeCount = item.tasks.filter((task) => task.classification === 'running' || task.classification === 'waiting').length;
  const usage = item.usage;
  return `<article class="card project-card" data-project-id="${escapeHTML(item.id)}">
    <div class="project-card-head">
      ${item.kind === 'project' ? `<button class="project-name icon-button-text" type="button" data-action="project-detail" data-id="${escapeHTML(item.id)}">${escapeHTML(title)}</button>` : `<span class="project-name">${escapeHTML(title)}</span>`}
      ${activeCount ? `<span class="activity-count" title="活跃任务">${playIcon()} ${activeCount}</span>` : ''}
      ${item.kind === 'project' ? `<button class="icon-button" type="button" data-action="pin" data-id="${escapeHTML(item.id)}" data-pinned="${item.isPinned}" title="${item.isPinned ? '取消固定' : '固定项目'}" aria-label="${item.isPinned ? '取消固定' : '固定项目'}">${icon('pin')}</button>` : ''}
    </div>
    <div class="task-list">${item.tasks.length ? item.tasks.map((task) => renderTask(task, project)).join('') : '<div class="empty">暂无任务</div>'}</div>
    ${item.kind === 'project' ? `<footer class="project-footer">
      <div class="usage-pair"><span>累计 <strong>${formatTokens(usage?.cumulativeTokens)}</strong></span><span>今日 <strong>${formatTokens(usage?.todayTokens)}</strong></span></div>
      <div class="project-actions">
        ${item.launchAction ? buttonIcon(`launch-project:${item.id}`, 'play', item.launchAction.title || '启动项目') : ''}
        ${buttonIcon(`create-task:${item.id}`, 'add', '新建任务')}
        ${buttonIcon(`open-folder:${item.id}`, 'folder', '打开项目目录')}
        ${buttonIcon(`archive-task:${item.id}`, 'archive', '归档项目')}
      </div>
    </footer>` : ''}
  </article>`;
}

function renderTask(task, project) {
  const link = state.dashboard.feishu.links.find((item) => item.taskKey === task.taskKey);
  const linkActive = link?.linkState === 'active';
  const detail = taskDetail(task, link);
  return `<div class="task-row">
    ${taskStateIcon(task.classification)}
    <div class="task-copy"><span class="task-name">${escapeHTML(task.name || '未命名任务')}</span><span class="task-detail">${escapeHTML(detail)}</span></div>
    <div class="task-actions">
      <button class="icon-button" type="button" data-action="task-detail" data-task="${escapeHTML(task.id)}" title="任务详情" aria-label="任务详情">${icon('info')}</button>
      ${link?.controls?.canInterrupt ? `<button class="icon-button" type="button" data-action="interrupt-link" data-thread="${escapeHTML(task.threadId)}" title="停止本轮" aria-label="停止本轮">${icon('stop')}</button>` : ''}
      <button class="icon-button" type="button" data-action="toggle-link" data-thread="${escapeHTML(task.threadId)}" data-title="${escapeHTML(task.name || '未命名任务')}" data-project="${escapeHTML(project?.name || '')}" data-linked="${linkActive}" title="${linkActive ? '解除飞书连接' : '连接到飞书'}" aria-label="${linkActive ? '解除飞书连接' : '连接到飞书'}" ${!linkActive && !canCreateTaskLink() ? 'disabled' : ''}>${icon(linkActive ? 'close' : 'send')}</button>
      <button class="icon-button" type="button" data-action="open-task" data-thread="${escapeHTML(task.threadId)}" title="在 Codex 中打开" aria-label="在 Codex 中打开">${icon('open')}</button>
    </div>
  </div>`;
}

function renderTaskPage() {
  const resolved = findTask(state.selectedTaskId);
  if (!resolved) return `${header('任务详情')}<section class="card empty"><strong>任务已不可用</strong>它可能已经被归档或暂未同步。</section>`;
  const { task, project } = resolved;
  const link = state.dashboard.feishu.links.find((item) => item.taskKey === task.taskKey);
  const linkActive = link?.linkState === 'active';
  const route = task.route;
  const routeRows = [];
  if (route?.category?.name) routeRows.push(['工作类别', route.category.name, route.category.validation_status]);
  for (const job of route?.jobs || []) routeRows.push([job.role === 'main' ? '主岗位' : '协同岗位', job.name || '未命名岗位', job.validation_status]);
  for (const ability of route?.abilities || []) routeRows.push(['基本功', ability.name || '未命名基本功', ability.validation_status]);
  if (route?.dispatchableSkills?.length) routeRows.push(['Skill', `${route.dispatchableSkills.length} 项可调度`, '']);
  return `${header(task.name || '未命名任务')}
    <section class="card detail-hero">
      <div class="detail-status"><span class="status-chip ${escapeHTML(task.classification)}">${escapeHTML(taskClassificationText(task))}</span><span>${escapeHTML(project?.name || '无项目')}</span></div>
      <p class="detail-summary">${escapeHTML(taskDetail(task, link))}</p>
      <div class="detail-actions"><button class="button primary" type="button" data-action="open-task" data-thread="${escapeHTML(task.threadId)}">打开 Codex</button><button class="button" type="button" data-action="toggle-link" data-thread="${escapeHTML(task.threadId)}" data-title="${escapeHTML(task.name || '未命名任务')}" data-project="${escapeHTML(project?.name || '')}" data-linked="${linkActive}" ${!linkActive && !canCreateTaskLink() ? 'disabled' : ''}>${linkActive ? '解除飞书' : '连接飞书'}</button>${link?.controls?.canInterrupt ? `<button class="button danger" type="button" data-action="interrupt-link" data-thread="${escapeHTML(task.threadId)}">停止本轮</button>` : ''}</div>
    </section>
    <div class="section-header"><h2 class="section-title">KSF 路由</h2></div>
    <section class="card route-list">${routeRows.length ? routeRows.map(([label, value, validation]) => `<div class="route-row"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value)}</strong>${validation ? `<small>${escapeHTML(validation)}</small>` : ''}</div>`).join('') : '<div class="empty"><strong>尚无已验证路由</strong>任务完成 KSF 路由后会在这里显示。</div>'}</section>`;
}

function renderProjectsPage() {
  const snapshot = state.dashboard?.projects;
  const items = snapshot?.projects || [];
  const dashboardById = new Map(items.map((item) => [item.id, item]));
  const catalog = snapshot?.catalog || [];
  const normalized = state.query.trim().toLocaleLowerCase();
  const projects = catalog.filter((project) => !normalized || `${project.name} ${project.summary}`.toLocaleLowerCase().includes(normalized));
  return `${header('项目列表')}
    <div class="page-toolbar"><input class="search" type="search" data-field="project-search" value="${escapeHTML(state.query)}" placeholder="搜索项目" aria-label="搜索项目"></div>
    <section class="card settings-list">${projects.length ? projects.map((project) => {
      const item = dashboardById.get(project.id);
      const active = item?.tasks?.filter((task) => task.classification === 'running' || task.classification === 'waiting').length || 0;
      return `<div class="library-row"><button class="project-name icon-button-text" type="button" data-action="project-detail" data-id="${escapeHTML(project.id)}"><span class="library-name">${escapeHTML(project.name)}</span><span class="library-summary">${escapeHTML(project.summary || project.focus || '暂无摘要')}</span></button><div class="task-actions">${active ? `<span class="status-chip running">${active} 活跃</span>` : ''}<button class="icon-button" type="button" data-action="pin" data-id="${escapeHTML(project.id)}" data-pinned="${Boolean(item?.isPinned)}" title="${item?.isPinned ? '取消固定' : '固定项目'}">${icon('pin')}</button></div></div>`;
    }).join('') : '<div class="empty"><strong>没有匹配项目</strong>调整搜索词后重试。</div>'}</section>`;
}

function renderProjectPage() {
  const snapshot = state.dashboard?.projects;
  const project = snapshot?.catalog?.find((item) => item.id === state.selectedProjectId);
  const item = snapshot?.projects?.find((value) => value.id === state.selectedProjectId) || { id: project?.id, kind: 'project', project, tasks: [], isPinned: state.settings?.pinnedProjectIds?.includes(project?.id), usage: null };
  if (!project) return `${header('项目详情')}<section class="card empty"><strong>项目不存在</strong>它可能已被归档或目录尚未同步。</section>`;
  return `${header(project.name)}
    <section class="card detail-hero"><h2 class="detail-name">${escapeHTML(project.name)}</h2><p class="detail-summary">${escapeHTML(project.summary || '暂无项目摘要')}</p><div class="detail-actions"><button class="button primary" type="button" data-action="create-task:${project.id}">新建任务</button><button class="button" type="button" data-action="open-folder:${project.id}">打开目录</button><button class="button" type="button" data-action="archive-task:${project.id}">归档项目</button></div></section>
    <div class="section-header"><h2 class="section-title">任务</h2><span class="section-meta">${item.tasks.length}</span></div>
    ${renderProjectCard(item)}`;
}

function renderSettingsPage() {
  const settings = state.settings || {};
  const feishu = state.dashboard?.feishu || { availability: 'notConfigured', targetAliases: [] };
  return `${header('设置')}
    <section class="card settings-list">
      <div class="setting"><div class="setting-title">运行组件</div><div class="component-status-list"><div class="component-status"><span>核心服务</span><strong>${state.dashboard?.coreVersion ? '运行中' : '不可用'}</strong></div><div class="component-status"><span>飞书服务</span><strong>${escapeHTML(feishuComponentStatusText(feishu))}</strong></div><div class="component-status"><span>本机事件</span><strong>${escapeHTML(feishuProfileText(feishu.profile))}</strong></div></div><div class="setting-description">退出 KSFAssistant 将停止核心服务、飞书服务及其子进程。</div></div>
      <div class="setting"><div class="setting-head"><div class="setting-copy"><div class="setting-title">KSF 知识库</div><div class="setting-description">KSF 是可选增强能力；未配置不影响额度、Token、任务状态和飞书。</div></div><button class="button" type="button" data-action="choose-ksf">${settings.ksfRoot ? '更换' : '选择目录'}</button></div><div class="setting-path">${escapeHTML(settings.ksfRoot || '未接入')}</div></div>
      <div class="setting"><div class="setting-head"><div class="setting-copy"><div class="setting-title">飞书</div><div class="setting-description">${escapeHTML(feishuSetupStageText(state.feishuSetup))} · ${escapeHTML(feishuStatusText(feishu))}</div></div><button class="button" type="button" data-action="feishu-settings">配置</button></div></div>
      <div class="setting"><div class="setting-head"><div class="setting-copy"><div class="setting-title">API 估算价格</div><div class="setting-description">${escapeHTML(selectedPricingPlan()?.displayName || 'GPT-5.6 Sol')}</div></div><button class="button" type="button" data-action="pricing">管理价格方案</button></div></div>
      <div class="setting"><div class="setting-head"><div class="setting-copy"><label class="setting-title" for="launch-login">登录时启动</label><div class="setting-description">登录 Windows 后在系统托盘中启动。</div></div><input id="launch-login" class="switch" type="checkbox" data-field="launch-login" ${settings.launchAtLogin ? 'checked' : ''}></div></div>
      <div class="setting"><div class="setting-head"><div class="setting-copy"><div class="setting-title">版本</div><div class="setting-description">Windows 0.10.0-preview.1 · 核心服务 ${escapeHTML(state.dashboard?.coreVersion || '—')}</div></div></div></div>
    </section>
    <div class="detail-actions"><button class="button danger" type="button" data-action="quit">退出 KSFAssistant</button></div>`;
}

function renderFeishuPage() {
  const feishu = state.dashboard?.feishu || { availability: 'notConfigured', targetAliases: [] };
  const setup = state.feishuSetup || { stage: 'not_started' };
  const payload = state.feishuSetupPayload || {};
  const verificationURL = payload.verificationUrl || setup.verificationURL || '';
  const qrDataURL = payload.qrDataURL || '';
  const isFaulted = feishu.processState === 'degraded';
  const setupError = setup.lastError ? `<section class="card callout error"><strong>此步骤未完成</strong>${escapeHTML(setup.lastError)}</section>` : '';
  let step = '';
  if (setup.stage === 'not_started') {
    step = `<section class="card settings-list"><div class="setting"><div class="setting-title">选择接入方式</div><div class="setting-description">整个过程都在 KSFAssistant 内发起；需要管理员确认时会直接打开飞书官方页面。</div><div class="detail-actions"><button class="button primary" type="button" data-action="feishu-begin-new">创建专用飞书应用</button><button class="button" type="button" data-action="feishu-show-existing">接入已有应用</button></div>${state.feishuSetupMode === 'existing' ? `<div class="feishu-credentials"><input type="text" data-field="feishu-app-id" autocomplete="off" placeholder="App ID" aria-label="飞书 App ID"><input type="password" data-field="feishu-app-secret" autocomplete="new-password" placeholder="App Secret" aria-label="飞书 App Secret"></div><button class="button primary" type="button" data-action="feishu-begin-existing">安全保存并继续</button>` : ''}</div></section>`;
  } else if (setup.stage === 'app_pending') {
    step = renderFeishuQRStep('在飞书中创建应用', '完成飞书官方页面中的确认后返回。', qrDataURL, verificationURL, setup.userCode, 'feishu-continue', '我已完成，继续');
  } else if (setup.stage === 'app_configured') {
    step = `<section class="card setting"><div class="setting-title">应用凭据已安全保存</div><div class="setting-description">下一步将申请固定能力注册表中的精确权限并发起 OAuth。</div><button class="button primary" type="button" data-action="feishu-continue">开始授权</button></section>`;
  } else if (setup.stage === 'authorization_pending') {
    step = renderFeishuQRStep('完成用户授权', '在飞书中确认授权，然后返回继续。', qrDataURL, verificationURL, setup.userCode, 'feishu-continue', '我已授权，继续');
  } else if (setup.stage === 'platform_pending' || setup.stage === 'failed') {
    step = setup.readyToActivate
      ? renderFeishuActivation(feishu)
      : `<section class="card settings-list"><div class="setting"><div class="setting-title">检查飞书后台设置</div><div class="setting-description">请在已打开的飞书页面确认机器人、精确权限、23 类事件、长连接和应用版本发布。KSFAssistant 会自动核验结果。</div>${verificationURL ? `<button class="button" type="button" data-action="feishu-open-url" data-url="${escapeHTML(verificationURL)}">在飞书中继续</button>` : ''}<div class="detail-actions"><button class="button primary" type="button" data-action="feishu-verify">重新检查</button><button class="button" type="button" data-action="feishu-cancel">重新配置</button></div></div></section>`;
  } else if (setup.stage === 'verifying') {
    step = '<section class="card skeleton" aria-label="正在核验飞书配置"></section>';
  } else {
    step = renderFeishuReady(feishu);
  }
  const componentSummary = setup.stage !== 'ready' || isFaulted
    ? `<section class="card setting"><div class="setting-head"><div class="setting-copy"><div class="setting-title">${escapeHTML(feishuSetupStageText(setup))}</div><div class="setting-description">桥进程 ${escapeHTML(feishuComponentStatusText(feishu))} · ${escapeHTML(feishuProfileText(feishu.profile))}</div></div>${isFaulted ? '<button class="button" type="button" data-action="feishu-restart">重新启动</button>' : ''}</div></section>`
    : '';
  return `${header('飞书配置')}
    ${componentSummary}${setupError}${step}`;
}

function renderFeishuActivation(feishu) {
  const aliases = feishu.targetAliases || [];
  const selected = state.settings?.selectedFeishuTargetAlias || '';
  return `<section class="card settings-list"><div class="setting"><div class="setting-title">授权与后台检查已通过</div><div class="setting-description">主动通道当前处于演练模式。确认后会启用真实发送，并向所选别名发送一条连接测试消息。</div><label class="setting-title" for="feishu-target">测试目标</label><select id="feishu-target" data-field="feishu-target" ${!aliases.length ? 'disabled' : ''}><option value="">请选择</option>${aliases.map((alias) => `<option value="${escapeHTML(alias)}" ${alias === selected ? 'selected' : ''}>${escapeHTML(alias)}</option>`).join('')}</select><div class="detail-actions"><button class="button primary" type="button" data-action="feishu-activate" ${!selected ? 'disabled' : ''}>确认启用并发送测试消息</button></div></div></section>`;
}

function renderFeishuQRStep(title, description, qrDataURL, verificationURL, userCode, action, actionLabel) {
  return `<section class="card setting"><div class="setting-title">${escapeHTML(title)}</div><div class="setting-description">${escapeHTML(description)}</div>${qrDataURL ? `<div class="feishu-auth"><img src="${escapeHTML(qrDataURL)}" alt="飞书授权二维码">${userCode ? `<div class="setting-description">验证码 ${escapeHTML(userCode)}</div>` : ''}</div>` : ''}<div class="detail-actions">${verificationURL ? `<button class="button" type="button" data-action="feishu-open-url" data-url="${escapeHTML(verificationURL)}">在飞书中继续</button>` : ''}<button class="button primary" type="button" data-action="${action}">${escapeHTML(actionLabel)}</button><button class="button" type="button" data-action="feishu-cancel">取消</button></div></section>`;
}

function renderFeishuReady(feishu) {
  const settings = state.settings || {};
  const overview = state.feishuOverview;
  const permissions = overview?.permissions || {};
  const health = overview?.health || {};
  const permissionState = (value) => value === 'verified' ? '已验证' : value === 'missing' ? '缺少授权' : '暂不可用';
  const statusRow = (title, value, healthy) => `<div class="setting-head"><div class="setting-copy"><div class="setting-title">${escapeHTML(title)}</div></div><div class="inline-status ${healthy ? 'healthy' : 'warning'}"><span aria-hidden="true"></span>${escapeHTML(value)}</div></div>`;
  const featureRows = (overview?.features || []).map((feature) => `<div class="setting-head feishu-feature-row"><div class="setting-copy"><label class="setting-title" for="feishu-feature-${escapeHTML(feature.id)}">${escapeHTML(feature.title)}</label><div class="setting-description">${escapeHTML(feature.description)}</div></div><select id="feishu-feature-${escapeHTML(feature.id)}" data-field="feishu-feature" data-feature="${escapeHTML(feature.id)}"><option value="off" ${feature.state === 'off' ? 'selected' : ''}>关闭</option>${feature.writable ? `<option value="dry_run" ${feature.state === 'dry_run' ? 'selected' : ''}>演练</option><option value="live" ${feature.state === 'live' ? 'selected' : ''}>真实执行</option>` : `<option value="enabled" ${feature.state === 'enabled' ? 'selected' : ''}>启用</option>`}</select></div>`).join('');
  const missing = permissions.missing?.length
    ? `<div class="setting-description warning">仍需处理：${escapeHTML(permissions.missing.join('、'))}</div><div class="detail-actions"><button class="button" type="button" data-action="feishu-refresh-overview">重新检查</button></div>`
    : '<div class="setting-description">基础单聊、卡片回调和 Codex 任务控制已授权。</div>';
  const targets = feishu.targetAliases || [];
  return `<section class="card settings-list">
    <div class="setting"><div class="setting-head"><div class="setting-copy"><div class="setting-title">飞书服务</div><div class="setting-description">${escapeHTML(overview?.summary || '正在读取可用范围…')}</div></div><div class="inline-status ${feishu.processState === 'degraded' ? 'warning' : 'healthy'}"><span aria-hidden="true"></span>${feishu.processState === 'degraded' ? '需要处理' : '已就绪'}</div></div></div>
    <div class="setting"><div class="setting-section-title">权限</div>${statusRow('应用权限', permissionState(permissions.application), permissions.application === 'verified')}${statusRow('当前用户授权', permissionState(permissions.user), permissions.user === 'verified')}${missing}</div>
    <div class="setting"><div class="setting-section-title">接收与高级功能</div><div class="setting-head"><div class="setting-copy"><label class="setting-title" for="feishu-profile">本机事件角色</label><div class="setting-description">主设备接收入站事件；仅手动能力仍可主动发送和处理队列。</div></div><select id="feishu-profile" data-field="feishu-profile"><option value="primary" ${feishu.profile === 'primary' ? 'selected' : ''}>主设备</option><option value="manual-only" ${feishu.profile === 'manual-only' ? 'selected' : ''}>仅手动能力</option></select></div>${featureRows}</div>
    <div class="setting"><div class="setting-section-title">诊断</div>${statusRow('核心服务', health.core === 'running' ? '运行中' : '需要处理', health.core === 'running')}${statusRow('飞书服务', health.bridge === 'running' ? '运行中' : health.bridge || '未知', health.bridge === 'running')}${statusRow('本机事件', health.inbound === 'connected' ? '已连接' : health.inbound === 'manual_only' ? '仅手动能力' : '未连接', health.inbound === 'connected' || health.inbound === 'manual_only')}${health.detail ? `<div class="setting-description">${escapeHTML(health.detail)}</div>` : ''}${feishu.processState === 'degraded' ? '<div class="detail-actions"><button class="button" type="button" data-action="feishu-restart">重新启动</button></div>' : ''}</div>
    <div class="setting"><div class="setting-section-title">连接测试</div><div class="setting-description">只显示软件已自动授权的别名。</div><div class="setting-head"><select id="feishu-target" data-field="feishu-target" ${!targets.length ? 'disabled' : ''}><option value="">请选择</option>${targets.map((alias) => `<option value="${escapeHTML(alias)}" ${alias === settings.selectedFeishuTargetAlias ? 'selected' : ''}>${escapeHTML(alias)}</option>`).join('')}</select><button class="button" type="button" data-action="feishu-test" ${!settings.selectedFeishuTargetAlias ? 'disabled' : ''}>发送测试消息</button></div></div>
  </section>`;
}

function selectHomeProjects(items = []) {
  return items.filter((item) => item.isPinned || item.tasks.some((task) => ['running', 'waiting'].includes(task.classification)));
}

function headlineRemaining(bucket) {
  if (!bucket) return null;
  const windows = [bucket.primary, bucket.secondary].filter(Boolean);
  if (!windows.length) return null;
  return Math.min(...windows.map((item) => Math.max(0, Math.min(100, 100 - item.usedPercent))));
}

function shortestWindow(bucket) {
  return [bucket?.primary, bucket?.secondary].filter(Boolean).sort((a, b) => (a.windowDurationMins ?? Infinity) - (b.windowDurationMins ?? Infinity))[0];
}

function durationWindow(minutes) {
  if (minutes % 1440 === 0) return `${minutes / 1440} 天窗口`;
  if (minutes % 60 === 0) return `${minutes / 60} 小时窗口`;
  return `${minutes} 分钟窗口`;
}

function formatReset(timestamp) {
  return `${new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }).format(new Date(timestamp * 1000))} 重置`;
}

function relativeTime(value) {
  if (!value) return '尚未更新';
  const seconds = Math.max(0, Math.round((Date.now() - new Date(value).getTime()) / 1000));
  if (seconds < 20) return '刚刚更新';
  if (seconds < 60) return `${seconds} 秒前更新`;
  if (seconds < 3600) return `${Math.floor(seconds / 60)} 分钟前更新`;
  return `${Math.floor(seconds / 3600)} 小时前更新`;
}

function formatTokens(value) {
  if (!Number.isFinite(value)) return '—';
  const absolute = Math.abs(value);
  if (absolute >= 1e9) return `${trim(value / 1e9)}B`;
  if (absolute >= 1e6) return `${trim(value / 1e6)}M`;
  if (absolute >= 1e3) return `${trim(value / 1e3)}K`;
  return String(value);
}

function trim(value) { return value.toFixed(value >= 100 ? 0 : value >= 10 ? 1 : 2).replace(/\.0+$|(?<=\.[0-9])0+$/, ''); }

function taskDetail(task, link) {
  if (link?.linkState === 'active') return `飞书 · ${turnStateText(link.turnState)} · ${Math.max(0, Math.floor(link.remainingSeconds / 3600))} 小时`;
  if (task.classification === 'waiting') return waitingReasonText(task.waitingReason);
  if (task.classification === 'running') return '正在运行';
  return '已完成';
}

function taskClassificationText(task) {
  if (task.classification === 'waiting') return waitingReasonText(task.waitingReason);
  if (task.classification === 'running') return '运行中';
  return '已完成';
}

function taskStateIcon(classification) {
  if (classification === 'running') return `<svg class="task-state running" viewBox="0 0 20 20" aria-hidden="true"><path fill="currentColor" d="M6 4.6v10.8L15 10 6 4.6Z"/></svg>`;
  if (classification === 'waiting') return `<svg class="task-state waiting" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="10" cy="10" r="7"/><path d="M10 6v4l2.5 2"/></svg>`;
  return `<svg class="task-state completed" viewBox="0 0 20 20" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><circle cx="10" cy="10" r="7"/><path d="m6.8 10 2.1 2.2 4.4-4.6"/></svg>`;
}

function playIcon() { return '<svg viewBox="0 0 16 16" aria-hidden="true"><path d="M4 2.8v10.4L13 8 4 2.8Z"/></svg>'; }
function waitingReasonText(reason) { return ({ approval: '等待审批', planConfirmation: '等待计划确认', userInput: '等待回答', actionRequired: '等待操作' })[reason] || '等待操作'; }
function turnStateText(value) { return ({ running: '运行中', waiting_input: '等待回答', completed: '已完成', failed: '失败', interrupted: '已停止', queued: '已排队', idle: '空闲' })[value] || value; }
function feishuStatusText(value) { return ({ ready: '产品托管进程已就绪。', stopped: '产品托管进程未运行。', dryRun: '产品托管进程处于演练模式。', notConfigured: '安装包内服务不可用。', unavailable: value.message || '暂不可用。' })[value.availability] || value.message || '状态未知。'; }
function feishuComponentStatusText(value) {
  if (value.processRunning) return '运行中';
  return ({ stopped: '已停止', notConfigured: '不可用', unavailable: '受限' })[value.availability] || '未运行';
}
function feishuProfileText(value) { return ({ primary: '主设备', 'manual-only': '仅手动能力' })[value] || '未配置'; }
function feishuSetupStageText(value) { return ({ not_started: '尚未配置', app_pending: '等待创建应用', app_configured: '应用已配置', authorization_pending: '等待用户授权', platform_pending: '等待后台确认', verifying: '正在核验', ready: '已就绪', failed: '需要处理' })[value?.stage] || '尚未配置'; }
function canCreateTaskLink() { return state.dashboard?.feishu?.taskLinkReady && state.settings?.selectedFeishuTargetAlias; }

async function refreshStaticData({ page = state.page, force = false } = {}) {
  const reads = [];
  if (!state.staticDataLoaded || force) {
    reads.push(api.settings().then((value) => { state.settings = value; }));
    reads.push(api.readFeishuSetup().then((value) => { state.feishuSetup = value; }).catch(() => {
      state.feishuSetup = { stage: 'not_started' };
    }));
  }
  if (!state.staticDataLoaded || page === 'pricing' || force) {
    reads.push(api.pricingCatalog().then((value) => { state.pricingCatalog = value; }));
  }
  if (page === 'feishu') {
    reads.push(api.feishuOverview().then((value) => { state.feishuOverview = value; }).catch(() => {
      state.feishuOverview = null;
    }));
  }
  await Promise.all(reads);
  state.staticDataLoaded = true;
}

async function refreshDashboard({ quiet = false } = {}) {
  if (state.refreshing) return;
  state.refreshing = true;
  if (!quiet) render();
  try {
    state.dashboard = await api.dashboard();
    state.error = '';
  } catch (error) {
    state.error = error.message;
    if (!quiet) showToast(error.message, true);
  } finally {
    state.loading = false;
    state.refreshing = false;
    render();
    scheduleRefresh();
  }
}

function scheduleRefresh() {
  clearTimeout(state.timer);
  const hasLiveState = state.dashboard?.activity?.runningCount || state.dashboard?.activity?.waitingCount || state.dashboard?.feishu?.links?.some((link) => link.linkState === 'active');
  state.timer = setTimeout(() => refreshDashboard({ quiet: true }), hasLiveState ? 3_000 : 15_000);
}

async function handleAction(action, element) {
  if (action === 'back') state.page = state.page === 'project' ? 'projects' : ['pricing', 'feishu'].includes(state.page) ? 'settings' : 'home';
  else if (action === 'projects') state.page = 'projects';
  else if (action === 'settings') state.page = 'settings';
  else if (action === 'feishu-settings') state.page = 'feishu';
  else if (action === 'pricing') state.page = 'pricing';
  else if (action === 'history') {
    state.page = 'history';
    state.historyLoading = true;
    state.historyError = '';
    state.historyServerError = '';
    render();
    try {
      const comparison = await api.tokenHistory({ dayCount: 30 });
      applyHistoryComparison(comparison, true);
    } catch (error) {
      state.historyError = error.message;
    } finally {
      state.historyLoading = false;
      render();
    }
    return;
  }
  else if (action === 'history-day') state.selectedHistoryDate = element.dataset.date;
  else if (action === 'pricing-add') state.pricingDraft = pricingDraft();
  else if (action === 'pricing-edit') state.pricingDraft = pricingDraft(state.pricingCatalog.plans.find((plan) => plan.id === element.dataset.id));
  else if (action === 'pricing-cancel') state.pricingDraft = null;
  else if (action === 'pricing-source') return api.openExternal(element.dataset.url);
  else if (action === 'pricing-delete') {
    const customPlans = (state.settings.customPricingPlans || []).filter((plan) => plan.id !== element.dataset.id);
    const selectedPricingPlanId = state.settings.selectedPricingPlanId === element.dataset.id ? state.pricingCatalog.defaultPlanId : state.settings.selectedPricingPlanId;
    state.settings = await api.updateSettings({ customPricingPlans, selectedPricingPlanId });
    state.pricingCatalog = await api.pricingCatalog();
    state.pricingDraft = null;
  }
  else if (action === 'refresh') return refreshDashboard();
  else if (action === 'quit') return api.quit();
  else if (action === 'project-detail') { state.selectedProjectId = element.dataset.id; state.page = 'project'; }
  else if (action === 'task-detail') { state.selectedTaskId = element.dataset.task; state.page = 'task'; }
  else if (action === 'pin') { await api.setPinned(element.dataset.id, element.dataset.pinned !== 'true'); return refreshDashboard({ quiet: true }); }
  else if (action === 'choose-ksf') { if (await api.chooseDirectory('ksfRoot')) return refreshDashboard(); }
  else if (action === 'feishu-test') { await api.sendFeishuTest(state.settings.selectedFeishuTargetAlias); showToast('测试消息已发送'); }
  else if (action === 'feishu-refresh-overview') { await refreshStaticData({ page: 'feishu', force: true }); render(); return; }
  else if (action === 'feishu-restart') { await api.restartFeishu(); showToast('飞书服务重启请求已提交'); return refreshDashboard({ quiet: true }); }
  else if (action === 'feishu-show-existing') state.feishuSetupMode = 'existing';
  else if (action === 'feishu-begin-new') {
    const result = await api.beginFeishuSetup({ mode: 'new' });
    state.feishuSetup = result.setup;
    state.feishuSetupPayload = result;
  }
  else if (action === 'feishu-begin-existing') {
    const appId = root.querySelector('[data-field="feishu-app-id"]')?.value || '';
    const appSecret = root.querySelector('[data-field="feishu-app-secret"]')?.value || '';
    if (!appId.trim() || !appSecret) throw new Error('请在软件内填写 App ID 和 App Secret');
    const result = await api.beginFeishuSetup({ mode: 'existing', appId: appId.trim(), appSecret });
    state.feishuSetup = result.setup;
    state.feishuSetupPayload = result;
  }
  else if (action === 'feishu-continue') {
    const result = await api.continueFeishuSetup();
    state.feishuSetup = result.setup;
    state.feishuSetupPayload = result;
  }
  else if (action === 'feishu-verify') {
    state.feishuSetup = { ...state.feishuSetup, stage: 'verifying' };
    render();
    const result = await api.verifyFeishuSetup();
    state.feishuSetup = result.setup;
    state.feishuSetupPayload = result;
  }
  else if (action === 'feishu-activate') {
    const targetAlias = state.settings?.selectedFeishuTargetAlias || '';
    if (!targetAlias) throw new Error('请选择软件内显示的测试目标');
    const result = await api.activateFeishuSetup(targetAlias);
    state.feishuSetup = result.setup;
    state.feishuSetupPayload = result;
    showToast(`飞书服务已启用，测试消息已发送到“${targetAlias}”`);
    return refreshDashboard({ quiet: true });
  }
  else if (action === 'feishu-cancel') { state.feishuSetup = await api.cancelFeishuSetup(); state.feishuSetupPayload = null; state.feishuSetupMode = 'new'; }
  else if (action === 'feishu-open-url') return api.openFeishuURL(element.dataset.url);
  else if (action === 'open-task') return api.openTask(element.dataset.thread);
  else if (action.startsWith('open-folder:')) { const project = findProject(action.split(':').slice(1).join(':')); return api.openPath(project.projectDirectory); }
  else if (action.startsWith('launch-project:')) return api.launchProject(action.split(':').slice(1).join(':'));
  else if (action.startsWith('create-task:') || action.startsWith('archive-task:')) {
    const id = action.split(':').slice(1).join(':');
    const purpose = action.startsWith('archive-task:') ? 'archiveProject' : 'contextPreparation';
    showToast(purpose === 'archiveProject' ? '正在创建归档任务…' : '正在创建任务…');
    findProject(id);
    const result = await api.createTask(id, purpose);
    showToast(result.warning || '任务已创建并在 Codex 中打开', Boolean(result.warning));
    return refreshDashboard({ quiet: true });
  } else if (action === 'toggle-link') {
    const payload = { threadId: element.dataset.thread, title: element.dataset.title, projectName: element.dataset.project, targetAlias: state.settings.selectedFeishuTargetAlias };
    if (element.dataset.linked === 'true') await api.releaseTaskLink(payload); else await api.createTaskLink(payload);
    return refreshDashboard({ quiet: true });
  } else if (action === 'interrupt-link') {
    await api.interruptTaskLink({ threadId: element.dataset.thread });
    return refreshDashboard({ quiet: true });
  }
  if (['settings', 'pricing', 'feishu'].includes(state.page)) {
    await refreshStaticData({ page: state.page });
  }
  render();
}

function findProject(id) {
  const project = state.dashboard?.projects?.catalog?.find((item) => item.id === id);
  if (!project) throw new Error('项目不存在或尚未同步');
  return project;
}

function findTask(id) {
  for (const item of state.dashboard?.projects?.projects || []) {
    const task = item.tasks?.find((candidate) => candidate.id === id);
    if (task) return { task, project: item.project };
  }
  return null;
}

function showToast(message, error = false) {
  toast.textContent = message;
  toast.classList.toggle('error', error);
  toast.hidden = false;
  clearTimeout(showToast.timer);
  showToast.timer = setTimeout(() => { toast.hidden = true; }, error ? 6_000 : 3_000);
}

function reportHeight() {
  requestAnimationFrame(() => api.resize(Math.max(320, root.scrollHeight + 1)));
}

root.addEventListener('click', async (event) => {
  const target = event.target.closest('[data-action]');
  if (!target || target.disabled) return;
  target.disabled = true;
  try { await handleAction(target.dataset.action, target); }
  catch (error) { showToast(error.message, true); }
  finally { target.disabled = false; }
});

root.addEventListener('input', (event) => {
  if (event.target.dataset.draft && state.pricingDraft) {
    state.pricingDraft[event.target.dataset.draft] = event.target.value;
  }
  if (event.target.dataset.field === 'project-search') {
    state.query = event.target.value;
    render();
    const field = root.querySelector('[data-field="project-search"]');
    field?.focus();
    field?.setSelectionRange(state.query.length, state.query.length);
  }
});

root.addEventListener('change', async (event) => {
  try {
    if (event.target.dataset.field === 'feishu-target') {
      state.settings = await api.updateSettings({ selectedFeishuTargetAlias: event.target.value });
    }
    if (event.target.dataset.field === 'feishu-profile') {
      await api.setFeishuProfile(event.target.value);
      state.dashboard = await api.dashboard();
    }
    if (event.target.dataset.field === 'feishu-feature') {
      const feature = event.target.dataset.feature;
      const mode = event.target.value;
      const confirmRealWrite = mode === 'live' && window.confirm('允许真实执行后，此功能可以处理真实写入操作。是否继续？');
      if (mode === 'live' && !confirmRealWrite) { await refreshDashboard({ quiet: true }); return; }
      state.feishuOverview = await api.updateFeishuFeature({ feature, mode, confirmRealWrite });
      state.dashboard = await api.dashboard();
    }
    if (event.target.dataset.field === 'launch-login') {
      state.settings = await api.updateSettings({ launchAtLogin: event.target.checked });
    }
    if (event.target.dataset.field === 'pricing-plan') {
      state.settings = await api.updateSettings({ selectedPricingPlanId: event.target.value });
      if (state.history.length) {
        const comparison = await api.tokenHistory({ dayCount: 30, repriceOnly: true });
        applyHistoryComparison(comparison);
      }
      state.dashboard = await api.dashboard();
    }
    render();
  } catch (error) { showToast(error.message, true); }
});

root.addEventListener('submit', async (event) => {
  if (event.target.dataset.form !== 'pricing') return;
  event.preventDefault();
  const draft = state.pricingDraft;
  const regular = parseRate(draft?.regular);
  const cached = parseRate(draft?.cached);
  const output = parseRate(draft?.output);
  if (!draft?.provider.trim() || !draft?.model.trim() || regular == null || cached == null || output == null) {
    showToast('请填写厂商、模型和 0–1000 美元的六位小数价格。', true);
    return;
  }
  const id = draft.id || `custom:${crypto.randomUUID().toLowerCase()}`;
  const plan = { id, provider: draft.provider.trim(), model: draft.model.trim(), variant: draft.variant.trim(), regularInputMicroUsdPerMillion: regular, cachedInputMicroUsdPerMillion: cached, outputMicroUsdPerMillion: output };
  const customPlans = [...(state.settings.customPricingPlans || []).filter((item) => item.id !== id), plan];
  try {
    state.settings = await api.updateSettings({ customPricingPlans });
    state.pricingCatalog = await api.pricingCatalog();
    if (!state.pricingCatalog.plans.some((item) => item.id === id)) throw new Error('核心服务拒绝了这个价格方案。');
    state.pricingDraft = null;
    render();
  } catch (error) { showToast(error.message, true); }
});

new ResizeObserver(reportHeight).observe(root);
window.addEventListener('keydown', (event) => { if (event.key === 'Escape') state.page === 'home' ? api.hide() : handleAction('back', root); });
window.addEventListener('focus', () => refreshDashboard({ quiet: true }));

async function start() {
  await refreshStaticData({ page: state.page });
  await refreshDashboard();
}

start().catch((error) => {
  state.loading = false;
  state.error = error.message;
  render();
});
