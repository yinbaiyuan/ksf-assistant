<script setup>
import { computed } from 'vue';
import { pretty } from '../api.js';
const props = defineProps({ value: {}, title: { default: '查看 JSON' }, open: Boolean });
const text = computed(() => pretty(props.value) ?? '无数据');
</script>

<template>
  <details class="json-view" :open="open">
    <summary>{{ title }}</summary>
    <pre tabindex="0" :aria-label="title">{{ text }}</pre>
  </details>
</template>
