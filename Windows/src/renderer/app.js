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
  pricingCatalog: { defaultPlanId: 'openai:gpt-6-astra', plans: [] },
  pricingDraft: null,
  feishuConfiguration: null,
  feishuConfigurationGeneration: 0,
  feishuConfigurationLoading: false,
  feishuManualRefresh: false,
  feishuReadError: '',
  feishuLastAction: '',
  feishuActionFeedback: '',
  feishuActionOutcome: '',
  feishuRetiredEpochs: new Set(),
  feishuFlowTimer: null,
  feishuPollTimer: null,
  feishuSetupMode: 'new',
  feishuSetupBusy: false,
  feishuSetupError: '',
  feishuSections: {},
  toolchain: null,
  toolchainBusy: false,
  toolchainError: '',
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

function buttonIcon(action, name, label, extra = '', disabled = false) {
  return `<button class="icon-button ${extra}" ${disabled ? 'disabled' : ''} type="button" data-action="${action}" title="${escapeHTML(label)}" aria-label="${escapeHTML(label)}">${icon(name)}</button>`;
}

function header(title, extraActions = '') {
  const secondary = state.page !== 'home';
  const accountLoading = title === 'Codex 用量' && (state.loading || state.refreshing);
  return `<header class="app-header">
    ${secondary ? buttonIcon('back', 'back', '返回') : ''}
    <h1 class="app-title">${escapeHTML(title)}${accountLoading ? '<span class="account-spinner" role="status" aria-label="正在读取 Codex 用量"></span>' : ''}</h1>
    <div class="header-actions">${extraActions}
      ${state.page === 'home' ? buttonIcon('settings', 'settings', '设置') : ''}
      ${buttonIcon('refresh', 'refresh', '刷新')}
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
  else if (state.page === 'workspaces') root.innerHTML = renderWorkspacesPage();
  else if (state.page === 'settings') root.innerHTML = renderSettingsPage();
  else if (state.page === 'project') root.innerHTML = renderProjectPage();
  else if (state.page === 'task') root.innerHTML = renderTaskPage();
  else if (state.page === 'history') root.innerHTML = renderHistoryPage();
  else if (state.page === 'pricing') root.innerHTML = renderPricingPage();
  else if (state.page === 'feishu') renderFeishuSurface();
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
  const workspaceLibrary = dashboard.workspaces?.workspaces || [];
  const workspaces = selectHomeWorkspaces(workspaceLibrary);
  return `${header('Codex 用量')}
    ${renderQuota(bucket, remaining, window)}
    <div class="section-header"><h2 class="section-title">Token 活动</h2><span class="section-action">${buttonIcon('history', 'chart', '查看每日 Token 历史')}</span></div>
    ${renderTokens(dashboard.usage)}
    ${state.settings?.ksfRoot ? `<div class="section-header"><h2 class="section-title">KSF 项目</h2><span class="section-meta">${projects.length}</span><span class="section-action">${buttonIcon('projects', 'grid', '查看全部项目')}</span></div>${renderProjectSection(projects, dashboard.projects)}` : ''}
    ${workspaceLibrary.length ? `<div class="section-header"><h2 class="section-title">Codex 工作区</h2><span class="section-meta">${workspaceLibrary.length}</span><span class="section-action">${buttonIcon('workspaces', 'grid', '查看全部 Codex 工作区')}</span></div>${workspaces.length ? `<section class="project-stack">${workspaces.map((item) => renderWorkspaceCard(item)).join('')}</section>` : ''}` : ''}
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
  const today = new Date();
  const dateKey = (offset) => {
    const date = new Date(today.getFullYear(), today.getMonth(), today.getDate() + offset);
    return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
  };
  const local = usage.localDailyUsage?.startDate === dateKey(0) ? usage.localDailyUsage : null;
  const previous = usage.localPreviousDailyUsage?.startDate === dateKey(-1) ? usage.localPreviousDailyUsage : null;
  const localBreakdown = local?.breakdown;
  const metrics = [
    ['模型普通输入', localBreakdown ? formatTokens(localBreakdown.regularInputTokens) : '—'],
    ['模型缓存输入', localBreakdown ? formatTokens(localBreakdown.cachedInputTokens) : '—'],
    ['模型输出', localBreakdown ? formatTokens(localBreakdown.outputTokens) : '—'],
    [accountLatestLabel(latest?.startDate), latest ? formatTokens(latest.tokens) : '未同步'],
    ['本机昨日', previous ? formatTokens(previous.tokens) : '—'],
    ['本机今日', local ? formatTokens(local.tokens) : '—'],
  ];
  const plan = selectedPricingPlan();
  return `<section class="card token-card">${metrics.map(([label, value]) => `<div class="metric"><div class="metric-label">${label}</div><div class="metric-value">${value}</div></div>`).join('')}<div class="token-cost-row"><span><small>API 估算</small><strong>${escapeHTML(compactPricingName(plan))}</strong></span><b>今日 ${formatCostEstimate(local ? usage.localDailyCost : null)}</b></div></section>
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
  return plan ? [plan.model, plan.variant].filter(Boolean).join(' · ') : 'GPT-6 Astra';
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
    <div class="task-list">${item.tasks.length ? item.tasks.map((task) => renderTask(task, project, null, title)).join('') : '<div class="empty">暂无任务</div>'}</div>
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

function renderTask(task, project, workspace = null, containerName = null) {
  const link = state.dashboard.feishu.links.find((item) => item.taskKey === task.taskKey);
  const linkActive = link?.linkState === 'active';
  const detail = taskDetail(task, link);
  return `<div class="task-row">
    ${taskStateIcon(task.classification)}
    <div class="task-copy"><span class="task-name">${escapeHTML(task.name || '未命名任务')}</span><span class="task-detail">${escapeHTML(detail)}</span>${project ? `<span class="task-detail">${escapeHTML(taskReportLabel(task.taskRuntime))}</span>` : ''}</div>
    <div class="task-actions">
      <button class="icon-button" type="button" data-action="task-detail" data-task="${escapeHTML(task.id)}" title="任务详情" aria-label="任务详情">${icon('info')}</button>
      ${link?.controls?.canInterrupt ? `<button class="icon-button" type="button" data-action="interrupt-link" data-thread="${escapeHTML(task.threadId)}" title="停止本轮" aria-label="停止本轮">${icon('stop')}</button>` : ''}
      <button class="icon-button" type="button" data-action="toggle-link" data-thread="${escapeHTML(task.threadId)}" data-title="${escapeHTML(task.name || '未命名任务')}" data-project="${escapeHTML(containerName || project?.name || workspace?.name || '其他任务')}" data-linked="${linkActive}" title="${linkActive ? '解除飞书连接' : '连接到飞书'}" aria-label="${linkActive ? '解除飞书连接' : '连接到飞书'}" >${icon(linkActive ? 'close' : 'send')}</button>
      <button class="icon-button" type="button" data-action="open-task" data-thread="${escapeHTML(task.threadId)}" title="在 Codex 中打开" aria-label="在 Codex 中打开">${icon('open')}</button>
    </div>
  </div>`;
}

function renderWorkspaceCard(item, activeOnly = false) {
  const tasks = activeOnly ? item.tasks.filter((task) => ['running', 'waiting'].includes(task.classification)) : item.tasks;
  return `<article class="card project-card workspace-card" data-workspace-id="${escapeHTML(item.id)}">
    <div class="project-card-head">
      <span class="workspace-symbol">${icon(item.kind === 'other' ? 'info' : 'folder')}</span>
      <span class="project-name">${escapeHTML(item.name)}</span>
      <span class="activity-count" title="运行中任务">${playIcon()} ${item.runningCount}</span>
      ${item.waitingCount ? `<span class="activity-count waiting-count" title="等待任务">待 ${item.waitingCount}</span>` : ''}
      <button class="icon-button" type="button" data-action="workspace-pin" data-id="${escapeHTML(item.id)}" data-pinned="${Boolean(item.isPinned)}" title="${item.isPinned ? '取消固定' : '固定工作区'}" aria-label="${item.isPinned ? '取消固定' : '固定工作区'}">${icon('pin')}</button>
    </div>
    ${item.path ? `<div class="workspace-path" title="${escapeHTML(item.path)}">${escapeHTML(item.path)}</div>` : ''}
    <div class="task-list">${tasks.map((task) => renderTask(task, null, item)).join('')}</div>
    ${!activeOnly && item.hiddenTaskCount ? `<div class="workspace-history">另有 ${item.hiddenTaskCount} 个较早任务未显示</div>` : ''}
    ${item.kind === 'workspace' && item.path ? `<footer class="project-footer">
      <div class="usage-pair"><span>累计 <strong>${formatTokens(item.usage?.cumulativeTokens)}</strong></span><span>今日 <strong>${formatTokens(item.usage?.todayTokens)}</strong></span></div>
      <div class="project-actions">
        ${buttonIcon(`workspace-create-task:${item.id}`, 'add', '新建任务')}
        ${buttonIcon(`workspace-open-folder:${item.id}`, 'folder', '打开工作区文件夹')}
      </div>
    </footer>` : ''}
  </article>`;
}

function renderTaskPage() {
  const resolved = findTask(state.selectedTaskId);
  if (!resolved) return `${header('任务详情')}<section class="card empty"><strong>任务已不可用</strong>它可能已经被归档或暂未同步。</section>`;
  const { task, project, workspace, ksfUnassigned } = resolved;
  const showsKSFRoute = Boolean(project) || ksfUnassigned;
  const containerName = ksfUnassigned ? '无项目' : project?.name || workspace?.name || '其他任务';
  const link = state.dashboard.feishu.links.find((item) => item.taskKey === task.taskKey);
  const linkActive = link?.linkState === 'active';
  const route = !task.taskRuntime || task.taskRuntime.routeFreshness === 'current' ? task.route : null;
  const routeRows = [];
  if (route?.category?.name) routeRows.push(['工作类别', route.category.name, route.category.validation_status]);
  for (const job of route?.jobs || []) routeRows.push([job.role === 'main' ? '主岗位' : '协同岗位', job.name || '未命名岗位', job.validation_status]);
  for (const ability of route?.abilities || []) routeRows.push(['基本功', ability.name || '未命名基本功', ability.validation_status]);
  if (route?.dispatchableSkills?.length) routeRows.push(['Skill', `${route.dispatchableSkills.length} 项可调度`, '']);
  return `${header(task.name || '未命名任务')}
    <section class="card detail-hero">
      <div class="detail-status"><span class="status-chip ${escapeHTML(task.classification)}">${escapeHTML(taskClassificationText(task))}</span><span>${escapeHTML(containerName)}</span></div>
      <p class="detail-summary">${escapeHTML(taskDetail(task, link))}</p>
      ${renderTaskRuntimeDetails(task.taskRuntime)}
      <div class="detail-actions"><button class="button primary" type="button" data-action="open-task" data-thread="${escapeHTML(task.threadId)}">打开 Codex</button><button class="button" type="button" data-action="toggle-link" data-thread="${escapeHTML(task.threadId)}" data-title="${escapeHTML(task.name || '未命名任务')}" data-project="${escapeHTML(containerName)}" data-linked="${linkActive}" >${linkActive ? '解除飞书' : '连接飞书'}</button>${link?.controls?.canInterrupt ? `<button class="button danger" type="button" data-action="interrupt-link" data-thread="${escapeHTML(task.threadId)}">停止本轮</button>` : ''}</div>
    </section>
    ${showsKSFRoute ? `<div class="section-header"><h2 class="section-title">KSF 路由</h2></div>
    <div class="setting-description">${escapeHTML(taskRouteSourceLabel(task.taskRuntime, Boolean(task.route)))}</div>
    <section class="card route-list">${routeRows.length ? routeRows.map(([label, value, validation]) => `<div class="route-row"><span>${escapeHTML(label)}</span><strong>${escapeHTML(value)}</strong>${validation ? `<small>${escapeHTML(validation)}</small>` : ''}</div>`).join('') : '<div class="empty"><strong>尚无已验证路由</strong>任务完成 KSF 路由后会在这里显示。</div>'}</section>` : ''}`;
}

function taskReportLabel(runtime) {
  if (!runtime) return '暂无可用的 Agent 上报';
  return ({ recent: 'Agent 已上报', stale: 'Agent 上报已过期' })[runtime.reportFreshness] || 'Agent 上报时效未知';
}

function taskRouteSourceLabel(runtime, hasRoute = false) {
  if (!runtime) return hasRoute ? 'KSF 投影 · 时效未知' : '尚无 KSF 已验证来源';
  return ({ current: 'KSF 来源已验证 · 当前有效', stale: 'KSF 来源已过期 · 不作为当前路由', unavailable: 'KSF 来源暂无法验证' })[runtime.routeFreshness] || '尚无 KSF 已验证来源';
}

function renderTaskRuntimeDetails(runtime) {
  if (!runtime) return '<div class="setting-description">暂无可用的 Agent 上报；尚未上报或暂无法读取，实际状态以 Desktop 观测为准。</div>';
  const reportedStatus = ({ running: '进行中', waiting: '等待中', blocked: '受阻', completed: '已完成' })[runtime.reportedStatus] || '未知';
  const reportedAt = runtime.reportedAt ? new Date(runtime.reportedAt) : null;
  const dateText = reportedAt && Number.isFinite(reportedAt.getTime()) ? reportedAt.toLocaleString('zh-CN') : '';
  const percent = runtime.progress?.percent;
  return `<div class="setting-description">${escapeHTML(taskReportLabel(runtime))} · Agent 自述：${reportedStatus}（不替代实际任务状态）</div>${dateText ? `<div class="setting-description">上报于 ${escapeHTML(dateText)}</div>` : ''}${runtime.progress?.summary ? `<div class="setting-description">Agent 进度自述：${escapeHTML(runtime.progress.summary)}</div>` : ''}${Number.isInteger(percent) && percent >= 0 && percent <= 100 ? `<div class="setting-description">Agent 自报进度 ${percent}%</div>` : ''}`;
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

function renderWorkspacesPage() {
  const workspaces = state.dashboard?.workspaces?.workspaces || [];
  return `${header('Codex 工作区')}
    <section class="project-stack">${workspaces.length ? workspaces.map((item) => renderWorkspaceCard(item)).join('') : '<div class="card empty"><strong>没有可用工作区</strong>当前没有可展示的普通 Codex 工作区。</div>'}</section>`;
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
      <div class="setting"><div class="setting-title">运行组件</div><div class="component-status-list"><div class="component-status"><span>核心服务</span><strong>${state.dashboard?.coreVersion ? '运行中' : '不可用'}</strong></div><div class="component-status"><span>飞书服务</span><strong>${escapeHTML(feishuComponentStatusText(feishu))}</strong></div></div><div class="setting-description">退出 KSFAssistant 将停止核心服务、飞书服务及其子进程。</div></div>
      <div class="setting"><div class="setting-head"><div class="setting-copy"><div class="setting-title">KSF 知识库</div><div class="setting-description">KSF 是可选增强能力；未配置不影响额度、Token、任务状态和飞书。</div></div><button class="button ${settings.ksfRoot ? 'danger' : ''}" type="button" data-action="${settings.ksfRoot ? 'clear-ksf' : 'choose-ksf'}">${settings.ksfRoot ? '取消' : '选择目录'}</button></div><div class="setting-path">${escapeHTML(settings.ksfRoot || '未接入')}</div></div>
      <div class="setting"><div class="setting-head"><div class="setting-copy"><div class="setting-title">飞书</div><div class="setting-description">${escapeHTML(feishuConfigurationPresentation().title)}</div></div><button class="button" type="button" data-action="feishu-settings">配置</button></div></div>
      <div class="setting"><div class="setting-head"><div class="setting-copy"><div class="setting-title">API 估算价格</div><div class="setting-description">${escapeHTML(selectedPricingPlan()?.displayName || 'GPT-6 Astra')}</div></div><button class="button" type="button" data-action="pricing">管理价格方案</button></div></div>
      <div class="setting"><div class="setting-head"><div class="setting-copy"><label class="setting-title" for="launch-login">登录时启动</label><div class="setting-description">登录 Windows 后在系统托盘中启动。</div></div><input id="launch-login" class="switch" type="checkbox" data-field="launch-login" ${settings.launchAtLogin ? 'checked' : ''}></div></div>
      <div class="setting"><div class="setting-head"><div class="setting-copy"><div class="setting-title">版本</div><div class="setting-description">Windows 0.11.0-preview.1 · 核心服务 ${escapeHTML(state.dashboard?.coreVersion || '—')}</div></div></div></div>
    </section>
    <div class="detail-actions"><button class="button danger" type="button" data-action="quit">退出 KSFAssistant</button></div>`;
}

function feishuConfigurationPresentation(snapshot = state.feishuConfiguration) {
  if (!snapshot) return { title: state.feishuSetupError ? '配置状态未知' : '正在读取配置', detail: '', tone: 'neutral' };
  return snapshot.summary;
}

function renderFeishuSurface() {
  const context = JSON.stringify([state.feishuConfiguration?.epoch, state.feishuConfiguration?.contextRevision]);
  const sameContext = root.dataset.feishuContext === context;
  const focused = document.activeElement;
  const focusKey = focused && root.contains(focused) ? { id: focused.id, action: focused.dataset.action, field: focused.dataset.field, feature: focused.dataset.feature } : null;
  const credentials = sameContext ? [...root.querySelectorAll('.feishu-credentials input')].map((field) => ({ id: field.id, value: field.value, start: field.selectionStart, end: field.selectionEnd })) : [];
  const html = renderFeishuPage();
  // Timestamp-only observations must not recreate controls, QR images or focus.
  if (root.feishuHTML === html && sameContext && root.querySelector('.feishu-page')) return;
  const scroll = document.scrollingElement?.scrollTop || 0;
  root.innerHTML = html;
  root.feishuHTML = html;
  if (document.scrollingElement) document.scrollingElement.scrollTop = scroll;
  root.dataset.feishuContext = context;
  for (const value of credentials) {
    const field = root.querySelector('#' + value.id);
    if (field) { field.value = value.value; field.setSelectionRange(value.start, value.end); }
  }
  if (focusKey) {
    const replacement = [...root.querySelectorAll('button, input, select, summary')].find((element) =>
      focusKey.id ? element.id === focusKey.id : focusKey.action ? element.dataset.action === focusKey.action
        : focusKey.field && element.dataset.field === focusKey.field && element.dataset.feature === focusKey.feature);
    replacement?.focus({ preventScroll: true });
  }
}

function feishuDisclosure(name, title, content) {
  return '<details class="feishu-disclosure" data-feishu-section="' + name + '" ' + (state.feishuSections?.[name] ? 'open' : '') + '><summary>' + title + '</summary><div class="feishu-disclosure-body">' + content + '</div></details>';
}

function feishuAction(id) {
  return state.feishuConfiguration?.actions.find((action) => action.id === id);
}

function renderFeishuAction(id, primary = false) {
  const action = feishuAction(id);
  if (!action) return '';
  const disabled = action.enabled !== true || state.feishuSetupBusy || state.feishuReadError || state.feishuSetupError
    || (id === 'test_message' && !state.feishuConfiguration?.connection?.targetAliases?.includes(state.feishuConfiguration?.diagnostics?.selfTarget));
  return '<button class="button ' + (primary ? 'primary feishu-primary' : id === 'logout' ? 'danger' : '') + '" type="button" data-action="feishu-config-' + escapeHTML(id) + '" ' + (disabled ? 'disabled' : '') + '>' + escapeHTML(action.title) + '</button>';
}

function renderFeishuFacts(ids) {
  const facts = (state.feishuConfiguration?.facts || []).filter((fact) => ids.includes(fact.id));
  const labels = { unknown: '未知', present: '已确认', missing: '缺少', failed: '检查失败', stale: '已过期' };
  return '<dl class="feishu-facts">' + facts.map((fact) => {
    const help = ['来源：' + (fact.source || '未知'), '状态：' + (labels[fact.state] || '未知'), fact.stale ? '此前观测，待复核' : ''].filter(Boolean).join('；');
    const value = fact.value || labels[fact.state] || '未知';
    const stale = fact.stale && fact.state !== 'stale' && !/过期|此前|待复核/.test(value) ? '（此前观测，待复核）' : '';
    return '<div><dt>' + escapeHTML(fact.title) + '</dt><dd title="' + escapeHTML(help) + '" aria-label="' + escapeHTML(value + stale + '；' + help) + '">' + escapeHTML(value) + '</dd></div>';
  }).join('') + '</dl>';
}

function feishuPrimaryAction() {
  const snapshot = state.feishuConfiguration;
  const enabled = (id) => feishuAction(id)?.enabled === true;
  const flow = snapshot?.flow;
  if (flow?.id && ['pending', 'completed'].includes(flow.state)) {
    if (flow.expiresAt && (!Number.isFinite(Date.parse(flow.expiresAt)) || Date.parse(flow.expiresAt) <= Date.now())) return null;
    const finish = { app: 'finish_app' }[flow.kind];
    return finish && enabled(finish) ? finish : null;
  }
  if (enabled('create_app')) return 'create_app';
  if (enabled('start_auth')) return 'start_auth';
  return null;
}

function feishuNeedsApplicationSetup(snapshot = state.feishuConfiguration) {
  return snapshot?.facts?.some((fact) => fact.id === 'application' && fact.state === 'missing') === true;
}

function renderFeishuApplicationSetup() {
  const create = feishuAction('create_app');
  if (!create) return '';
  const reason = create.enabled ? '' : '<p class="setting-description">' + escapeHTML(create.reason || '请刷新并检查当前飞书状态。') + '</p>';
  const cleanup = feishuAction('logout')?.enabled ? renderFeishuAction('logout') : '';
  return '<h2>连接飞书</h2><p class="setting-description">扫码后在飞书官方页面创建或选择应用；正常流程只需扫码一次。</p>'
    + renderFeishuAction('create_app', true) + reason + cleanup;
}

function renderFeishuOverview(step = '') {
  const summary = feishuConfigurationPresentation();
  const tone = ['success', 'warning', 'neutral'].includes(summary.tone) ? summary.tone : 'neutral';
  return '<section class="feishu-overview" aria-label="飞书接入状态" title="' + escapeHTML(summary.title + '：' + summary.detail) + '"><h2 class="setting-title">飞书接入状态</h2>'
    + renderFeishuFacts(['robot', 'authorizedUser', 'taskConnection']) + step + '</section>';
}

function renderFeishuDiagnostics() {
  const snapshot = state.feishuConfiguration;
  const d = snapshot?.diagnostics || {};
  const missing = (d.missingApplicationScopes || []).map((scope) => '应用权限缺项：' + scope).concat((d.missingUserScopes || []).map((scope) => '用户授权缺项：' + scope));
  const contents = renderFeishuFacts(['application'])
    + missing.map((text) => '<p class="setting-description">' + escapeHTML(text) + '</p>').join('')
    + (d.recentOperations || []).map((op) => '<p class="setting-description">' + escapeHTML((op.title || op.action) + ' · ' + (op.statusText || op.outcome) + ' · ' + (op.stageText || op.stage) + ' · ' + (op.code || '') + ' · ' + op.updatedAt + '：' + op.message) + '</p>').join('')
    + (feishuAction('restart')?.enabled ? renderFeishuAction('restart') : '')
    + (d.selfTarget ? '<div class="feishu-self-test">' + (state.feishuSetupBusy && state.feishuLastAction === 'test_message' ? '<p role="status">正在发送测试消息…</p>' : renderFeishuAction('test_message'))
      + (state.feishuLastAction === 'test_message' && state.feishuActionFeedback ? '<p class="setting-description" role="status">' + escapeHTML(state.feishuActionFeedback) + '</p>' : '') + '</div>' : '');
  return '<section class="feishu-diagnostics">' + feishuDisclosure('diagnostics', '诊断详情', contents) + '</section>';
}

function renderFeishuVersions() {
  const d = state.feishuConfiguration?.diagnostics || {};
  const summary = 'KSFAssistant ' + (d.serviceVersion ? 'v' + d.serviceVersion : '未知') + ' · lark-cli ' + (d.cliVersion ? 'v' + d.cliVersion : '未知');
  return '<p class="feishu-versions setting-description">' + escapeHTML(summary) + '</p>';
}

function activeFeishuFlow() {
  const flow = state.feishuConfiguration?.flow;
  if (!flow?.id || flow.state !== 'pending') return null;
  if (flow.expiresAt && (!Number.isFinite(Date.parse(flow.expiresAt)) || Date.parse(flow.expiresAt) <= Date.now())) return null;
  return flow;
}

function renderFeishuFlow() {
  const flow = activeFeishuFlow();
  if (!flow) return '';
  const qr = typeof flow.qrDataURL === 'string' && /^data:image\/png;base64,[A-Za-z0-9+/=]+$/.test(flow.qrDataURL) ? flow.qrDataURL : '';
  return '<div class="feishu-flow" data-flow-id="' + escapeHTML(flow.id) + '">'
    + (qr ? '<div class="feishu-auth"><img src="' + escapeHTML(qr) + '" alt="当前飞书会话二维码"></div>' : '')
    + (flow.userCode ? '<p class="setting-description">验证码 ' + escapeHTML(flow.userCode) + '</p>' : '')
    + '<p class="setting-description feishu-wait" role="status">' + (state.feishuSetupBusy ? (state.feishuLastAction === 'cancel_flow' ? '正在取消登录…' : '正在确认登录…') : flow.kind === 'user' ? '请用飞书补充本人授权' : '请用飞书扫码连接') + '</p>'
    + '<div class="feishu-login-links">'
    + (flow.verificationURL ? '<button class="button feishu-text" type="button" data-action="feishu-flow-open" ' + (state.feishuSetupBusy ? 'disabled' : '') + '>在浏览器中授权</button>' : '')
    + (feishuAction('cancel_flow')?.enabled ? '<button class="button feishu-text" type="button" data-action="feishu-config-cancel_flow" ' + (state.feishuSetupBusy ? 'disabled' : '') + '>' + (flow.kind === 'user' ? '取消登录' : '取消创建') + '</button>' : '') + '</div>'
    + '<p class="setting-description">' + (flow.kind === 'user' ? '取消本次登录，不撤销已有授权' : '取消本次等待，不删除已创建的应用') + '</p></div>';
}

function renderFeishuPage() {
  const snapshot = state.feishuConfiguration;
  const disabled = state.feishuSetupBusy ? 'disabled' : '';
  const primaryID = feishuPrimaryAction();
  const applicationSetup = feishuNeedsApplicationSetup(snapshot);
  let controls = applicationSetup ? renderFeishuApplicationSetup() : primaryID ? renderFeishuAction(primaryID, true) : '';
  const flow = renderFeishuFlow();
  let step = flow + controls;
  if ((state.feishuSetupBusy || (state.feishuLastAction === 'start_auth' && state.feishuActionOutcome === 'pending' && !snapshot?.flow)) && !flow && state.feishuLastAction !== 'test_message') {
    step = '<p class="feishu-wait setting-description" role="status"><span class="feishu-spinner" aria-hidden="true"></span>' + (state.feishuLastAction === 'start_auth' ? '正在准备登录…' : state.feishuLastAction === 'logout' ? '正在注销…' : '正在处理…') + '</p>';
  } else if (!snapshot) step = '<p class="setting-description" role="status">正在读取配置…</p>';
  if (['expired', 'failed'].includes(snapshot?.flow?.state)) step = '<p class="feishu-step-error">登录未完成或二维码已过期，请重新登录。</p>' + step;
  if (step) step = '<div class="feishu-step" aria-label="当前配置操作">' + step + '</div>';
  const error = state.feishuReadError || (state.feishuLastAction !== 'test_message' ? state.feishuSetupError : '') || snapshot?.issues?.find((issue) => !(issue.component === 'operation' && issue.code === 'test_message'))?.message;
  if (error) step += '<p class="feishu-step-error" role="alert">' + escapeHTML(error) + '</p>';
  return header('飞书配置', buttonIcon('feishu-configuration-check', 'refresh', state.feishuManualRefresh ? '正在刷新接入状态' : '刷新接入状态', state.feishuManualRefresh ? 'feishu-refreshing' : '', Boolean(disabled || state.feishuManualRefresh)))
    + '<div class="feishu-page">' + renderFeishuOverview(step) + renderToolchainSettings() + renderFeishuDiagnostics() + renderFeishuVersions()
    + (feishuAction('logout')?.enabled && !applicationSetup ? '<div class="feishu-logout">' + renderFeishuAction('logout') + '</div>' : '') + '</div>';
}

function renderToolchainSettings() {
  const toolchain = state.toolchain;
  const title = toolchain?.installationTitle || (state.toolchainError ? '检查失败' : '正在检查安装状态…');
  const action = toolchain?.installationAction;
  const details = toolchain?.installationDetails || [];
  return `<section class="feishu-toolchain feishu-overview" aria-busy="${Boolean(state.toolchainBusy)}"><h3 class="setting-title">Codex 飞书技能</h3><p class="setting-description">让 Codex 使用当前飞书接入的消息、文档、日历等能力。</p><p role="status">${escapeHTML(title)}${toolchain?.healthy ? ` · ${toolchain.skills.length} 项 · ksf-lark-*` : ''}</p><div class="feishu-self-test">${action ? `<button class="button" data-action="toolchain-install" ${state.toolchainBusy ? 'disabled' : ''}>${escapeHTML(action)}</button>` : !toolchain ? `<button class="button" data-action="toolchain-status" ${state.toolchainBusy ? 'disabled' : ''}>检查安装</button>` : ''}</div>${details.length ? `<details><summary>查看具体文件</summary>${details.map(d => `<p class="setting-description">${escapeHTML(d)}</p>`).join('')}</details>` : ''}${state.toolchainError ? `<p role="alert">${escapeHTML(state.toolchainError)}</p>` : ''}</section>`;

}

async function updateToolchain(install = false) {
  if (state.toolchainBusy) return;
  if (install && !window.confirm('安装当前应用内置的 ksf-lark-* 技能及必要执行入口？迁移本应用管理且未修改的旧版，不覆盖自行修改的文件，不自动授权或发送消息。')) return;
  state.toolchainBusy = true;
  state.toolchainError = '';
  render();
  try {
    state.toolchain = install ? await api.installToolchain(true) : await api.toolchainStatus();
    if (state.toolchain?.schemaVersion !== 1) throw new Error('unsupported schema');
  } catch {
    state.toolchain = null;
    state.toolchainError = install ? '工具链未安装完成；请检查现有文件冲突后重试。' : '无法检查官方工具链，请重试。';
  } finally {
    state.toolchainBusy = false;
    render();
  }
}

function applyFeishuConfiguration(snapshot, generation) {
  if (generation !== state.feishuConfigurationGeneration) return false;
  if (snapshot?.schemaVersion !== 2 || !snapshot.summary || typeof snapshot.summary.title !== 'string'
    || !Array.isArray(snapshot.facts) || !Array.isArray(snapshot.actions)
    || typeof snapshot.epoch !== 'string' || !snapshot.epoch || typeof snapshot.contextRevision !== 'string'
    || !Number.isSafeInteger(snapshot.revision) || snapshot.revision < 0) throw new Error('不支持的配置快照');
  if (snapshot.facts.some((fact) => !fact || typeof fact.id !== 'string' || typeof fact.title !== 'string')
    || snapshot.actions.some((action) => !action || typeof action.id !== 'string' || typeof action.title !== 'string' || typeof action.enabled !== 'boolean')
    || (snapshot.overview?.features && !Array.isArray(snapshot.overview.features))
    || (snapshot.connection?.targetAliases && !Array.isArray(snapshot.connection.targetAliases))) throw new Error('不支持的配置内容');
  const previous = state.feishuConfiguration;
  if (state.feishuRetiredEpochs.has(snapshot.epoch)) return false;
  if (previous?.epoch === snapshot.epoch && snapshot.revision < previous.revision) return false;
  if (previous && previous.epoch !== snapshot.epoch) state.feishuRetiredEpochs.add(previous.epoch);
  state.feishuConfiguration = snapshot;
  state.feishuReadError = '';
  if (previous?.flow?.kind === 'user' && !snapshot.flow && snapshot.auth?.identityValid) { state.feishuSetupError = ''; state.feishuActionFeedback = ''; state.feishuActionOutcome = 'completed'; }
  clearTimeout(state.feishuFlowTimer);
  if (snapshot.flow?.expiresAt) {
    const delay = Date.parse(snapshot.flow.expiresAt) - Date.now();
    if (Number.isFinite(delay) && delay > 0) state.feishuFlowTimer = setTimeout(() => render(), Math.min(delay + 1, 2147483647));
  }
  return true;
}

async function readFeishuConfiguration(refresh = false, quiet = false) {
  clearTimeout(state.feishuPollTimer);
  const generation = ++state.feishuConfigurationGeneration;
  state.feishuConfigurationLoading = true;
  state.feishuManualRefresh = state.feishuManualRefresh || refresh;
  if (!quiet) render();
  try {
    const snapshot = await api.readFeishuConfiguration({ refresh });
    applyFeishuConfiguration(snapshot, generation);
  } catch {
    if (generation === state.feishuConfigurationGeneration) state.feishuReadError = '状态更新失败，请刷新重试。';
  } finally {
    if (generation === state.feishuConfigurationGeneration) {
      state.feishuConfigurationLoading = false;
      state.feishuManualRefresh = state.feishuManualRefresh && !state.feishuReadError && state.feishuConfiguration?.refreshing === true;
      render();
      scheduleFeishuConfigurationPoll();
    }
  }
}

async function performFeishuConfigurationAction(action, options = {}) {
  if (["set_feature", "enable_outbound"].includes(action)) return;
  if (state.feishuSetupBusy || state.feishuReadError || state.feishuSetupError || feishuAction(action)?.enabled !== true) return;
  const snapshot = state.feishuConfiguration;
  const payload = { action, requestId: crypto.randomUUID(), epoch: snapshot.epoch, revision: snapshot.revision, contextRevision: snapshot.contextRevision, confirm: false };
  if (action === 'start_auth' && feishuAction(action)?.authorizationRequestId) payload.authorizationRequestId = feishuAction(action).authorizationRequestId;
  if (['finish_auth', 'finish_app', 'cancel_flow'].includes(action)) {
    if (!snapshot.flow?.id) return;
    payload.flowId = snapshot.flow.id;
  }
  if (action === 'test_message') {
    payload.targetAlias = snapshot.diagnostics?.selfTarget;
    if (!payload.targetAlias || !snapshot.connection?.targetAliases?.includes(payload.targetAlias)) return;
  }
  const generation = ++state.feishuConfigurationGeneration;
  clearTimeout(state.feishuPollTimer);
  state.feishuLastAction = action;
  state.feishuSetupError = '';
  state.feishuActionFeedback = '';
  state.feishuSetupBusy = true;
  render();
  try {
    const result = await api.actFeishuConfiguration(payload);
    if (generation !== state.feishuConfigurationGeneration) return;
    if (!['completed', 'pending', 'failed', 'unknown'].includes(result?.outcome)) throw new Error('不支持的操作结果');
    if (!applyFeishuConfiguration(result.snapshot, generation)) return;
    if (result.outcome === 'failed' && result.cancelled === true) {
      showToast(result.message || '已取消，未执行配置操作。');
      return;
    }
    if (result.outcome === 'unknown' || result.outcome === 'failed') {
      state.feishuSetupError = (feishuAction(action)?.title || action) + '：' + (result.message || '结果待核实');
    }
    state.feishuActionFeedback = result.message || '';
    state.feishuActionOutcome = result.outcome;
  } catch {
    if (generation === state.feishuConfigurationGeneration) { state.feishuSetupError = '操作结果未知，请刷新查看诊断；不会重复执行。'; state.feishuActionFeedback = state.feishuSetupError; }
  } finally {
    payload.appSecret = undefined;
    state.feishuSetupBusy = false;
    render();
    scheduleFeishuConfigurationPoll();
  }
}

function scheduleFeishuConfigurationPoll() {
  clearTimeout(state.feishuPollTimer);
  if (state.feishuSetupBusy || state.feishuConfigurationLoading) return;
  state.feishuPollTimer = setTimeout(() => {
    if (!state.feishuSetupBusy) void readFeishuConfiguration(false, true);
  }, 2500);
}

function selectHomeProjects(items = []) {
  return items.filter((item) => item.isPinned || item.tasks.some((task) => ['running', 'waiting'].includes(task.classification)));
}

function selectHomeWorkspaces(items = []) {
  return items.filter((item) => item.isPinned || item.runningCount > 0 || item.waitingCount > 0);
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

function taskLinkUnavailableReason() {
  const blockers = state.dashboard?.feishu?.readinessBlockers || [];
  if (blockers.includes('taskCardWriteDisabled')) return '飞书写操作已关闭，无法发送或更新任务卡片。请在飞书设置中检查写操作。';
  if (blockers.includes('taskCardWriteDryRun')) return '飞书写操作处于演练模式，不会实际发送任务卡片。';
  if (!state.dashboard?.feishu?.taskLinkReady) return '飞书消息或卡片服务未就绪，请在设置中检查飞书连接。';
  if (!state.settings?.selectedFeishuTargetAlias) return '请先在飞书设置中选择接收人。';
  return '';
}

async function refreshStaticData({ page = state.page, force = false } = {}) {
  const reads = [];
  if (!state.staticDataLoaded || force) {
    reads.push(api.settings().then((value) => { state.settings = value; }));
  }
  if (!state.staticDataLoaded || page === 'feishu' || force) reads.push(readFeishuConfiguration(force));
  if (!state.staticDataLoaded || page === 'pricing' || force) {
    reads.push(api.pricingCatalog().then((value) => { state.pricingCatalog = value; }));
  }
  if (page === 'feishu') reads.push(updateToolchain());
  await Promise.all(reads);
  state.staticDataLoaded = true;
}

async function refreshDashboard({ quiet = false, forceAccountRefresh = !quiet } = {}) {
  const clearAccountUsage = () => {
    if (!state.dashboard) return;
    state.dashboard = { ...state.dashboard, usage: { ...state.dashboard.usage, buckets: [], tokenSummary: null, dailyUsageBuckets: [], rateUpdatedAt: null, tokenUpdatedAt: null } };
  };
  if (state.refreshing) {
    state.pendingAccountRefresh = state.pendingAccountRefresh || forceAccountRefresh;
    return;
  }
  state.refreshing = true;
  if (!quiet || forceAccountRefresh) render();
  try {
    const dashboard = await api.dashboard(forceAccountRefresh);
    if (!state.pendingAccountRefresh) state.dashboard = dashboard;
    state.error = '';
  } catch (error) {
    clearAccountUsage();
    state.error = error.message;
    if (!quiet) showToast(error.message, true);
  } finally {
    state.loading = false;
    state.refreshing = false;
    render();
    if (state.pendingAccountRefresh) {
      state.pendingAccountRefresh = false;
      await refreshDashboard({ quiet: true, forceAccountRefresh: true });
    } else {
      scheduleRefresh();
    }
  }
}

function scheduleRefresh() {
  clearTimeout(state.timer);
  const hasLiveState = state.dashboard?.activity?.runningCount || state.dashboard?.activity?.waitingCount || state.dashboard?.feishu?.links?.some((link) => link.linkState === 'active');
  state.timer = setTimeout(() => refreshDashboard({ quiet: true }), hasLiveState ? 3_000 : 15_000);
}

async function handleAction(action, element) {
  if (action === 'feishu-configuration-check') return Promise.all([readFeishuConfiguration(true), updateToolchain()]);
  if (action.startsWith('feishu-config-')) return performFeishuConfigurationAction(action.slice('feishu-config-'.length));
  if (action === 'feishu-flow-open') {
    const flow = activeFeishuFlow();
    if (flow?.id === element.dataset.flowId && flow.verificationURL) {
      const snapshot = state.feishuConfiguration;
      return api.openFeishuFlow({ flowId: flow.id, epoch: snapshot.epoch, contextRevision: snapshot.contextRevision });
    }
    return;
  }
  if (action === 'toolchain-status') return updateToolchain();
  if (action === 'toolchain-install') return updateToolchain(true);
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
  else if (action === 'refresh') return state.page === 'feishu' ? readFeishuConfiguration(true) : refreshDashboard();
  else if (action === 'quit') return api.quit();
  else if (action === 'project-detail') { state.selectedProjectId = element.dataset.id; state.page = 'project'; }
  else if (action === 'workspaces') { state.page = 'workspaces'; }
  else if (action === 'task-detail') { state.selectedTaskId = element.dataset.task; state.page = 'task'; }
  else if (action === 'pin') { await api.setPinned(element.dataset.id, element.dataset.pinned !== 'true'); return refreshDashboard({ quiet: true }); }
  else if (action === 'workspace-pin') { await api.setWorkspacePinned(element.dataset.id, element.dataset.pinned !== 'true'); return refreshDashboard({ quiet: true }); }
  else if (action === 'choose-ksf') {
    const settings = await api.chooseDirectory('ksfRoot');
    if (settings) { state.settings = settings; return refreshDashboard(); }
  }
  else if (action === 'clear-ksf') {
    state.settings = await api.clearDirectory('ksfRoot');
    return refreshDashboard();
  }
  else if (action === 'open-task') return api.openTask(element.dataset.thread);
  else if (action.startsWith('open-folder:')) { const project = findProject(action.split(':').slice(1).join(':')); return api.openPath(project.projectDirectory); }
  else if (action.startsWith('workspace-open-folder:')) { const workspace = findWorkspace(action.split(':').slice(1).join(':')); return api.openWorkspacePath(workspace.id, workspace.path); }
  else if (action.startsWith('workspace-create-task:')) {
    const workspace = findWorkspace(action.split(':').slice(1).join(':'));
    showToast('正在创建任务…');
    await api.createWorkspaceTask({ workspaceId: workspace.id, path: workspace.path, name: workspace.name });
    showToast('任务已创建并在 Codex 中打开');
    return refreshDashboard({ quiet: true });
  }
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
    const reason = taskLinkUnavailableReason();
    if (element.dataset.linked !== 'true' && reason) { showToast(reason, true); return; }
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

function findWorkspace(id) {
  const workspace = state.dashboard?.workspaces?.workspaces?.find((item) => item.id === id);
  if (!workspace || workspace.kind !== 'workspace' || !workspace.path) throw new Error('工作区不存在或尚未同步');
  return workspace;
}

function findTask(id) {
  for (const item of state.dashboard?.projects?.projects || []) {
    const task = item.tasks?.find((candidate) => candidate.id === id);
    if (task) return { task, project: item.project, ksfUnassigned: item.kind === 'unassigned' };
  }
  for (const workspace of state.dashboard?.workspaces?.workspaces || []) {
    const task = workspace.tasks?.find((candidate) => candidate.id === id);
    if (task) return { task, project: null, workspace };
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

root.addEventListener('toggle', (event) => {
  const section = event.target.dataset.feishuSection;
  if (!section) return;
  state.feishuSections[section] = event.target.open;
  if (event.target.open) {
    for (const other of root.querySelectorAll('details[data-feishu-section]')) {
      if (other !== event.target) {
        other.open = false;
        state.feishuSections[other.dataset.feishuSection] = false;
      }
    }
  }
  reportHeight();
}, true);

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
      if (state.feishuSetupBusy) return;
      state.settings = await api.updateSettings({ selectedFeishuTargetAlias: event.target.value });
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
document.addEventListener('visibilitychange', () => {
  // Visibility affects rendering, never the authorization observation lifecycle.
  if (!document.hidden && !state.feishuSetupBusy && !state.feishuConfigurationLoading) {
    void readFeishuConfiguration(false, true);
  }
});
window.addEventListener('keydown', (event) => { if (event.key === 'Escape') state.page === 'home' ? api.hide() : handleAction('back', root); });
window.addEventListener('focus', () => refreshDashboard({ quiet: true, forceAccountRefresh: true }));

async function start() {
  await refreshStaticData({ page: state.page });
  await refreshDashboard();
}

start().catch((error) => {
  state.loading = false;
  state.error = error.message;
  render();
});
