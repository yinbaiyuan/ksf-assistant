<script setup>
import { computed, onMounted, reactive, ref, watch } from 'vue';
import { query, timeLabel } from './api.js';
import { decision, decisionLabels, filterCapabilities, riskLabels } from '../shared/model.mjs';
import Workbench from './components/Workbench.vue';
import Permissions from './components/Permissions.vue';
import Policy from './components/Policy.vue';
import Diagnostics from './components/Diagnostics.vue';
import Records from './components/Records.vue';
import Icon from './components/Icon.vue';

const tabs = { catalog: ['能力实验', '选择能力，校验参数，再发起受控测试。'], permissions: ['身份与权限', '查看真实授权，区分平台权限和本机治理。'], policy: ['治理策略', '直接放行、禁止、逐次确认，一处解释。'], diagnostics: ['服务诊断', '观察连接、工作箱与事件，不自动修复。'], records: ['运行记录', '沿原 Operation 核验结果，避免重复执行。'] };
const tab = ref('catalog'), catalog = ref([]), policy = ref(null), snapshot = ref(null), selectedId = ref(''), busy = ref(false), errors = ref([]), updated = ref(null), page = ref(1), history = ref([]), cases = ref([]), seed = ref(null), favorites = ref([]), onlyFavorites = ref(false);
const filters = reactive({ query: '', domain: '', identity: '', risk: '', permission: '' });
const selected = computed(() => catalog.value.find(item => item.id === selectedId.value));
const domains = computed(() => [...new Set(catalog.value.map(item => item.domain))].sort());
const filtered = computed(() => filterCapabilities(catalog.value, filters, policy.value).filter(item => !onlyFavorites.value || favorites.value.includes(item.id)));
const pageCount = computed(() => Math.max(1, Math.ceil(filtered.value.length / 25)));
const visible = computed(() => filtered.value.slice((page.value - 1) * 25, page.value * 25));
const pending = computed(() => history.value.filter(row => row.status === 'awaiting_confirmation').length);
const connected = computed(() => snapshot.value?.processRunning === true);
const summary = computed(() => catalog.value.reduce((counts, item) => { counts[decision(item, policy.value).value]++; return counts; }, { allowed: 0, disabled: 0, confirm_each: 0, unknown: 0 }));
watch([filters, onlyFavorites], () => { page.value = 1; }, { deep: true });
watch(pageCount, maximum => { page.value = Math.min(page.value, maximum); });

async function refresh() {
  if (busy.value) return;
  busy.value = true; errors.value = [];
  const sources = await Promise.allSettled([query('catalog'), query('policy'), query('snapshot')]);
  if (sources[0].status === 'fulfilled') {
    catalog.value = sources[0].value.capabilities || [];
    if (!selected.value) selectedId.value = catalog.value[0]?.id || '';
  }
  if (sources[1].status === 'fulfilled') policy.value = sources[1].value.policy;
  else policy.value = null;
  if (sources[2].status === 'fulfilled') snapshot.value = sources[2].value;
  else snapshot.value = null;
  sources.forEach((source, index) => { if (source.status === 'rejected') errors.value.push(`${['能力目录', '治理策略', '服务快照'][index]}：${source.reason.message}`); });
  updated.value = Date.now(); busy.value = false;
}

function record(row) {
  if (row.operationId) history.value = history.value.filter(item => item.operationId !== row.operationId);
  const response = row.response ? { status: row.response.status, challenge: row.status === 'awaiting_confirmation' ? row.response.challenge : undefined, operation: row.response.operation ? { id: row.response.operation.id, status: row.response.operation.status, summary: row.response.operation.summary, challengeExpiresAt: row.response.operation.challengeExpiresAt } : undefined } : undefined;
  history.value = [{ ...row, response }, ...history.value].slice(0, 100);
}

function saveCase(value) {
  cases.value = [{ ...value, name: `${value.capabilityId.split('.').slice(-2).join(' / ')} · ${timeLabel(Date.now())}`, id: crypto.randomUUID() }, ...cases.value].slice(0, 12);
}

function useCase(value) { selectedId.value = value.capabilityId; seed.value = { ...value }; }
function favorite() {
  favorites.value = favorites.value.includes(selectedId.value) ? favorites.value.filter(id => id !== selectedId.value) : [...favorites.value, selectedId.value];
}
onMounted(refresh);
</script>

<template>
  <a class="skip-link" href="#main">跳到工作区</a>
  <div class="app-shell">
    <aside class="navigation"><div class="brand"><span class="brand-mark"><Icon name="mark" /></span><div><strong>KSFAssistant</strong><small>飞书实验台</small></div></div>
      <nav aria-label="主导航"><button v-for="(content, name) in tabs" :key="name" :class="{ current: tab === name }" :aria-current="tab === name ? 'page' : undefined" @click="tab = name"><Icon :name="name" /><span>{{ content[0] }}</span><span v-if="name === 'records' && pending" class="nav-count">{{ pending }}</span></button></nav>
      <div class="navigation-footer"><span class="connection-dot" :class="{ connected }" /><strong>{{ connected ? 'Core 网关已连接' : '等待应用连接' }}</strong><p>仅本机访问<br />不会自动启动飞书服务</p><span class="small">Vue 3 · Local client</span></div>
    </aside>
    <main id="main"><header class="page-header"><div><h1>{{ tabs[tab][0] }}</h1><p>{{ tabs[tab][1] }}</p></div><div class="refresh-group"><span class="muted">更新于 {{ timeLabel(updated) }}</span><button :disabled="busy" @click="refresh">{{ busy ? '读取中…' : '刷新目录与状态' }}</button></div></header>
      <div v-if="errors.length" class="notice error global-notice" role="alert"><strong>部分数据未就绪</strong><p v-for="error in errors" :key="error">{{ error }}</p><span>仍可浏览静态目录；业务不会离线排队。</span></div>
      <div v-if="snapshot?.readinessBlockers?.length" class="notice warning global-notice" role="status">服务报告限制：{{ snapshot.readinessBlockers.join('、') }}。基础连通不代表所有能力已通过。</div>
      <section v-show="tab === 'catalog'" class="catalog-page">
        <div class="catalog-summary"><span><strong>{{ catalog.length }}</strong> 项已发布能力</span><span v-for="name in ['allowed', 'confirm_each', 'disabled', 'unknown']" :key="name" class="summary-item"><i :class="name" />{{ decisionLabels[name] }} {{ summary[name] }}</span><span class="muted">策略 revision {{ policy?.revision ?? '—' }}</span></div>
        <div class="filters"><label class="search-field">搜索能力<input v-model="filters.query" type="search" placeholder="按能力 ID、业务域、风险检索…" /></label><label>业务域<select v-model="filters.domain"><option value="">全部业务域</option><option v-for="domain in domains" :key="domain">{{ domain }}</option></select></label><label>身份<select v-model="filters.identity"><option value="">全部身份</option><option>bot</option><option>user</option></select></label><label>风险<select v-model="filters.risk"><option value="">全部风险</option><option v-for="(label, value) in riskLabels" :key="value" :value="value">{{ label }}</option></select></label><label>治理<select v-model="filters.permission"><option value="">全部决策</option><option v-for="(label, value) in decisionLabels" :key="value" :value="value">{{ label }}</option></select></label></div>
        <div class="catalog-layout"><section class="catalog-list" aria-label="能力目录"><div class="list-heading"><span>{{ filtered.length }} 项匹配</span><label class="inline-check"><input v-model="onlyFavorites" type="checkbox" />仅收藏</label></div>
          <div v-if="busy && !catalog.length" class="empty-state">正在读取安装版能力目录…</div>
          <div v-else-if="!visible.length" class="empty-state"><h3>没有匹配能力</h3><p>试试清空关键词或放宽筛选。明确排除项可在服务诊断查看。</p></div>
          <button v-for="item in visible" :key="item.id" class="capability-row" :class="{ selected: selectedId === item.id }" :aria-pressed="selectedId === item.id" @click="selectedId = item.id"><div><span class="domain-label">{{ item.domain }}</span><span class="small">{{ riskLabels[item.risk] }} · {{ item.identity }}</span></div><strong>{{ item.id.split('.').slice(1).join(' / ') }}</strong><code>{{ item.id }}</code><span class="row-decision" :class="decision(item, policy).value">{{ decisionLabels[decision(item, policy).value] }}<span v-if="favorites.includes(item.id)"> · 已收藏</span></span></button>
          <footer class="pagination"><button :disabled="page <= 1" @click="page--">上一页</button><span>{{ page }} / {{ pageCount }}</span><button :disabled="page >= pageCount" @click="page++">下一页</button></footer>
        </section><div class="inspector"><div v-if="selected" class="inspector-tools"><span class="small">请求只驻留当前会话</span><button class="quiet" @click="favorite">{{ favorites.includes(selectedId) ? '取消收藏' : '收藏能力' }}</button></div><Workbench v-if="selected" :key="selected.id" :capability="selected" :policy="policy" :seed="seed" @result="record" @save-case="saveCase" /><div v-else class="empty-state"><h2>从能力目录开始</h2><p>选择一项能力，检查它的风险、身份和输入，再进行人工测试。</p></div></div></div>
        <section v-if="cases.length" class="session-cases"><div class="section-heading"><h3>会话用例 · {{ cases.length }}/12</h3><button class="quiet" @click="cases = []">清空用例</button></div><p class="hint">只存内存，不保存文件内容；刷新页面即清除。加载后仍需重新校验。</p><div class="case-list"><button v-for="item in cases" :key="item.id" @click="useCase(item)">{{ item.name }}</button></div></section>
      </section>
      <Permissions v-if="tab === 'permissions'" />
      <Policy v-if="tab === 'policy'" :policy="policy" :catalog="catalog" @refresh="refresh" @updated="policy = $event" />
      <Diagnostics v-if="tab === 'diagnostics'" :snapshot="snapshot" />
      <Records v-if="tab === 'records'" :history="history" @clear="history = []" @result="record" />
      <footer class="page-footer"><span>浏览器 → 本机适配器 → 原生 CLI → Core → 飞书服务</span><span>真实执行不会自动重试</span></footer>
    </main>
  </div>
</template>
