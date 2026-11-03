<script setup lang="ts">
import { nextTick, ref, useAttrs, watch } from 'vue'
import { resetListScroll } from '../utils/list-scroll'

defineOptions({ inheritAttrs: false })
const attrs = useAttrs()
const props = defineProps<{ currentPage?: number; pageSize?: number; total?: number; resetKey?: unknown }>()
const emit = defineEmits<{ 'current-change': [page: number]; 'size-change': [size: number] }>()
const pagination = ref<{ $el: HTMLElement }>()

async function resetScroll() {
  await nextTick()
  if (pagination.value) resetListScroll(pagination.value.$el)
}

function changePage(page: number) {
  emit('current-change', page)
  void resetScroll()
}

function changeSize(size: number) {
  emit('size-change', size)
  void resetScroll()
}

watch(() => [props.currentPage, props.pageSize, props.total, props.resetKey], resetScroll, { flush: 'post' })
</script>

<template>
  <el-pagination ref="pagination" v-bind="attrs" :current-page="currentPage" :page-size="pageSize" :total="total" @current-change="changePage" @size-change="changeSize" />
</template>
