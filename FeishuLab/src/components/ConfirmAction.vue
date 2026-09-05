<script setup>
import { ref, watch } from 'vue';
const props = defineProps({ label: String, message: String, disabled: Boolean, primary: Boolean });
const emit = defineEmits(['confirm']);
const opened = ref(false);
watch(() => props.disabled, value => { if (value) opened.value = false; });
function submit() { opened.value = false; emit('confirm'); }
</script>
<template>
  <div class="confirmation-action">
    <button :class="{ primary }" :disabled="disabled" :aria-expanded="opened" @click="opened = !opened">{{ label }}</button>
    <div v-if="opened" class="notice warning confirmation-prompt" role="group" :aria-label="`${label}：二次确认`">
      <strong>{{ label }}：二次确认</strong><p>{{ message }}</p>
      <div class="action-row"><button class="primary" :disabled="disabled" @click="submit">已核对，继续</button><button @click="opened = false">返回核对</button></div>
    </div>
  </div>
</template>
<style scoped>
.confirmation-action { max-width: 100%; }
.confirmation-prompt { margin-block: 12px; max-width: 60ch; overflow-wrap: anywhere; }
</style>
