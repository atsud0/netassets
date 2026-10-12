# Shared components
Element Plus supplies button, input, card, table, tag, form, dialog, drawer, and pagination primitives. Project wrappers:

## web/src/components/SavedFilterViews.vue
```vue
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
const emit = defineEmits<{ apply: [filters: Record<string, string>] }>()

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

```

## web/src/components/WorkingVersionDiffDrawer.vue
```vue
<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface FieldChange {
  field: string
  label: string
  before: unknown
  after: unknown
}

interface WorkingVersionDiff {
  resource_type: 'network_asset' | 'business_system'
  draft_id: string
  source_id?: string
  organization_id: string
  task_id?: string
  operation: 'create' | 'update' | 'delete'
  draft_status: 'editing' | 'submitted' | 'returned'
  resource_key: string
  resource_name: string
  changes: FieldChange[]
}

interface DiffResponse { working_version_diff: WorkingVersionDiff }

const props = defineProps<{ resourceType: 'network_asset' | 'business_system' }>()
const visible = ref(false)
const loading = ref(false)
const diff = ref<WorkingVersionDiff | null>(null)

const operationLabels: Record<string, string> = { create: '首次上报', update: '变更上报', delete: '下线申请' }
const operationTagTypes: Record<string, 'success' | 'warning' | 'danger'> = { create: 'success', update: 'warning', delete: 'danger' }
const statusLabels: Record<string, string> = { editing: '编辑中', submitted: '待审核', returned: '已退回' }
const valueLabels: Record<string, string> = {
  yes: '是', no: '否', unknown: '未知', partial: '部分', pending: '待确认',
  normal: '一般', important: '重要', core: '核心',
  planning: '规划中', running: '运行中', suspended: '暂停', offline: '已下线', archived: '已归档',
  active: '启用', disabled: '停用', production: '生产', staging: '预发布', testing: '测试', development: '开发', office: '办公', other: '其他',
  public: '公网', private: '内网', domain: '域名', ip: 'IP 地址', url: 'URL', service: '服务',
}

const title = computed(() => diff.value ? `${diff.value.resource_name} · 工作版本变更` : '工作版本变更')
const contextText = computed(() => {
  if (!diff.value) return ''
  if (diff.value.operation === 'create') return '该资源尚无集团正式版本，通过审核后将首次进入集团台账。'
  if (diff.value.operation === 'delete') return '该工作版本申请将资源从集团正式台账下线；原正式数据保留在下方用于确认。'
  if (!diff.value.changes.length) return '工作版本与当前集团正式版本没有字段差异。'
  return `工作版本相对集团正式版本共有 ${diff.value.changes.length} 个字段发生变化。`
})

function formatValue(value: unknown) {
  if (value === null || value === undefined || value === '') return '—'
  if (Array.isArray(value)) return value.length ? value.map((item) => valueLabels[String(item)] || String(item)).join('、') : '—'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') return new Intl.NumberFormat('zh-CN').format(value)
  if (typeof value === 'object') return JSON.stringify(value)
  return valueLabels[String(value)] || String(value)
}

async function open(draftID: string) {
  visible.value = true
  loading.value = true
  diff.value = null
  try {
    const response = await apiClient.get<DiffResponse>(`/inventory-tasks/working-versions/${props.resourceType}/${draftID}/diff`)
    diff.value = response.data.working_version_diff
  } catch (error) {
    visible.value = false
    ElMessage.error(getAPIErrorMessage(error, '读取工作版本变更失败'))
  } finally {
    loading.value = false
  }
}

defineExpose({ open })
</script>

<template>
  <el-drawer v-model="visible" :title="title" size="760px" destroy-on-close>
    <div v-loading="loading" class="working-version-diff">
      <template v-if="diff">
        <div class="working-version-diff-heading">
          <div>
            <span>资源标识</span>
            <strong>{{ diff.resource_key }}</strong>
          </div>
          <div class="working-version-diff-tags">
            <el-tag :type="operationTagTypes[diff.operation]" effect="plain">{{ operationLabels[diff.operation] }}</el-tag>
            <el-tag type="info" effect="plain">{{ statusLabels[diff.draft_status] || diff.draft_status }}</el-tag>
          </div>
        </div>
        <el-alert
          :title="contextText"
          :type="diff.operation === 'delete' ? 'warning' : 'info'"
          :closable="false"
          show-icon
          class="working-version-diff-alert"
        />
        <div v-if="diff.changes.length" class="working-version-change-list">
          <div v-for="change in diff.changes" :key="change.field" class="submission-field-change">
            <strong>{{ change.label }}</strong>
            <span class="before">{{ formatValue(change.before) }}</span>
            <span class="change-arrow">→</span>
            <span class="after">{{ formatValue(change.after) }}</span>
          </div>
        </div>
        <el-empty v-else :description="diff.operation === 'delete' ? '本次仅申请下线，没有修改资源字段' : '工作版本与集团正式版本一致'" />
      </template>
    </div>
  </el-drawer>
</template>

```

## web/src/components/ResourceSubmissionHistoryDrawer.vue
```vue
<script setup lang="ts">
import { computed, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface FieldChange {
  field: string
  label: string
  before: unknown
  after: unknown
}

interface SubmissionHistoryItem {
  submission_id: string
  task_id: string
  task_name: string
  organization_id: string
  version: number
  status: 'submitted' | 'approved' | 'returned'
  operation: 'create' | 'update' | 'delete' | 'first_report' | 'confirmed'
  submitted_at: string
  submitted_by_name?: string
  reviewed_at?: string
  reviewed_by_name?: string
  return_reason?: string
  note?: string
  resource_key: string
  resource_name: string
  changes: FieldChange[]
}

interface HistoryResponse {
  items: SubmissionHistoryItem[]
  total: number
  page: number
  page_size: number
}

const props = withDefaults(defineProps<{ resourceType: 'network_asset' | 'business_system'; allowRestore?: boolean }>(), { allowRestore: false })
const emit = defineEmits<{ restored: [workingVersionID: string] }>()
const visible = ref(false)
const loading = ref(false)
const resourceID = ref('')
const items = ref<SubmissionHistoryItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const restoringSubmissionID = ref('')

const operationLabels: Record<string, string> = {
  create: '首次新增上报', update: '变更上报', delete: '下线申请', first_report: '首次随任务确认', confirmed: '随任务确认',
}
const operationTagTypes: Record<string, 'success' | 'warning' | 'danger' | 'info'> = {
  create: 'success', update: 'warning', delete: 'danger', first_report: 'success', confirmed: 'info',
}
const statusLabels: Record<string, string> = { submitted: '待审核', approved: '已发布', returned: '已退回' }
const statusTagTypes: Record<string, 'warning' | 'success' | 'danger'> = { submitted: 'warning', approved: 'success', returned: 'danger' }
const valueLabels: Record<string, string> = {
  yes: '是', no: '否', unknown: '未知', partial: '部分', pending: '待确认',
  normal: '一般', important: '重要', core: '核心', planning: '规划中', running: '运行中', suspended: '暂停', offline: '已下线', archived: '已归档',
  active: '启用', disabled: '停用', production: '生产', staging: '预发布', testing: '测试', development: '开发', office: '办公', other: '其他',
  public: '公网', private: '内网', domain: '域名', ip: 'IP 地址', url: 'URL', service: '服务',
}

const title = computed(() => items.value[0]?.resource_name ? `${items.value[0].resource_name} · 上报历史` : '资源上报历史')

function formatDateTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString('zh-CN', { hour12: false })
}

function formatValue(value: unknown) {
  if (value === null || value === undefined || value === '') return '—'
  if (Array.isArray(value)) return value.length ? value.map((item) => valueLabels[String(item)] || String(item)).join('、') : '—'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') return new Intl.NumberFormat('zh-CN').format(value)
  if (typeof value === 'object') return JSON.stringify(value)
  return valueLabels[String(value)] || String(value)
}

async function load() {
  if (!resourceID.value) return
  loading.value = true
  try {
    const response = await apiClient.get<HistoryResponse>(`/inventory-tasks/resource-submissions/${props.resourceType}/${resourceID.value}`, {
      params: { page: page.value, page_size: pageSize.value },
    })
    items.value = response.data.items
    total.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取资源上报历史失败'))
  } finally {
    loading.value = false
  }
}

async function open(id: string) {
  resourceID.value = id
  page.value = 1
  items.value = []
  total.value = 0
  visible.value = true
  await load()
}

function handlePageChange(value: number) {
  page.value = value
  void load()
}

function handlePageSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  void load()
}

function canRestore(item: SubmissionHistoryItem) {
  return props.allowRestore && item.status === 'approved' && item.operation !== 'delete'
}

async function restoreSubmission(item: SubmissionHistoryItem) {
  if (!resourceID.value || !canRestore(item) || restoringSubmissionID.value) return
  try {
    await ElMessageBox.confirm(
      '该操作不会修改集团正式台账，而会将此历史版本生成新的未上报工作版本。当前已有未上报工作版本时，其内容将被替换。是否继续？',
      '恢复历史版本',
      { confirmButtonText: '恢复为工作版本', cancelButtonText: '取消', type: 'warning' },
    )
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    throw error
  }

  restoringSubmissionID.value = item.submission_id
  try {
    const response = await apiClient.post<{ result: { working_version_id: string; warnings?: string[] } }>(
      `/inventory-tasks/resource-submissions/${props.resourceType}/${resourceID.value}/${item.submission_id}/restore`,
    )
    ElMessage.success('已恢复为未上报工作版本，请在台账页面复核后选择纳入填报任务')
    if (response.data.result.warnings?.length) {
      ElMessage.warning(response.data.result.warnings.join('；'))
    }
    emit('restored', response.data.result.working_version_id)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '恢复资源历史版本失败'))
  } finally {
    restoringSubmissionID.value = ''
  }
}

defineExpose({ open, restoreSubmission })
</script>

<template>
  <el-drawer v-model="visible" :title="title" size="800px" destroy-on-close>
    <div v-loading="loading" class="resource-submission-history">
      <el-alert title="这里展示该资源历次不可变提交快照；“已发布”表示该版本已经通过审核并进入集团正式台账。" type="info" :closable="false" show-icon />
      <div v-if="items.length" class="resource-submission-timeline">
        <article v-for="item in items" :key="item.submission_id" class="resource-submission-card">
          <header>
            <div>
              <strong>{{ item.task_name }}</strong>
              <span>第 {{ item.version }} 次提交 · {{ formatDateTime(item.submitted_at) }}</span>
            </div>
            <div class="resource-submission-actions">
              <div class="resource-submission-tags">
                <el-tag :type="operationTagTypes[item.operation]" effect="plain">{{ operationLabels[item.operation] || item.operation }}</el-tag>
                <el-tag :type="statusTagTypes[item.status]" effect="plain">{{ statusLabels[item.status] || item.status }}</el-tag>
              </div>
              <el-button v-if="canRestore(item)" link type="primary" :loading="restoringSubmissionID === item.submission_id" @click="restoreSubmission(item)">恢复为工作版本</el-button>
            </div>
          </header>
          <div class="resource-submission-meta">
            <span>提交人：{{ item.submitted_by_name || '未知账号' }}</span>
            <span v-if="item.reviewed_at">审核时间：{{ formatDateTime(item.reviewed_at) }}</span>
            <span v-if="item.reviewed_by_name">审核人：{{ item.reviewed_by_name }}</span>
          </div>
          <el-alert v-if="item.return_reason" :title="`退回原因：${item.return_reason}`" type="error" :closable="false" show-icon />
          <p v-if="item.note" class="resource-submission-note">提交说明：{{ item.note }}</p>
          <div v-if="item.changes.length" class="working-version-change-list">
            <div v-for="change in item.changes" :key="change.field" class="submission-field-change">
              <strong>{{ change.label }}</strong>
              <span class="before">{{ formatValue(change.before) }}</span>
              <span class="change-arrow">→</span>
              <span class="after">{{ formatValue(change.after) }}</span>
            </div>
          </div>
          <el-empty v-else :image-size="56" description="本次确认未产生字段变化" />
        </article>
      </div>
      <el-empty v-else-if="!loading" description="该资源尚无上报记录" />
      <div v-if="total" class="table-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="total" :current-page="page" :page-size="pageSize" :page-sizes="[20, 50, 100]" @current-change="handlePageChange" @size-change="handlePageSizeChange" />
      </div>
    </div>
  </el-drawer>
</template>

```
