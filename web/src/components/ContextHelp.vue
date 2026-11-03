<script setup lang="ts">
withDefaults(defineProps<{ label?: string }>(), { label: '操作说明' })
</script>

<template>
  <details class="context-help">
    <summary>{{ label }}</summary>
    <div class="context-help-content"><slot /></div>
  </details>
</template>
