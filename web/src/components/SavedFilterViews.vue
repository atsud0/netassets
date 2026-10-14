<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface SavedFilterView {
  id: string
  page_key: string
  name: string
  filters: Record<string, string>
  is_default: boolean
  created_at: string
  updated_at: string
}

interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface ItemResponse<T> { item: T }

const props = defineProps<{
  pageKey: 'assets' | 'security_work_orders' | 'data_quality_issues'
  currentFilters: Record<string, string>
  disabled?: boolean
}>()
// 'changed' is emitted whenever the set of saved views changes, including when one is
// deleted. A deletion is a change to what the page is showing: the deleted view was
// the only name for the filters currently applied, so the host page has to reload its
// list rather than keep rendering results under a name that no longer exists.
const emit = defineEmits<{
  apply: [filters: Record<string, string>]
  changed: [views: SavedFilterView[]]
}>()

const views = ref<SavedFilterView[]>([])
const loading = ref(false)
const saving = ref(false)
const selectedID = ref('')
const dialogVisible = ref(false)
const viewName = ref('')
const makeDefault = ref(false)
const selectedView = computed(() => views.value.find(item => item.id === selectedID.value))

async function initialize(): Promise<Record<string, string> | undefined> {
  await loadViews()
  const defaultView = views.value.find(item => item.is_default)
  if (!defaultView) return undefined
  selectedID.value = defaultView.id
  return { ...defaultView.filters }
}

async function loadViews() {
  loading.value = true
  try {
    const response = await apiClient.get<ListResponse<SavedFilterView>>('/saved-filter-views', {
      params: { page_key: props.pageKey, page: 1, page_size: 20 },
    })
    views.value = response.data.items
    if (selectedID.value && !views.value.some(item => item.id === selectedID.value)) selectedID.value = ''
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取保存视图失败'))
  } finally {
    loading.value = false
  }
}

function applySelected(value: string) {
  const item = views.value.find(view => view.id === value)
  if (item) emit('apply', { ...item.filters })
}

function openSaveDialog() {
  viewName.value = ''
  makeDefault.value = false
  dialogVisible.value = true
}

async function saveView() {
  const name = viewName.value.trim()
  if (!name) {
    ElMessage.warning('请输入视图名称')
    return
  }
  saving.value = true
  try {
    const response = await apiClient.post<ItemResponse<SavedFilterView>>('/saved-filter-views', {
      page_key: props.pageKey,
      name,
      filters: props.currentFilters,
      is_default: makeDefault.value,
    })
    ElMessage.success('当前筛选已保存')
    dialogVisible.value = false
    await loadViews()
    selectedID.value = response.data.item.id
    emit('changed', views.value)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '保存筛选视图失败'))
  } finally {
    saving.value = false
  }
}

async function setDefault() {
  const item = selectedView.value
  if (!item || item.is_default) return
  try {
    await apiClient.patch(`/saved-filter-views/${item.id}`, {
      page_key: item.page_key,
      name: item.name,
      filters: item.filters,
      is_default: true,
    })
    await loadViews()
    selectedID.value = item.id
    emit('changed', views.value)
    ElMessage.success('已设为本页面默认视图')
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '设置默认视图失败'))
  }
}

async function deleteSelected() {
  const item = selectedView.value
  if (!item) return
  try {
    await ElMessageBox.confirm(`确认删除个人视图“${item.name}”吗？`, '删除保存视图', {
      confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning',
    })
    await apiClient.delete(`/saved-filter-views/${item.id}`)
    selectedID.value = ''
    await loadViews()
    emit('changed', views.value)
    ElMessage.success('保存视图已删除')
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '删除保存视图失败'))
  }
}

defineExpose({ initialize, loadViews })
</script>

<template>
  <div class="saved-filter-views">
    <el-select
      v-model="selectedID"
      :loading="loading"
      :disabled="disabled"
      clearable
      placeholder="已保存视图"
      class="saved-view-select"
      @change="applySelected"
    >
      <el-option v-for="item in views" :key="item.id" :value="item.id" :label="item.name">
        <span>{{ item.name }}</span><el-tag v-if="item.is_default" size="small" type="success" class="default-tag">默认</el-tag>
      </el-option>
    </el-select>
    <el-button :disabled="disabled" @click="openSaveDialog">保存当前筛选</el-button>
    <el-button :disabled="disabled || !selectedView || selectedView.is_default" @click="setDefault">设为默认</el-button>
    <el-button :disabled="disabled || !selectedView" type="danger" plain @click="deleteSelected">删除</el-button>

    <el-dialog v-model="dialogVisible" title="保存当前筛选" width="460px" append-to-body>
      <el-form label-position="top" @submit.prevent="saveView">
        <el-form-item label="视图名称" required>
          <el-input v-model="viewName" maxlength="100" show-word-limit placeholder="例如：待确认的公网域名" @keyup.enter="saveView" />
        </el-form-item>
        <el-form-item>
          <el-checkbox v-model="makeDefault">设为本页面默认视图</el-checkbox>
          <div class="default-help">下次进入本页面时，如没有 URL 指定筛选条件，将优先应用该视图。</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveView">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.saved-filter-views { display:flex; align-items:center; gap:8px; flex-wrap:wrap; }
.saved-filter-views > .el-button { margin-left:0; }
.saved-view-select { width:190px; }
.default-tag { float:right; margin-left:12px; }
.default-help { margin-top:7px; color:var(--muted); font-size:12px; line-height:1.55; }
</style>
