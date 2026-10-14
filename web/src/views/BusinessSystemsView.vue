<script setup lang="ts">
import RowActions from '../components/RowActions.vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules, type UploadFile, type UploadFiles } from 'element-plus'
import { UploadFilled } from '@element-plus/icons-vue'
import { apiClient, getAPIErrorMessage, getDownloadAPIError } from '../api/client'
import { createExportJob } from '../api/export-jobs'
import { useAuthStore } from '../stores/auth'
import WorkingVersionDiffDrawer from '../components/WorkingVersionDiffDrawer.vue'
import ResourceSubmissionHistoryDrawer from '../components/ResourceSubmissionHistoryDrawer.vue'
import { draftLastEditedHint, draftTakeoverSummary } from '../utils/inventory-drafts'
import { reportEditConflict } from '../utils/edit-conflict'
import { createSuggestedColumnMapping, getImportColumnMappingError, importColumnLabel, isImportColumnUsed, serializeImportColumnMapping, type ImportColumn, type ImportColumnMapping, type ImportField } from '../utils/import-column-mapping'

interface Organization { id: string; code: string; name: string; type: string; status: string }
interface CustomField { id: string; field_key: string; field_name: string; description?: string; field_type: string; options: string[]; is_required: boolean }
interface RelatedAsset { id: string; type: string; value: string; name?: string; status: string; importance: string; network_scope: string; owner_name?: string }
interface BusinessSystem {
  id: string; code: string; name: string; short_name?: string; organization_id: string; organization_name: string; organization_code?: string
  owner_name?: string; owner_contact?: string; status: string; level: string; deployment_type?: string; internet_exposure: string; description?: string
  contains_personal_information: string; contains_sensitive_personal_information: string
  personal_information_record_count?: number; data_volume_over_100k: string
  contains_important_data: string; contains_core_data: string; data_storage_location?: string; security_notes?: string
  asset_count?: number; created_at: string; updated_at: string
  task_id?: string; draft_status?: string; operation?: 'create' | 'update' | 'delete'; is_draft?: boolean; source_system_id?: string; version?: number
  custom_values?: Array<{ field_key: string; value: unknown }>
  reporting_status?: 'unreported' | 'pending_review' | 'reported' | 'changed' | 'returned' | 'pending_offline'; is_working_copy?: boolean; published_id?: string
  assigned_to?: string; assigned_to_name?: string; last_edited_by?: string; last_edited_by_name?: string
  takeover_reason?: string; taken_over_at?: string; takeover_by_name?: string
}
interface FieldChange { field: string; label: string; before: string; after: string }
interface ChangeHistoryItem {
  id: number; action: string; action_label: string; summary?: string; display_name?: string; username?: string
  created_at: string; details?: { changes?: FieldChange[] }
}
interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface ImportError { row: number; code: string; message: string }
interface ImportResult { id: string; file_name: string; file_type: string; import_mode: 'skip_duplicates' | 'update_working'; status: string; total_count: number; success_count: number; created_count: number; updated_count: number; failure_count: number; duplicate_count: number; errors: ImportError[] }
interface ImportHistory extends ImportResult { organization_id: string; organization_code: string; organization_name: string; created_by_name?: string; created_at: string }
interface DetailResponse { item: BusinessSystem }
interface BatchOperationItem { id: string; success: boolean; code?: string; message: string; item?: BusinessSystem; resource_name?: string }
interface BatchOperationResult { total_count: number; success_count: number; failure_count: number; items: BatchOperationItem[] }

type FormData = {
  code: string; name: string; short_name: string; organization_id: string; owner_name: string; owner_contact: string
  status: string; level: string; deployment_type: string; internet_exposure: string; description: string
  contains_personal_information: string; contains_sensitive_personal_information: string
  personal_information_record_count: number | undefined; data_volume_over_100k: string
  contains_important_data: string; contains_core_data: string; data_storage_location: string; security_notes: string
}

const auth = useAuthStore()
const route = useRoute()
const loading = ref(false)
const submitting = ref(false)
const dialogVisible = ref(false)
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<BusinessSystem | null>(null)
const detailAssets = ref<RelatedAsset[]>([])
const detailAssetLoading = ref(false)
const detailAssetPage = ref(1)
const detailAssetPageSize = ref(20)
const detailAssetTotal = ref(0)
const detailCustomFields = ref<CustomField[]>([])
const editingID = ref('')
const editingIsDraft = ref(false)
const editingExpectedVersion = ref(0)
const formRef = ref<FormInstance>()
const systems = ref<BusinessSystem[]>([])
const draftSystems = ref<BusinessSystem[]>([])
const draftPage = ref(1)
const draftPageSize = ref(20)
const draftTotal = ref(0)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const search = ref('')
const statusFilter = ref('')
const organizationFilter = ref('')
const organizations = ref<Organization[]>([])
const customFields = ref<CustomField[]>([])
const customValues = reactive<Record<string, unknown>>({})
const detailCustomValues = ref<Record<string, unknown>>({})
const eligibleOrganizations = computed(() => organizations.value.filter((item) => item.status === 'enabled'))
const inventoryTaskID = computed(() => typeof route.query.task_id === 'string' ? route.query.task_id : '')
const inventoryOrganizationID = computed(() => typeof route.query.organization_id === 'string' ? route.query.organization_id : '')
const inventoryTaskName = computed(() => typeof route.query.task_name === 'string' ? route.query.task_name : '当前填报任务')
const inventoryMode = computed(() => route.query.mode === 'inventory' && Boolean(inventoryTaskID.value && inventoryOrganizationID.value))
const inventoryOrganizationName = computed(() => organizations.value.find((item) => item.id === inventoryOrganizationID.value)?.name || '当前填报组织')
const draftStatusLabels: Record<string, string> = { editing: '编辑中', returned: '已退回', submitted: '已提交', published: '已发布' }
const draftOperationLabels: Record<string, string> = { create: '新增', update: '修改', delete: '删除' }
const reportingStatusLabels: Record<string, string> = { unreported: '未上报', pending_review: '待审核', reported: '已上报', changed: '有变更待上报', returned: '已退回', pending_offline: '待下线' }
const form = reactive<FormData>(emptyForm())
const importDialogVisible = ref(false)
const historyDialogVisible = ref(false)
const changeHistoryVisible = ref(false)
const workingVersionDiffRef = ref<InstanceType<typeof WorkingVersionDiffDrawer>>()
const resourceSubmissionHistoryRef = ref<InstanceType<typeof ResourceSubmissionHistoryDrawer>>()
const changeHistoryLoading = ref(false)
const changeHistoryItems = ref<ChangeHistoryItem[]>([])
const changeHistoryTitle = ref('')
const changeHistoryResourceID = ref('')
const changeHistoryPage = ref(1)
const changeHistoryPageSize = ref(20)
const changeHistoryTotal = ref(0)
const importLoading = ref(false)
const downloading = ref(false)
const exporting = ref(false)
const selectedExporting = ref(false)
const historyLoading = ref(false)
const importFile = ref<File>()
const importColumns = ref<ImportColumn[]>([])
const importFields = ref<ImportField[]>([])
const importColumnMapping = reactive<ImportColumnMapping>({})
const importColumnsLoading = ref(false)
const importResult = ref<ImportResult>()
const importResultMode = ref<'preview' | 'completed'>()
const importHistory = ref<ImportHistory[]>([])
const importPage = ref(1)
const importPageSize = ref(20)
const importTotal = ref(0)
const importForm = reactive<{ organization_id: string; import_mode: 'skip_duplicates' | 'update_working' }>({ organization_id: '', import_mode: 'skip_duplicates' })
const selectedSystems = ref<BusinessSystem[]>([])
const batchLoading = ref(false)
const batchResultVisible = ref(false)
const batchResult = ref<BatchOperationResult>()
const batchResultTitle = ref('批量处理结果')

function emptyForm(): FormData {
  return {
    code: '', name: '', short_name: '', organization_id: '', owner_name: '', owner_contact: '', status: 'running', level: 'normal',
    deployment_type: '', internet_exposure: 'unknown', description: '', contains_personal_information: 'unknown',
    contains_sensitive_personal_information: 'unknown', personal_information_record_count: undefined, data_volume_over_100k: 'unknown',
    contains_important_data: 'unknown', contains_core_data: 'unknown', data_storage_location: '', security_notes: '',
  }
}
const rules: FormRules = {
  code: [{ required: true, message: '请输入系统编码', trigger: 'blur' }, { pattern: /^[A-Za-z0-9][A-Za-z0-9_-]{1,63}$/, message: '使用 2-64 位字母、数字、下划线或横线', trigger: 'blur' }],
  name: [{ required: true, message: '请输入系统名称', trigger: 'blur' }],
  organization_id: [{ required: true, message: '请选择所属组织', trigger: 'change' }],
}
const yesNoLabels: Record<string, string> = { yes: '是', no: '否', unknown: '待确认', pending: '待认定' }
const statusLabels: Record<string, string> = { planning: '规划中', running: '运行中', suspended: '暂停', offline: '已下线', archived: '已归档' }
const levelLabels: Record<string, string> = { normal: '一般', important: '重要', core: '核心' }
const assetTypeLabels: Record<string, string> = { domain: '域名', ip: 'IP', url: 'URL', service: '服务', other: '其他' }
const assetStatusLabels: Record<string, string> = { active: '正常', pending: '待确认', disabled: '已停用', archived: '已归档' }
const dialogTitle = computed(() => editingID.value ? '编辑业务系统' : '新增业务系统')
const importMappingError = computed(() => getImportColumnMappingError(importFields.value, importColumnMapping))
const submitLabel = computed(() => editingID.value ? '保存修改' : (inventoryMode.value ? '保存待提交' : '确认新增'))

watch(() => form.personal_information_record_count, (value) => {
  if (typeof value === 'number') form.data_volume_over_100k = value >= 100000 ? 'yes' : 'no'
})
watch(() => form.contains_personal_information, (value) => {
  if (value === 'no') {
    form.contains_sensitive_personal_information = 'no'
    form.personal_information_record_count = undefined
    form.data_volume_over_100k = 'no'
  }
})

async function loadSystems() {
  loading.value = true
  try {
    const response = await apiClient.get<ListResponse<BusinessSystem>>('/business-systems', {
      params: {
        page: page.value,
        page_size: pageSize.value,
        q: search.value.trim() || undefined,
        status: statusFilter.value || undefined,
        organization_id: organizationFilter.value || undefined,
      },
    })
    systems.value = response.data.items
    total.value = response.data.total
    selectedSystems.value = []
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取业务系统失败')) }
  finally { loading.value = false }
}

async function loadDraftSystems() {
  if (!inventoryMode.value) { draftSystems.value = []; draftTotal.value = 0; return }
  try {
    const response = await apiClient.get<ListResponse<BusinessSystem>>(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/business-system-drafts`, { params: { page: draftPage.value, page_size: draftPageSize.value } })
    draftSystems.value = response.data.items
    draftTotal.value = response.data.total
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取本次上报业务系统失败')) }
}

async function load() {
  try {
    const organizationResponse = await apiClient.get<ListResponse<Organization>>('/organizations', { params: { page_size: 100 } })
    organizations.value = organizationResponse.data.items
    try {
      const customResponse = await apiClient.get<ListResponse<CustomField>>('/custom-fields/fillable', { params: { resource_type: 'business_system', page_size: 100 } })
      customFields.value = customResponse.data.items
    } catch {
      // Users without system maintenance permission can still read the system ledger.
      customFields.value = []
    }
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取业务系统基础数据失败')) }
  if (inventoryMode.value || inventoryOrganizationID.value) organizationFilter.value = inventoryOrganizationID.value
  await Promise.all([loadSystems(), loadDraftSystems()])
}

async function resetAndLoadSystems() {
  page.value = 1
  await loadSystems()
}

async function refreshAfterMutation() {
  await load()
  if (systems.value.length === 0 && total.value > 0 && page.value > 1) {
    page.value -= 1
    await loadSystems()
  }
}

function handlePageChange(value: number) {
  page.value = value
  void loadSystems()
}

function handlePageSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  void loadSystems()
}

function defaultCustomValues() {
  Object.keys(customValues).forEach((key) => delete customValues[key])
  customFields.value.forEach((field) => { customValues[field.field_key] = field.field_type === 'multiselect' ? [] : field.field_type === 'boolean' ? false : '' })
}
function isEmptyCustomValue(value: unknown) { return value == null || (typeof value === 'string' && value.trim() === '') || (Array.isArray(value) && value.length === 0) }
function customValueLabel(value: unknown) {
  if (Array.isArray(value)) return value.join('、') || '未填写'
  if (value === true) return '是'
  if (value === false) return '否'
  return value == null || value === '' ? '未填写' : String(value)
}
async function loadCustomValues(id: string) {
  const { data } = await apiClient.get<{ values: Array<{ field_key: string; value: unknown }> }>(`/business-systems/${id}/custom-fields`)
  defaultCustomValues()
  data.values.forEach((item) => { customValues[item.field_key] = item.value })
}
function openWorkingVersionDiff(row: { id: string }) {
  void workingVersionDiffRef.value?.open(row.id)
}
function openResourceSubmissionHistory(row: { id: string }) {
  void resourceSubmissionHistoryRef.value?.open(row.id)
}

function openCreateDialog() {
  Object.assign(form, emptyForm(), { organization_id: inventoryMode.value ? inventoryOrganizationID.value : (auth.user?.organization?.id ?? eligibleOrganizations.value[0]?.id ?? '') })
  defaultCustomValues()
  editingID.value = ''
  editingExpectedVersion.value = 0
  editingIsDraft.value = false
  dialogVisible.value = true
}

async function openEditDialog(row: BusinessSystem) {
  if ((inventoryMode.value && row.is_draft) || row.is_working_copy) {
    Object.assign(form, {
      code: row.code, name: row.name, short_name: row.short_name ?? '', organization_id: row.organization_id,
      owner_name: row.owner_name ?? '', owner_contact: row.owner_contact ?? '', status: row.status, level: row.level,
      deployment_type: row.deployment_type ?? '', internet_exposure: row.internet_exposure, description: row.description ?? '',
      contains_personal_information: row.contains_personal_information, contains_sensitive_personal_information: row.contains_sensitive_personal_information,
      personal_information_record_count: row.personal_information_record_count, data_volume_over_100k: row.data_volume_over_100k,
      contains_important_data: row.contains_important_data, contains_core_data: row.contains_core_data, data_storage_location: row.data_storage_location ?? '', security_notes: row.security_notes ?? '',
    })
    defaultCustomValues()
    row.custom_values?.forEach((item) => { customValues[item.field_key] = item.value })
    editingID.value = row.id
    editingExpectedVersion.value = row.version ?? 0
    editingIsDraft.value = Boolean(inventoryMode.value && row.task_id === inventoryTaskID.value)
    dialogVisible.value = true
    return
  }
  submitting.value = true
  try {
    const response = await apiClient.get<DetailResponse>(`/business-systems/${row.id}`)
    const item = response.data.item
    Object.assign(form, {
      code: item.code, name: item.name, short_name: item.short_name ?? '', organization_id: item.organization_id,
      owner_name: item.owner_name ?? '', owner_contact: item.owner_contact ?? '', status: item.status, level: item.level,
      deployment_type: item.deployment_type ?? '', internet_exposure: item.internet_exposure, description: item.description ?? '',
      contains_personal_information: item.contains_personal_information, contains_sensitive_personal_information: item.contains_sensitive_personal_information,
      personal_information_record_count: item.personal_information_record_count, data_volume_over_100k: item.data_volume_over_100k,
      contains_important_data: item.contains_important_data, contains_core_data: item.contains_core_data,
      data_storage_location: item.data_storage_location ?? '', security_notes: item.security_notes ?? '',
    })
    await loadCustomValues(row.id)
    editingID.value = row.id
    editingExpectedVersion.value = item.version ?? 0
    editingIsDraft.value = false
    dialogVisible.value = true
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取业务系统详情失败')) }
  finally { submitting.value = false }
}

async function submit() {
  if (!formRef.value || !(await formRef.value.validate())) return
  const missing = customFields.value.find((field) => field.is_required && isEmptyCustomValue(customValues[field.field_key]))
  if (missing) { ElMessage.warning(`请填写自定义字段：${missing.field_name}`); return }
  submitting.value = true
  try {
    const customPayload = customFields.value.map((field) => ({ field_key: field.field_key, value: customValues[field.field_key] }))
    let response
    if (inventoryMode.value) {
      const base = `/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/business-system-drafts`
      const payload = { ...form, organization_id: inventoryOrganizationID.value, custom_values: customPayload, ...(editingID.value && editingIsDraft.value && editingExpectedVersion.value > 0 ? { expected_version: editingExpectedVersion.value } : {}) }
      if (editingID.value && editingIsDraft.value) response = await apiClient.patch<{ item: BusinessSystem }>(`${base}/${editingID.value}`, payload)
      else response = editingID.value
        ? await apiClient.patch<{ item: BusinessSystem }>(`/business-systems/${editingID.value}`, withWorkingVersion(payload))
        : await apiClient.post<{ item: BusinessSystem }>('/business-systems', payload)
    } else {
      const payload = withWorkingVersion({ ...form, custom_values: customPayload })
      response = editingID.value
        ? await apiClient.patch<{ item: BusinessSystem }>(`/business-systems/${editingID.value}`, payload)
        : await apiClient.post<{ item: BusinessSystem }>('/business-systems', payload)
    }
    const id = editingID.value || response.data.item.id
    if (!inventoryMode.value && customFields.value.length && !response.data.item.is_working_copy) await apiClient.put(`/business-systems/${id}/custom-fields`, { values: customPayload })
    ElMessage.success(inventoryMode.value ? (editingIsDraft.value ? '本次上报内容已更新' : (editingID.value ? '系统变更已保存，请选择后纳入本次上报' : '系统已保存为未上报状态，请选择后纳入本次上报')) : (response.data.item.is_working_copy ? (editingID.value ? '业务系统已更新，当前仍为未上报状态' : '业务系统已新增，默认未上报') : (editingID.value ? '业务系统已更新' : '业务系统已新增')))
    dialogVisible.value = false
    await refreshAfterMutation()
    if (detailVisible.value && detail.value?.id === id) await openDetail(detail.value as BusinessSystem)
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    if (await reportEditConflict(error, '业务系统已被其他人更新', load)) return
    ElMessage.error(getAPIErrorMessage(error, editingID.value ? '编辑业务系统失败' : '新增业务系统失败'))
  }
  finally { submitting.value = false }
}

// withWorkingVersion only attaches the optimistic-lock value when the dialog is
// editing an existing working copy. Published systems are updated through the
// official path, which must not receive a working-version lock.
function withWorkingVersion(payload: Record<string, unknown>) {
  if (!editingID.value || editingExpectedVersion.value <= 0) return payload
  return { ...payload, expected_version: editingExpectedVersion.value }
}

async function selectWorkspaceBusinessSystem(row: BusinessSystem) {
  try {
    await apiClient.post(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/business-system-drafts/${row.id}/select`, { expected_version: row.version })
    ElMessage.success('已选择该业务系统纳入本次上报')
    await refreshAfterMutation()
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '选择上报业务系统失败')) }
}

function canSelectBusinessSystem(row: BusinessSystem) {
  return inventoryMode.value ? Boolean(row.is_working_copy && !row.task_id && !auth.isGroupAdmin) : !row.is_working_copy
}

async function batchSelectWorkspaceBusinessSystems() {
  if (!selectedSystems.value.length) return ElMessage.warning('请先选择需要上报的业务系统')
  if (selectedSystems.value.length > 200) return ElMessage.warning('单次最多选择上报 200 个业务系统')
  try {
    await ElMessageBox.confirm(`确认将选中的 ${selectedSystems.value.length} 个未上报业务系统纳入本次填报任务吗？系统会逐项校验并返回处理结果。`, '批量选择上报', {
      type: 'warning', confirmButtonText: '确认纳入', cancelButtonText: '取消',
    })
    batchLoading.value = true
    const targets = [...selectedSystems.value]
    const response = await apiClient.post<{ result: BatchOperationResult }>(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/business-system-drafts/batch-select`, {
      items: targets.map((item) => ({ id: item.id, expected_version: item.version })),
    })
    const names = new Map(targets.map((item) => [item.id, item.name]))
    batchResult.value = {
      ...response.data.result,
      items: response.data.result.items.map((item) => ({ ...item, resource_name: names.get(item.id) || item.id })),
    }
    batchResultTitle.value = '批量选择上报业务系统结果'
    batchResultVisible.value = true
    if (response.data.result.failure_count === 0) ElMessage.success(`已将 ${response.data.result.success_count} 个业务系统纳入本次上报`)
    await refreshAfterMutation()
    selectedSystems.value = []
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '批量选择上报业务系统失败'))
  } finally { batchLoading.value = false }
}

async function removeDraft(row: BusinessSystem) {
  try {
    await ElMessageBox.confirm(`确认取消任务中的“${draftOperationLabels[row.operation || 'create']}”变更“${row.name}”吗？`, '取消任务变更', { type: 'warning' })
    if (!row.version || row.version <= 0) throw new Error('草稿版本信息缺失，请刷新后重试')
    await apiClient.delete(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/business-system-drafts/${row.id}`, { params: { expected_version: row.version } })
    ElMessage.success('任务变更已取消')
    await loadDraftSystems()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    if (await reportEditConflict(error, '草稿已被其他人更新', loadDraftSystems)) return
    ElMessage.error(getAPIErrorMessage(error, '删除业务系统草稿失败'))
  }
}

function draftOwnerLabel(row: BusinessSystem) {
  return row.assigned_to_name || row.takeover_by_name || row.assigned_to || ''
}

function canTakeOverDraft(row: BusinessSystem) {
  if (!inventoryMode.value || auth.isGroupAdmin) return false
  if (!row.assigned_to || row.assigned_to === auth.user?.id) return false
  return row.draft_status === 'editing' || row.draft_status === 'returned'
}

async function takeoverDraft(row: BusinessSystem) {
  const { value } = await ElMessageBox.prompt(`确认接管“${row.name}”的填报草稿吗？接管原因会记入审计日志，原填报人会看到该提示。`, '接管填报草稿', {
    confirmButtonText: '确认接管', cancelButtonText: '取消', inputType: 'textarea',
    inputPlaceholder: '例如：原填报人休假，值班同事接手',
    inputValidator: (input) => Boolean(String(input ?? '').trim()) || '请填写接管原因',
  }).then((result) => ({ value: String(result.value ?? '').trim() })).catch(() => ({ value: '' }))
  const reason = String(value ?? '').trim()
  if (!reason) return
  try {
    if (!row.version || row.version <= 0) throw new Error('草稿版本信息缺失，请刷新后重试')
    await apiClient.post(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/business-system-drafts/${row.id}/takeover`, { expected_version: row.version, reason })
    ElMessage.success('已接管该草稿，接管原因已记入审计日志')
    await loadDraftSystems()
  } catch (error) {
    if (await reportEditConflict(error, '草稿已被其他人更新', loadDraftSystems)) return
    ElMessage.error(getAPIErrorMessage(error, '接管业务系统草稿失败'))
  }
}

async function requestSystemDeletion(row: BusinessSystem) {
  try {
    await ElMessageBox.confirm(`确认将业务系统“${row.name}”加入本次任务的删除草稿吗？提交并发布前不会删除正式台账；仍有关联资产时发布会被阻止。`, '申请删除业务系统', { type: 'warning', confirmButtonText: '加入删除草稿', cancelButtonText: '取消' })
    await apiClient.post(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/business-systems/${row.id}/delete-draft`)
    ElMessage.success('已加入删除草稿，提交前正式台账保持不变')
    await loadDraftSystems()
  } catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '创建业务系统删除草稿失败')) }
}
function handleDraftPageChange(value: number) { draftPage.value = value; void loadDraftSystems() }
function handleDraftPageSizeChange(value: number) { draftPageSize.value = value; draftPage.value = 1; void loadDraftSystems() }

async function loadDetailAssets(systemID: string) {
  detailAssetLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<RelatedAsset>>(`/business-systems/${systemID}/assets`, {
      params: { page: detailAssetPage.value, page_size: detailAssetPageSize.value },
    })
    detailAssets.value = response.data.items
    detailAssetTotal.value = response.data.total
    if (!detailAssets.value.length && detailAssetPage.value > 1 && detailAssetTotal.value > 0) {
      detailAssetPage.value -= 1
      await loadDetailAssets(systemID)
    }
  } finally { detailAssetLoading.value = false }
}

async function copySystemCode() {
  if (!detail.value?.code) return
  try {
    await navigator.clipboard.writeText(detail.value.code)
    ElMessage.success('系统编码已复制')
  } catch {
    ElMessage.error('复制系统编码失败，请手动选择复制')
  }
}

async function openDetail(row: BusinessSystem) {
  detailVisible.value = true
  detailLoading.value = true
  detailAssetPage.value = 1
  detailAssets.value = []
  detailAssetTotal.value = 0
  try {
    if (row.is_draft || row.is_working_copy) {
      detail.value = row
      detailCustomFields.value = customFields.value
      detailCustomValues.value = Object.fromEntries((row.custom_values || []).map((item) => [item.field_key, item.value]))
      return
    }
    const [response, customResponse] = await Promise.all([
      apiClient.get<DetailResponse>(`/business-systems/${row.id}`),
      apiClient.get<{ definitions: CustomField[]; values: Array<{ field_key: string; value: unknown }> }>(`/business-systems/${row.id}/custom-fields`),
      loadDetailAssets(row.id),
    ])
    detail.value = response.data.item
    detailCustomFields.value = customResponse.data.definitions
    detailCustomValues.value = Object.fromEntries(customResponse.data.values.map((item) => [item.field_key, item.value]))
  } catch (error) {
    detailVisible.value = false
    ElMessage.error(getAPIErrorMessage(error, '读取业务系统详情失败'))
  } finally { detailLoading.value = false }
}

async function handleDetailAssetPageChange(value: number) {
  if (!detail.value) return
  detailAssetPage.value = value
  try { await loadDetailAssets(detail.value.id) }
  catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取关联网络资产失败')) }
}

async function handleDetailAssetPageSizeChange(value: number) {
  if (!detail.value) return
  detailAssetPageSize.value = value
  detailAssetPage.value = 1
  try { await loadDetailAssets(detail.value.id) }
  catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取关联网络资产失败')) }
}

function handleSelectionChange(rows: BusinessSystem[]) {
  selectedSystems.value = rows
}

async function runBatchArchive(archive: boolean) {
  const targets = selectedSystems.value.filter((item) => archive ? item.status !== 'archived' : item.status === 'archived')
  if (!targets.length) {
    ElMessage.warning(archive ? '所选系统均已归档' : '请选择状态为“已归档”的业务系统')
    return
  }
  const action = archive ? '归档' : '恢复'
  try {
    await ElMessageBox.confirm(
      archive
        ? `确认批量归档选中的 ${targets.length} 个业务系统吗？关联资产不会被删除。`
        : `确认批量恢复选中的 ${targets.length} 个业务系统吗？恢复后状态为“运行中”。`,
      `批量${action}确认`,
      { type: archive ? 'warning' : 'info', confirmButtonText: `确认${action}`, cancelButtonText: '取消' },
    )
    batchLoading.value = true
    const response = await apiClient.post<{ result: BatchOperationResult }>('/business-systems/batch/archive', {
      items: targets.map((item) => ({ id: item.id, expected_version: item.version })), archived: archive,
    })
    const names = new Map(targets.map((item) => [item.id, item.name]))
    batchResult.value = {
      ...response.data.result,
      items: response.data.result.items.map((item) => ({ ...item, resource_name: item.item?.name || names.get(item.id) || item.id })),
    }
    batchResultTitle.value = `批量${action}结果`
    batchResultVisible.value = true
    if (response.data.result.failure_count === 0) ElMessage.success(`已成功${action} ${response.data.result.success_count} 个业务系统`)
    await refreshAfterMutation()
    selectedSystems.value = []
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, `批量${action}业务系统失败`))
  } finally { batchLoading.value = false }
}

async function changeArchive(row: BusinessSystem, archive: boolean) {
  try {
    await ElMessageBox.confirm(archive ? `确认归档“${row.name}”吗？归档后仍可恢复，关联资产不会被删除。` : `确认恢复“${row.name}”吗？恢复后状态为“运行中”。`, archive ? '归档确认' : '恢复确认', { type: archive ? 'warning' : 'info', confirmButtonText: '确认', cancelButtonText: '取消' })
    await apiClient.post(`/business-systems/${row.id}/${archive ? 'archive' : 'restore'}`)
    ElMessage.success(archive ? '业务系统已归档' : '业务系统已恢复')
    await refreshAfterMutation()
    if (detailVisible.value && detail.value?.id === row.id) await openDetail(row)
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, archive ? '归档业务系统失败' : '恢复业务系统失败'))
  }
}

async function remove(row: BusinessSystem) {
  try {
    await ElMessageBox.confirm(`删除后业务系统将不再展示，确认删除“${row.name}”吗？`, '删除确认', { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' })
    await apiClient.delete(`/business-systems/${row.id}`, { params: row.version && row.version > 0 ? { expected_version: row.version } : {} })
    ElMessage.success('业务系统已删除')
    await refreshAfterMutation()
    if (detail.value?.id === row.id) detailVisible.value = false
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '删除业务系统失败'))
  }
}

function resetImportPreview() {
  importResult.value = undefined
  importResultMode.value = undefined
}
function clearImportColumnMapping() {
  importColumns.value = []
  importFields.value = []
  Object.keys(importColumnMapping).forEach((key) => delete importColumnMapping[key])
}
async function analyzeImportColumns() {
  if (!importForm.organization_id || !importFile.value) return false
  importColumnsLoading.value = true
  resetImportPreview()
  clearImportColumnMapping()
  try {
    const data = new FormData()
    data.append('organization_id', importForm.organization_id)
    data.append('file', importFile.value)
    const response = await apiClient.post<{ columns: ImportColumn[]; fields: ImportField[] }>('/business-systems/import/columns', data, {
      headers: { 'Content-Type': 'multipart/form-data' }, timeout: 60_000,
    })
    importColumns.value = response.data.columns
    importFields.value = response.data.fields
    Object.assign(importColumnMapping, createSuggestedColumnMapping(response.data.fields))
    return true
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取文件列失败'))
    return false
  } finally {
    importColumnsLoading.value = false
  }
}
function openImportDialog() {
  importForm.organization_id = auth.user?.organization?.id ?? eligibleOrganizations.value[0]?.id ?? ''
  importForm.import_mode = 'skip_duplicates'
  importFile.value = undefined
  clearImportColumnMapping()
  resetImportPreview()
  importDialogVisible.value = true
}
async function handleImportFileChange(file: UploadFile, _files: UploadFiles) {
  importFile.value = file.raw
  await analyzeImportColumns()
}
function handleImportFileRemove() {
  importFile.value = undefined
  clearImportColumnMapping()
  resetImportPreview()
}
async function handleImportOrganizationChange() {
  if (importFile.value) await analyzeImportColumns()
  else resetImportPreview()
}
function handleImportMappingChange() {
  resetImportPreview()
}
async function runImport(preview = false) {
  if (!importForm.organization_id) return ElMessage.warning('请选择所属组织')
  if (!importFile.value) return ElMessage.warning('请选择 CSV 或 XLSX 文件')
  if (!importFields.value.length && !await analyzeImportColumns()) return
  if (importMappingError.value) return ElMessage.warning(importMappingError.value)
  if (!preview && (importResultMode.value !== 'preview' || !importResult.value?.success_count)) {
    return ElMessage.warning('请先完成预检，并确认至少有一行可以导入')
  }
  importLoading.value = true
  try {
    const data = new FormData()
    data.append('organization_id', importForm.organization_id)
    data.append('import_mode', importForm.import_mode)
    data.append('column_mapping', serializeImportColumnMapping(importFields.value, importColumnMapping))
    data.append('file', importFile.value)
    const endpoint = preview ? '/business-systems/import/preview' : '/business-systems/import'
    const response = await apiClient.post<{ result: ImportResult }>(endpoint, data, { headers: { 'Content-Type': 'multipart/form-data' }, timeout: 60_000 })
    importResult.value = response.data.result
    importResultMode.value = preview ? 'preview' : 'completed'
    if (preview) {
      ElMessage.success('文件预检完成，尚未写入台账')
    } else {
      ElMessage.success(auth.isGroupAdmin
        ? `成功导入 ${response.data.result.success_count} 个业务系统到正式台账`
        : `已新增 ${response.data.result.created_count} 个、更新 ${response.data.result.updated_count} 个未上报工作版本`)
      await refreshAfterMutation()
    }
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, preview ? '导入文件预检失败' : '批量导入业务系统失败')) }
  finally { importLoading.value = false }
}
async function exportLedger(format: 'csv' | 'xlsx') {
  exporting.value = true
  try {
    const response = await apiClient.get('/business-systems/export', {
      params: {
        format,
        q: search.value.trim() || undefined,
        status: statusFilter.value || undefined,
        organization_id: organizationFilter.value || undefined,
      },
      responseType: 'blob',
      timeout: 60_000,
    })
    const href = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = href
    link.download = `业务系统台账.${format}`
    link.click()
    URL.revokeObjectURL(href)
    const count = response.headers['x-export-count'] ?? systems.value.length
    ElMessage.success(`已生成并开始下载 ${count} 个业务系统`)
  } catch (error) {
    const detail = await getDownloadAPIError(error, '导出业务系统台账失败')
    if (format === 'xlsx' && detail.code === 'synchronous_xlsx_limit_exceeded') {
      try { await queueXLSXExport() } catch (queueError) { ElMessage.error(getAPIErrorMessage(queueError, '创建后台导出任务失败')) }
    } else {
      ElMessage.error(detail.message)
    }
  } finally {
    exporting.value = false
  }
}

async function exportSelectedSystems(format: 'csv' | 'xlsx') {
  if (!selectedSystems.value.length) return ElMessage.warning('请先选择需要导出的业务系统')
  if (selectedSystems.value.length > 200) return ElMessage.warning('单次最多导出 200 个业务系统')
  selectedExporting.value = true
  try {
    const selectedIDs = selectedSystems.value.map((item) => item.id)
    const response = await apiClient.get('/business-systems/export', {
      params: { format, selected_ids: selectedIDs.join(',') },
      responseType: 'blob',
      timeout: 60_000,
    })
    const href = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = href
    link.download = `业务系统选中项.${format}`
    link.click()
    URL.revokeObjectURL(href)
    const count = response.headers['x-export-count'] ?? selectedIDs.length
    ElMessage.success(`已生成并开始下载 ${count} 个选中业务系统`)
  } catch (error) {
    const detail = await getDownloadAPIError(error, '导出选中业务系统失败')
    ElMessage.error(detail.message)
  } finally {
    selectedExporting.value = false
  }
}

async function queueXLSXExport() {
  await createExportJob('business_system', {
    q: search.value.trim() || undefined,
    status: statusFilter.value || undefined,
    organization_id: organizationFilter.value || undefined,
  })
  ElMessage.success('后台 XLSX 导出任务已创建，可在“导出任务”中查看进度和下载')
}

async function downloadTemplate(format: 'csv' | 'xlsx') {
  if (!importForm.organization_id) {
    ElMessage.warning('请先选择导入到的所属组织')
    return
  }
  downloading.value = true
  try {
    const response = await apiClient.get(`/business-systems/import-template?format=${format}&organization_id=${encodeURIComponent(importForm.organization_id)}`, { responseType: 'blob' })
    const href = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = href
    link.download = `业务系统导入模板.${format}`
    link.click()
    URL.revokeObjectURL(href)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '下载模板失败'))
  } finally {
    downloading.value = false
  }
}
async function loadChangeHistory() {
  if (!changeHistoryResourceID.value) return
  changeHistoryLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<ChangeHistoryItem>>(`/business-systems/${changeHistoryResourceID.value}/change-history`, {
      params: { page: changeHistoryPage.value, page_size: changeHistoryPageSize.value },
    })
    changeHistoryItems.value = response.data.items
    changeHistoryTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取业务系统变更历史失败'))
  } finally {
    changeHistoryLoading.value = false
  }
}
async function openChangeHistory(row: { id: string; name?: string; value?: string; code?: string }) {
  changeHistoryTitle.value = `${row.name || row.value || row.code || '资源'}的变更历史`
  changeHistoryResourceID.value = row.id
  changeHistoryPage.value = 1
  changeHistoryItems.value = []
  changeHistoryTotal.value = 0
  changeHistoryVisible.value = true
  await loadChangeHistory()
}
function handleChangeHistoryPageChange(value: number) {
  changeHistoryPage.value = value
  void loadChangeHistory()
}
function handleChangeHistoryPageSizeChange(value: number) {
  changeHistoryPageSize.value = value
  changeHistoryPage.value = 1
  void loadChangeHistory()
}
function changesOf(item: ChangeHistoryItem) {
  return Array.isArray(item.details?.changes) ? item.details.changes : []
}

async function loadImportHistory() {
  historyLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<ImportHistory>>('/business-systems/imports', { params: { page: importPage.value, page_size: importPageSize.value } })
    importHistory.value = response.data.items
    importTotal.value = response.data.total
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取导入记录失败')) }
  finally { historyLoading.value = false }
}
async function openImportHistory() {
  historyDialogVisible.value = true
  importPage.value = 1
  await loadImportHistory()
}
function handleImportPageChange(value: number) {
  importPage.value = value
  void loadImportHistory()
}
function handleImportPageSizeChange(value: number) {
  importPageSize.value = value
  importPage.value = 1
  void loadImportHistory()
}

function yesNo(value: string) { return yesNoLabels[value] ?? value }
function statusLabel(value: string) { return statusLabels[value] ?? value }
function levelLabel(value: string) { return levelLabels[value] ?? value }
function assetTypeLabel(value: string) { return assetTypeLabels[value] ?? value }
function assetStatusLabel(value: string) { return assetStatusLabels[value] ?? value }
watch(() => route.query.organization_id, (value) => {
  if (inventoryMode.value) return
  const nextOrganizationID = typeof value === 'string' ? value : ''
  if (organizationFilter.value === nextOrganizationID) return
  organizationFilter.value = nextOrganizationID
  page.value = 1
  void loadSystems()
})

onMounted(load)
</script>

<template>
  <section>
    <el-alert v-if="inventoryMode" :title="`正在填报：${inventoryTaskName} · ${inventoryOrganizationName}`" description="这里展示组织工作台账。新增或修改先保存为未上报版本，选择纳入本次任务并提交审核后，才会发布到集团正式台账。" type="warning" :closable="false" show-icon class="inventory-draft-alert" />
    <div class="page-heading">
      <h1>业务系统</h1>
    </div>
      <div class="button-row ledger-page-actions business-system-page-actions"><el-dropdown v-if="!inventoryMode"><el-button :loading="exporting">导出台账</el-button><template #dropdown><el-dropdown-menu><el-dropdown-item @click="exportLedger('xlsx')">导出 XLSX</el-dropdown-item><el-dropdown-item @click="exportLedger('csv')">导出 CSV</el-dropdown-item><el-dropdown-item divided @click="queueXLSXExport">后台生成 XLSX</el-dropdown-item></el-dropdown-menu></template></el-dropdown><el-button v-if="!inventoryMode" @click="openImportHistory">导入记录</el-button><el-button v-if="!inventoryMode" @click="openImportDialog">批量导入</el-button><el-button v-if="!inventoryMode || !auth.isGroupAdmin" type="primary" :disabled="eligibleOrganizations.length === 0" @click="openCreateDialog">{{ '新增业务系统' }}</el-button></div>
    <div class="panel asset-filter-panel">
      <el-input v-model="search" clearable placeholder="搜索系统名称、组织或负责人，也可输入编码" @keyup.enter="resetAndLoadSystems" @clear="resetAndLoadSystems" />
      <el-select v-model="statusFilter" clearable placeholder="全部状态" @change="resetAndLoadSystems" @clear="resetAndLoadSystems">
        <el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" />
      </el-select>
      <el-select v-if="auth.isGroupAdmin && !inventoryMode" v-model="organizationFilter" clearable filterable placeholder="全部组织" @change="resetAndLoadSystems" @clear="resetAndLoadSystems">
        <el-option v-for="item in eligibleOrganizations" :key="item.id" :label="item.name" :value="item.id" />
      </el-select>
      <el-button @click="resetAndLoadSystems">查询</el-button>
      <span class="asset-filter-count">共 {{ total }} 条</span>
    </div>
    <div v-if="inventoryMode" class="panel data-table-panel inventory-draft-panel" v-loading="loading">
      <div class="table-panel-heading"><div><strong>本次上报内容</strong><span>共 {{ draftTotal }} 个；首次上报、变更上报和下线申请均在审核发布后影响集团正式台账</span></div><el-tag type="warning" effect="plain">待提交</el-tag></div>
      <el-table :data="draftSystems" row-key="id" empty-text="尚未选择需要上报的业务系统">
        <el-table-column label="业务系统" min-width="200"><template #default="scope"><el-button link type="primary" class="table-link" :disabled="scope.row.draft_status === 'submitted' || scope.row.operation === 'delete'" @click="openEditDialog(scope.row)">{{ scope.row.name }}</el-button><span v-if="scope.row.short_name" class="list-secondary">{{ scope.row.short_name }}</span></template></el-table-column>

        <el-table-column prop="organization_name" label="所属组织" min-width="130" />
        <el-table-column label="状态/等级" min-width="130"><template #default="scope"><el-tag effect="plain">{{ statusLabel(scope.row.status) }}</el-tag><span class="inline-note">{{ levelLabel(scope.row.level) }}</span></template></el-table-column>
        <el-table-column label="变更类型" width="90"><template #default="scope"><el-tag :type="scope.row.operation === 'delete' ? 'danger' : scope.row.operation === 'update' ? 'warning' : 'success'" effect="plain">{{ draftOperationLabels[scope.row.operation || 'create'] }}</el-tag></template></el-table-column>
        <el-table-column label="草稿状态" width="105"><template #default="scope"><el-tag type="warning" effect="plain">{{ draftStatusLabels[scope.row.draft_status || 'editing'] || scope.row.draft_status }}</el-tag></template></el-table-column>
        <el-table-column label="填报归属" min-width="180"><template #default="scope"><span v-if="!draftOwnerLabel(scope.row)" class="list-secondary">尚未分配填报人</span><template v-else><span>{{ draftOwnerLabel(scope.row) }}</span><span v-if="draftTakeoverSummary(scope.row)" class="list-secondary">{{ draftTakeoverSummary(scope.row) }}</span><span v-else-if="draftLastEditedHint(scope.row)" class="list-secondary">{{ draftLastEditedHint(scope.row) }}</span></template></template></el-table-column>
        <el-table-column label="操作" width="120" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="scope"><RowActions :label="`操作：${scope.row.name || scope.row.title || scope.row.code || scope.row.display_name || scope.row.id}`"><el-button link type="primary" @click="openWorkingVersionDiff(scope.row)">查看变更</el-button><el-button v-if="scope.row.operation !== 'delete'" link type="primary" :disabled="scope.row.draft_status === 'submitted'" @click="openEditDialog(scope.row)">编辑</el-button><el-button v-if="canTakeOverDraft(scope.row)" link type="warning" @click="takeoverDraft(scope.row)">接管</el-button><el-button link type="danger" :disabled="scope.row.draft_status === 'submitted'" @click="removeDraft(scope.row)">取消上报</el-button></RowActions></template></el-table-column>
      </el-table>
      <div v-if="draftTotal > 0" class="table-pagination"><el-pagination background layout="total, sizes, prev, pager, next" :total="draftTotal" :current-page="draftPage" :page-size="draftPageSize" :page-sizes="[20, 50, 100]" @current-change="handleDraftPageChange" @size-change="handleDraftPageSizeChange" /></div>
    </div>
    <div class="panel data-table-panel">
      <div v-if="inventoryMode" class="batch-toolbar">
        <div><strong>选择上报</strong><span>已选择 {{ selectedSystems.length }} 个未上报业务系统，单次最多 200 个</span></div>
        <div class="button-row"><el-button type="primary" :loading="batchLoading" :disabled="selectedSystems.length === 0" @click="batchSelectWorkspaceBusinessSystems">批量纳入本次上报</el-button></div>
      </div>
      <div v-else class="batch-toolbar">
        <div><strong>批量操作</strong><span>已选择 {{ selectedSystems.length }} 个业务系统，单次最多 200 个</span></div>
        <div class="batch-toolbar-actions">
          <el-button :disabled="selectedSystems.length === 0" :loading="batchLoading" @click="runBatchArchive(false)">批量恢复</el-button>
          <el-button type="warning" plain :disabled="selectedSystems.length === 0" :loading="batchLoading" @click="runBatchArchive(true)">批量归档</el-button>
          <el-dropdown :disabled="selectedSystems.length === 0 || selectedExporting">
            <el-button :loading="selectedExporting" :disabled="selectedSystems.length === 0">导出选中</el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item @click="exportSelectedSystems('xlsx')">导出选中 XLSX</el-dropdown-item>
                <el-dropdown-item @click="exportSelectedSystems('csv')">导出选中 CSV</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>
      <el-table v-loading="loading" :data="systems" row-key="id" empty-text="暂无业务系统" @selection-change="handleSelectionChange">
        <el-table-column type="selection" width="46" :selectable="canSelectBusinessSystem" />
        <el-table-column label="业务系统" min-width="200">
          <template #default="scope"><el-button link type="primary" class="table-link" @click="openDetail(scope.row)">{{ scope.row.name }}</el-button><span v-if="scope.row.short_name" class="list-secondary">{{ scope.row.short_name }}</span></template>
        </el-table-column>

        <el-table-column prop="organization_name" label="所属组织" min-width="130" />
        <el-table-column label="状态/等级" min-width="130"><template #default="scope"><el-tag effect="plain">{{ statusLabel(scope.row.status) }}</el-tag><span class="inline-note">{{ levelLabel(scope.row.level) }}</span><span v-if="inventoryMode && scope.row.is_working_copy && scope.row.task_id" class="list-secondary">{{ scope.row.task_id === inventoryTaskID ? '已纳入本次上报' : '已纳入其他任务' }}</span></template></el-table-column>
        <el-table-column label="数据安全" min-width="155"><template #default="scope"><span>个人信息：{{ yesNo(scope.row.contains_personal_information) }}</span><span class="list-secondary">敏感信息：{{ yesNo(scope.row.contains_sensitive_personal_information) }} · 超 10 万：{{ yesNo(scope.row.data_volume_over_100k) }}</span></template></el-table-column>


        <el-table-column prop="owner_name" label="负责人" min-width="90"><template #default="scope">{{ scope.row.owner_name || '—' }}</template></el-table-column>
        <el-table-column v-if="!auth.isGroupAdmin && !inventoryMode" label="上报状态" width="130"><template #default="scope"><el-tag :type="scope.row.reporting_status === 'reported' ? 'success' : 'warning'" effect="plain">{{ reportingStatusLabels[scope.row.reporting_status || 'reported'] }}</el-tag></template></el-table-column>
        <el-table-column label="操作" fixed="right" width="88" align="right" header-align="right" class-name="table-action-column"><template #default="scope"><RowActions :label="`操作：${scope.row.name || scope.row.title || scope.row.code || scope.row.display_name || scope.row.id}`"><template v-if="!inventoryMode"><template v-if="scope.row.is_working_copy"><el-button link type="primary" @click="openWorkingVersionDiff(scope.row)">查看变更</el-button><el-button link type="primary" @click="openEditDialog(scope.row)">编辑</el-button><el-button link type="danger" @click="remove(scope.row)">删除</el-button></template><template v-else><el-button link type="primary" @click="openChangeHistory(scope.row)">历史</el-button><el-button link type="primary" @click="openEditDialog(scope.row)">编辑</el-button><el-button link :type="scope.row.status === 'archived' ? 'success' : 'warning'" @click="changeArchive(scope.row, scope.row.status !== 'archived')">{{ scope.row.status === 'archived' ? '恢复' : '归档' }}</el-button><el-button link type="danger" @click="remove(scope.row)">删除</el-button></template></template><template v-else><template v-if="scope.row.is_working_copy"><el-button link type="primary" @click="openWorkingVersionDiff(scope.row)">查看变更</el-button><el-button v-if="!scope.row.task_id" link type="primary" @click="selectWorkspaceBusinessSystem(scope.row)">选择上报</el-button></template><template v-else><el-button link type="primary" @click="openEditDialog(scope.row)">修改后上报</el-button><el-button link type="danger" @click="requestSystemDeletion(scope.row)">申请下线</el-button></template></template></RowActions></template></el-table-column>
      </el-table>
      <div class="table-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="total" :current-page="page" :page-size="pageSize" :page-sizes="[20, 50, 100]" @current-change="handlePageChange" @size-change="handlePageSizeChange" />
      </div>
    </div>

    <el-dialog v-model="batchResultVisible" :title="batchResultTitle" width="720px">
      <template v-if="batchResult">
        <el-alert :title="`共处理 ${batchResult.total_count} 个，成功 ${batchResult.success_count} 个，失败 ${batchResult.failure_count} 个`" :type="batchResult.failure_count ? 'warning' : 'success'" :closable="false" show-icon />
        <el-table :data="batchResult.items" max-height="420" class="batch-result-table">
          <el-table-column prop="resource_name" label="业务系统" min-width="220" />
          <el-table-column label="结果" width="90"><template #default="scope"><el-tag :type="scope.row.success ? 'success' : 'danger'">{{ scope.row.success ? '成功' : '失败' }}</el-tag></template></el-table-column>
          <el-table-column prop="message" label="说明" min-width="230" />
        </el-table>
      </template>
      <template #footer><el-button type="primary" @click="batchResultVisible = false">关闭</el-button></template>
    </el-dialog>

    <WorkingVersionDiffDrawer ref="workingVersionDiffRef" resource-type="business_system" />
    <ResourceSubmissionHistoryDrawer ref="resourceSubmissionHistoryRef" resource-type="business_system" :allow-restore="!auth.isGroupAdmin && auth.hasPermission('inventory.manage') && auth.hasPermission('system.manage')" @restored="load()" />

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="800px" destroy-on-close>
      <el-alert v-if="inventoryMode" title="这里保存的是组织工作版本；只有选择纳入本次上报并通过审核后，才会更新集团正式台账。" type="info" :closable="false" show-icon class="dialog-context-alert" /><el-form ref="formRef" :model="form" :rules="rules" label-position="top">
        <div class="form-section-title">基本信息</div>
        <div class="form-grid two-columns">
          <el-form-item label="所属组织" prop="organization_id"><el-select v-model="form.organization_id" :disabled="inventoryMode || !auth.isGroupAdmin" style="width:100%"><el-option v-for="item in eligibleOrganizations" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="系统编码" prop="code"><el-input v-model="form.code" placeholder="如：CRM_001" maxlength="64" /><div class="muted-text">系统字段，用于导入、接口关联和数据去重；日常列表不显示。</div></el-form-item>
          <el-form-item label="系统名称" prop="name"><el-input v-model="form.name" placeholder="请输入系统名称" maxlength="255" /></el-form-item>
          <el-form-item label="系统简称"><el-input v-model="form.short_name" maxlength="128" /></el-form-item>
          <el-form-item label="运行状态"><el-select v-model="form.status" style="width:100%"><el-option label="运行中" value="running" /><el-option label="规划中" value="planning" /><el-option label="暂停" value="suspended" /><el-option label="已下线" value="offline" /><el-option v-if="editingID" label="已归档" value="archived" /></el-select></el-form-item>
          <el-form-item label="系统等级"><el-select v-model="form.level" style="width:100%"><el-option label="一般" value="normal" /><el-option label="重要" value="important" /><el-option label="核心" value="core" /></el-select></el-form-item>
          <el-form-item label="负责人"><el-input v-model="form.owner_name" maxlength="128" /></el-form-item>
          <el-form-item label="联系方式"><el-input v-model="form.owner_contact" maxlength="255" /></el-form-item>
          <el-form-item label="互联网暴露情况"><el-select v-model="form.internet_exposure" style="width:100%"><el-option label="是" value="yes" /><el-option label="否" value="no" /><el-option label="部分" value="partial" /><el-option label="待确认" value="unknown" /></el-select></el-form-item>
          <el-form-item label="部署方式"><el-input v-model="form.deployment_type" placeholder="如：集团私有云" /></el-form-item>
          <el-form-item label="系统说明" class="full-span"><el-input v-model="form.description" type="textarea" :rows="2" /></el-form-item>
        </div>
        <div class="form-section-title">数据安全属性</div>
        <div class="form-grid two-columns">
          <el-form-item label="是否包含个人信息"><el-select v-model="form.contains_personal_information" style="width:100%"><el-option label="是" value="yes" /><el-option label="否" value="no" /><el-option label="待确认" value="unknown" /></el-select></el-form-item>
          <el-form-item label="是否包含敏感个人信息"><el-select v-model="form.contains_sensitive_personal_information" :disabled="form.contains_personal_information === 'no'" style="width:100%"><el-option label="是" value="yes" /><el-option label="否" value="no" /><el-option label="待确认" value="unknown" /></el-select></el-form-item>
          <el-form-item label="个人信息记录数"><el-input-number v-model="form.personal_information_record_count" :disabled="form.contains_personal_information === 'no'" :min="0" :step="10000" controls-position="right" style="width:100%" /></el-form-item>
          <el-form-item label="数据量是否超过 10 万条"><el-select v-model="form.data_volume_over_100k" :disabled="typeof form.personal_information_record_count === 'number'" style="width:100%"><el-option label="是" value="yes" /><el-option label="否" value="no" /><el-option label="待确认" value="unknown" /></el-select></el-form-item>
          <el-form-item label="是否包含重要数据"><el-select v-model="form.contains_important_data" style="width:100%"><el-option label="是" value="yes" /><el-option label="否" value="no" /><el-option label="待认定" value="pending" /><el-option label="待确认" value="unknown" /></el-select></el-form-item>
          <el-form-item label="是否包含核心数据"><el-select v-model="form.contains_core_data" style="width:100%"><el-option label="是" value="yes" /><el-option label="否" value="no" /><el-option label="待认定" value="pending" /><el-option label="待确认" value="unknown" /></el-select></el-form-item>
          <el-form-item label="数据存储位置" class="full-span"><el-input v-model="form.data_storage_location" placeholder="如：上海数据中心、集团私有云" /></el-form-item>
          <el-form-item label="数据安全备注" class="full-span"><el-input v-model="form.security_notes" type="textarea" :rows="2" /></el-form-item>
        </div>
        <template v-if="customFields.length">
          <div class="form-section-title">集团自定义字段</div>
          <div class="form-grid two-columns">
            <el-form-item v-for="field in customFields" :key="field.id" :label="field.field_name" :required="field.is_required" :class="field.field_type === 'textarea' ? 'full-span' : ''">
              <el-input v-if="field.field_type === 'text'" v-model="customValues[field.field_key]" :placeholder="field.description" />
              <el-input v-else-if="field.field_type === 'textarea'" v-model="customValues[field.field_key]" type="textarea" :rows="2" :placeholder="field.description" />
              <el-input-number v-else-if="field.field_type === 'number'" v-model="customValues[field.field_key]" style="width:100%" />
              <el-date-picker v-else-if="field.field_type === 'date'" v-model="customValues[field.field_key]" type="date" value-format="YYYY-MM-DD" style="width:100%" />
              <el-switch v-else-if="field.field_type === 'boolean'" v-model="customValues[field.field_key]" />
              <el-select v-else-if="field.field_type === 'select'" v-model="customValues[field.field_key]" style="width:100%"><el-option v-for="option in field.options" :key="option" :label="option" :value="option" /></el-select>
              <el-select v-else-if="field.field_type === 'multiselect'" v-model="customValues[field.field_key]" multiple style="width:100%"><el-option v-for="option in field.options" :key="option" :label="option" :value="option" /></el-select>
            </el-form-item>
          </div>
        </template>
      </el-form>
      <template #footer><el-button @click="dialogVisible=false">取消</el-button><el-button type="primary" :loading="submitting" @click="submit">{{ submitLabel }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="importDialogVisible" title="批量导入业务系统" width="720px" destroy-on-close>
      <el-alert :title="auth.isGroupAdmin ? '所属组织由当前表单统一指定；成功项直接进入集团正式台账。请先选择所属组织再下载模板，模板会包含当前启用的集团自定义字段；系统编码已存在的行会跳过，不覆盖原记录。' : '所属组织固定为当前组织；成功项保存为未上报工作版本，集团暂不可见，后续需在填报任务中选择并提交上报。系统编码已存在的行会跳过，不覆盖正式记录或现有工作版本。'" type="info" :closable="false" show-icon />
      <div class="import-toolbar"><el-button link type="primary" :loading="downloading" @click="downloadTemplate('xlsx')">下载 XLSX 模板</el-button><el-button link type="primary" :loading="downloading" @click="downloadTemplate('csv')">下载 CSV 模板</el-button></div>
      <el-form label-position="top">
        <el-form-item label="导入到所属组织" required><el-select v-model="importForm.organization_id" filterable :disabled="!auth.isGroupAdmin" @change="handleImportOrganizationChange" style="width: 100%"><el-option v-for="item in eligibleOrganizations" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item>
        <el-form-item v-if="!auth.isGroupAdmin" label="重复记录处理方式">
          <div class="import-mode-field">
            <el-radio-group v-model="importForm.import_mode" @change="resetImportPreview"><el-radio value="skip_duplicates">重复记录跳过（默认）</el-radio><el-radio value="update_working">覆盖为未上报工作版本</el-radio></el-radio-group>
            <p class="muted-text">覆盖模式不会直接修改集团正式台账：已上报系统会生成更新工作版本，已有可编辑工作版本会被文件内容更新；已纳入任务、已提交或待下线版本不可覆盖，导入后仍需通过填报任务提交上报。</p>
          </div>
        </el-form-item>
        <el-form-item label="CSV / XLSX 文件" required><el-upload drag :auto-upload="false" :limit="1" accept=".csv,.xlsx" :on-change="handleImportFileChange" :on-remove="handleImportFileRemove"><el-icon><UploadFilled /></el-icon><div class="el-upload__text">拖入文件，或<em>点击选择</em></div><template #tip><div class="el-upload__tip">文件不超过 10 MB，单次最多 5000 行；允许部分成功。</div></template></el-upload></el-form-item>
        <el-form-item v-if="importFile" label="文件列映射" required>
          <div v-loading="importColumnsLoading" class="import-mapping-panel">
            <div class="import-mapping-header"><span>系统字段</span><span>文件列</span></div>
            <div v-for="field in importFields" :key="field.key" class="import-mapping-row">
              <div class="import-mapping-field"><strong>{{ field.name }}</strong><el-tag v-if="field.required" size="small" type="danger" effect="plain">必填</el-tag><el-tag v-if="field.custom" size="small" effect="plain">自定义</el-tag></div>
              <el-select v-model="importColumnMapping[field.key]" clearable placeholder="不导入该字段" @change="handleImportMappingChange">
                <el-option v-for="column in importColumns" :key="column.index" :label="importColumnLabel(column)" :value="column.index" :disabled="isImportColumnUsed(importColumnMapping, column.index, field.key)" />
              </el-select>
            </div>
            <el-alert v-if="importMappingError" :title="importMappingError" type="warning" :closable="false" show-icon />
            <p v-else-if="importFields.length" class="muted-text import-mapping-tip">已自动识别标准模板列；非标准文件可在此调整。未映射的可选字段不会导入，预检和正式导入使用同一份映射。</p>
          </div>
        </el-form-item>
      </el-form>
      <div v-if="importResult" class="import-result" data-testid="business-system-import-result">
        <el-alert :title="importResultMode === 'preview' ? '预检结果：尚未写入台账，请确认后执行导入' : (auth.isGroupAdmin ? '正式台账导入结果' : '组织工作台账导入结果：成功项当前为未上报')" :type="importResultMode === 'preview' ? 'info' : 'success'" :closable="false" show-icon />
        <div class="import-result-summary"><div><span>总行数</span><strong>{{ importResult.total_count }}</strong></div><div class="success"><span>{{ importResultMode === 'preview' ? '可导入' : '成功' }}</span><strong>{{ importResult.success_count }}</strong></div><div class="duplicate"><span>重复</span><strong>{{ importResult.duplicate_count }}</strong></div><div class="failure"><span>失败</span><strong>{{ importResult.failure_count }}</strong></div></div>
        <p class="muted-text import-result-breakdown">成功项：新增 {{ importResult.created_count }} 个，更新 {{ importResult.updated_count }} 个。</p>
        <el-table v-if="importResult.errors.length" :data="importResult.errors" size="small" max-height="220"><el-table-column prop="row" label="行号" width="70" /><el-table-column prop="code" label="错误代码" width="150" /><el-table-column prop="message" label="处理说明" min-width="260" /></el-table>
      </div>
      <template #footer><el-button @click="importDialogVisible=false">关闭</el-button><el-button :loading="importLoading || importColumnsLoading" :disabled="Boolean(importMappingError) || !importFields.length" @click="runImport(true)">预检文件</el-button><el-button type="primary" :loading="importLoading" :disabled="importResultMode !== 'preview' || !importResult?.success_count" @click="runImport(false)">确认导入</el-button></template>
    </el-dialog>

    <el-dialog v-model="historyDialogVisible" title="业务系统导入记录" width="980px">
      <el-table v-loading="historyLoading" :data="importHistory" size="small" empty-text="暂无导入记录" max-height="520"><el-table-column prop="created_at" label="时间" width="175" /><el-table-column prop="organization_name" label="所属组织" min-width="130" /><el-table-column prop="file_name" label="文件名" min-width="190" /><el-table-column label="模式" width="130"><template #default="scope">{{ scope.row.import_mode === 'update_working' ? '覆盖为工作版本' : '重复跳过' }}</template></el-table-column><el-table-column label="结果" min-width="280"><template #default="scope">总数 {{ scope.row.total_count }} · 成功 {{ scope.row.success_count }}（新增 {{ scope.row.created_count }} / 更新 {{ scope.row.updated_count }}）· 重复 {{ scope.row.duplicate_count }} · 失败 {{ scope.row.failure_count }}</template></el-table-column><el-table-column prop="created_by_name" label="操作人" width="110" /></el-table>
      <div class="table-pagination dialog-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="importTotal" :current-page="importPage" :page-size="importPageSize" :page-sizes="[20, 50, 100]" @current-change="handleImportPageChange" @size-change="handleImportPageSizeChange" />
      </div>
      <template #footer><el-button @click="historyDialogVisible = false">关闭</el-button></template>
    </el-dialog>

    <el-drawer v-model="detailVisible" title="业务系统详情" size="620px">
      <div v-loading="detailLoading" class="detail-drawer">
        <template v-if="detail">
          <div class="detail-hero"><div><span class="eyebrow">SYSTEM PROFILE</span><h2>{{ detail.name }}</h2><p>{{ detail.organization_name }}</p><div class="detail-system-code"><span>系统编码（系统字段）</span><code>{{ detail.code }}</code><el-button link type="primary" size="small" @click="copySystemCode">复制</el-button></div></div><el-tag effect="plain">{{ statusLabel(detail.status) }}</el-tag></div>
          <div class="detail-grid"><div><span>系统等级</span><strong>{{ levelLabel(detail.level) }}</strong></div><div><span>负责人</span><strong>{{ detail.owner_name || '未填写' }}</strong></div><div><span>联系方式</span><strong>{{ detail.owner_contact || '未填写' }}</strong></div><div><span>互联网暴露</span><strong>{{ yesNo(detail.internet_exposure) }}</strong></div><div><span>个人信息</span><strong>{{ yesNo(detail.contains_personal_information) }}</strong></div><div><span>敏感个人信息</span><strong>{{ yesNo(detail.contains_sensitive_personal_information) }}</strong></div><div><span>记录数</span><strong>{{ detail.personal_information_record_count ?? '未填写' }}</strong></div><div><span>超过 10 万条</span><strong>{{ yesNo(detail.data_volume_over_100k) }}</strong></div><div><span>重要数据</span><strong>{{ yesNo(detail.contains_important_data) }}</strong></div><div><span>核心数据</span><strong>{{ yesNo(detail.contains_core_data) }}</strong></div><div><span>部署方式</span><strong>{{ detail.deployment_type || '未填写' }}</strong></div><div><span>存储位置</span><strong>{{ detail.data_storage_location || '未填写' }}</strong></div></div>
          <template v-if="detailCustomFields.length">
            <div class="form-section-title">集团自定义字段</div>
            <div class="detail-grid"><div v-for="field in detailCustomFields" :key="field.id"><span>{{ field.field_name }}</span><strong>{{ customValueLabel(detailCustomValues[field.field_key]) }}</strong></div></div>
          </template>
          <div class="form-section-title">系统说明</div><p class="detail-description">{{ detail.description || '暂无说明' }}</p>
          <div class="form-section-title">关联网络资产（{{ detailAssetTotal }}）</div>
          <el-table v-loading="detailAssetLoading" :data="detailAssets" size="small" empty-text="暂未关联网络资产" max-height="360"><el-table-column label="资产" min-width="210"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.value }}</strong><span>{{ scope.row.name || assetTypeLabel(scope.row.type) }}</span></div></template></el-table-column><el-table-column label="范围" width="80"><template #default="scope">{{ scope.row.network_scope === 'public' ? '公网' : scope.row.network_scope === 'private' ? '内网' : '待确认' }}</template></el-table-column><el-table-column label="状态" width="90"><template #default="scope">{{ assetStatusLabel(scope.row.status) }}</template></el-table-column><el-table-column prop="owner_name" label="负责人" min-width="90" /></el-table>
          <div v-if="detailAssetTotal > 0" class="table-pagination dialog-pagination"><el-pagination background small layout="total, sizes, prev, pager, next" :total="detailAssetTotal" :current-page="detailAssetPage" :page-size="detailAssetPageSize" :page-sizes="[20, 50, 100]" @current-change="handleDetailAssetPageChange" @size-change="handleDetailAssetPageSizeChange" /></div>
          <div class="detail-actions"><el-button type="primary" plain @click="openResourceSubmissionHistory(detail as BusinessSystem)">上报历史</el-button><template v-if="!inventoryMode"><el-button v-if="!detail.is_working_copy" @click="openChangeHistory(detail as BusinessSystem)">变更历史</el-button><el-button @click="openEditDialog(detail as BusinessSystem)">编辑系统</el-button><el-button v-if="!detail.is_working_copy" :type="detail.status === 'archived' ? 'success' : 'warning'" @click="changeArchive(detail, detail.status !== 'archived')">{{ detail.status === 'archived' ? '恢复系统' : '归档系统' }}</el-button></template></div>
        </template>
      </div>
    </el-drawer>

    <el-drawer v-model="changeHistoryVisible" :title="changeHistoryTitle" size="760px">
      <div v-loading="changeHistoryLoading" class="detail-drawer">
        <el-timeline v-if="changeHistoryItems.length" class="change-history-timeline">
          <el-timeline-item v-for="item in changeHistoryItems" :key="item.id" :timestamp="item.created_at" placement="top">
            <el-card shadow="never">
              <div class="change-history-title"><strong>{{ item.action_label || item.action }}</strong><span>{{ item.display_name || item.username || '系统用户' }}</span></div>
              <el-table v-if="changesOf(item).length" :data="changesOf(item)" size="small" border class="change-history-table">
                <el-table-column prop="label" label="字段" min-width="140" />
                <el-table-column prop="before" label="修改前" min-width="210" />
                <el-table-column prop="after" label="修改后" min-width="210" />
              </el-table>
              <p v-else class="muted-text">{{ item.summary || '该历史记录未保存字段级前后值。' }}</p>
            </el-card>
          </el-timeline-item>
        </el-timeline>
        <el-empty v-else-if="!changeHistoryLoading" description="暂无变更历史" />
        <div v-if="changeHistoryTotal > 0" class="table-pagination">
          <el-pagination
            background
            layout="total, sizes, prev, pager, next"
            :total="changeHistoryTotal"
            :current-page="changeHistoryPage"
            :page-size="changeHistoryPageSize"
            :page-sizes="[20, 50, 100]"
            @current-change="handleChangeHistoryPageChange"
            @size-change="handleChangeHistoryPageSizeChange"
          />
        </div>
      </div>
    </el-drawer>
  </section>
</template>
