<script setup>
import { ref } from 'vue';
import { api, query, timeLabel, downloadReport } from '../api.js';
import { reportRows } from '../../shared/model.mjs';
import JsonView from './JsonView.vue';
const props = defineProps({ history: Array });
const emit = defineEmits(['clear', 'result']);
const mode = ref('operation'), box = ref('actionbox'), id = ref(''), limit = ref(20), data = ref(null), error = ref(''), busy = ref(false);
async function read() {
  busy.value = true; error.value = '';
  try { data.value = await query(mode.value, mode.value === 'operation' ? { id: id.value.trim() } : mode.value === 'recent' ? { box: box.value, limit: limit.value } : { box: box.value, id: id.value.trim() }); }
  catch (issue) { error.value = issue.message; }
  finally { busy.value = false; }
}
async function approval(row, action) {
  if (busy.value || !window.confirm(`${action === 'confirm' ? '确认并执行' : '取消'} ${row.operationId}？请先核对摘要与目标。`)) return;
  busy.value = true; error.value = '';
  try {
    const result = await api('operation', { action, id: row.operationId, challenge: action === 'confirm' ? row.response.challenge : undefined, acknowledge: true });
    emit('result', { ...row, time: Date.now(), status: result.operation?.status || result.status, response: result });
    data.value = result;
  } catch (issue) { error.value = issue.message; }
  finally { busy.value = false; }
}
</script>
<template>
  <section class="page-section"><div class="section-heading"><div><h2>运行记录</h2><p class="muted">当前页面会话 · 不自动保存输入、正文或 challenge</p></div><div class="action-row"><button :disabled="!history.length" @click="downloadReport(reportRows(props.history))">导出脱敏摘要</button><button :disabled="!history.length" @click="emit('clear')">清空本页记录</button></div></div>
    <table v-if="history.length"><thead><tr><th>时间 / 耗时</th><th>能力 / Operation</th><th>状态</th></tr></thead><tbody><tr v-for="(row, index) in history" :key="index"><td>{{ timeLabel(row.time) }}<small>{{ row.elapsedMs }} ms</small></td><td><code>{{ row.capabilityId }}</code><small><code>{{ row.operationId || '无 Operation 返回' }}</code></small></td><td>{{ row.status }}<button v-if="row.operationId" class="quiet" @click="id = row.operationId; mode = 'operation'; read()">查询</button></td></tr></tbody></table><p v-else class="empty-state">尚无会话运行。去能力目录校验参数，再选择一个明确目标进行测试。</p>
    <section v-for="row in history.filter(item => item.status === 'awaiting_confirmation')" :key="row.operationId" class="notice warning"><h3>待逐次确认 · {{ row.capabilityId }}</h3><code>{{ row.operationId }}</code><p>{{ row.response?.operation?.summary }}</p><p>有效期至 {{ timeLabel(row.response?.operation?.challengeExpiresAt) }}，以服务最新状态为准。</p><div class="action-row"><button :disabled="busy || !row.response?.challenge" @click="approval(row, 'confirm')">确认并执行此操作</button><button :disabled="busy" @click="approval(row, 'cancel')">取消此操作</button></div></section>
    <h3>查询服务权威记录</h3><div class="toolbar"><label>查询类型<select v-model="mode"><option value="operation">Operation 状态</option><option value="recent">最近工作箱 / 审计</option><option value="result">工作箱结果 ID</option></select></label><label v-if="mode !== 'operation'">工作箱<select v-model="box"><option>actionbox</option><option>outbox</option><option>docbox</option><option v-if="mode === 'recent'">audit</option></select></label><label v-if="mode !== 'recent'" class="grow">{{ mode === 'operation' ? 'OP-…' : 'OUT- / DOC- / ACT- / CAP-…' }}<input v-model="id" placeholder="粘贴原请求 ID，不创建新请求" autocomplete="off" /></label><label v-else>条数<input v-model.number="limit" type="number" min="1" max="50" /></label><button :disabled="busy" @click="read">{{ busy ? '查询中…' : '查询原记录' }}</button></div>
    <p class="hint">等待超时、结果未知时先查询原记录。不要靠重新执行来确认成功。清空本页不会删除服务审计。</p><div v-if="error" class="notice error" role="alert">{{ error }}</div><JsonView v-if="data" :value="data" title="权威查询结果" open />
  </section>
</template>
