<script setup>
defineProps({ name: String });
const paths = {
  catalog: 'M4 4h6v6H4z M14 4h6v6h-6z M4 14h6v6H4z M14 14h6v6h-6z',
  permissions: 'M12 3 4 6v6c0 5 8 9 8 9s8-4 8-9V6z M8 12l3 3 5-6',
  policy: 'M4 6h16 M4 12h16 M4 18h16 M8 3v6 M16 9v6 M10 15v6',
  diagnostics: 'M3 12h4l3-8 4 16 3-8h4',
  records: 'M6 3h12v18H6z M9 7h6 M9 11h6 M9 15h4',
  mark: 'M4 4v16 M4 12l8-8 M4 12l8 8 M16 4h4 M16 12h4 M16 20h4',
};
</script>
<template><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path :d="paths[name] || paths.catalog" /></svg></template>
