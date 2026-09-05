<script setup>
import { ref } from 'vue';
import { query, timeLabel } from '../api.js';
import JsonView from './JsonView.vue';
defineProps({ snapshot: Object });
const selected = ref('status'), limit = ref(20), fingerprint = ref(''), data = ref(null), error = ref(''), busy = ref(false), updated = ref(null), resultTitle = ref('');
const options = { status: '进程与入站连接', doctor: '基础健康检查', eventStatus: '事件收件箱状态', eventCatalog: '固定事件目录', events: '最近事件（有限读取）', event: '按指纹查看单条事件', targets: '目标别名（不修改绑定）', links: 'Core 任务关联（只读）', capabilities: '能力与明确排除项' };
async function read() {
  busy.value = true; error.value = '';
  try { data.value = await query(selected.value, selected.value === 'events' ? { limit: limit.value } : selected.value === 'event' ? { fingerprint: fingerprint.value.trim() } : {}); updated.value = Date.now(); resultTitle.value = options[selected.value]; }
  catch (issue) { error.value = issue.message; }
  finally { busy.value = false; }
}
</script>
<template>
  <section class="page-section"><h2>服务与事件诊断</h2><p class="muted">只通过现有服务查询。不重启、不修改配置、不创建事件消费者。</p>
    <table v-if="snapshot?.capabilities"><thead><tr><th>组件</th><th>状态</th><th>说明</th></tr></thead><tbody><tr v-for="(health, name) in snapshot.capabilities" :key="name"><td><code>{{ name }}</code></td><td><span class="badge" :class="health.state === 'ready' ? 'allowed' : health.state === 'degraded' ? 'confirm_each' : 'unknown'">{{ health.state }}</span></td><td>{{ health.detail || '—' }}</td></tr></tbody></table>
    <div class="toolbar"><label class="grow">诊断项目<select v-model="selected" :disabled="busy"><option v-for="(label, value) in options" :key="value" :value="value">{{ label }}</option></select></label><label v-if="selected === 'events'">条数上限<input v-model.number="limit" type="number" min="1" max="50" /></label><label v-if="selected === 'event'">事件指纹<input v-model="fingerprint" placeholder="使用服务返回的 fingerprint" /></label><button class="primary" :disabled="busy" @click="read">{{ busy ? '读取中…' : '读取诊断' }}</button></div>
    <p class="hint">长连接就绪不等于卡片回调和消息业务闭环已通过。诊断结果可能含私有信息，请勿直接截图外传。</p>
    <div v-if="error" class="notice error" role="alert">{{ error }}</div>
    <p v-if="updated" class="muted">读取于 {{ timeLabel(updated) }}</p><JsonView v-if="data" :value="data" :title="resultTitle" open /><div v-else class="empty-state">选择一项诊断，按需读取；不自动扫描业务内容。</div>
  </section>
</template>
