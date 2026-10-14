<script setup lang="ts">
import RowActions from '../components/RowActions.vue'
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface ItemResponse<T> { item: T }
interface Asset {
  id: string
  type: string
  value: string
  name?: string
  organization_id: string
  organization_name: string
  business_system_id?: string
  business_system_name?: string
  network_scope: string
  network_area_id?: string
  network_area_name?: string
  environment: string
  importance: string
  status: string
}

interface OrganizationReference {
  id: string
  code: string
  name: string
  type: string
  status: string
}
interface BusinessSystemReference {
  id: string
  code: string
  name: string
  organization_id: string
  organization_name: string
  status: string
}
interface NetworkAreaReference {
  id: string
  code?: string
  name: string
  organization_id: string
  organization_name: string
  status: string
}
type ScopePickerMode = 'asset' | 'organization' | 'business_system' | 'network_area'
type ScopeReference = OrganizationReference | BusinessSystemReference | NetworkAreaReference
interface AssetSelectionResponse { items: Asset[]; total: number }
interface ScopeAsset {
  id: string
  asset_id?: string
  asset_type: string
  asset_value: string
  asset_name?: string
  organization_id?: string
  organization_code: string
  organization_name: string
  business_system_code?: string
  business_system_name?: string
  network_scope: string
  network_area_name?: string
  environment: string
  importance: string
  snapshot_at: string
}
interface BatchAttachment {
  id: string
  batch_id: string
  category: string
  file_name: string
  content_type: string
  size_bytes: number
  description?: string
  uploaded_by_name?: string
  created_at: string
}
interface BatchEvent {
  id: number
  action: string
  from_status?: string
  to_status?: string
  comment?: string
  actor_name?: string
  created_at: string
}
interface SecurityTestBatch {
  id: string
  code: string
  name: string
  test_type: string
  status: string
  provider: string
  authorization_note: string
  authorized_by_name?: string
  authorized_at?: string
  start_at: string
  end_at: string
  started_at?: string
  completed_at?: string
  cancelled_at?: string
  notes?: string
  asset_count: number
  organization_count: number
  created_by_name?: string
  created_at: string
  updated_at: string
  scope_assets?: ScopeAsset[]
}

const loading = ref(false)
const submitting = ref(false)
const actionLoading = ref(false)
const batches = ref<SecurityTestBatch[]>([])
const selectedAssetMap = ref<Record<string, Asset>>({})
const scopePickerVisible = ref(false)
const scopePickerMode = ref<ScopePickerMode>('asset')
const scopePickerLoading = ref(false)
const scopePickerApplying = ref(false)
const scopePickerAssets = ref<Asset[]>([])
const scopePickerReferences = ref<ScopeReference[]>([])
const scopePickerSelectedIDs = ref<string[]>([])
const scopePickerKeyword = ref('')
const scopePickerPage = ref(1)
const scopePickerPageSize = ref(20)
const scopePickerTotal = ref(0)
const statusFilter = ref('')
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const dialogVisible = ref(false)
const detailVisible = ref(false)
const editingID = ref('')
const currentBatch = ref<SecurityTestBatch | null>(null)
const formRef = ref<FormInstance>()
const attachmentInput = ref<HTMLInputElement>()
const attachmentFile = ref<File | null>(null)
const attachmentCategory = ref('test_report')
const attachmentDescription = ref('')
const attachmentLoading = ref(false)
const batchAttachments = ref<BatchAttachment[]>([])
const attachmentListLoading = ref(false)
const attachmentPage = ref(1)
const attachmentPageSize = ref(20)
const attachmentTotal = ref(0)
const batchEvents = ref<BatchEvent[]>([])
const eventListLoading = ref(false)
const eventPage = ref(1)
const eventPageSize = ref(20)
const eventTotal = ref(0)

const form = reactive({
  batch_name: '',
  test_type: 'penetration',
  provider: '集团安全团队',
  authorization_note: '',
  start_at: null as Date | null,
  end_at: null as Date | null,
  notes: '',
  asset_ids: [] as string[],
})

const statusLabels: Record<string, string> = {
  draft: '草稿', authorized: '已授权', in_progress: '测试中', completed: '已完成', cancelled: '已取消',
}
const statusTypes: Record<string, string> = {
  draft: 'info', authorized: 'warning', in_progress: 'primary', completed: 'success', cancelled: 'info',
}
const typeLabels: Record<string, string> = {
  penetration: '渗透测试', manual: '人工检查', scan: '扫描测试', retest: '复测', other: '其他',
}
const assetTypeLabels: Record<string, string> = { domain: '域名', ip: 'IP', url: 'URL', service: '网络服务', other: '其他' }
const eventLabels: Record<string, string> = {
  create: '创建批次', update: '更新批次', authorize: '完成授权', start: '开始测试', complete: '完成测试', cancel: '取消批次',
  attachment_upload: '上传附件', attachment_delete: '删除附件',
}
const attachmentCategoryLabels: Record<string, string> = { test_report: '测试报告', reference: '参考材料' }

const selectedAssets = computed(() => form.asset_ids.map(id => selectedAssetMap.value[id]).filter((item): item is Asset => Boolean(item)))
const selectedOrganizationCount = computed(() => new Set(selectedAssets.value.map(item => item.organization_id)).size)
const scopePickerTitles: Record<ScopePickerMode, string> = {
  asset: '逐项添加公网资产', organization: '按组织纳入公网资产', business_system: '按业务系统纳入公网资产', network_area: '按网络区域纳入公网资产',
}
const scopePickerTitle = computed(() => scopePickerTitles[scopePickerMode.value])
const scopePickerDimensionLabels: Record<ScopePickerMode, string> = { asset: '资产', organization: '组织', business_system: '业务系统', network_area: '网络区域' }
const scopePickerDimensionLabel = computed(() => scopePickerDimensionLabels[scopePickerMode.value])
const scopeByOrganization = computed(() => {
  const groups = new Map<string, number>()
  for (const item of currentBatch.value?.scope_assets ?? []) {
    groups.set(item.organization_name, (groups.get(item.organization_name) ?? 0) + 1)
  }
  return Array.from(groups, ([name, count]) => ({ name, count }))
})

const rules: FormRules = {
  batch_name: [{ required: true, message: '请输入批次名称', trigger: 'blur' }],
  test_type: [{ required: true, message: '请选择测试类型', trigger: 'change' }],
  provider: [{ required: true, message: '请输入测试单位或团队', trigger: 'blur' }],
  authorization_note: [{ required: true, message: '请输入授权说明', trigger: 'blur' }],
  start_at: [{ required: true, message: '请选择计划开始时间', trigger: 'change' }],
  end_at: [{ required: true, message: '请选择计划结束时间', trigger: 'change' }],
  asset_ids: [{ required: true, type: 'array', min: 1, max: 200, message: '请选择 1 至 200 项公网资产', trigger: 'change' }],
}

onMounted(load)

async function load() {
  loading.value = true
  try {
    const params: Record<string, string | number> = { page: page.value, page_size: pageSize.value }
    if (statusFilter.value) params.status = statusFilter.value
    const response = await apiClient.get<ListResponse<SecurityTestBatch>>('/security-test-batches', { params })
    batches.value = response.data.items
    total.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取安全测试批次失败'))
  } finally {
    loading.value = false
  }
}

async function refreshAfterMutation() {
  await load()
  if (batches.value.length === 0 && total.value > 0 && page.value > 1) {
    page.value -= 1
    await load()
  }
}

function applyStatusFilter() {
  page.value = 1
  void load()
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

function defaultTimeRange() {
  const start = new Date()
  start.setDate(start.getDate() + 1)
  start.setHours(9, 0, 0, 0)
  const end = new Date(start)
  end.setDate(end.getDate() + 5)
  end.setHours(18, 0, 0, 0)
  return { start, end }
}

function openCreate() {
  const range = defaultTimeRange()
  editingID.value = ''
  selectedAssetMap.value = {}
  Object.assign(form, {
    batch_name: '', test_type: 'penetration', provider: '集团安全团队', authorization_note: '',
    start_at: range.start, end_at: range.end, notes: '禁止破坏性测试；不得超出授权资产范围。', asset_ids: [],
  })
  dialogVisible.value = true
}

async function openEdit(item: SecurityTestBatch) {
  const detail = await fetchDetail(item.id)
  if (!detail) return
  editingID.value = detail.id
  const scopeAssets = (detail.scope_assets ?? []).filter(asset => asset.asset_id)
  selectedAssetMap.value = Object.fromEntries(scopeAssets.map(asset => [asset.asset_id as string, {
    id: asset.asset_id as string,
    type: asset.asset_type,
    value: asset.asset_value,
    name: asset.asset_name,
    organization_id: asset.organization_id ?? '',
    organization_name: asset.organization_name,
    business_system_name: asset.business_system_name,
    network_scope: asset.network_scope,
    network_area_name: asset.network_area_name,
    environment: asset.environment,
    importance: asset.importance,
    status: 'active',
  } satisfies Asset]))
  Object.assign(form, {
    batch_name: detail.name, test_type: detail.test_type, provider: detail.provider,
    authorization_note: detail.authorization_note, start_at: new Date(detail.start_at), end_at: new Date(detail.end_at),
    notes: detail.notes ?? '', asset_ids: scopeAssets.map(asset => asset.asset_id as string),
  })
  dialogVisible.value = true
}

function scopeReferenceTitle(item: ScopeReference) {
  if (scopePickerMode.value === 'organization') {
    const organization = item as OrganizationReference
    return organization.name
  }
  if (scopePickerMode.value === 'business_system') {
    const system = item as BusinessSystemReference
    return system.name
  }
  const area = item as NetworkAreaReference
  return `${area.code ? `${area.code} · ` : ''}${area.name}`
}

function scopeReferenceSubtitle(item: ScopeReference) {
  if (scopePickerMode.value === 'organization') return (item as OrganizationReference).type === 'group' ? '集团总部' : '下级组织'
  return (item as BusinessSystemReference | NetworkAreaReference).organization_name
}

function isAssetSelected(id: string) {
  return form.asset_ids.includes(id)
}

function addAsset(item: Asset) {
  if (isAssetSelected(item.id)) return
  if (form.asset_ids.length >= 200) {
    ElMessage.warning('测试范围最多只能包含 200 项资产')
    return
  }
  selectedAssetMap.value = { ...selectedAssetMap.value, [item.id]: item }
  form.asset_ids.push(item.id)
  formRef.value?.clearValidate('asset_ids')
}

function removeAsset(id: string) {
  form.asset_ids = form.asset_ids.filter(item => item !== id)
  const next = { ...selectedAssetMap.value }
  delete next[id]
  selectedAssetMap.value = next
}

function toggleScopeReference(id: string, checked: unknown) {
  if (Boolean(checked)) {
    if (scopePickerSelectedIDs.value.length >= 100) {
      ElMessage.warning('单次最多选择 100 个圈选条件')
      return
    }
    if (!scopePickerSelectedIDs.value.includes(id)) scopePickerSelectedIDs.value.push(id)
    return
  }
  scopePickerSelectedIDs.value = scopePickerSelectedIDs.value.filter(item => item !== id)
}

async function openScopePicker(mode: ScopePickerMode) {
  scopePickerMode.value = mode
  scopePickerKeyword.value = ''
  scopePickerPage.value = 1
  scopePickerPageSize.value = 20
  scopePickerSelectedIDs.value = []
  scopePickerAssets.value = []
  scopePickerReferences.value = []
  scopePickerVisible.value = true
  await loadScopePicker()
}

async function loadScopePicker() {
  scopePickerLoading.value = true
  try {
    const params: Record<string, string | number | boolean> = {
      page: scopePickerPage.value,
      page_size: scopePickerPageSize.value,
    }
    if (scopePickerKeyword.value.trim()) params.q = scopePickerKeyword.value.trim()
    if (scopePickerMode.value === 'asset') params.public_only = true
    const endpoint = {
      asset: '/security-reference/assets',
      organization: '/security-reference/organizations',
      business_system: '/security-reference/business-systems',
      network_area: '/security-reference/network-areas',
    }[scopePickerMode.value]
    const response = await apiClient.get<ListResponse<Asset | ScopeReference>>(endpoint, { params })
    scopePickerTotal.value = response.data.total
    if (scopePickerMode.value === 'asset') {
      scopePickerAssets.value = response.data.items as Asset[]
      scopePickerReferences.value = []
    } else {
      scopePickerReferences.value = response.data.items as ScopeReference[]
      scopePickerAssets.value = []
    }
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取测试范围候选失败'))
  } finally {
    scopePickerLoading.value = false
  }
}

function searchScopePicker() {
  scopePickerPage.value = 1
  void loadScopePicker()
}

function handleScopePickerPageChange(value: number) {
  scopePickerPage.value = value
  void loadScopePicker()
}

function handleScopePickerPageSizeChange(value: number) {
  scopePickerPageSize.value = value
  scopePickerPage.value = 1
  void loadScopePicker()
}

async function applyBulkScope() {
  if (scopePickerMode.value === 'asset') return
  if (scopePickerSelectedIDs.value.length === 0) {
    ElMessage.warning(`请至少选择一个${scopePickerDimensionLabel.value}`)
    return
  }
  scopePickerApplying.value = true
  try {
    const response = await apiClient.post<AssetSelectionResponse>('/security-reference/assets/resolve', {
      dimension: scopePickerMode.value,
      ids: scopePickerSelectedIDs.value,
    })
    if (response.data.total === 0) {
      ElMessage.warning(`所选${scopePickerDimensionLabel.value}没有可纳入的未归档公网资产`)
      return
    }
    if (response.data.total > 200) {
      ElMessage.warning(`共匹配 ${response.data.total} 项公网资产，超过 200 项上限，请缩小圈选范围`)
      return
    }
    const existing = new Set(form.asset_ids)
    const additions = response.data.items.filter(item => !existing.has(item.id))
    const mergedCount = form.asset_ids.length + additions.length
    if (mergedCount > 200) {
      ElMessage.warning(`合并后将达到 ${mergedCount} 项，超过 200 项上限；请先移除部分资产或缩小圈选范围`)
      return
    }
    const nextMap = { ...selectedAssetMap.value }
    for (const item of response.data.items) nextMap[item.id] = item
    selectedAssetMap.value = nextMap
    form.asset_ids.push(...additions.map(item => item.id))
    formRef.value?.clearValidate('asset_ids')
    const duplicateCount = response.data.items.length - additions.length
    ElMessage.success(`已纳入 ${additions.length} 项公网资产${duplicateCount ? `，跳过 ${duplicateCount} 项重复资产` : ''}`)
    scopePickerVisible.value = false
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '批量纳入公网资产失败'))
  } finally {
    scopePickerApplying.value = false
  }
}

async function save() {
  if (!formRef.value || !(await formRef.value.validate())) return
  if (!form.start_at || !form.end_at || form.end_at <= form.start_at) {
    ElMessage.warning('计划结束时间必须晚于开始时间')
    return
  }
  submitting.value = true
  try {
    const payload = {
      ...form,
      start_at: form.start_at.toISOString(),
      end_at: form.end_at.toISOString(),
    }
    if (editingID.value) await apiClient.patch(`/security-test-batches/${editingID.value}`, payload)
    else await apiClient.post('/security-test-batches', payload)
    ElMessage.success(editingID.value ? '安全测试批次已更新' : '安全测试批次已创建')
    dialogVisible.value = false
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, editingID.value ? '更新批次失败' : '创建批次失败'))
  } finally {
    submitting.value = false
  }
}

async function fetchDetail(id: string) {
  try {
    const response = await apiClient.get<ItemResponse<SecurityTestBatch>>(`/security-test-batches/${id}`)
    return response.data.item
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取批次详情失败'))
    return null
  }
}

async function loadBatchAttachments() {
  if (!currentBatch.value) return
  attachmentListLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<BatchAttachment>>(`/security-test-batches/${currentBatch.value.id}/attachments`, {
      params: { page: attachmentPage.value, page_size: attachmentPageSize.value },
    })
    batchAttachments.value = response.data.items
    attachmentTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取测试附件失败'))
  } finally {
    attachmentListLoading.value = false
  }
}

async function loadBatchEvents() {
  if (!currentBatch.value) return
  eventListLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<BatchEvent>>(`/security-test-batches/${currentBatch.value.id}/events`, {
      params: { page: eventPage.value, page_size: eventPageSize.value },
    })
    batchEvents.value = response.data.items
    eventTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取批次时间线失败'))
  } finally {
    eventListLoading.value = false
  }
}

async function openDetail(item: SecurityTestBatch) {
  const detail = await fetchDetail(item.id)
  if (!detail) return
  currentBatch.value = detail
  batchAttachments.value = []
  batchEvents.value = []
  attachmentPage.value = 1
  eventPage.value = 1
  attachmentTotal.value = 0
  eventTotal.value = 0
  detailVisible.value = true
  await Promise.all([loadBatchAttachments(), loadBatchEvents()])
}

function handleAttachmentPageChange(value: number) { attachmentPage.value = value; void loadBatchAttachments() }
function handleAttachmentPageSizeChange(value: number) { attachmentPageSize.value = value; attachmentPage.value = 1; void loadBatchAttachments() }
function handleEventPageChange(value: number) { eventPage.value = value; void loadBatchEvents() }
function handleEventPageSizeChange(value: number) { eventPageSize.value = value; eventPage.value = 1; void loadBatchEvents() }

async function refreshAttachmentsAfterMutation() {
  await loadBatchAttachments()
  if (batchAttachments.value.length === 0 && attachmentTotal.value > 0 && attachmentPage.value > 1) {
    attachmentPage.value -= 1
    await loadBatchAttachments()
  }
}

async function runAction(item: SecurityTestBatch, action: 'authorize' | 'start' | 'complete' | 'cancel') {
  const prompts = {
    authorize: { title: '确认授权测试', message: '授权后测试范围将被冻结，不能再编辑。请输入授权补充说明（可留空）。', required: false, type: 'warning' },
    start: { title: '开始安全测试', message: '请输入开始测试说明（可留空）。', required: false, type: 'warning' },
    complete: { title: '完成安全测试', message: '请输入测试完成说明（可留空）。', required: false, type: 'success' },
    cancel: { title: '取消安全测试批次', message: '请输入取消原因。', required: true, type: 'warning' },
  } as const
  const config = prompts[action]
  try {
    const result = await ElMessageBox.prompt(config.message, config.title, {
      confirmButtonText: '确认', cancelButtonText: '返回', type: config.type,
      inputValidator: value => !config.required || Boolean(value?.trim()) || '取消原因不能为空',
    })
    actionLoading.value = true
    await apiClient.post(`/security-test-batches/${item.id}/${action}`, { comment: result.value ?? '' })
    ElMessage.success({ authorize: '批次已授权', start: '测试已开始', complete: '测试已完成', cancel: '批次已取消' }[action])
    await refreshAfterMutation()
    if (detailVisible.value && currentBatch.value?.id === item.id) {
      const detail = await fetchDetail(item.id)
      if (detail) currentBatch.value = detail
      eventPage.value = 1
      await loadBatchEvents()
    }
  } catch (error: unknown) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, '批次状态操作失败'))
  } finally {
    actionLoading.value = false
  }
}

function openAttachmentPicker() {
  attachmentInput.value?.click()
}

function onAttachmentSelected(event: Event) {
  const target = event.target as HTMLInputElement
  const file = target.files?.[0] ?? null
  if (file && file.size > 20 * 1024 * 1024) {
    ElMessage.warning('单个附件不能超过 20MB')
    target.value = ''
    attachmentFile.value = null
    return
  }
  attachmentFile.value = file
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${(value / 1024 / 1024).toFixed(1)} MB`
}

async function uploadAttachment() {
  if (!currentBatch.value || !attachmentFile.value) {
    ElMessage.warning('请先选择要上传的附件')
    return
  }
  attachmentLoading.value = true
  try {
    const payload = new FormData()
    payload.append('file', attachmentFile.value)
    payload.append('category', attachmentCategory.value)
    payload.append('description', attachmentDescription.value.trim())
    await apiClient.post(`/security-test-batches/${currentBatch.value.id}/attachments`, payload, { headers: { 'Content-Type': 'multipart/form-data' } })
    ElMessage.success('测试附件已上传')
    attachmentFile.value = null
    attachmentDescription.value = ''
    if (attachmentInput.value) attachmentInput.value.value = ''
    attachmentPage.value = 1
    eventPage.value = 1
    await Promise.all([loadBatchAttachments(), loadBatchEvents()])
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '上传测试附件失败'))
  } finally {
    attachmentLoading.value = false
  }
}

async function downloadAttachment(item: BatchAttachment) {
  if (!currentBatch.value) return
  try {
    const response = await apiClient.get(`/security-test-batches/${currentBatch.value.id}/attachments/${item.id}/download`, { responseType: 'blob' })
    const url = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = url
    link.download = item.file_name
    link.click()
    URL.revokeObjectURL(url)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '下载测试附件失败'))
  }
}

async function deleteAttachment(item: BatchAttachment) {
  if (!currentBatch.value) return
  try {
    await ElMessageBox.confirm(`确定删除附件“${item.file_name}”吗？`, '删除测试附件', {
      confirmButtonText: '删除', cancelButtonText: '取消', type: 'warning',
    })
    attachmentLoading.value = true
    await apiClient.delete(`/security-test-batches/${currentBatch.value.id}/attachments/${item.id}`)
    ElMessage.success('测试附件已删除')
    await refreshAttachmentsAfterMutation()
    eventPage.value = 1
    await loadBatchEvents()
  } catch (error: unknown) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, '删除测试附件失败'))
  } finally {
    attachmentLoading.value = false
  }
}

function formatTime(value?: string) {
  return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '-'
}
</script>

<template>
  <section data-testid="security-test-batches-page">
    <div class="page-heading"><h1>安全测试</h1>
      <div class="button-row">
        <el-button type="primary" data-testid="create-security-test-batch" @click="openCreate">新建测试批次</el-button>
      </div>
    </div>

    <div class="panel security-test-filter-panel">
      <el-select v-model="statusFilter" clearable placeholder="全部状态" style="width: 180px" @change="applyStatusFilter">
        <el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" />
      </el-select>
      <span class="security-test-filter-hint">测试范围仅允许选择未归档的公网资产，单批次最多 200 项。</span>
      <span class="security-test-filter-count">当前页 {{ batches.length }} 条，共 {{ total }} 个批次</span>
    </div>

    <div class="panel data-table-panel" v-loading="loading">
      <el-table :data="batches" row-key="id" empty-text="暂无安全测试批次">

        <el-table-column label="测试批次" min-width="230">
          <template #default="{ row }"><button class="link-button" type="button" @click="openDetail(row)">{{ row.name }}</button><span class="list-secondary">{{ row.code }}</span></template>
        </el-table-column>
        <el-table-column label="类型" width="95"><template #default="{ row }">{{ typeLabels[row.test_type] ?? row.test_type }}</template></el-table-column>
        <el-table-column label="状态" width="95"><template #default="{ row }"><el-tag :type="statusTypes[row.status] as any">{{ statusLabels[row.status] }}</el-tag></template></el-table-column>
        <el-table-column label="测试范围" width="130"><template #default="{ row }">{{ row.asset_count }} 项 / {{ row.organization_count }} 家组织</template></el-table-column>
        <el-table-column prop="provider" label="测试单位" min-width="130" />
        <el-table-column label="计划窗口" min-width="165"><template #default="{ row }">{{ formatTime(row.start_at) }}<br>{{ formatTime(row.end_at) }}</template></el-table-column>
        <el-table-column label="操作" width="88" fixed="right" align="right" header-align="right" class-name="table-action-column">
          <template #default="{ row }"><RowActions :direct="!['draft', 'authorized', 'in_progress'].includes(row.status)" :label="`操作：${row.name || row.title || row.code || row.display_name || row.id}`">
            <el-button link type="primary" @click="openDetail(row)">详情</el-button>
            <el-button v-if="row.status === 'draft'" link type="primary" @click="openEdit(row)">编辑</el-button>
            <el-button v-if="row.status === 'draft'" link type="warning" :loading="actionLoading" @click="runAction(row, 'authorize')">授权</el-button>
            <el-button v-if="row.status === 'authorized'" link type="primary" :loading="actionLoading" @click="runAction(row, 'start')">开始</el-button>
            <el-button v-if="row.status === 'in_progress'" link type="success" :loading="actionLoading" @click="runAction(row, 'complete')">完成</el-button>
            <el-button v-if="['draft', 'authorized', 'in_progress'].includes(row.status)" link type="danger" :loading="actionLoading" @click="runAction(row, 'cancel')">取消</el-button>
          </RowActions></template>
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

    <el-dialog v-model="dialogVisible" :title="editingID ? '编辑安全测试批次' : '新建安全测试批次'" width="980px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-width="120px">
        <div class="form-grid-two">
          <el-form-item label="批次名称" prop="batch_name"><el-input v-model="form.batch_name" maxlength="255" placeholder="如：2026年第三季度互联网资产渗透测试" /></el-form-item>
          <el-form-item label="测试类型" prop="test_type"><el-select v-model="form.test_type" style="width:100%"><el-option v-for="(label, value) in typeLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="测试单位" prop="provider"><el-input v-model="form.provider" maxlength="255" placeholder="集团安全团队或第三方机构" /></el-form-item>
          <el-form-item label="测试时间" required>
            <div class="date-range-inline"><el-date-picker v-model="form.start_at" type="datetime" placeholder="开始时间" /><span>至</span><el-date-picker v-model="form.end_at" type="datetime" placeholder="结束时间" /></div>
          </el-form-item>
        </div>
        <el-form-item label="授权说明" prop="authorization_note"><el-input v-model="form.authorization_note" type="textarea" :rows="3" maxlength="20000" show-word-limit placeholder="说明授权人、授权依据、允许的测试方式与边界" /></el-form-item>
        <el-form-item label="测试约束"><el-input v-model="form.notes" type="textarea" :rows="2" maxlength="20000" placeholder="如：禁止破坏性测试，不得进行拒绝服务测试" /></el-form-item>
        <el-form-item label="公网资产范围" prop="asset_ids">
          <div class="scope-picker">
            <div class="scope-picker-actions">
              <el-button type="primary" plain data-testid="scope-add-asset" @click="openScopePicker('asset')">逐项添加资产</el-button>
              <el-button data-testid="scope-add-organization" @click="openScopePicker('organization')">按组织纳入</el-button>
              <el-button data-testid="scope-add-business-system" @click="openScopePicker('business_system')">按业务系统纳入</el-button>
              <el-button data-testid="scope-add-network-area" @click="openScopePicker('network_area')">按网络区域纳入</el-button>
            </div>
            <el-table v-if="selectedAssets.length" :data="selectedAssets" border max-height="260" class="selected-scope-table" data-testid="selected-security-test-assets">
              <el-table-column label="资产" min-width="260"><template #default="{ row }"><strong>[{{ assetTypeLabels[row.type] ?? row.type }}] {{ row.value }}</strong><div class="scope-row-secondary">{{ row.name || '-' }}</div></template></el-table-column>
              <el-table-column prop="organization_name" label="责任组织" min-width="150" />
              <el-table-column label="业务系统 / 网络区域" min-width="180"><template #default="{ row }">{{ row.business_system_name || '-' }}<div class="scope-row-secondary">{{ row.network_area_name || '-' }}</div></template></el-table-column>
              <el-table-column label="操作" width="72" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="{ row }"><el-button link type="danger" @click="removeAsset(row.id)">移除</el-button></template></el-table-column>
            </el-table>
            <el-empty v-else description="尚未选择测试资产，可逐项添加或按组织、业务系统、网络区域批量纳入" :image-size="54" class="scope-empty" />
            <div class="scope-summary">已选 <strong>{{ form.asset_ids.length }}</strong> / 200 项资产，覆盖 <strong>{{ selectedOrganizationCount }}</strong> 家组织。批量纳入会自动去重，授权后将固化为历史快照。</div>
          </div>
        </el-form-item>
      </el-form>
      <template #footer><el-button @click="dialogVisible = false">取消</el-button><el-button type="primary" :loading="submitting" data-testid="save-security-test-batch" @click="save">保存草稿</el-button></template>
    </el-dialog>

    <el-dialog v-model="scopePickerVisible" :title="scopePickerTitle" width="820px" append-to-body destroy-on-close>
      <el-alert v-if="scopePickerMode !== 'asset'" type="info" :closable="false" show-icon class="scope-picker-alert">
        <template #title>只纳入匹配的未归档公网资产；最终仍执行 1～200 项范围校验，授权后冻结范围快照。</template>
      </el-alert>
      <div class="scope-search-bar">
        <el-input v-model="scopePickerKeyword" clearable :placeholder="scopePickerMode === 'asset' ? '搜索资产值、名称、组织、业务系统或网络区域' : `搜索${scopePickerDimensionLabel}`" @keyup.enter="searchScopePicker" @clear="searchScopePicker" />
        <el-button type="primary" @click="searchScopePicker">搜索</el-button>
      </div>
      <el-table v-if="scopePickerMode === 'asset'" v-loading="scopePickerLoading" :data="scopePickerAssets" border height="390" data-testid="security-test-asset-picker">
        <el-table-column label="公网资产" min-width="280"><template #default="{ row }"><strong>[{{ assetTypeLabels[row.type] ?? row.type }}] {{ row.value }}</strong><div class="scope-row-secondary">{{ row.name || '-' }}</div></template></el-table-column>
        <el-table-column prop="organization_name" label="责任组织" min-width="150" />
        <el-table-column label="业务系统 / 网络区域" min-width="170"><template #default="{ row }">{{ row.business_system_name || '-' }}<div class="scope-row-secondary">{{ row.network_area_name || '-' }}</div></template></el-table-column>
        <el-table-column label="操作" width="84" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="{ row }"><el-button v-if="!isAssetSelected(row.id)" link type="primary" @click="addAsset(row)">加入</el-button><el-button v-else link type="danger" @click="removeAsset(row.id)">移除</el-button></template></el-table-column>
      </el-table>
      <el-table v-else v-loading="scopePickerLoading" :data="scopePickerReferences" border height="390" data-testid="security-test-bulk-scope-picker">
        <el-table-column label="选择" width="70" align="center"><template #default="{ row }"><el-checkbox :model-value="scopePickerSelectedIDs.includes(row.id)" @change="toggleScopeReference(row.id, $event)" /></template></el-table-column>
        <el-table-column :label="scopePickerDimensionLabel" min-width="300"><template #default="{ row }"><strong>{{ scopeReferenceTitle(row) }}</strong><div class="scope-row-secondary">{{ scopeReferenceSubtitle(row) }}</div></template></el-table-column>
      </el-table>
      <div class="scope-picker-pagination">
        <span v-if="scopePickerMode !== 'asset'">已选择 {{ scopePickerSelectedIDs.length }} 个{{ scopePickerDimensionLabel }}</span>
        <el-pagination background small layout="total, sizes, prev, pager, next" :total="scopePickerTotal" :current-page="scopePickerPage" :page-size="scopePickerPageSize" :page-sizes="[20, 50, 100]" @current-change="handleScopePickerPageChange" @size-change="handleScopePickerPageSizeChange" />
      </div>
      <template #footer><el-button @click="scopePickerVisible = false">关闭</el-button><el-button v-if="scopePickerMode !== 'asset'" type="primary" :loading="scopePickerApplying" data-testid="apply-bulk-security-test-scope" @click="applyBulkScope">纳入所选{{ scopePickerDimensionLabel }}</el-button></template>
    </el-dialog>

    <el-drawer v-model="detailVisible" title="安全测试批次详情" size="78%">
      <template v-if="currentBatch">
        <div class="detail-heading">
          <div><p class="eyebrow">{{ currentBatch.code }}</p><h2>{{ currentBatch.name }}</h2></div>
          <el-tag :type="statusTypes[currentBatch.status] as any" size="large">{{ statusLabels[currentBatch.status] }}</el-tag>
        </div>
        <el-descriptions :column="3" border>
          <el-descriptions-item label="测试类型">{{ typeLabels[currentBatch.test_type] }}</el-descriptions-item>
          <el-descriptions-item label="测试单位">{{ currentBatch.provider }}</el-descriptions-item>
          <el-descriptions-item label="范围规模">{{ currentBatch.asset_count }} 项 / {{ currentBatch.organization_count }} 家组织</el-descriptions-item>
          <el-descriptions-item label="计划开始">{{ formatTime(currentBatch.start_at) }}</el-descriptions-item>
          <el-descriptions-item label="计划结束">{{ formatTime(currentBatch.end_at) }}</el-descriptions-item>
          <el-descriptions-item label="授权时间">{{ formatTime(currentBatch.authorized_at) }}</el-descriptions-item>
          <el-descriptions-item label="授权说明" :span="3">{{ currentBatch.authorization_note }}</el-descriptions-item>
          <el-descriptions-item label="测试约束" :span="3">{{ currentBatch.notes || '-' }}</el-descriptions-item>
        </el-descriptions>

        <div class="section-heading"><div><h3>授权范围快照</h3><p>后续资产名称、归属或系统关联发生变化，不影响本批次历史授权证据。</p></div><div class="scope-chips"><el-tag v-for="item in scopeByOrganization" :key="item.name" effect="plain">{{ item.name }} {{ item.count }} 项</el-tag></div></div>
        <el-table :data="currentBatch.scope_assets ?? []" border max-height="420">
          <el-table-column label="类型" width="95"><template #default="{ row }">{{ assetTypeLabels[row.asset_type] ?? row.asset_type }}</template></el-table-column>
          <el-table-column prop="asset_value" label="资产值" min-width="240" />
          <el-table-column prop="asset_name" label="资产名称" min-width="150" />
          <el-table-column prop="organization_name" label="责任组织" min-width="160" />
          <el-table-column prop="business_system_name" label="业务系统" min-width="150" />
          <el-table-column prop="network_area_name" label="网络区域" min-width="120" />
          <el-table-column label="快照时间" min-width="170"><template #default="{ row }">{{ formatTime(row.snapshot_at) }}</template></el-table-column>
        </el-table>

        <div class="section-heading attachment-heading">
          <div><h3>测试报告与参考材料</h3><p>上传正式测试报告、扫描结果或授权参考材料，附件仅限具备安全测试管理权限的集团人员访问，单个附件不超过 20MB。</p></div>
        </div>
        <div v-if="!['draft', 'cancelled'].includes(currentBatch.status)" class="attachment-upload-panel">
          <input ref="attachmentInput" data-testid="security-test-batch-attachment-input" class="hidden-file-input" type="file" accept=".pdf,.png,.jpg,.jpeg,.txt,.csv,.xlsx,.docx,.zip" @change="onAttachmentSelected">
          <div class="attachment-upload-row">
            <el-button @click="openAttachmentPicker">选择文件</el-button>
            <span class="selected-file-name">{{ attachmentFile?.name || '尚未选择文件' }}</span>
            <el-select v-model="attachmentCategory" style="width: 140px">
              <el-option v-for="(label, value) in attachmentCategoryLabels" :key="value" :label="label" :value="value" />
            </el-select>
            <el-button type="primary" :loading="attachmentLoading" :disabled="!attachmentFile" @click="uploadAttachment">上传附件</el-button>
          </div>
          <el-input v-model="attachmentDescription" maxlength="500" placeholder="附件说明（可选）" />
        </div>
        <div v-loading="attachmentListLoading">
          <el-empty v-if="!attachmentListLoading && !batchAttachments.length" description="暂未上传测试附件" :image-size="60" />
          <el-table v-else-if="batchAttachments.length" data-testid="security-test-batch-attachments" :data="batchAttachments" border>
            <el-table-column label="文件" min-width="230">
              <template #default="{ row }"><el-button link type="primary" @click="downloadAttachment(row)">{{ row.file_name }}</el-button></template>
            </el-table-column>
            <el-table-column label="类别" width="110"><template #default="{ row }">{{ attachmentCategoryLabels[row.category] ?? row.category }}</template></el-table-column>
            <el-table-column label="大小" width="100"><template #default="{ row }">{{ formatBytes(row.size_bytes) }}</template></el-table-column>
            <el-table-column prop="description" label="说明" min-width="180" />
            <el-table-column label="上传信息" min-width="180"><template #default="{ row }">{{ row.uploaded_by_name || '-' }}<br>{{ formatTime(row.created_at) }}</template></el-table-column>
            <el-table-column label="操作" width="80" align="right" header-align="right" class-name="table-action-column"><template #default="{ row }"><el-button link type="danger" :loading="attachmentLoading" @click="deleteAttachment(row)">删除</el-button></template></el-table-column>
          </el-table>
          <div v-if="attachmentTotal > 0" class="table-pagination detail-pagination">
            <el-pagination background small layout="total, sizes, prev, pager, next" :total="attachmentTotal" :current-page="attachmentPage" :page-size="attachmentPageSize" :page-sizes="[20, 50, 100]" @current-change="handleAttachmentPageChange" @size-change="handleAttachmentPageSizeChange" />
          </div>
        </div>

        <div class="section-heading"><div><h3>批次时间线（{{ eventTotal }}）</h3><p>记录创建、调整范围、状态操作及附件变更，最新记录优先显示。</p></div></div>
        <div v-loading="eventListLoading">
          <el-timeline v-if="batchEvents.length">
            <el-timeline-item v-for="event in batchEvents" :key="event.id" :timestamp="formatTime(event.created_at)" placement="top">
              <strong>{{ eventLabels[event.action] ?? event.action }}</strong><span v-if="event.actor_name"> · {{ event.actor_name }}</span>
              <p v-if="event.comment">{{ event.comment }}</p>
            </el-timeline-item>
          </el-timeline>
          <el-empty v-else-if="!eventListLoading" description="暂无批次处理记录" :image-size="60" />
          <div v-if="eventTotal > 0" class="table-pagination detail-pagination">
            <el-pagination background small layout="total, sizes, prev, pager, next" :total="eventTotal" :current-page="eventPage" :page-size="eventPageSize" :page-sizes="[20, 50, 100]" @current-change="handleEventPageChange" @size-change="handleEventPageSizeChange" />
          </div>
        </div>
      </template>
    </el-drawer>
  </section>
</template>

<style scoped>
.security-test-filter-panel { display:flex; align-items:center; gap:14px; margin-bottom:16px; }
.security-test-filter-hint { color:var(--muted); font-size:13px; }
.security-test-filter-count { margin-left:auto; color:var(--muted); font-size:13px; white-space:nowrap; }
.table-pagination { display:flex; justify-content:flex-end; margin-top:18px; }
.detail-pagination { overflow-x:auto; padding-bottom:2px; }
.link-button { padding:0; border:0; background:transparent; color:var(--blue); cursor:pointer; text-align:left; font:inherit; }
.form-grid-two { display:grid; grid-template-columns:1fr 1fr; gap:0 18px; }
.date-range-inline { display:flex; align-items:center; gap:8px; width:100%; }
.date-range-inline :deep(.el-date-editor) { flex:1; min-width:0; }
.scope-picker { width:100%; }
.scope-picker-actions { display:flex; flex-wrap:wrap; gap:10px; margin-bottom:12px; }
.scope-picker-actions :deep(.el-button + .el-button) { margin-left:0; }
.selected-scope-table { width:100%; }
.scope-row-secondary { margin-top:4px; color:var(--muted); font-size:12px; font-weight:400; }
.scope-empty { padding:18px 0; border:1px dashed var(--line); border-radius:10px; }
.scope-summary { margin-top:10px; color:var(--muted); font-size:13px; }
.scope-picker-alert { margin-bottom:14px; }
.scope-search-bar { display:flex; gap:10px; margin-bottom:14px; }
.scope-picker-pagination { display:flex; justify-content:space-between; align-items:center; gap:16px; margin-top:16px; color:var(--muted); font-size:13px; }
.detail-heading, .section-heading { display:flex; justify-content:space-between; align-items:flex-start; gap:20px; }
.detail-heading { margin-bottom:22px; }
.detail-heading h2, .section-heading h3 { margin:0; }
.section-heading { margin:28px 0 14px; }
.section-heading p { margin:6px 0 0; color:var(--muted); }
.hidden-file-input { display:none; }
.attachment-upload-panel { display:flex; flex-direction:column; gap:10px; padding:14px; border:1px solid var(--line); border-radius:10px; background:#f8fafc; }
.attachment-upload-row { display:flex; align-items:center; gap:12px; }
.selected-file-name { flex:1; min-width:0; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color:var(--muted); font-size:13px; }
.attachment-heading { margin-bottom:14px; }
.scope-chips { display:flex; flex-wrap:wrap; justify-content:flex-end; gap:8px; }
@media (max-width: 900px) { .security-test-filter-panel { align-items:flex-start; flex-wrap:wrap; } .security-test-filter-count { width:100%; margin-left:0; } .form-grid-two { grid-template-columns:1fr; } .date-range-inline { flex-direction:column; align-items:stretch; } .attachment-upload-row { flex-wrap:wrap; } .selected-file-name { flex-basis:100%; order:3; } .scope-picker-pagination { align-items:flex-start; flex-direction:column; } }
</style>
