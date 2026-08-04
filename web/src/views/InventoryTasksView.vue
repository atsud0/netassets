<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { apiClient, getAPIErrorMessage, getDownloadAPIError } from '../api/client'
import { createExportJob } from '../api/export-jobs'
import { useAuthStore } from '../stores/auth'

interface Organization {
  id: string
  code: string
  name: string
  type: string
  status: string
}
interface OrganizationProgress {
  organization_id: string
  organization_code: string
  organization_name: string
  status: string
  started_at?: string
  submitted_at?: string
  reviewed_at?: string
  submission_note?: string
  submitted_system_count: number
  submitted_asset_count: number
  submitted_by_name?: string
  reviewed_by_name?: string
  return_reason?: string
  snapshot_available: boolean
  submission_version: number
}
interface InventoryTask {
  id: string
  name: string
  description?: string
  start_at: string
  deadline_at: string
  status: string
  asset_network_scope: 'all' | 'public' | 'private'
  required_fields: string[]
  quality_rules: string[]
  require_review: boolean
  allow_late_submission: boolean
  target_count: number
  submitted_count: number
  created_at: string
}
interface QualityIssue {
  code: string
  severity: 'error' | 'warning'
  message: string
  count: number
}
interface QualityCheck {
  passed: boolean
  system_count: number
  asset_count: number
  error_count: number
  warning_count: number
  issues: QualityIssue[]
}
interface QualityCheckResponse { quality_check: QualityCheck }
interface ScopePreviewItem {
  organization_id: string
  organization_code: string
  organization_name: string
  system_count: number
  asset_count: number
}
interface ScopePreview {
  items: ScopePreviewItem[]
  organization_count: number
  system_count: number
  asset_count: number
  empty_organization_count: number
}
interface ScopePreviewResponse { scope_preview: ScopePreview }
interface SubmissionMetadata {
  id: string
  task_id: string
  task_name: string
  organization_id: string
  organization_name: string
  version: number
  submitted_at: string
  submitted_by_name?: string
  note?: string
  system_count: number
  asset_count: number
}
interface SubmissionFieldChange {
  field: string
  label: string
  before: unknown
  after: unknown
}
interface SubmissionResourceChange {
  resource_id: string
  resource_key: string
  resource_name: string
  changes?: SubmissionFieldChange[]
}
type DiffResourceType = 'business_system' | 'network_asset'
type DiffChangeType = 'added' | 'modified' | 'removed'
type DiffListKey = `${DiffResourceType}:${DiffChangeType}`
interface SubmissionDiffSummary { added: number; modified: number; removed: number }
interface SubmissionDiff {
  submission: SubmissionMetadata
  previous_submission?: SubmissionMetadata
  business_systems: SubmissionDiffSummary
  network_assets: SubmissionDiffSummary
  summary: SubmissionDiffSummary
}
interface SubmissionDiffResponse { submission_diff: SubmissionDiff }
interface DiffListState {
  items: SubmissionResourceChange[]
  total: number
  page: number
  pageSize: number
  loading: boolean
  loaded: boolean
}
interface ReviewResource {
  resource_type: 'business_system' | 'network_asset'
  resource_id: string
  resource_key: string
  resource_name: string
  snapshot_data: Record<string, unknown>
}
interface ReviewComment {
  id: string
  submission_id: string
  submission_version: number
  resource_type: 'business_system' | 'network_asset'
  resource_id: string
  resource_key: string
  resource_name: string
  comment: string
  created_by_name?: string
  created_at: string
}
interface ReviewContext {
  submission: SubmissionMetadata
}
interface ReviewContextResponse { review_context: ReviewContext }
interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface TaskOrganizationState {
  items: OrganizationProgress[]
  total: number
  page: number
  pageSize: number
  loading: boolean
  loaded: boolean
}

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const loading = ref(false)
const submitting = ref(false)
const actionLoading = ref('')
const exportLoading = ref('')
const createDialogVisible = ref(false)
const scopePreviewLoading = ref(false)
const scopePreview = ref<ScopePreview | null>(null)
const scopePreviewError = ref('')
let scopePreviewTimer: ReturnType<typeof setTimeout> | undefined
let scopePreviewRequestID = 0
const submissionDialogVisible = ref(false)
const returnDialogVisible = ref(false)
const qualityLoading = ref(false)
const qualityCheck = ref<QualityCheck | null>(null)
const diffDrawerVisible = ref(false)
const diffLoading = ref(false)
const submissionDiff = ref<SubmissionDiff | null>(null)
const diffTaskID = ref('')
const diffOrganizationID = ref('')
const diffActiveTabs = reactive<Record<DiffResourceType, DiffChangeType>>({
  business_system: 'added',
  network_asset: 'added',
})
function newDiffListState(): DiffListState {
  return { items: [], total: 0, page: 1, pageSize: 20, loading: false, loaded: false }
}
const diffListStates = reactive<Record<DiffListKey, DiffListState>>({
  'business_system:added': newDiffListState(),
  'business_system:modified': newDiffListState(),
  'business_system:removed': newDiffListState(),
  'network_asset:added': newDiffListState(),
  'network_asset:modified': newDiffListState(),
  'network_asset:removed': newDiffListState(),
})
const reviewContextVisible = ref(false)
const reviewContextLoading = ref(false)
const reviewContext = ref<ReviewContext | null>(null)
const reviewComments = ref<ReviewComment[]>([])
const reviewCommentLoading = ref(false)
const reviewCommentPage = ref(1)
const reviewCommentPageSize = ref(20)
const reviewCommentTotal = ref(0)
const returnContextLoading = ref(false)
const returnReviewContext = ref<ReviewContext | null>(null)
const returnResources = ref<ReviewResource[]>([])
const returnResourceLoading = ref(false)
const returnResourceSearch = ref('')
const returnResourcePage = ref(1)
const returnResourcePageSize = ref(20)
const returnResourceTotal = ref(0)
const returnCommentDrafts = reactive<Record<string, string>>({})
const returnResourceIndex = reactive<Record<string, ReviewResource>>({})
const formRef = ref<FormInstance>()
const tasks = ref<InventoryTask[]>([])
const taskOrganizationStates = reactive<Record<string, TaskOrganizationState>>({})
const taskDetailVisible = ref(false)
const detailTask = ref<InventoryTask | null>(null)
const organizations = ref<Organization[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const selectedTask = ref<InventoryTask | null>(null)
const selectedProgress = ref<OrganizationProgress | null>(null)
const submissionForm = reactive({ note: '' })
const returnForm = reactive({ reason: '' })
function initialStart() {
  const value = new Date()
  value.setSeconds(0, 0)
  return value
}
function initialDeadline(start: Date) {
  return new Date(start.getTime() + 7 * 24 * 60 * 60 * 1000)
}
const defaultStart = initialStart()
const form = reactive({
  name: '',
  description: '',
  start_at: defaultStart,
  deadline_at: initialDeadline(defaultStart),
  status: 'active',
  asset_network_scope: 'all' as 'all' | 'public' | 'private',
  organization_ids: [] as string[],
  required_fields: ['business_systems', 'network_assets'] as string[],
  require_review: true,
  allow_late_submission: false,
})
const rules: FormRules = {
  name: [{ required: true, message: '请输入任务名称', trigger: 'blur' }],
  start_at: [{ required: true, message: '请选择开始时间', trigger: 'change' }],
  deadline_at: [{ required: true, message: '请选择截止时间', trigger: 'change' }],
  organization_ids: [{ type: 'array', required: true, min: 1, message: '请选择至少一个填报组织', trigger: 'change' }],
}

const eligibleOrganizations = computed(() => organizations.value.filter((item) => item.status === 'enabled' && item.type !== 'group'))
const scopePreviewHasResourceType = computed(() => form.required_fields.some((field) => ['business_systems', 'data_security', 'network_assets'].includes(field)))
const scopePreviewReady = computed(() => form.organization_ids.length > 0 && scopePreviewHasResourceType.value)
const activeCount = computed(() => tasks.value.filter((item) => item.status === 'active').length)
const draftCount = computed(() => tasks.value.filter((item) => item.status === 'draft').length)
const overdueCount = computed(() => tasks.value.filter((item) => item.status === 'active' && new Date(item.deadline_at).getTime() < Date.now()).length)
const taskStatusLabels: Record<string, string> = { draft: '草稿', active: '进行中', closed: '已关闭', completed: '已完成', archived: '已归档' }
const progressStatusLabels: Record<string, string> = {
  not_started: '未开始', in_progress: '填报中', submitted: '待审核', returned: '已退回', approved: '已通过', overdue: '已逾期',
}
const requiredFieldLabels: Record<string, string> = {
  business_systems: '业务系统信息', data_security: '数据安全属性', network_assets: '网络资产信息', owner_contacts: '负责人及联系方式',
}
const assetNetworkScopeLabels: Record<string, string> = {
  all: '全部网络资产', public: '仅公网资产', private: '仅内网资产',
}
const snapshotValueLabels: Record<string, string> = {
  yes: '是', no: '否', unknown: '未知', partial: '部分', pending: '待确认',
  normal: '一般', important: '重要', core: '核心',
  planning: '规划中', running: '运行中', suspended: '暂停', offline: '已下线', archived: '已归档',
  active: '启用', disabled: '停用', production: '生产', staging: '预发布', testing: '测试', development: '开发', office: '办公', other: '其他',
  public: '公网', private: '内网', domain: '域名', ip: 'IP 地址', url: 'URL', service: '服务',
}
const diffChangeTabs: Array<{ key: DiffChangeType; label: string; type: 'success' | 'warning' | 'danger'; emptyText: string }> = [
  { key: 'added', label: '新增', type: 'success', emptyText: '无新增记录' },
  { key: 'modified', label: '修改', type: 'warning', emptyText: '无修改记录' },
  { key: 'removed', label: '删除/归档', type: 'danger', emptyText: '无删除或归档记录' },
]
const diffGroups = computed(() => {
  if (!submissionDiff.value) return []
  return [
    { key: 'business_system' as const, label: '业务系统', summary: submissionDiff.value.business_systems },
    { key: 'network_asset' as const, label: '网络资产', summary: submissionDiff.value.network_assets },
  ]
})

const focusedTaskID = computed(() => typeof route.query.task_id === 'string' ? route.query.task_id.trim() : '')
const focusedOrganizationID = computed(() => typeof route.query.organization_id === 'string' ? route.query.organization_id.trim() : '')
const hasMessageFocus = computed(() => focusedTaskID.value !== '')

async function loadOrganizations() {
  if (!auth.isGroupAdmin) {
    organizations.value = []
    return
  }
  try {
    const response = await apiClient.get<ListResponse<Organization>>('/organizations', {
      params: { page: 1, page_size: 100 },
    })
    organizations.value = response.data.items
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取可填报组织失败'))
  }
}

function taskOrganizationState(task: InventoryTask) {
  if (!taskOrganizationStates[task.id]) {
    taskOrganizationStates[task.id] = { items: [], total: 0, page: 1, pageSize: 20, loading: false, loaded: false }
  }
  return taskOrganizationStates[task.id]
}

async function loadTaskOrganizations(task: InventoryTask) {
  const state = taskOrganizationState(task)
  state.loading = true
  try {
    const organizationID = task.id === focusedTaskID.value ? focusedOrganizationID.value : ''
    const response = await apiClient.get<ListResponse<OrganizationProgress>>(`/inventory-tasks/${task.id}/organizations`, {
      params: {
        page: state.page,
        page_size: state.pageSize,
        organization_id: organizationID || undefined,
      },
    })
    state.items = response.data.items
    state.total = response.data.total
    state.loaded = true
    if (!state.items.length && state.page > 1 && state.total > 0) {
      state.page -= 1
      await loadTaskOrganizations(task)
    }
  } finally {
    state.loading = false
  }
}

async function openTaskDetail(task: InventoryTask) {
  const switchedTask = detailTask.value?.id !== task.id
  detailTask.value = task
  taskDetailVisible.value = true
  const state = taskOrganizationState(task)
  if (switchedTask) state.page = 1
  try { await loadTaskOrganizations(task) }
  catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取填报组织进度失败')) }
}

function closeTaskDetail() {
  detailTask.value = null
}

async function handleTaskOrganizationPageChange(task: InventoryTask, value: number) {
  taskOrganizationState(task).page = value
  try { await loadTaskOrganizations(task) }
  catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取填报组织进度失败')) }
}

async function handleTaskOrganizationPageSizeChange(task: InventoryTask, value: number) {
  const state = taskOrganizationState(task)
  state.pageSize = value
  state.page = 1
  try { await loadTaskOrganizations(task) }
  catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取填报组织进度失败')) }
}

async function load() {
  loading.value = true
  try {
    const response = await apiClient.get<ListResponse<InventoryTask>>('/inventory-tasks', {
      params: {
        page: page.value,
        page_size: pageSize.value,
        task_id: focusedTaskID.value || undefined,
      },
    })
    tasks.value = response.data.items
    total.value = response.data.total
    if (focusedTaskID.value) {
      const focusedTask = tasks.value.find((item) => item.id === focusedTaskID.value)
      if (focusedTask) await openTaskDetail(focusedTask)
    }
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取填报任务失败'))
  } finally {
    loading.value = false
  }
}

function resetTaskOrganizationStates() {
  Object.keys(taskOrganizationStates).forEach((taskID) => { delete taskOrganizationStates[taskID] })
}

async function clearMessageFocus() {
  taskDetailVisible.value = false
  detailTask.value = null
  const query = { ...route.query }
  delete query.task_id
  delete query.organization_id
  await router.replace({ query })
}

async function applyRouteFocus() {
  page.value = 1
  taskDetailVisible.value = false
  detailTask.value = null
  resetTaskOrganizationStates()
  await load()
}

async function refreshAfterMutation() {
  const openTaskID = detailTask.value?.id
  await load()
  if (tasks.value.length === 0 && total.value > 0 && page.value > 1) {
    page.value -= 1
    await load()
  }
  if (openTaskID) {
    const refreshedTask = tasks.value.find((task) => task.id === openTaskID)
    if (refreshedTask) detailTask.value = refreshedTask
    else {
      taskDetailVisible.value = false
      detailTask.value = null
    }
  }
  await Promise.allSettled(tasks.value
    .filter((task) => taskOrganizationStates[task.id]?.loaded)
    .map((task) => loadTaskOrganizations(task)))
}

function handlePageChange(value: number) {
  taskDetailVisible.value = false
  detailTask.value = null
  page.value = value
  void load()
}

function handlePageSizeChange(value: number) {
  taskDetailVisible.value = false
  detailTask.value = null
  pageSize.value = value
  page.value = 1
  void load()
}

function resetScopePreview() {
  if (scopePreviewTimer) clearTimeout(scopePreviewTimer)
  scopePreviewTimer = undefined
  scopePreviewRequestID += 1
  scopePreviewLoading.value = false
  scopePreview.value = null
  scopePreviewError.value = ''
}

function scheduleScopePreview() {
  if (scopePreviewTimer) clearTimeout(scopePreviewTimer)
  scopePreviewTimer = undefined
  scopePreviewRequestID += 1
  scopePreviewLoading.value = false
  scopePreview.value = null
  scopePreviewError.value = ''
  if (!createDialogVisible.value || !scopePreviewReady.value) return
  scopePreviewTimer = setTimeout(() => { void loadScopePreview() }, 300)
}

async function loadScopePreview() {
  if (!scopePreviewReady.value) {
    scheduleScopePreview()
    return
  }
  if (scopePreviewTimer) clearTimeout(scopePreviewTimer)
  scopePreviewTimer = undefined
  const requestID = ++scopePreviewRequestID
  scopePreviewLoading.value = true
  scopePreviewError.value = ''
  try {
    const response = await apiClient.post<ScopePreviewResponse>('/inventory-tasks/scope-preview', {
      organization_ids: form.organization_ids,
      required_fields: form.required_fields,
      asset_network_scope: form.required_fields.includes('network_assets') ? form.asset_network_scope : 'all',
    })
    if (requestID === scopePreviewRequestID) scopePreview.value = response.data.scope_preview
  } catch (error) {
    if (requestID === scopePreviewRequestID) {
      scopePreview.value = null
      scopePreviewError.value = getAPIErrorMessage(error, '读取任务范围预览失败')
    }
  } finally {
    if (requestID === scopePreviewRequestID) scopePreviewLoading.value = false
  }
}

function openCreateDialog() {
  const start = initialStart()
  resetScopePreview()
  Object.assign(form, {
    name: '', description: '', start_at: start, deadline_at: initialDeadline(start), status: 'active',
    asset_network_scope: 'all', organization_ids: [], required_fields: ['business_systems', 'network_assets'], require_review: true, allow_late_submission: false,
  })
  createDialogVisible.value = true
}

watch(
  [() => form.organization_ids.slice(), () => form.required_fields.slice(), () => form.asset_network_scope],
  scheduleScopePreview,
)

async function createTask() {
  if (!formRef.value || !(await formRef.value.validate())) return
  if (!form.required_fields.some((field) => ['business_systems', 'data_security', 'network_assets'].includes(field))) {
    ElMessage.warning('请至少选择业务系统、数据安全属性或网络资产中的一项填报内容')
    return
  }
  if (!(form.deadline_at > form.start_at)) {
    ElMessage.warning('截止时间必须晚于开始时间')
    return
  }
  submitting.value = true
  try {
    await apiClient.post('/inventory-tasks', {
      ...form,
      asset_network_scope: form.required_fields.includes('network_assets') ? form.asset_network_scope : 'all',
      quality_rules: [],
    })
    createDialogVisible.value = false
    resetScopePreview()
    ElMessage.success(form.status === 'active' ? '填报任务已创建并下发' : '填报任务草稿已保存')
    page.value = 1
    await load()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '创建填报任务失败'))
  } finally {
    submitting.value = false
  }
}

function operationKey(task: InventoryTask, progress?: OrganizationProgress) {
  return `${task.id}:${progress?.organization_id ?? 'task'}`
}

function isTaskOpen(task: InventoryTask) {
  const now = Date.now()
  return task.status === 'active' && now >= new Date(task.start_at).getTime() &&
    (task.allow_late_submission || now <= new Date(task.deadline_at).getTime())
}

function effectiveProgressStatus(task: InventoryTask, progress: OrganizationProgress) {
  if (task.status === 'active' && new Date(task.deadline_at).getTime() < Date.now() &&
      !['submitted', 'approved'].includes(progress.status)) return 'overdue'
  return progress.status
}

function canStart(task: InventoryTask, progress: OrganizationProgress) {
  return isTaskOpen(task) && ['not_started', 'returned', 'overdue'].includes(progress.status)
}

function canSubmit(task: InventoryTask, progress: OrganizationProgress) {
  return isTaskOpen(task) && ['in_progress', 'returned', 'overdue'].includes(progress.status)
}

function openInventoryAssets(task: InventoryTask, progress: OrganizationProgress) {
  void router.push({
    path: '/assets',
    query: {
      mode: 'inventory',
      task_id: task.id,
      task_name: task.name,
      organization_id: progress.organization_id,
      asset_network_scope: task.asset_network_scope,
    },
  })
}

function openInventoryBusinessSystems(task: InventoryTask, progress: OrganizationProgress) {
  void router.push({
    path: '/systems',
    query: { mode: 'inventory', task_id: task.id, task_name: task.name, organization_id: progress.organization_id },
  })
}

function openSelectedInventoryBusinessSystems() {
  if (!selectedTask.value || !selectedProgress.value) return
  submissionDialogVisible.value = false
  openInventoryBusinessSystems(selectedTask.value, selectedProgress.value)
}

function openSelectedInventoryAssets() {
  if (!selectedTask.value || !selectedProgress.value) return
  submissionDialogVisible.value = false
  openInventoryAssets(selectedTask.value, selectedProgress.value)
}

async function startProgress(task: InventoryTask, progress: OrganizationProgress) {
  const key = operationKey(task, progress)
  actionLoading.value = key
  try {
    await apiClient.post(`/inventory-tasks/${task.id}/organizations/${progress.organization_id}/start`)
    ElMessage.success('已开始填报，请完善业务系统和网络资产后提交')
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '开始填报失败'))
  } finally {
    actionLoading.value = ''
  }
}

async function loadQualityCheck() {
  if (!selectedTask.value || !selectedProgress.value) return
  qualityLoading.value = true
  qualityCheck.value = null
  try {
    const response = await apiClient.get<QualityCheckResponse>(
      `/inventory-tasks/${selectedTask.value.id}/organizations/${selectedProgress.value.organization_id}/quality-check`,
    )
    qualityCheck.value = response.data.quality_check
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '执行填报质量检查失败'))
  } finally {
    qualityLoading.value = false
  }
}

function openSubmissionDialog(task: InventoryTask, progress: OrganizationProgress) {
  selectedTask.value = task
  selectedProgress.value = progress
  submissionForm.note = progress.submission_note || ''
  qualityCheck.value = null
  submissionDialogVisible.value = true
  void loadQualityCheck()
}

async function submitProgress() {
  if (!selectedTask.value || !selectedProgress.value) return
  if (!qualityCheck.value?.passed) {
    ElMessage.warning('请先修正质量检查中的错误项并重新检查')
    return
  }
  const key = operationKey(selectedTask.value, selectedProgress.value)
  actionLoading.value = key
  try {
    await apiClient.post(`/inventory-tasks/${selectedTask.value.id}/organizations/${selectedProgress.value.organization_id}/submit`, {
      note: submissionForm.note,
    })
    submissionDialogVisible.value = false
    ElMessage.success(selectedTask.value.require_review ? '填报结果已提交，等待集团审核' : '填报结果已提交并自动完成')
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '提交填报任务失败'))
  } finally {
    actionLoading.value = ''
  }
}

function diffListKey(resourceType: DiffResourceType, changeType: DiffChangeType): DiffListKey {
  return `${resourceType}:${changeType}`
}

function diffListState(resourceType: DiffResourceType, changeType: DiffChangeType) {
  return diffListStates[diffListKey(resourceType, changeType)]
}

function resetDiffListStates() {
  for (const state of Object.values(diffListStates)) Object.assign(state, newDiffListState())
}

function initialDiffTab(summary: SubmissionDiffSummary): DiffChangeType {
  if (summary.added > 0) return 'added'
  if (summary.modified > 0) return 'modified'
  if (summary.removed > 0) return 'removed'
  return 'added'
}

async function loadSubmissionDiffItems(resourceType: DiffResourceType, changeType: DiffChangeType, force = false) {
  const state = diffListState(resourceType, changeType)
  if ((!force && state.loaded) || state.loading || !diffTaskID.value || !diffOrganizationID.value) return
  const taskID = diffTaskID.value
  const organizationID = diffOrganizationID.value
  state.loading = true
  try {
    const response = await apiClient.get<ListResponse<SubmissionResourceChange>>(
      `/inventory-tasks/${taskID}/organizations/${organizationID}/submission-diff/items`,
      { params: { resource_type: resourceType, change_type: changeType, page: state.page, page_size: state.pageSize } },
    )
    if (taskID !== diffTaskID.value || organizationID !== diffOrganizationID.value) return
    state.items = response.data.items
    state.total = response.data.total
    state.loaded = true
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取填报提交差异明细失败'))
  } finally {
    state.loading = false
  }
}

function handleDiffTabChange(resourceType: DiffResourceType, name: string | number) {
  const changeType = String(name) as DiffChangeType
  diffActiveTabs[resourceType] = changeType
  void loadSubmissionDiffItems(resourceType, changeType)
}

function handleDiffPageChange(resourceType: DiffResourceType, changeType: DiffChangeType, value: number) {
  const state = diffListState(resourceType, changeType)
  state.page = value
  void loadSubmissionDiffItems(resourceType, changeType, true)
}

function handleDiffPageSizeChange(resourceType: DiffResourceType, changeType: DiffChangeType, value: number) {
  const state = diffListState(resourceType, changeType)
  state.pageSize = value
  state.page = 1
  void loadSubmissionDiffItems(resourceType, changeType, true)
}

async function openSubmissionDiff(task: InventoryTask, progress: OrganizationProgress) {
  diffDrawerVisible.value = true
  diffLoading.value = true
  submissionDiff.value = null
  diffTaskID.value = task.id
  diffOrganizationID.value = progress.organization_id
  resetDiffListStates()
  try {
    const response = await apiClient.get<SubmissionDiffResponse>(
      `/inventory-tasks/${task.id}/organizations/${progress.organization_id}/submission-diff`,
    )
    if (task.id !== diffTaskID.value || progress.organization_id !== diffOrganizationID.value) return
    submissionDiff.value = response.data.submission_diff
    diffActiveTabs.business_system = initialDiffTab(response.data.submission_diff.business_systems)
    diffActiveTabs.network_asset = initialDiffTab(response.data.submission_diff.network_assets)
    await Promise.all([
      loadSubmissionDiffItems('business_system', diffActiveTabs.business_system),
      loadSubmissionDiffItems('network_asset', diffActiveTabs.network_asset),
    ])
  } catch (error) {
    diffDrawerVisible.value = false
    ElMessage.error(getAPIErrorMessage(error, '读取填报提交差异失败'))
  } finally {
    diffLoading.value = false
  }
}

function formatSnapshotValue(value: unknown) {
  if (value === null || value === undefined || value === '') return '—'
  if (Array.isArray(value)) return value.length ? value.map((item) => snapshotValueLabels[String(item)] || String(item)).join('、') : '—'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') return new Intl.NumberFormat('zh-CN').format(value)
  if (typeof value === 'object') return JSON.stringify(value)
  return snapshotValueLabels[String(value)] || String(value)
}

async function approveProgress(task: InventoryTask, progress: OrganizationProgress) {
  try {
    await ElMessageBox.confirm(`确认通过“${progress.organization_name}”提交的填报结果吗？`, '审核通过', { type: 'success', confirmButtonText: '通过', cancelButtonText: '取消' })
  } catch {
    return
  }
  const key = operationKey(task, progress)
  actionLoading.value = key
  try {
    await apiClient.post(`/inventory-tasks/${task.id}/organizations/${progress.organization_id}/review`, { decision: 'approved', return_reason: '' })
    ElMessage.success('填报结果已审核通过')
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '审核填报任务失败'))
  } finally {
    actionLoading.value = ''
  }
}

function reviewResourceKey(resource: ReviewResource) {
  return `${resource.resource_type}:${resource.resource_id}`
}

function reviewResourceTypeLabel(value: string) {
  return value === 'business_system' ? '业务系统' : '网络资产'
}

function clearReturnCommentDrafts() {
  for (const key of Object.keys(returnCommentDrafts)) delete returnCommentDrafts[key]
  for (const key of Object.keys(returnResourceIndex)) delete returnResourceIndex[key]
}

async function fetchReviewContext(task: InventoryTask, progress: OrganizationProgress) {
  const response = await apiClient.get<ReviewContextResponse>(
    `/inventory-tasks/${task.id}/organizations/${progress.organization_id}/review-context`,
  )
  return response.data.review_context
}

async function loadReviewComments() {
  if (!selectedTask.value || !selectedProgress.value) return
  reviewCommentLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<ReviewComment>>(
      `/inventory-tasks/${selectedTask.value.id}/organizations/${selectedProgress.value.organization_id}/review-comments`,
      { params: { page: reviewCommentPage.value, page_size: reviewCommentPageSize.value } },
    )
    reviewComments.value = response.data.items
    reviewCommentTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取逐条审核意见失败'))
  } finally {
    reviewCommentLoading.value = false
  }
}

async function loadReturnResources() {
  if (!selectedTask.value || !selectedProgress.value) return
  returnResourceLoading.value = true
  try {
    const params: Record<string, string | number> = { page: returnResourcePage.value, page_size: returnResourcePageSize.value }
    if (returnResourceSearch.value.trim()) params.q = returnResourceSearch.value.trim()
    const response = await apiClient.get<ListResponse<ReviewResource>>(
      `/inventory-tasks/${selectedTask.value.id}/organizations/${selectedProgress.value.organization_id}/review-resources`,
      { params },
    )
    returnResources.value = response.data.items
    returnResourceTotal.value = response.data.total
    for (const resource of response.data.items) returnResourceIndex[reviewResourceKey(resource)] = resource
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取本次提交明细失败'))
  } finally {
    returnResourceLoading.value = false
  }
}

function handleReviewCommentPageChange(value: number) { reviewCommentPage.value = value; void loadReviewComments() }
function handleReviewCommentPageSizeChange(value: number) { reviewCommentPageSize.value = value; reviewCommentPage.value = 1; void loadReviewComments() }
function applyReturnResourceSearch() { returnResourcePage.value = 1; void loadReturnResources() }
function handleReturnResourcePageChange(value: number) { returnResourcePage.value = value; void loadReturnResources() }
function handleReturnResourcePageSizeChange(value: number) { returnResourcePageSize.value = value; returnResourcePage.value = 1; void loadReturnResources() }

async function openReviewComments(task: InventoryTask, progress: OrganizationProgress) {
  selectedTask.value = task
  selectedProgress.value = progress
  reviewContextVisible.value = true
  reviewContextLoading.value = true
  reviewContext.value = null
  reviewComments.value = []
  reviewCommentPage.value = 1
  reviewCommentTotal.value = 0
  try {
    reviewContext.value = await fetchReviewContext(task, progress)
    await loadReviewComments()
  } catch (error) {
    reviewContextVisible.value = false
    ElMessage.error(getAPIErrorMessage(error, '读取逐条审核意见失败'))
  } finally {
    reviewContextLoading.value = false
  }
}

async function openReturnDialog(task: InventoryTask, progress: OrganizationProgress) {
  selectedTask.value = task
  selectedProgress.value = progress
  returnForm.reason = ''
  returnResourceSearch.value = ''
  returnReviewContext.value = null
  returnResources.value = []
  returnResourcePage.value = 1
  returnResourceTotal.value = 0
  clearReturnCommentDrafts()
  returnDialogVisible.value = true
  returnContextLoading.value = true
  try {
    returnReviewContext.value = await fetchReviewContext(task, progress)
    await loadReturnResources()
  } catch (error) {
    returnDialogVisible.value = false
    ElMessage.error(getAPIErrorMessage(error, '读取本次提交明细失败'))
  } finally {
    returnContextLoading.value = false
  }
}


async function returnProgress() {
  if (!selectedTask.value || !selectedProgress.value) return
  if (!returnForm.reason.trim()) {
    ElMessage.warning('请填写退回原因')
    return
  }
  const comments = Object.entries(returnCommentDrafts).flatMap(([key, value]) => {
    const resource = returnResourceIndex[key]
    const comment = value.trim()
    return resource && comment ? [{ resource_type: resource.resource_type, resource_id: resource.resource_id, comment }] : []
  })
  if (comments.length > 100) {
    ElMessage.warning('单次最多填写 100 条逐条审核意见')
    return
  }
  const key = operationKey(selectedTask.value, selectedProgress.value)
  actionLoading.value = key
  try {
    await apiClient.post(`/inventory-tasks/${selectedTask.value.id}/organizations/${selectedProgress.value.organization_id}/review`, {
      decision: 'returned', return_reason: returnForm.reason, comments,
    })
    returnDialogVisible.value = false
    ElMessage.success('填报结果已退回修改')
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '退回填报任务失败'))
  } finally {
    actionLoading.value = ''
  }
}

async function updateTaskStatus(task: InventoryTask, status: string) {
  const labels: Record<string, string> = { active: '下发', closed: '关闭', archived: '归档' }
  try {
    await ElMessageBox.confirm(`确认${labels[status]}任务“${task.name}”吗？`, `${labels[status]}任务`, {
      type: status === 'closed' ? 'warning' : 'info', confirmButtonText: labels[status], cancelButtonText: '取消',
    })
  } catch {
    return
  }
  const key = operationKey(task)
  actionLoading.value = key
  try {
    await apiClient.patch(`/inventory-tasks/${task.id}/status`, { status })
    ElMessage.success(`任务已${labels[status]}`)
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, `${labels[status]}任务失败`))
  } finally {
    actionLoading.value = ''
  }
}

async function exportTask(task: InventoryTask, format: 'csv' | 'xlsx') {
  exportLoading.value = task.id
  try {
    const response = await apiClient.get(`/inventory-tasks/${task.id}/export?format=${format}`, {
      responseType: 'blob',
      timeout: 60_000,
    })
    const href = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    const safeName = task.name.replace(/[\\/:*?"<>|]/g, '-').replace(/^[. ]+|[. ]+$/g, '') || '未命名任务'
    link.href = href
    link.download = `填报任务结果-${safeName}.${format}`
    link.click()
    URL.revokeObjectURL(href)
    const count = response.headers['x-export-count'] ?? task.target_count
    ElMessage.success(`已生成并开始下载 ${count} 个组织的填报结果`)
  } catch (error) {
    const detail = await getDownloadAPIError(error, '导出填报任务结果失败')
    if (format === 'xlsx' && detail.code === 'synchronous_xlsx_limit_exceeded') {
      try { await queueTaskExport(task) } catch (queueError) { ElMessage.error(getAPIErrorMessage(queueError, '创建后台导出任务失败')) }
    } else {
      ElMessage.error(detail.message)
    }
  } finally {
    exportLoading.value = ''
  }
}

async function queueTaskExport(task: InventoryTask) {
  await createExportJob('inventory_task', { task_id: task.id })
  ElMessage.success('后台 XLSX 导出任务已创建，可在“导出任务”中查看进度和下载')
}

function handleExportCommand(task: InventoryTask, command: unknown) {
  if (command === 'csv' || command === 'xlsx') void exportTask(task, command)
  if (command === 'background_xlsx') void queueTaskExport(task)
}

function formatDate(value?: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(value))
}
function taskStatusTag(value: string) {
  return value === 'active' ? 'success' : value === 'draft' ? 'info' : value === 'completed' ? 'primary' : 'warning'
}
function progressStatusTag(value: string) {
  return value === 'approved' ? 'success' : value === 'submitted' ? 'primary' : value === 'returned' || value === 'overdue' ? 'danger' : value === 'in_progress' ? 'warning' : 'info'
}
function pendingSubmissionCount(task: InventoryTask) {
  return Math.max(task.target_count - task.submitted_count, 0)
}

watch(
  () => [route.query.task_id, route.query.organization_id],
  () => { void applyRouteFocus() },
)

onMounted(async () => {
  await Promise.all([load(), loadOrganizations()])
})
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
        <span class="eyebrow">INVENTORY</span>
        <h1>填报任务</h1>
        <p>{{ auth.isGroupAdmin ? '集团选择下级组织并下发填报任务，统一跟踪提交和审核进度。' : '维护本组织的业务系统和网络资产，并在任务中提交集团审核。' }}</p>
      </div>
      <el-button v-if="auth.isGroupAdmin" type="primary" :disabled="eligibleOrganizations.length === 0" @click="openCreateDialog">创建任务</el-button>
    </div>

    <el-alert v-if="hasMessageFocus" type="info" :closable="false" show-icon class="page-alert">
      <template #title>当前正在查看消息关联的填报任务{{ focusedOrganizationID ? '及目标组织' : '' }}</template>
      <el-button type="primary" link @click="clearMessageFocus">查看全部任务</el-button>
    </el-alert>

    <el-alert v-if="auth.isGroupAdmin && eligibleOrganizations.length === 0" title="请先在“组织机构”中新增并启用至少一个子公司或部门，之后才能创建填报任务。" type="warning" :closable="false" show-icon class="page-alert" />
    <el-alert v-if="!auth.isGroupAdmin" title="填报任务以系统中当前的业务系统和网络资产为准。请先维护数据，再回到本页面提交。" type="info" :closable="false" show-icon class="page-alert">
      <template #default>
        <div class="inventory-guide-actions">
          <span>填报任务以系统中当前的业务系统和网络资产为准。请先维护数据，再回到本页面提交。</span>
          <el-button size="small" @click="router.push('/systems')">维护日常业务系统台账</el-button>
          <el-button size="small" @click="router.push('/inventory-tasks')">从具体任务进入资产草稿</el-button>
        </div>
      </template>
    </el-alert>

    <div class="organization-summary">
      <div><span>任务总数</span><strong>{{ total }}</strong></div>
      <div><span>本页进行中</span><strong>{{ activeCount }}</strong></div>
      <div><span>本页草稿</span><strong>{{ draftCount }}</strong></div>
      <div><span>本页已逾期</span><strong>{{ overdueCount }}</strong></div>
    </div>

    <div class="panel data-table-panel" v-loading="loading">
      <el-table :data="tasks" row-key="id" empty-text="暂无填报任务">
        <el-table-column label="任务名称" min-width="220">
          <template #default="scope"><el-button link type="primary" class="table-link" @click="openTaskDetail(scope.row)">{{ scope.row.name }}</el-button></template>
        </el-table-column>
        <el-table-column label="状态" width="105"><template #default="scope"><el-tag :type="taskStatusTag(scope.row.status)" effect="plain">{{ taskStatusLabels[scope.row.status] || scope.row.status }}</el-tag></template></el-table-column>
        <el-table-column label="开始时间" min-width="165"><template #default="scope">{{ formatDate(scope.row.start_at) }}</template></el-table-column>
        <el-table-column label="截止时间" min-width="165"><template #default="scope">{{ formatDate(scope.row.deadline_at) }}</template></el-table-column>
        <el-table-column label="提交进度" width="205">
          <template #default="scope">
            <div class="submission-progress">
              <span>已提交 <strong>{{ scope.row.submitted_count }}</strong></span>
              <i aria-hidden="true">·</i>
              <span>待提交 <strong>{{ pendingSubmissionCount(scope.row) }}</strong></span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="任务操作" width="180" fixed="right">
          <template #default="scope">
            <template v-if="auth.isGroupAdmin">
              <el-button v-if="scope.row.status === 'draft'" type="primary" link :loading="actionLoading === operationKey(scope.row)" @click="updateTaskStatus(scope.row, 'active')">下发</el-button>
              <el-button v-else-if="scope.row.status === 'active'" type="warning" link :loading="actionLoading === operationKey(scope.row)" @click="updateTaskStatus(scope.row, 'closed')">关闭</el-button>
              <el-button v-else-if="['closed', 'completed'].includes(scope.row.status)" type="info" link :loading="actionLoading === operationKey(scope.row)" @click="updateTaskStatus(scope.row, 'archived')">归档</el-button>
            </template>
            <el-dropdown trigger="click" @command="handleExportCommand(scope.row, $event)">
              <el-button type="primary" link :loading="exportLoading === scope.row.id">导出</el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item command="xlsx">导出 XLSX</el-dropdown-item>
                  <el-dropdown-item command="csv">导出 CSV</el-dropdown-item>
                  <el-dropdown-item command="background_xlsx" divided>后台生成 XLSX</el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
          </template>
        </el-table-column>
      </el-table>
      <div class="table-pagination">
        <el-pagination
          background
          layout="total, sizes, prev, pager, next"
          :total="total"
          :current-page="page"
          :page-size="pageSize"
          :page-sizes="[20, 50, 100]"
          @current-change="handlePageChange"
          @size-change="handlePageSizeChange"
        />
      </div>
    </div>

    <el-drawer v-model="taskDetailVisible" title="填报任务详情" size="88%" destroy-on-close @closed="closeTaskDetail">
      <div v-if="detailTask" class="detail-drawer inventory-task-detail-drawer">
        <div class="detail-hero">
          <div>
            <span class="eyebrow">INVENTORY TASK</span>
            <h2>{{ detailTask.name }}</h2>
            <p>{{ formatDate(detailTask.start_at) }} 至 {{ formatDate(detailTask.deadline_at) }}</p>
          </div>
          <el-tag :type="taskStatusTag(detailTask.status)" effect="plain">{{ taskStatusLabels[detailTask.status] || detailTask.status }}</el-tag>
        </div>
        <div class="detail-grid inventory-task-detail-grid">
          <div><span>目标组织</span><strong>{{ detailTask.target_count }} 家</strong></div>
          <div><span>提交进度</span><strong>已提交 {{ detailTask.submitted_count }} · 待提交 {{ pendingSubmissionCount(detailTask) }}</strong></div>
          <div><span>必填范围</span><strong>{{ detailTask.required_fields.map((item: string) => requiredFieldLabels[item] || item).join('、') || '未指定' }}</strong></div>
          <div><span>网络资产范围</span><strong>{{ detailTask.required_fields.includes('network_assets') ? (assetNetworkScopeLabels[detailTask.asset_network_scope] || detailTask.asset_network_scope) : '本任务不核查网络资产' }}</strong></div>
          <div><span>审核要求</span><strong>{{ detailTask.require_review ? '需要集团审核' : '提交后直接完成' }}</strong></div>
          <div><span>逾期提交</span><strong>{{ detailTask.allow_late_submission ? '允许' : '不允许' }}</strong></div>
        </div>
        <div class="form-section-title">任务说明</div>
        <p class="detail-description">{{ detailTask.description || '暂无说明' }}</p>
        <div class="form-section-title">组织填报进度（{{ taskOrganizationState(detailTask).total }}）</div>
        <el-table v-loading="taskOrganizationState(detailTask).loading" :data="taskOrganizationState(detailTask).items" size="small" class="task-organization-table" empty-text="暂无组织" max-height="460">
          <el-table-column label="填报组织" min-width="190">
            <template #default="itemScope"><strong>{{ itemScope.row.organization_name }}</strong></template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="itemScope"><el-tag :type="progressStatusTag(effectiveProgressStatus(detailTask, itemScope.row))" effect="plain">{{ progressStatusLabels[effectiveProgressStatus(detailTask, itemScope.row)] || itemScope.row.status }}</el-tag></template>
          </el-table-column>
          <el-table-column label="提交快照" min-width="170">
            <template #default="itemScope">
              <template v-if="itemScope.row.submitted_at"><strong>{{ itemScope.row.submitted_system_count }}</strong> 个系统 / <strong>{{ itemScope.row.submitted_asset_count }}</strong> 项资产<span class="table-secondary">{{ itemScope.row.submitted_by_name }} · {{ formatDate(itemScope.row.submitted_at) }}</span></template>
              <span v-else>—</span>
            </template>
          </el-table-column>
          <el-table-column label="填报说明/退回原因" min-width="210">
            <template #default="itemScope">
              <span v-if="itemScope.row.return_reason" class="return-reason">退回：{{ itemScope.row.return_reason }}</span>
              <span v-else>{{ itemScope.row.submission_note || '—' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="320" fixed="right">
            <template #default="itemScope">
              <el-button v-if="itemScope.row.snapshot_available" type="primary" link @click="openSubmissionDiff(detailTask, itemScope.row)">查看差异</el-button>
              <el-button v-if="itemScope.row.snapshot_available" type="primary" link @click="openReviewComments(detailTask, itemScope.row)">审核意见</el-button>
              <template v-if="auth.isGroupAdmin">
                <el-button v-if="detailTask.status === 'active' && itemScope.row.status === 'submitted'" type="success" link :loading="actionLoading === operationKey(detailTask, itemScope.row)" @click="approveProgress(detailTask, itemScope.row)">通过</el-button>
                <el-button v-if="detailTask.status === 'active' && itemScope.row.status === 'submitted'" type="danger" link :disabled="actionLoading === operationKey(detailTask, itemScope.row)" @click="openReturnDialog(detailTask, itemScope.row)">退回</el-button>
                <span v-else-if="itemScope.row.reviewed_at" class="table-secondary">审核：{{ itemScope.row.reviewed_by_name || '系统自动' }}<br>{{ formatDate(itemScope.row.reviewed_at) }}</span>
              </template>
              <template v-else>
                <el-button v-if="isTaskOpen(detailTask) && !['submitted', 'approved'].includes(itemScope.row.status)" type="primary" link @click="openInventoryBusinessSystems(detailTask, itemScope.row)">维护业务系统</el-button><el-button v-if="isTaskOpen(detailTask) && !['submitted', 'approved'].includes(itemScope.row.status)" type="primary" link @click="openInventoryAssets(detailTask, itemScope.row)">维护网络资产</el-button>
                <el-button v-if="canStart(detailTask, itemScope.row)" type="primary" link :loading="actionLoading === operationKey(detailTask, itemScope.row)" @click="startProgress(detailTask, itemScope.row)">开始填报</el-button>
                <el-button v-if="canSubmit(detailTask, itemScope.row)" type="success" link :disabled="actionLoading === operationKey(detailTask, itemScope.row)" @click="openSubmissionDialog(detailTask, itemScope.row)">提交</el-button>
                <span v-if="detailTask.status === 'active' && new Date(detailTask.start_at).getTime() > Date.now()" class="table-secondary">尚未到开始时间</span>
                <span v-else-if="detailTask.status === 'active' && !detailTask.allow_late_submission && new Date(detailTask.deadline_at).getTime() < Date.now() && !['submitted', 'approved'].includes(itemScope.row.status)" class="table-secondary">已截止，不允许逾期提交</span>
              </template>
            </template>
          </el-table-column>
        </el-table>
        <div v-if="taskOrganizationState(detailTask).total > 0" class="table-pagination task-organization-pagination">
          <el-pagination
            background
            small
            layout="total, sizes, prev, pager, next"
            :total="taskOrganizationState(detailTask).total"
            :current-page="taskOrganizationState(detailTask).page"
            :page-size="taskOrganizationState(detailTask).pageSize"
            :page-sizes="[20, 50, 100]"
            @current-change="handleTaskOrganizationPageChange(detailTask, $event)"
            @size-change="handleTaskOrganizationPageSizeChange(detailTask, $event)"
          />
        </div>
      </div>
    </el-drawer>

    <el-drawer v-model="diffDrawerVisible" title="填报提交差异" size="78%" destroy-on-close>
      <div v-loading="diffLoading" class="submission-diff-drawer">
        <template v-if="submissionDiff">
          <el-descriptions :column="3" border class="submission-diff-meta">
            <el-descriptions-item label="填报组织">{{ submissionDiff.submission.organization_name }}</el-descriptions-item>
            <el-descriptions-item label="当前提交">第 {{ submissionDiff.submission.version }} 版 · {{ formatDate(submissionDiff.submission.submitted_at) }}</el-descriptions-item>
            <el-descriptions-item label="提交人">{{ submissionDiff.submission.submitted_by_name || '—' }}</el-descriptions-item>
            <el-descriptions-item label="对比基线" :span="2">
              <template v-if="submissionDiff.previous_submission">
                {{ submissionDiff.previous_submission.task_name }} / 第 {{ submissionDiff.previous_submission.version }} 版 · {{ formatDate(submissionDiff.previous_submission.submitted_at) }}
              </template>
              <span v-else>首次提交，无历史基线</span>
            </el-descriptions-item>
            <el-descriptions-item label="快照规模">{{ submissionDiff.submission.system_count }} 个系统 / {{ submissionDiff.submission.asset_count }} 项资产</el-descriptions-item>
          </el-descriptions>

          <el-alert v-if="!submissionDiff.previous_submission" title="这是该组织的首次提交，当前全部系统和资产均计为新增。" type="info" :closable="false" show-icon />
          <div class="submission-diff-summary">
            <div class="added"><span>新增</span><strong>{{ submissionDiff.summary.added }}</strong></div>
            <div class="modified"><span>修改</span><strong>{{ submissionDiff.summary.modified }}</strong></div>
            <div class="removed"><span>删除/归档</span><strong>{{ submissionDiff.summary.removed }}</strong></div>
          </div>

          <section v-for="groupItem in diffGroups" :key="groupItem.key" class="submission-diff-group">
            <div class="submission-diff-group-heading">
              <h3>{{ groupItem.label }}</h3>
              <div>
                <el-tag type="success" effect="plain">新增 {{ groupItem.summary.added }}</el-tag>
                <el-tag type="warning" effect="plain">修改 {{ groupItem.summary.modified }}</el-tag>
                <el-tag type="danger" effect="plain">删除/归档 {{ groupItem.summary.removed }}</el-tag>
              </div>
            </div>
            <el-tabs v-model="diffActiveTabs[groupItem.key]" @tab-change="(name: string | number) => handleDiffTabChange(groupItem.key, name)">
              <el-tab-pane
                v-for="tabItem in diffChangeTabs"
                :key="tabItem.key"
                :name="tabItem.key"
                :label="`${tabItem.label}（${groupItem.summary[tabItem.key]}）`"
              >
                <el-table
                  v-loading="diffListState(groupItem.key, tabItem.key).loading"
                  :data="diffListState(groupItem.key, tabItem.key).items"
                  size="small"
                  :empty-text="tabItem.emptyText"
                  max-height="420"
                >
                  <el-table-column v-if="tabItem.key === 'modified'" type="expand">
                    <template #default="changeScope">
                      <div class="submission-field-changes">
                        <div v-for="change in changeScope.row.changes" :key="change.field" class="submission-field-change">
                          <strong>{{ change.label }}</strong>
                          <span class="before">{{ formatSnapshotValue(change.before) }}</span>
                          <span class="change-arrow">→</span>
                          <span class="after">{{ formatSnapshotValue(change.after) }}</span>
                        </div>
                      </div>
                    </template>
                  </el-table-column>
                  <el-table-column prop="resource_name" label="名称" min-width="220" />
                  <el-table-column prop="resource_key" label="唯一标识" min-width="260" show-overflow-tooltip />
                  <el-table-column v-if="tabItem.key === 'modified'" label="变更字段" width="110">
                    <template #default="changeScope">{{ changeScope.row.changes?.length || 0 }} 项</template>
                  </el-table-column>
                </el-table>
                <el-pagination
                  v-if="diffListState(groupItem.key, tabItem.key).total > 0"
                  class="table-pagination"
                  background
                  layout="total, sizes, prev, pager, next"
                  :page-sizes="[20, 50, 100]"
                  :current-page="diffListState(groupItem.key, tabItem.key).page"
                  :page-size="diffListState(groupItem.key, tabItem.key).pageSize"
                  :total="diffListState(groupItem.key, tabItem.key).total"
                  @current-change="(value: number) => handleDiffPageChange(groupItem.key, tabItem.key, value)"
                  @size-change="(value: number) => handleDiffPageSizeChange(groupItem.key, tabItem.key, value)"
                />
              </el-tab-pane>
            </el-tabs>
          </section>
        </template>
      </div>
    </el-drawer>

    <el-dialog v-model="createDialogVisible" title="创建填报任务" width="860px" class="inventory-task-create-dialog" destroy-on-close @closed="resetScopePreview">
      <el-alert title="任务创建后将关联到所选组织；网络资产核查范围会同步作用于质量检查、提交数量和快照。" type="info" :closable="false" show-icon />
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top" class="dialog-form-with-alert">
        <div class="form-grid two-columns">
          <el-form-item label="任务名称" prop="name" class="full-span"><el-input v-model="form.name" maxlength="255" placeholder="如：2026 年第三季度网络资产填报" /></el-form-item>
          <el-form-item label="开始时间" prop="start_at"><el-date-picker v-model="form.start_at" type="datetime" placeholder="选择开始时间" style="width:100%" /></el-form-item>
          <el-form-item label="截止时间" prop="deadline_at"><el-date-picker v-model="form.deadline_at" type="datetime" placeholder="选择截止时间" style="width:100%" /></el-form-item>
          <el-form-item label="任务状态"><el-select v-model="form.status" style="width:100%"><el-option label="立即下发" value="active" /><el-option label="保存草稿" value="draft" /></el-select></el-form-item>
          <el-form-item label="填报组织" prop="organization_ids"><el-select v-model="form.organization_ids" multiple filterable collapse-tags collapse-tags-tooltip placeholder="请选择子公司或部门" style="width:100%"><el-option v-for="item in eligibleOrganizations" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="填报内容" class="full-span"><el-checkbox-group v-model="form.required_fields"><el-checkbox label="business_systems">业务系统信息</el-checkbox><el-checkbox label="data_security">数据安全属性</el-checkbox><el-checkbox label="network_assets">网络资产信息</el-checkbox><el-checkbox label="owner_contacts">负责人及联系方式</el-checkbox></el-checkbox-group></el-form-item>
          <el-form-item label="网络资产核查范围" class="full-span">
            <el-radio-group v-model="form.asset_network_scope" :disabled="!form.required_fields.includes('network_assets')">
              <el-radio-button value="all">全部网络资产</el-radio-button>
              <el-radio-button value="public">仅公网资产</el-radio-button>
              <el-radio-button value="private">仅内网资产</el-radio-button>
            </el-radio-group>
            <div class="form-help">选择“仅公网”或“仅内网”时，范围为“未知”的资产不会进入本次质量检查和提交快照。</div>
          </el-form-item>
          <el-form-item label="范围影响预览" class="full-span">
            <div class="scope-preview-card" data-testid="scope-preview" v-loading="scopePreviewLoading">
              <div class="scope-preview-header">
                <div><strong>当前台账预计纳入范围</strong><span>仅用于下发前确认，子公司提交时会按届时最新台账重新计算。</span></div>
                <el-button size="small" :loading="scopePreviewLoading" :disabled="!scopePreviewReady" @click="loadScopePreview">刷新预览</el-button>
              </div>
              <el-empty v-if="form.organization_ids.length === 0" :image-size="48" description="选择填报组织后显示范围预览" />
              <el-alert v-else-if="!scopePreviewHasResourceType" title="请至少选择一种业务系统或网络资产填报内容" type="warning" :closable="false" show-icon />
              <el-alert v-else-if="scopePreviewError" :title="scopePreviewError" type="error" :closable="false" show-icon />
              <template v-else-if="scopePreview">
                <div class="scope-preview-summary" data-testid="scope-preview-summary">
                  <span><strong>{{ scopePreview.organization_count }}</strong> 家组织</span>
                  <span><strong>{{ scopePreview.system_count }}</strong> 个业务系统</span>
                  <span><strong>{{ scopePreview.asset_count }}</strong> 项网络资产</span>
                </div>
                <el-alert v-if="scopePreview.empty_organization_count > 0" :title="`${scopePreview.empty_organization_count} 家组织在当前核查范围内暂无资源，请确认是否仍需下发。`" type="warning" :closable="false" show-icon />
                <el-table :data="scopePreview.items" size="small" max-height="220" empty-text="暂无预览数据">
                  <el-table-column prop="organization_name" label="填报组织" min-width="240" />
                  <el-table-column label="业务系统" prop="system_count" width="120" align="right" />
                  <el-table-column label="网络资产" prop="asset_count" width="120" align="right" />
                  <el-table-column label="范围状态" width="120"><template #default="scope"><el-tag :type="scope.row.system_count + scope.row.asset_count > 0 ? 'success' : 'warning'" effect="plain">{{ scope.row.system_count + scope.row.asset_count > 0 ? '有数据' : '暂无资源' }}</el-tag></template></el-table-column>
                </el-table>
              </template>
            </div>
          </el-form-item>
          <el-form-item label="任务说明" class="full-span"><el-input v-model="form.description" type="textarea" :rows="3" maxlength="5000" show-word-limit placeholder="填写本次填报范围、口径和注意事项" /></el-form-item>
          <el-form-item label="需要集团审核"><el-switch v-model="form.require_review" /></el-form-item>
          <el-form-item label="允许逾期提交"><el-switch v-model="form.allow_late_submission" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="createDialogVisible = false">取消</el-button><el-button type="primary" :loading="submitting" @click="createTask">{{ form.status === 'active' ? '创建并下发' : '保存草稿' }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="submissionDialogVisible" title="提交填报结果" width="640px" destroy-on-close>
      <el-alert :title="`系统会按照任务范围检查必填数据，并记录${selectedTask?.required_fields.includes('network_assets') ? `“${assetNetworkScopeLabels[selectedTask.asset_network_scope] || selectedTask.asset_network_scope}”` : ''}提交快照。`" type="info" :closable="false" show-icon />
      <div class="quality-check" v-loading="qualityLoading">
        <div class="quality-check-header">
          <div><strong>提交前质量检查</strong><span v-if="qualityCheck">{{ qualityCheck.system_count }} 个系统 / {{ qualityCheck.asset_count }} 项资产</span></div>
          <el-button size="small" :loading="qualityLoading" @click="loadQualityCheck">重新检查</el-button>
        </div>
        <el-alert v-if="qualityCheck?.passed" :title="qualityCheck.warning_count ? `检查通过，另有 ${qualityCheck.warning_count} 项提醒` : '检查通过，可以提交'" :type="qualityCheck.warning_count ? 'warning' : 'success'" :closable="false" show-icon />
        <el-alert v-else-if="qualityCheck" :title="`发现 ${qualityCheck.error_count} 项错误，修正后才能提交`" type="error" :closable="false" show-icon />
        <ul v-if="qualityCheck?.issues.length" class="quality-issue-list">
          <li v-for="issue in qualityCheck.issues" :key="issue.code" :class="issue.severity">
            <el-tag :type="issue.severity === 'error' ? 'danger' : 'warning'" effect="plain" size="small">{{ issue.severity === 'error' ? '错误' : '提醒' }}</el-tag>
            <span>{{ issue.message }}</span><strong>{{ issue.count }}</strong>
          </li>
        </ul>
        <div v-if="qualityCheck && !qualityCheck.passed" class="quality-actions">
          <el-button size="small" @click="openSelectedInventoryBusinessSystems">维护业务系统草稿</el-button>
          <el-button size="small" @click="openSelectedInventoryAssets">维护网络资产草稿</el-button>
        </div>
      </div>
      <el-form label-position="top">
        <el-form-item label="任务"><el-input :model-value="selectedTask?.name" disabled /></el-form-item>
        <el-form-item label="填报组织"><el-input :model-value="selectedProgress?.organization_name" disabled /></el-form-item>
        <el-form-item label="填报说明"><el-input v-model="submissionForm.note" type="textarea" :rows="4" maxlength="5000" show-word-limit placeholder="可填写数据范围、统计口径或需要集团关注的事项" /></el-form-item>
      </el-form>
      <template #footer><el-button @click="submissionDialogVisible = false">取消</el-button><el-button type="primary" :loading="actionLoading !== ''" :disabled="qualityLoading || !qualityCheck?.passed" @click="submitProgress">确认提交</el-button></template>
    </el-dialog>

    <el-dialog v-model="returnDialogVisible" title="退回填报结果" width="920px" destroy-on-close>
      <el-form label-position="top">
        <el-form-item label="填报组织"><el-input :model-value="selectedProgress?.organization_name" disabled /></el-form-item>
        <el-form-item label="整体退回原因" required><el-input v-model="returnForm.reason" type="textarea" :rows="3" maxlength="2000" show-word-limit placeholder="请明确说明本次退回的总体原因" /></el-form-item>
      </el-form>
      <el-divider content-position="left">逐条审核意见（可选）</el-divider>
      <el-alert title="可以针对本次提交中的具体业务系统或网络资产填写意见；子公司将在任务中查看这些意见。单次最多 100 条。" type="info" :closable="false" show-icon />
      <div v-loading="returnContextLoading" class="review-resource-panel">
        <div class="review-resource-toolbar">
          <el-input v-model="returnResourceSearch" clearable placeholder="按名称或资产标识搜索" class="review-resource-search" @keyup.enter="applyReturnResourceSearch" @clear="applyReturnResourceSearch" />
          <el-button :loading="returnResourceLoading" @click="applyReturnResourceSearch">搜索</el-button>
        </div>
        <el-table v-loading="returnResourceLoading" :data="returnResources" max-height="390" empty-text="本次提交没有可填写意见的明细">
          <el-table-column label="类型" width="95"><template #default="scope">{{ reviewResourceTypeLabel(scope.row.resource_type) }}</template></el-table-column>
          <el-table-column label="对象" min-width="230"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.resource_name }}</strong><span>{{ scope.row.resource_key }}</span></div></template></el-table-column>
          <el-table-column label="审核意见" min-width="390"><template #default="scope"><el-input v-model="returnCommentDrafts[reviewResourceKey(scope.row)]" maxlength="2000" placeholder="针对该条数据填写整改要求（可选）" /></template></el-table-column>
        </el-table>
        <div v-if="returnResourceTotal > 0" class="table-pagination dialog-pagination">
          <el-pagination background small layout="total, sizes, prev, pager, next" :total="returnResourceTotal" :current-page="returnResourcePage" :page-size="returnResourcePageSize" :page-sizes="[20, 50, 100]" @current-change="handleReturnResourcePageChange" @size-change="handleReturnResourcePageSizeChange" />
        </div>
      </div>
      <template #footer><el-button @click="returnDialogVisible = false">取消</el-button><el-button type="danger" :loading="actionLoading !== ''" :disabled="returnContextLoading || returnResourceLoading" @click="returnProgress">确认退回</el-button></template>
    </el-dialog>

    <el-dialog v-model="reviewContextVisible" title="逐条审核意见" width="860px" destroy-on-close>
      <div v-loading="reviewContextLoading">
        <el-descriptions v-if="reviewContext" :column="3" border class="review-context-summary">
          <el-descriptions-item label="填报组织">{{ reviewContext.submission.organization_name }}</el-descriptions-item>
          <el-descriptions-item label="提交版本">第 {{ reviewContext.submission.version }} 版</el-descriptions-item>
          <el-descriptions-item label="提交时间">{{ formatDate(reviewContext.submission.submitted_at) }}</el-descriptions-item>
        </el-descriptions>
        <div v-loading="reviewCommentLoading">
          <el-alert v-if="reviewContext && !reviewCommentLoading && reviewCommentTotal === 0" title="当前任务尚无逐条审核意见。" type="info" :closable="false" show-icon />
          <el-table v-else-if="reviewContext && reviewComments.length" :data="reviewComments" max-height="460" empty-text="暂无逐条审核意见">
            <el-table-column label="版本" width="80"><template #default="scope">第 {{ scope.row.submission_version }} 版</template></el-table-column>
            <el-table-column label="类型" width="95"><template #default="scope">{{ reviewResourceTypeLabel(scope.row.resource_type) }}</template></el-table-column>
            <el-table-column label="对象" min-width="210"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.resource_name }}</strong><span>{{ scope.row.resource_key }}</span></div></template></el-table-column>
            <el-table-column prop="comment" label="审核意见" min-width="270" />
            <el-table-column label="审核人 / 时间" width="175"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.created_by_name || '集团审核人' }}</strong><span>{{ formatDate(scope.row.created_at) }}</span></div></template></el-table-column>
          </el-table>
          <div v-if="reviewCommentTotal > 0" class="table-pagination dialog-pagination">
            <el-pagination background small layout="total, sizes, prev, pager, next" :total="reviewCommentTotal" :current-page="reviewCommentPage" :page-size="reviewCommentPageSize" :page-sizes="[20, 50, 100]" @current-change="handleReviewCommentPageChange" @size-change="handleReviewCommentPageSizeChange" />
          </div>
        </div>
      </div>
      <template #footer><el-button type="primary" @click="reviewContextVisible = false">关闭</el-button></template>
    </el-dialog>
  </section>
</template>

<style scoped>
.form-help { width:100%; margin-top:8px; color:var(--muted); font-size:12px; line-height:1.6; }
.scope-preview-card { width:100%; min-height:118px; padding:16px; border:1px solid var(--line); border-radius:12px; background:#f7f9fb; box-sizing:border-box; }
.scope-preview-header { display:flex; align-items:flex-start; justify-content:space-between; gap:16px; margin-bottom:14px; }
.scope-preview-header > div { display:flex; flex-direction:column; gap:4px; }
.scope-preview-header span { color:var(--muted); font-size:12px; line-height:1.5; }
.scope-preview-summary { display:flex; flex-wrap:wrap; gap:10px; margin-bottom:12px; }
.scope-preview-summary span { min-width:130px; padding:10px 12px; border:1px solid var(--line); border-radius:10px; background:var(--surface); color:var(--muted); }
.scope-preview-summary strong { margin-right:4px; color:var(--ink); font-size:18px; }
.scope-preview-card :deep(.el-alert) { margin-bottom:12px; }
.scope-preview-card :deep(.el-empty) { padding:8px 0 2px; }
:global(.inventory-task-create-dialog .el-dialog__body) { max-height:calc(100vh - 190px); overflow-y:auto; }
.table-pagination { display:flex; justify-content:flex-end; margin-top:18px; }
.dialog-pagination { overflow-x:auto; padding-bottom:2px; }
.inventory-task-detail-drawer { min-width:0; }
.inventory-task-detail-grid { grid-template-columns:repeat(3, minmax(0, 1fr)); }
.task-organization-pagination { flex-direction:row !important; }
.submission-progress { display:flex; align-items:center; gap:8px; white-space:nowrap; color:var(--muted); }
.submission-progress span { display:inline-flex; align-items:baseline; gap:4px; }
.submission-progress strong { color:var(--navy); font-size:15px; }
.submission-progress i { color:var(--line); font-style:normal; }
.review-resource-toolbar { display:flex; gap:10px; margin:14px 0; }
.review-resource-search { flex:1; }
@media (max-width:900px) {
  .inventory-task-detail-grid { grid-template-columns:1fr; }
}
</style>
