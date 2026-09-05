<script setup>
import { computed, ref, watch } from 'vue';
import { api } from '../api.js';
import { decisionLabels, riskLabels } from '../../shared/model.mjs';
import ConfirmAction from './ConfirmAction.vue';
const props = defineProps({ policy: Object, catalog: Array });
const emit = defineEmits(['refresh', 'updated']);
const draft = ref(null), editing = ref(false), busy = ref(false), error = ref(''), chosen = ref(''), choice = ref('confirm_each');
const baseline = ref(null);
const clone = value => value ? JSON.parse(JSON.stringify(value)) : null;
const stale = computed(() => !props.policy || props.policy.revision !== baseline.value?.revision);
watch(() => props.policy, value => {
  if (editing.value) return;
  draft.value = clone(value); baseline.value = clone(value);
}, { immediate: true });
const differences = computed(() => {
  if (!draft.value || !baseline.value) return [];
  const changes = [];
  for (const section of ['riskDefaults', 'capabilityOverrides']) for (const key of new Set([...Object.keys(baseline.value[section] || {}), ...Object.keys(draft.value[section] || {})])) {
    const before = baseline.value[section]?.[key], after = draft.value[section]?.[key];
    if (before !== after) changes.push({ section, key, value: after, before: decisionLabels[before] || '继承默认', after: decisionLabels[after] || '继承默认' });
  }
  return changes;
});
function addOverride() {
  if (!props.catalog.some(item => item.id === chosen.value)) { error.value = '请选择目录中的完整能力 ID。'; return; }
  draft.value.capabilityOverrides[chosen.value] = choice.value; error.value = '';
}
function toggleEditing() {
  draft.value = clone(props.policy); baseline.value = clone(props.policy);
  editing.value = !editing.value; error.value = '';
}
function rebase() {
  if (!props.policy || busy.value) return;
  const next = clone(props.policy);
  for (const change of differences.value) {
    if (change.value === undefined) delete next[change.section][change.key];
    else next[change.section][change.key] = change.value;
  }
  baseline.value = clone(props.policy); draft.value = next; error.value = '';
}
async function save() {
  if (stale.value || busy.value) return;
  busy.value = true; error.value = '';
  try { const result = await api('policy', { policy: draft.value, expectedRevision: baseline.value.revision, acknowledge: true }); baseline.value = clone(result.policy); draft.value = clone(result.policy); emit('updated', result.policy); editing.value = false; }
  catch (issue) { error.value = `${issue.message} 若发生版本冲突，请重新读取后核对差异。`; }
  finally { busy.value = false; }
}
</script>
<template>
  <section class="page-section"><div class="section-heading"><div><h2>本机治理策略</h2><p class="muted">覆盖规则优先于风险默认 · revision {{ policy?.revision ?? '未知' }}</p></div><div class="action-row"><button :disabled="busy" @click="emit('refresh')">重新读取</button><button :disabled="(!policy && !editing) || busy" @click="toggleEditing">{{ editing ? '放弃草稿' : '编辑策略' }}</button></div></div>
    <div class="notice warning">策略影响所有客户端，不只实验台。“逐次确认”是本机 Operation 确认，不是飞书审批业务，也不会替你补授权或打开工作箱。</div>
    <div v-if="error" role="alert" class="notice error">{{ error }}</div>
    <div v-if="editing && stale" class="notice warning" role="status"><p>草稿基于 revision {{ baseline?.revision }}，最新策略{{ policy ? '版本已变化' : '未能读取' }}。本次编辑已保留；重新读取后核对差异，不能直接覆盖。</p><button :disabled="busy || !policy" @click="rebase">保留差异，重新核对最新版本</button></div>
    <template v-if="draft"><h3>风险默认</h3><table><thead><tr><th>风险</th><th>处理方式</th></tr></thead><tbody><tr v-for="(value, risk) in draft.riskDefaults" :key="risk"><td>{{ riskLabels[risk] || risk }}</td><td><select v-if="editing" v-model="draft.riskDefaults[risk]" :aria-label="`${risk} 默认决策`" :disabled="busy"><option v-for="key in ['allowed', 'confirm_each', 'disabled']" :key="key" :value="key">{{ decisionLabels[key] }}</option></select><span v-else class="badge" :class="value">{{ decisionLabels[value] }}</span></td></tr></tbody></table>
      <div class="section-heading"><h3>能力覆盖</h3><span class="muted">{{ Object.keys(draft.capabilityOverrides).length }} 条</span></div>
      <div v-if="editing" class="toolbar"><label class="grow">能力 ID<input v-model="chosen" list="policy-capabilities" placeholder="输入或选择目录中的能力 ID" :disabled="busy" /><datalist id="policy-capabilities"><option v-for="item in catalog" :key="item.id" :value="item.id" /></datalist></label><label>处理方式<select v-model="choice" :disabled="busy"><option v-for="key in ['allowed', 'confirm_each', 'disabled']" :key="key" :value="key">{{ decisionLabels[key] }}</option></select></label><button :disabled="busy || !chosen" @click="addOverride">加入草稿</button></div>
      <table v-if="Object.keys(draft.capabilityOverrides).length"><thead><tr><th>能力</th><th>覆盖</th><th v-if="editing">操作</th></tr></thead><tbody><tr v-for="(value, id) in draft.capabilityOverrides" :key="id"><td><code>{{ id }}</code></td><td><span class="badge" :class="value">{{ decisionLabels[value] }}</span></td><td v-if="editing"><button :disabled="busy" @click="delete draft.capabilityOverrides[id]">恢复继承</button></td></tr></tbody></table><p v-else class="empty-state">尚无能力覆盖，使用风险默认。</p>
      <section v-if="editing" class="result-section"><h3>待提交差异 · {{ differences.length }}</h3><ul class="change-list"><li v-for="item in differences" :key="item.key"><code>{{ item.key }}</code><span>{{ item.before }} → {{ item.after }}</span></li></ul><ConfirmAction :key="JSON.stringify(draft)" primary label="确认并保存策略" :disabled="busy || stale || !differences.length" :message="`将修改 ${differences.length} 条治理规则，影响所有客户端。请核对上方差异；原 revision 为 ${baseline.revision}。`" @confirm="save" /></section>
    </template><p v-else class="empty-state">未读取到策略，不能推断为直接放行。请确认应用正在运行。</p>
  </section>
</template>
