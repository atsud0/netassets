<script setup lang="ts">
withDefaults(defineProps<{ label?: string; selection?: number; actionsVisible?: boolean }>(), { label: '列表工具栏', actionsVisible: true })
</script>

<template>
  <div v-if="selection === undefined || selection > 0" class="list-toolbar" :class="{ 'list-toolbar-selection': selection !== undefined }" role="group" :aria-label="label">
    <div class="list-toolbar-main">
      <span v-if="selection !== undefined" class="list-toolbar-summary" role="status">已选 {{ selection }} 项</span>
      <slot />
    </div>
    <div v-if="$slots.actions" v-show="actionsVisible" class="list-toolbar-end"><slot name="actions" /></div>
  </div>
</template>
