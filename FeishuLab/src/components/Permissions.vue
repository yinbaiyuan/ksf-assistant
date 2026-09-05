<script setup>
import { computed, ref } from 'vue';
import { query, timeLabel } from '../api.js';
import JsonView from './JsonView.vue';
const report = ref(null), busy = ref(false), error = ref(''), updated = ref(null), search = ref(''), group = ref('missing');
const identities = computed(() => report.value?.permissions?.identities || {});
const scopes = computed(() => (identities.value.user?.[group.value] || []).filter(scope => scope.toLowerCase().includes(search.value.toLowerCase())));
async function refresh() {
  busy.value = true; error.value = '';
  try { report.value = await query('permissions'); updated.value = Date.now(); }
  catch (issue) { error.value = issue.message; }
  finally { busy.value = false; }
}
</script>
<template>
  <section class="page-section">
    <div class="section-heading"><div><h2>身份与权限</h2><p class="muted">{{ timeLabel(updated) }} · 在线校验身份与 scope，不会发起授权或修改权限。</p></div><button class="primary" :disabled="busy" @click="refresh">{{ busy ? '在线核验中…' : '核验当前授权' }}</button></div>
    <div v-if="error" class="notice error" role="alert">{{ error }}</div>
    <div v-if="!report" class="empty-state"><h3>先区分两种身份</h3><p>机器人身份负责消息与事件，用户身份负责已授权的个人能力。点击核验读取真实状态；未核验不会显示为通过。</p></div>
    <template v-else>
      <div class="identity-grid"><article v-for="name in ['bot', 'user']" :key="name" class="identity"><div class="section-heading"><h3>{{ name === 'bot' ? '机器人 · bot' : '用户 · user' }}</h3><span class="badge" :class="identities[name]?.ready ? 'allowed' : 'disabled'">{{ identities[name]?.ready ? '身份可用' : '身份未就绪' }}</span></div><p v-if="name === 'bot'">平台未暴露逐项 bot scope 校验；身份可用不代表每个资源可访问。</p><template v-else><p>有效授权 {{ identities.user?.grantedCount ?? '未知' }} 项 · 必要 scope {{ identities.user?.requiredCount ?? '未知' }} 项</p><p>{{ identities.user?.complete ? '声明所需权限已覆盖' : '存在缺项，按实际测试能力核对，勿盲目补齐全部权限。' }}</p><dl><dt>应用权限</dt><dd>{{ identities.user?.application ? identities.user.application.complete ? '完整' : '存在缺项' : '未报告' }}</dd><dt>OAuth 授权</dt><dd>{{ identities.user?.oauth ? identities.user.oauth.complete ? '完整' : '存在缺项' : '未报告' }}</dd></dl></template></article></div>
      <div class="notice">权限是否具备、本机治理是否放行、服务开关是否启用是三件不同的事。还需满足飞书资源自身的可见性与成员权限。</div>
      <div class="toolbar"><label>scope 集合<select v-model="group"><option value="missing">缺失权限</option><option value="excess">额外权限（不代表已支持）</option></select></label><label class="grow">搜索 scope<input v-model="search" type="search" placeholder="例如 im:message、calendar" /></label><span class="muted">{{ scopes.length }} 项</span></div>
      <ul class="scope-list"><li v-for="scope in scopes" :key="scope"><code>{{ scope }}</code></li></ul><p v-if="!scopes.length" class="empty-state">该集合下没有匹配项。</p>
      <JsonView :value="report" title="完整授权比较（不含凭据）" />
    </template>
  </section>
</template>
