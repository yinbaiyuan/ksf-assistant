<script setup>
import { computed, reactive, ref, watch, onUnmounted } from 'vue';
import { api, query, pretty, timeLabel } from '../api.js';
import { decision, decisionLabels, riskLabels, parseFields, terminalStates } from '../../shared/model.mjs';
import JsonView from './JsonView.vue';

const props = defineProps({ capability: Object, policy: Object, seed: Object });
const emit = defineEmits(['result', 'save-case']);
const values = reactive({});
const files = reactive({});
const mode = ref('form');
const raw = ref('{}');
const error = ref('');
const busy = ref('');
const validation = ref(null);
const result = ref(null);
const acknowledge = ref(false);
const clock = ref(Date.now());
const timer = setInterval(() => { clock.value = Date.now(); }, 1000);
onUnmounted(() => clearInterval(timer));
const permission = computed(() => decision(props.capability, props.policy));
const proofValid = computed(() => validation.value && validation.value.expiresAt > clock.value);
const operation = computed(() => result.value?.operation);
const pending = computed(() => operation.value && !terminalStates.has(operation.value.status));
const fields = computed(() => props.capability.inputFields || []);
const invalidates = () => { validation.value = null; acknowledge.value = false; };
watch([values, files, raw, mode], invalidates, { deep: true });
watch(() => props.policy?.revision, invalidates);
watch(() => props.capability.id, () => {
  Object.keys(values).forEach(key => delete values[key]); Object.keys(files).forEach(key => delete files[key]);
  raw.value = '{}'; error.value = ''; result.value = null; invalidates();
}, { immediate: true });
watch(() => props.seed, seed => {
  if (!seed || seed.capabilityId !== props.capability.id) return;
  mode.value = 'json'; raw.value = pretty(seed.input); invalidates();
});

function input() {
  const value = mode.value === 'json' ? JSON.parse(raw.value) : parseFields(fields.value, values);
  if (!value || Array.isArray(value) || typeof value !== 'object') throw new Error('输入必须是 JSON 对象。');
  return value;
}

function switchMode(next) {
  try {
    if (next === 'json') raw.value = pretty(parseFields(fields.value, values));
    else {
      const value = input(); Object.keys(values).forEach(key => delete values[key]);
      for (const field of fields.value) if (value[field.name] !== undefined) values[field.name] = field.type === 'json' ? pretty(value[field.name]) : String(value[field.name]);
    }
    mode.value = next; error.value = '';
  } catch (issue) { error.value = issue.message; }
}

async function upload(name, event) {
  const file = event.target.files?.[0];
  if (!file) { delete files[name]; return; }
  if (file.size > 4 * 1024 * 1024) { error.value = '单次输入及文件合计最多 4 MiB。'; event.target.value = ''; return; }
  const bytes = new Uint8Array(await file.arrayBuffer());
  let encoded = '';
  for (let offset = 0; offset < bytes.length; offset += 8192) encoded += String.fromCharCode(...bytes.subarray(offset, offset + 8192));
  files[name] = { name: file.name, data: btoa(encoded) };
}

async function act(action) {
  if (busy.value) return;
  error.value = ''; busy.value = action;
  const start = performance.now();
  const capabilityId = props.capability.id;
  try {
    if (action === 'validate') {
      const next = await api('validate', { capabilityId, input: input(), files: { ...files } });
      validation.value = next; clock.value = Date.now();
    } else {
      let next;
      if (action === 'execute') {
        if (!proofValid.value || !acknowledge.value) throw new Error('请先校验参数并确认执行意图。');
        const body = { capabilityId, input: input(), files: { ...files }, proof: validation.value.proof, requestId: crypto.randomUUID(), acknowledge: true };
        invalidates();
        next = await api('execute', body);
      } else if (action === 'status') next = await query('operation', { id: operation.value.id });
      else {
        const verb = action === 'confirm' ? '确认并执行' : '取消';
        if (!window.confirm(`${verb} ${operation.value.id}？${action === 'confirm' ? '可能立即产生真实副作用。' : '已提交远端的动作不保证撤销。'}`)) return;
        next = await api('operation', { action, id: operation.value.id, challenge: action === 'confirm' ? result.value.challenge : undefined, acknowledge: true });
      }
      result.value = { ...next, challenge: next.challenge || (action === 'status' ? result.value?.challenge : undefined) };
      emit('result', { time: Date.now(), capabilityId, status: next.operation?.status || next.status, operationId: next.operation?.id, elapsedMs: Math.round(performance.now() - start), response: result.value });
    }
  } catch (issue) {
    error.value = issue.message;
    if (action !== 'validate') emit('result', { time: Date.now(), capabilityId, status: issue.code || 'failed', elapsedMs: Math.round(performance.now() - start) });
  } finally { busy.value = ''; }
}

function saveCase() {
  try { emit('save-case', { capabilityId: props.capability.id, input: input() }); error.value = ''; }
  catch (issue) { error.value = issue.message; }
}
</script>

<template>
  <section class="workbench" aria-label="能力测试工作区" :aria-busy="!!busy">
    <header class="workbench-header">
      <div class="tag-row"><span class="badge" :class="permission.value">{{ decisionLabels[permission.value] }}</span><span>{{ capability.identity }} 身份</span><span>{{ riskLabels[capability.risk] }}</span></div>
      <h2>{{ capability.id.split('.').slice(-2).join(' / ') }}</h2>
      <code class="full-id">{{ capability.id }}</code>
      <p class="muted">{{ permission.source }} · {{ capability.queue }} · {{ capability.backend || '服务适配器' }}</p>
    </header>
    <details class="contract"><summary>能力契约与执行边界</summary>
      <dl><dt>副作用 / 可逆性</dt><dd>{{ capability.effect }} / {{ capability.reversibility }}</dd><dt>真实预检</dt><dd>{{ capability.preflight || '未声明' }}</dd><dt>复读核验</dt><dd>{{ capability.reread || '未声明' }}</dd><dt>必要 scope</dt><dd>{{ capability.requiredScopes?.join('、') || '目录未逐项声明，不能据此视为无需权限' }}</dd><dt>并发冲突键</dt><dd>{{ capability.conflictKey?.join('、') || '服务决定' }}</dd><dt>参数约束</dt><dd>必填与类型见表单；枚举、范围和互斥规则以服务校验为准。</dd></dl>
      <JsonView :value="capability" title="完整能力声明" />
    </details>
    <div class="section-heading"><h3>请求参数</h3><div class="segmented" aria-label="编辑方式"><button :class="{ active: mode === 'form' }" :disabled="!!busy" @click="switchMode('form')">表单</button><button :class="{ active: mode === 'json' }" :disabled="!!busy" @click="switchMode('json')">JSON</button></div></div>
    <fieldset :disabled="!!busy">
      <template v-if="mode === 'form'">
        <div v-for="field in fields.filter(item => item.type !== 'path')" :key="field.name" class="field">
          <label :for="`field-${field.name}`">{{ field.name }} <span v-if="field.required" class="required">必填</span><span class="field-type">{{ field.type }}{{ field.private ? ' · 私有' : '' }}</span></label>
          <select v-if="field.type === 'boolean'" :id="`field-${field.name}`" v-model="values[field.name]"><option value="">不传此参数</option><option value="true">true</option><option value="false">false</option></select>
          <textarea v-else-if="field.type === 'json' || field.private || /text|content|message|description/.test(field.name)" :id="`field-${field.name}`" v-model="values[field.name]" :rows="field.type === 'json' ? 5 : 3" :placeholder="field.type === 'json' ? '输入合法 JSON' : '仅驻留当前会话，不自动保存'" spellcheck="false" />
          <input v-else :id="`field-${field.name}`" v-model="values[field.name]" :type="field.type === 'integer' ? 'number' : 'text'" :placeholder="field.type === 'enum' ? '枚举值由服务校验' : field.required ? '填写明确目标或参数' : '可选，留空不传'" autocomplete="off" />
        </div>
        <p v-if="!fields.length" class="muted">此能力无需输入参数。</p>
      </template>
      <div v-else class="field"><label for="request-json">输入对象（不支持服务器文件路径）</label><textarea id="request-json" v-model="raw" class="code-input" rows="13" spellcheck="false" /></div>
      <div v-for="field in fields.filter(item => item.type === 'path')" :key="field.name" class="field">
        <template v-if="!/^(output|output-dir|output-file|out)$/.test(field.name)"><label :for="`file-${field.name}`">{{ field.name }} <span v-if="field.required" class="required">必填</span><span class="field-type">附件 · 合计 4 MiB</span></label><input :id="`file-${field.name}`" :key="capability.id + field.name" type="file" @change="upload(field.name, $event)" /></template>
        <p v-else class="hint">{{ field.name }}：实验台不开放服务端导出路径，请在结果区查看 JSON。</p>
      </div>
    </fieldset>
    <div v-if="error" class="notice error" role="alert">{{ error }}</div>
    <div v-if="validation" class="notice" :class="proofValid ? 'success' : 'warning'" role="status">{{ proofValid ? '结构校验通过' : '校验已过期，请重新校验' }} · {{ timeLabel(validation.expiresAt) }} 到期<br /><span class="small">不是授权结果；真实预检与最新权限在执行时检查。</span></div>
    <label class="check-line"><input v-model="acknowledge" type="checkbox" :disabled="!!busy || !proofValid" />{{ capability.risk === 'read' ? '我确认读取范围；服务可能产生审计记录。' : '我确认目标与内容，执行可能产生真实写入。' }}</label>
    <div class="action-row"><button :disabled="!!busy" @click="act('validate')">{{ busy === 'validate' ? '校验中…' : '校验参数' }}</button><button class="primary" :disabled="!!busy || !proofValid || !acknowledge || ['disabled', 'unknown'].includes(permission.value) || pending" @click="act('execute')">{{ busy === 'execute' ? '执行中…' : permission.value === 'confirm_each' ? '创建待确认操作' : '执行能力' }}</button><button class="quiet" :disabled="!!busy" @click="saveCase">存为会话用例</button></div>
    <p v-if="permission.value === 'disabled'" class="hint">当前策略禁止执行；校验参数不会放宽策略。</p>
    <section v-if="result" class="result-section" aria-live="polite">
      <div class="section-heading"><h3>执行结果</h3><span class="badge" :class="operation?.status === 'succeeded' ? 'allowed' : 'unknown'">{{ operation?.status || result.status }}</span></div>
      <template v-if="operation"><code class="full-id">{{ operation.id }}</code><p>{{ operation.summary }}</p><p v-if="operation.errorCode" class="notice error">{{ operation.errorCode }}</p><p v-if="operation.nextAction" class="hint">下一步：{{ operation.nextAction }}</p>
        <div v-if="operation.status === 'awaiting_confirmation'" class="notice warning"><strong>等待本机逐次确认</strong><p>不是飞书审批流。请核对上方 Operation 摘要；{{ timeLabel(operation.challengeExpiresAt) }} 后过期。</p><div class="action-row"><button class="primary" :disabled="!!busy || !result.challenge" @click="act('confirm')">确认并执行</button><button :disabled="!!busy" @click="act('cancel')">取消操作</button></div></div>
        <p v-if="operation.status === 'outcome_unknown'" class="notice warning">结果未知不等于失败。先核对远端及审计，不能自动重试。</p>
        <div class="action-row"><button :disabled="!!busy" @click="act('status')">查询原操作状态</button><button v-if="pending && operation.status !== 'awaiting_confirmation'" :disabled="!!busy" @click="act('cancel')">请求取消</button></div>
      </template>
      <JsonView :value="{ ...result, challenge: result.challenge ? '[仅用于当前会话确认]' : undefined }" title="服务返回（文本展示，不自动导出）" open />
    </section>
  </section>
</template>
