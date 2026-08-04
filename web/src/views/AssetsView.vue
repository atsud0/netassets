<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules, type UploadFile, type UploadFiles } from 'element-plus'
import { apiClient, getAPIErrorMessage, getDownloadAPIError } from '../api/client'
import { createExportJob } from '../api/export-jobs'
import { useAuthStore } from '../stores/auth'
import SavedFilterViews from '../components/SavedFilterViews.vue'

interface Organization { id: string; code: string; name: string; type: string; status: string }
interface BusinessSystem { id: string; code: string; name: string; organization_id: string; organization_name: string }
interface OwnerOption { id: string; username: string; display_name: string; email?: string; mobile_phone?: string }
interface Asset {
  id: string; type: string; value: string; normalized_value: string; name?: string
  organization_id: string; organization_code: string; organization_name: string
  business_system_id?: string; business_system_code?: string; business_system_name?: string
  environment: string; network_scope: string; network_area_id?: string; network_area_name?: string
  status: string; importance: string; source: string
  owner_user_id?: string; owner_username?: string; owner_user_display_name?: string
  owner_name?: string; notes?: string; expires_at?: string; created_at: string; updated_at: string
  is_draft?: boolean; draft_status?: 'editing' | 'submitted' | 'returned' | 'published'; task_id?: string
}
interface NetworkArea {
  id: string; organization_id: string; organization_code: string; organization_name: string
  name: string; code?: string; status: 'enabled' | 'disabled'; sort_order: number; notes?: string; asset_count: number
}
interface CustomField { id: string; field_key: string; field_name: string; description?: string; field_type: string; options: string[]; is_required: boolean }
interface ImportError { row: number; code: string; message: string }
interface ImportResult {
  id: string; file_name: string; file_type: string; status: string; total_count: number
  success_count: number; failure_count: number; duplicate_count: number; errors: ImportError[]
}
interface ImportHistory extends ImportResult {
  organization_id: string; organization_code: string; organization_name: string
  created_by_name?: string; created_at: string
}
interface FieldChange { field: string; label: string; before: string; after: string }
interface ChangeHistoryItem {
  id: number; action: string; action_label: string; summary?: string; display_name?: string; username?: string
  created_at: string; details?: { changes?: FieldChange[] }
}
interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface BatchOperationItem { id: string; success: boolean; code?: string; message: string; item?: Asset; resource_name?: string }
interface BatchOperationResult { total_count: number; success_count: number; failure_count: number; items: BatchOperationItem[] }
interface DetailResponse { item: Asset }

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const loading = ref(false)
const submitting = ref(false)
const importing = ref(false)
const downloading = ref(false)
const exporting = ref(false)
const historyLoading = ref(false)
const editDialogVisible = ref(false)
const detailVisible = ref(false)
const detailLoading = ref(false)
const detail = ref<Asset | null>(null)
const detailCustomFields = ref<CustomField[]>([])
const detailCustomValues = ref<Record<string, unknown>>({})
const importDialogVisible = ref(false)
const historyDialogVisible = ref(false)
const changeHistoryVisible = ref(false)
const changeHistoryLoading = ref(false)
const changeHistoryItems = ref<ChangeHistoryItem[]>([])
const changeHistoryTitle = ref('')
const changeHistoryResourceID = ref('')
const changeHistoryPage = ref(1)
const changeHistoryPageSize = ref(20)
const changeHistoryTotal = ref(0)
const areaManagementVisible = ref(false)
const areaFormVisible = ref(false)
const areaSubmitting = ref(false)
const editingNetworkAreaID = ref('')
const editingAssetID = ref('')
const formRef = ref<FormInstance>()
const assets = ref<Asset[]>([])
const draftAssets = ref<Asset[]>([])
const draftTotal = ref(0)
const draftPage = ref(1)
const draftPageSize = ref(20)
const editingIsDraft = ref(false)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const organizations = ref<Organization[]>([])
const systems = ref<BusinessSystem[]>([])
const networkAreas = ref<NetworkArea[]>([])
const ownerOptions = ref<OwnerOption[]>([])
const ownerOptionsLoading = ref(false)
const customFields = ref<CustomField[]>([])
const customValues = reactive<Record<string, unknown>>({})
const importHistory = ref<ImportHistory[]>([])
const importPage = ref(1)
const importPageSize = ref(20)
const importTotal = ref(0)
const importFile = ref<File>()
const importResult = ref<ImportResult>()
const importResultMode = ref<'preview' | 'completed'>()
const search = ref('')
const typeFilter = ref('')
const statusFilter = ref('')
const organizationFilter = ref('')
const savedFilterViews = ref<InstanceType<typeof SavedFilterViews>>()
const selectedAssets = ref<Asset[]>([])
const batchLoading = ref(false)
const batchStatusValue = ref('')
const batchNetworkAreaValue = ref('')
const batchResultVisible = ref(false)
const batchResult = ref<BatchOperationResult>()
const batchResultTitle = ref('批量更新结果')

const form = reactive({
  type: 'domain', value: '', name: '', organization_id: '', business_system_id: '', network_area_id: '',
  environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', owner_user_id: '', owner_name: '', notes: '', expires_at: null as Date | null,
})
const networkAreaForm = reactive({ organization_id: '', name: '', code: '', status: 'enabled', sort_order: 0, notes: '' })
const importForm = reactive({ organization_id: '' })
const rules: FormRules = {
  type: [{ required: true, message: '请选择资产类型', trigger: 'change' }],
  value: [{ required: true, message: '请输入资产值', trigger: 'blur' }],
  organization_id: [{ required: true, message: '请选择所属组织', trigger: 'change' }],
}

const inventoryTaskID = computed(() => typeof route.query.task_id === 'string' ? route.query.task_id : '')
const inventoryOrganizationID = computed(() => typeof route.query.organization_id === 'string' ? route.query.organization_id : '')
const inventoryTaskName = computed(() => typeof route.query.task_name === 'string' ? route.query.task_name : '当前填报任务')
const inventoryNetworkScope = computed(() => typeof route.query.asset_network_scope === 'string' ? route.query.asset_network_scope : 'all')
const inventoryMode = computed(() => route.query.mode === 'inventory' && Boolean(inventoryTaskID.value && inventoryOrganizationID.value))
const inventoryOrganizationName = computed(() => organizations.value.find((item) => item.id === inventoryOrganizationID.value)?.name || '当前填报组织')
const draftStatusLabels: Record<string, string> = { editing: '编辑中', returned: '已退回', submitted: '已提交', published: '已发布' }

const enabledOrganizations = computed(() => organizations.value.filter((item) => item.status === 'enabled'))
const availableSystems = computed(() => systems.value.filter((item) => item.organization_id === form.organization_id))
const availableNetworkAreas = computed(() => networkAreas.value.filter((item) => item.organization_id === form.organization_id))
const dialogTitle = computed(() => editingAssetID.value ? '编辑网络资产' : '新增网络资产')
const submitLabel = computed(() => editingAssetID.value ? '保存修改' : '确认新增')
const selectedOrganizationID = computed(() => {
  const ids = new Set(selectedAssets.value.map((item) => item.organization_id))
  return ids.size === 1 ? [...ids][0] : ''
})
const selectedNetworkAreas = computed(() => networkAreas.value.filter((item) => item.organization_id === selectedOrganizationID.value && item.status === 'enabled'))
const activeCount = computed(() => assets.value.filter((item) => item.status === 'active').length)
const publicCount = computed(() => assets.value.filter((item) => item.network_scope === 'public').length)
const domainCount = computed(() => assets.value.filter((item) => item.type === 'domain').length)
const valuePlaceholder = computed(() => ({
  domain: '如：portal.example.com', ip: '如：203.0.113.10', url: '如：https://portal.example.com/login',
  service: '如：10.0.0.8:443', other: '请输入资产标识',
}[form.type] ?? '请输入资产值'))

const typeLabels: Record<string, string> = { domain: '域名', ip: 'IP 地址', url: 'URL', service: '网络服务', other: '其他' }
const environmentLabels: Record<string, string> = { production: '生产', staging: '预发布', testing: '测试', development: '开发', office: '办公', other: '其他' }
const scopeLabels: Record<string, string> = { public: '公网', private: '内网', unknown: '未知' }
const importanceLabels: Record<string, string> = { normal: '一般', important: '重要', core: '核心' }
const sourceLabels: Record<string, string> = { manual: '手工', import: '导入', api: 'API', plugin: '插件' }
const statusLabels: Record<string, string> = { active: '启用', pending: '待确认', disabled: '停用', archived: '已归档' }
const importStatusLabels: Record<string, string> = { completed: '完成', partial: '部分成功', failed: '失败' }

const currentAssetFilters = computed<Record<string, string>>(() => ({
  ...(search.value.trim() ? { q: search.value.trim() } : {}),
  ...(typeFilter.value ? { type: typeFilter.value } : {}),
  ...(statusFilter.value ? { status: statusFilter.value } : {}),
  ...(organizationFilter.value ? { organization_id: organizationFilter.value } : {}),
}))

function setAssetFilters(filters: Record<string, string>) {
  search.value = filters.q ?? ''
  typeFilter.value = filters.type ?? ''
  statusFilter.value = filters.status ?? ''
  organizationFilter.value = filters.organization_id ?? ''
}

function applySavedAssetFilters(filters: Record<string, string>) {
  setAssetFilters(filters)
  page.value = 1
  void loadAssets()
}

watch(() => form.organization_id, () => {
  if (!availableSystems.value.some((item) => item.id === form.business_system_id)) form.business_system_id = ''
  if (!availableNetworkAreas.value.some((item) => item.id === form.network_area_id)) form.network_area_id = ''
})

function ownerOptionLabel(item: Pick<OwnerOption, 'username' | 'display_name'>) {
  return item.display_name && item.display_name !== item.username ? `${item.display_name}（${item.username}）` : item.username
}

function assetOwnerLabel(item: Asset) {
  if (!item.owner_user_id || !item.owner_username) return '未绑定'
  return ownerOptionLabel({ username: item.owner_username, display_name: item.owner_user_display_name ?? '' })
}

async function loadOwnerOptions(keyword = '') {
  if (!form.organization_id) {
    ownerOptions.value = []
    return
  }
  ownerOptionsLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<OwnerOption>>('/assets/owner-options', {
      params: { organization_id: form.organization_id, q: keyword.trim() || undefined, page_size: 50 },
    })
    const current = ownerOptions.value.find((item) => item.id === form.owner_user_id)
    ownerOptions.value = current && !response.data.items.some((item) => item.id === current.id)
      ? [current, ...response.data.items]
      : response.data.items
  } catch (error) {
    ownerOptions.value = []
    ElMessage.error(getAPIErrorMessage(error, '读取责任人候选失败'))
  } finally {
    ownerOptionsLoading.value = false
  }
}

function handleFormOrganizationChange() {
  form.owner_user_id = ''
  ownerOptions.value = []
  void loadOwnerOptions()
}

async function loadAssetDrafts() {
  if (!inventoryMode.value) {
    draftAssets.value = []
    draftTotal.value = 0
    return
  }
  const response = await apiClient.get<ListResponse<Asset>>(
    `/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/asset-drafts`,
    { params: { page: draftPage.value, page_size: draftPageSize.value } },
  )
  draftAssets.value = response.data.items
  draftTotal.value = response.data.total
}

async function loadAssets() {
  loading.value = true
  try {
    if (inventoryMode.value) organizationFilter.value = inventoryOrganizationID.value
    const [response] = await Promise.all([
      apiClient.get<ListResponse<Asset>>('/assets', {
        params: {
          page: page.value,
          page_size: pageSize.value,
          q: search.value.trim() || undefined,
          type: typeFilter.value || undefined,
          status: statusFilter.value || undefined,
          organization_id: inventoryMode.value ? inventoryOrganizationID.value : (organizationFilter.value || undefined),
        },
      }),
      loadAssetDrafts(),
    ])
    assets.value = response.data.items
    total.value = response.data.total
    selectedAssets.value = []
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取网络资产失败'))
  } finally {
    loading.value = false
  }
}

async function load(applyDefaultView = false) {
  try {
    const [organizationResponse, systemResponse, areaResponse] = await Promise.all([
      apiClient.get<ListResponse<Organization>>('/organizations', { params: { page_size: 100 } }),
      apiClient.get<ListResponse<BusinessSystem>>('/business-systems', { params: { page_size: 100 } }),
      apiClient.get<ListResponse<NetworkArea>>('/network-areas', { params: { page_size: 100 } }),
    ])
    organizations.value = organizationResponse.data.items
    systems.value = systemResponse.data.items
    networkAreas.value = areaResponse.data.items
    try {
      const customResponse = await apiClient.get<ListResponse<CustomField>>('/custom-fields/fillable', { params: { resource_type: 'asset', page_size: 100 } })
      customFields.value = customResponse.data.items
    } catch {
      // Users without asset maintenance permission can still read the asset ledger.
      customFields.value = []
    }
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取网络资产基础数据失败'))
  }
  if (applyDefaultView) {
    const defaultFilters = await savedFilterViews.value?.initialize()
    if (defaultFilters) setAssetFilters(defaultFilters)
  }
  await loadAssets()
}

async function resetAndLoadAssets() {
  page.value = 1
  await loadAssets()
}

async function refreshAfterMutation() {
  await load()
  if (assets.value.length === 0 && total.value > 0 && page.value > 1) {
    page.value -= 1
    await loadAssets()
  }
}

function handlePageChange(value: number) {
  page.value = value
  void loadAssets()
}

function handlePageSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  void loadAssets()
}

function handleDraftPageChange(value: number) {
  draftPage.value = value
  void loadAssetDrafts()
}

function handleDraftPageSizeChange(value: number) {
  draftPageSize.value = value
  draftPage.value = 1
  void loadAssetDrafts()
}

function defaultOrganizationID() {
  return inventoryMode.value ? inventoryOrganizationID.value : (auth.user?.organization?.id ?? enabledOrganizations.value[0]?.id ?? '')
}
function resetForm() {
  Object.keys(customValues).forEach((key) => delete customValues[key])
  customFields.value.forEach((field) => { customValues[field.field_key] = field.field_type === 'multiselect' ? [] : field.field_type === 'boolean' ? false : '' })
  Object.assign(form, {
    type: 'domain', value: '', name: '', organization_id: defaultOrganizationID(), business_system_id: '', network_area_id: '',
    environment: 'production', network_scope: inventoryMode.value && inventoryNetworkScope.value !== 'all' ? inventoryNetworkScope.value : 'public', status: 'active', importance: 'normal', owner_user_id: '', owner_name: '', notes: '', expires_at: null,
  })
  ownerOptions.value = []
}
function openCreateDialog() {
  editingAssetID.value = ''
  editingIsDraft.value = false
  resetForm()
  editDialogVisible.value = true
  void loadOwnerOptions()
}
function openEditDialog(item: Asset) {
  editingAssetID.value = item.id
  editingIsDraft.value = Boolean(item.is_draft)
  Object.assign(form, {
    type: item.type, value: item.value, name: item.name ?? '', organization_id: item.organization_id,
    business_system_id: item.business_system_id ?? '', network_area_id: item.network_area_id ?? '', environment: item.environment, network_scope: item.network_scope,
    status: item.status, importance: item.importance, owner_user_id: item.owner_user_id ?? '', owner_name: item.owner_name ?? '', notes: item.notes ?? '', expires_at: item.expires_at ? new Date(item.expires_at) : null,
  })
  ownerOptions.value = item.owner_user_id && item.owner_username ? [{ id: item.owner_user_id, username: item.owner_username, display_name: item.owner_user_display_name ?? item.owner_username }] : []
  void loadOwnerOptions()
  if (!item.is_draft) void loadCustomValues(item.id)
  editDialogVisible.value = true
}
function isEmptyCustomValue(value: unknown) { return value == null || (typeof value === 'string' && value.trim() === '') || (Array.isArray(value) && value.length === 0) }
function customValueLabel(value: unknown) {
  if (Array.isArray(value)) return value.join('、') || '未填写'
  if (value === true) return '是'
  if (value === false) return '否'
  return value == null || value === '' ? '未填写' : String(value)
}
async function loadCustomValues(id: string) {
  try {
    const { data } = await apiClient.get<{ values: Array<{ field_key: string; value: unknown }> }>(`/assets/${id}/custom-fields`)
    Object.keys(customValues).forEach((key) => delete customValues[key])
    customFields.value.forEach((field) => { customValues[field.field_key] = field.field_type === 'multiselect' ? [] : field.field_type === 'boolean' ? false : '' })
    data.values.forEach((item) => { customValues[item.field_key] = item.value })
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取自定义字段失败'))
  }
}
async function openDetail(row: Asset) {
  detailVisible.value = true
  detailLoading.value = true
  try {
    const [assetResponse, customResponse] = await Promise.all([
      apiClient.get<DetailResponse>(`/assets/${row.id}`),
      apiClient.get<{ definitions: CustomField[]; values: Array<{ field_key: string; value: unknown }> }>(`/assets/${row.id}/custom-fields`),
    ])
    detail.value = assetResponse.data.item
    detailCustomFields.value = customResponse.data.definitions
    detailCustomValues.value = Object.fromEntries(customResponse.data.values.map((item) => [item.field_key, item.value]))
  } catch (error) {
    detailVisible.value = false
    ElMessage.error(getAPIErrorMessage(error, '读取网络资产详情失败'))
  } finally {
    detailLoading.value = false
  }
}

function assetPayload(item: Asset, overrides: Partial<typeof form> = {}) {
  return {
    type: item.type, value: item.value, name: item.name ?? '', organization_id: item.organization_id,
    business_system_id: item.business_system_id ?? '', network_area_id: item.network_area_id ?? '', environment: item.environment, network_scope: item.network_scope,
    status: item.status, importance: item.importance, owner_user_id: item.owner_user_id ?? '', owner_name: item.owner_name ?? '', notes: item.notes ?? '', expires_at: item.expires_at ?? null, ...overrides,
  }
}
async function submit() {
  if (!formRef.value || !(await formRef.value.validate())) return
  submitting.value = true
  try {
    if (!inventoryMode.value) {
      const missing = customFields.value.find((field) => field.is_required && isEmptyCustomValue(customValues[field.field_key]))
      if (missing) { ElMessage.warning(`请填写自定义字段：${missing.field_name}`); return }
    }
    const payload = { ...form, organization_id: inventoryMode.value ? inventoryOrganizationID.value : form.organization_id, expires_at: form.expires_at?.toISOString() ?? null }
    let response
    if (inventoryMode.value) {
      const base = `/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/asset-drafts`
      response = editingAssetID.value && editingIsDraft.value
        ? await apiClient.patch<{ item: Asset }>(`${base}/${editingAssetID.value}`, payload)
        : await apiClient.post<{ item: Asset }>(base, payload)
    } else {
      response = editingAssetID.value
        ? await apiClient.patch<{ item: Asset }>(`/assets/${editingAssetID.value}`, payload)
        : await apiClient.post<{ item: Asset }>('/assets', payload)
    }
    const id = editingAssetID.value || response.data.item.id
    if (!inventoryMode.value && customFields.value.length) await apiClient.put(`/assets/${id}/custom-fields`, { values: customFields.value.map((field) => ({ field_key: field.field_key, value: customValues[field.field_key] })) })
    ElMessage.success(inventoryMode.value ? (editingAssetID.value ? '填报资产草稿已更新' : '填报资产草稿已保存，集团审核前不会进入正式台账') : (editingAssetID.value ? '网络资产已更新' : '网络资产已新增'))
    editDialogVisible.value = false
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, inventoryMode.value ? '保存填报资产草稿失败' : (editingAssetID.value ? '编辑网络资产失败' : '新增网络资产失败')))
  } finally {
    submitting.value = false
  }
}

async function removeDraft(item: Asset) {
  try {
    await ElMessageBox.confirm(`确认删除填报资产草稿“${item.name || item.value}”吗？`, '删除草稿', { type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消' })
    await apiClient.delete(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/asset-drafts/${item.id}`)
    ElMessage.success('填报资产草稿已删除')
    await loadAssetDrafts()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, '删除填报资产草稿失败'))
  }
}

function handleSelectionChange(rows: Asset[]) {
  selectedAssets.value = rows
  if (!selectedOrganizationID.value) batchNetworkAreaValue.value = ''
}

async function runBatchUpdate(payload: { status?: string; network_area_id?: string }, action: string) {
  if (!selectedAssets.value.length) return ElMessage.warning('请先选择网络资产')
  if (selectedAssets.value.length > 200) return ElMessage.warning('单次最多批量处理 200 个网络资产')
  try {
    await ElMessageBox.confirm(`确认对选中的 ${selectedAssets.value.length} 个网络资产执行“${action}”吗？系统会逐项校验组织权限并返回处理结果。`, `批量${action}确认`, {
      type: 'warning', confirmButtonText: '确认执行', cancelButtonText: '取消',
    })
    batchLoading.value = true
    const targets = [...selectedAssets.value]
    const response = await apiClient.patch<{ result: BatchOperationResult }>('/assets/batch', {
      ids: targets.map((item) => item.id), ...payload,
    })
    const names = new Map(targets.map((item) => [item.id, item.name || item.value]))
    batchResult.value = {
      ...response.data.result,
      items: response.data.result.items.map((item) => ({ ...item, resource_name: item.item?.name || item.item?.value || names.get(item.id) || item.id })),
    }
    batchResultTitle.value = `批量${action}结果`
    batchResultVisible.value = true
    if (response.data.result.failure_count === 0) ElMessage.success(`已成功更新 ${response.data.result.success_count} 个网络资产`)
    await refreshAfterMutation()
    selectedAssets.value = []
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, `批量${action}失败`))
  } finally {
    batchLoading.value = false
    batchStatusValue.value = ''
    batchNetworkAreaValue.value = ''
  }
}

function applyBatchStatus(value: string) {
  if (!value) return
  void runBatchUpdate({ status: value }, `设置状态为“${statusLabel(value)}”`)
}

function applyBatchNetworkArea(value: string) {
  if (!value) return
  const networkAreaID = value === '__clear__' ? '' : value
  const area = networkAreas.value.find((item) => item.id === networkAreaID)
  void runBatchUpdate({ network_area_id: networkAreaID }, value === '__clear__' ? '清除网络区域' : `设置网络区域为“${area?.name || '所选区域'}”`)
}

async function toggleArchive(item: Asset) {
  const archived = item.status === 'archived'
  const action = archived ? '启用' : '归档'
  try {
    await ElMessageBox.confirm(`确定${action}资产“${item.name || item.value}”吗？`, `${action}网络资产`, {
      confirmButtonText: action, cancelButtonText: '取消', type: archived ? 'info' : 'warning',
    })
    await apiClient.patch(`/assets/${item.id}`, assetPayload(item, { status: archived ? 'active' : 'archived' }))
    ElMessage.success(`网络资产已${action}`)
    await refreshAfterMutation()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, `${action}网络资产失败`))
  }
}
async function removeAsset(item: Asset) {
  try {
    await ElMessageBox.confirm(`删除后资产将不再出现在列表中，确定删除“${item.name || item.value}”吗？`, '删除网络资产', {
      confirmButtonText: '删除', cancelButtonText: '取消', type: 'error',
    })
    await apiClient.delete(`/assets/${item.id}`)
    ElMessage.success('网络资产已删除')
    await refreshAfterMutation()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, '删除网络资产失败'))
  }
}
function resetImportPreview() {
  importResult.value = undefined
  importResultMode.value = undefined
}
function openImportDialog() {
  importForm.organization_id = defaultOrganizationID()
  importFile.value = undefined
  resetImportPreview()
  importDialogVisible.value = true
}
function handleFileChange(uploadFile: UploadFile, _uploadFiles: UploadFiles) {
  importFile.value = uploadFile.raw
  resetImportPreview()
}
function handleFileRemove() {
  importFile.value = undefined
  resetImportPreview()
}
async function runImport(preview = false) {
  if (!importForm.organization_id) return ElMessage.warning('请选择所属组织')
  if (!importFile.value) return ElMessage.warning('请选择 CSV 或 XLSX 文件')
  if (!preview && (importResultMode.value !== 'preview' || !importResult.value?.success_count)) {
    return ElMessage.warning('请先完成预检，并确认至少有一行可以导入')
  }
  importing.value = true
  try {
    const data = new FormData()
    data.append('organization_id', importForm.organization_id)
    data.append('file', importFile.value)
    const endpoint = preview ? '/assets/import/preview' : '/assets/import'
    const response = await apiClient.post<{ result: ImportResult }>(endpoint, data, {
      headers: { 'Content-Type': 'multipart/form-data' }, timeout: 60_000,
    })
    importResult.value = response.data.result
    importResultMode.value = preview ? 'preview' : 'completed'
    if (preview) {
      ElMessage.success('文件预检完成，尚未写入台账')
    } else {
      ElMessage.success('导入处理完成')
      await refreshAfterMutation()
    }
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, preview ? '导入文件预检失败' : '批量导入失败'))
  } finally {
    importing.value = false
  }
}
async function loadChangeHistory() {
  if (!changeHistoryResourceID.value) return
  changeHistoryLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<ChangeHistoryItem>>(`/assets/${changeHistoryResourceID.value}/change-history`, {
      params: { page: changeHistoryPage.value, page_size: changeHistoryPageSize.value },
    })
    changeHistoryItems.value = response.data.items
    changeHistoryTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取网络资产变更历史失败'))
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
    const response = await apiClient.get<ListResponse<ImportHistory>>('/assets/imports', { params: { page: importPage.value, page_size: importPageSize.value } })
    importHistory.value = response.data.items
    importTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取导入记录失败'))
  } finally {
    historyLoading.value = false
  }
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
function openAreaManagement() {
  if (!auth.isGroupAdmin) return
  areaManagementVisible.value = true
}
function resetNetworkAreaForm() {
  Object.assign(networkAreaForm, { organization_id: enabledOrganizations.value[0]?.id ?? '', name: '', code: '', status: 'enabled', sort_order: 0, notes: '' })
}
function openCreateNetworkArea() {
  editingNetworkAreaID.value = ''
  resetNetworkAreaForm()
  areaFormVisible.value = true
}
function openEditNetworkArea(item: NetworkArea) {
  editingNetworkAreaID.value = item.id
  Object.assign(networkAreaForm, { organization_id: item.organization_id, name: item.name, code: item.code ?? '', status: item.status, sort_order: item.sort_order, notes: item.notes ?? '' })
  areaFormVisible.value = true
}
async function submitNetworkArea() {
  if (!networkAreaForm.organization_id || !networkAreaForm.name.trim()) return ElMessage.warning('请选择所属组织并填写网络区域名称')
  areaSubmitting.value = true
  try {
    if (editingNetworkAreaID.value) {
      await apiClient.patch(`/network-areas/${editingNetworkAreaID.value}`, networkAreaForm)
      ElMessage.success('网络区域已更新')
    } else {
      await apiClient.post('/network-areas', networkAreaForm)
      ElMessage.success('网络区域已新增')
    }
    areaFormVisible.value = false
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, editingNetworkAreaID.value ? '编辑网络区域失败' : '新增网络区域失败'))
  } finally {
    areaSubmitting.value = false
  }
}

async function exportLedger(format: 'csv' | 'xlsx') {
  exporting.value = true
  try {
    const response = await apiClient.get(`/assets/export?format=${format}`, {
      params: {
        q: search.value.trim() || undefined,
        type: typeFilter.value || undefined,
        status: statusFilter.value || undefined,
        organization_id: organizationFilter.value || undefined,
      },
      responseType: 'blob',
      timeout: 60_000,
    })
    const href = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = href
    link.download = `网络资产台账.${format}`
    link.click()
    URL.revokeObjectURL(href)
    const count = response.headers['x-export-count'] ?? total.value
    ElMessage.success(`已生成并开始下载 ${count} 条网络资产`)
  } catch (error) {
    const detail = await getDownloadAPIError(error, '导出网络资产台账失败')
    if (format === 'xlsx' && detail.code === 'synchronous_xlsx_limit_exceeded') {
      try {
        await queueXLSXExport()
      } catch (queueError) {
        ElMessage.error(getAPIErrorMessage(queueError, '创建后台导出任务失败'))
      }
    } else {
      ElMessage.error(detail.message)
    }
  } finally {
    exporting.value = false
  }
}

async function queueXLSXExport() {
  await createExportJob('asset', {
    q: search.value.trim() || undefined,
    type: typeFilter.value || undefined,
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
    const response = await apiClient.get(`/assets/import-template?format=${format}&organization_id=${encodeURIComponent(importForm.organization_id)}`, { responseType: 'blob' })
    const href = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = href
    link.download = `网络资产导入模板.${format}`
    link.click()
    URL.revokeObjectURL(href)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '下载模板失败'))
  } finally {
    downloading.value = false
  }
}
function typeLabel(value: string) { return typeLabels[value] ?? value }
function environmentLabel(value: string) { return environmentLabels[value] ?? value }
function scopeLabel(value: string) { return scopeLabels[value] ?? value }
function importanceLabel(value: string) { return importanceLabels[value] ?? value }
function sourceLabel(value: string) { return sourceLabels[value] ?? value }
function statusLabel(value: string) { return statusLabels[value] ?? value }
function statusTag(value: string) { return value === 'active' ? 'success' : value === 'archived' ? 'info' : value === 'disabled' ? 'danger' : 'warning' }
function importanceTag(value: string) { return value === 'core' ? 'danger' : value === 'important' ? 'warning' : 'info' }
function importStatusTag(value: string) { return value === 'completed' ? 'success' : value === 'partial' ? 'warning' : 'danger' }
function formatDate(value: string) { return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'short', timeStyle: 'short' }).format(new Date(value)) }
function expiryLabel(value?: string) {
  if (!value) return '长期有效'
  const expiresAt = new Date(value).getTime()
  const remainingDays = Math.ceil((expiresAt - Date.now()) / 86400000)
  if (remainingDays < 0) return `已到期 · ${formatDate(value)}`
  if (remainingDays <= 30) return `即将到期 · ${formatDate(value)}`
  return formatDate(value)
}
function expiryTag(value?: string) {
  if (!value) return 'info'
  const remaining = new Date(value).getTime() - Date.now()
  return remaining < 0 ? 'danger' : remaining <= 30 * 86400000 ? 'warning' : 'success'
}

onMounted(() => {
  if (inventoryMode.value) organizationFilter.value = inventoryOrganizationID.value
  void load(!inventoryMode.value)
})
</script>

<template>
  <section>
    <el-alert
      v-if="inventoryMode"
      :title="`当前为“${inventoryTaskName}”填报草稿模式`"
      :description="`新增资产仅保存在 ${inventoryOrganizationName} 的任务草稿中；提交后集团可审核，审核通过后才会发布到正式网络资产台账。`"
      type="warning"
      :closable="false"
      show-icon
      class="inventory-draft-alert"
    >
      <template #default><el-button size="small" @click="router.push('/inventory-tasks')">返回填报任务</el-button></template>
    </el-alert>
    <div class="page-heading">
      <div>
        <span class="eyebrow">NETWORK ASSETS</span>
        <h1>网络资产</h1>
        <p>集团统一维护域名、IP、URL 和网络服务，子公司仅管理授权组织范围内的数据。</p>
      </div>
      <div class="button-row ledger-page-actions asset-page-actions">
        <el-dropdown v-if="!inventoryMode">
          <el-button :loading="exporting">导出台账</el-button>
          <template #dropdown><el-dropdown-menu><el-dropdown-item @click="exportLedger('xlsx')">导出 XLSX</el-dropdown-item><el-dropdown-item @click="exportLedger('csv')">导出 CSV</el-dropdown-item><el-dropdown-item divided @click="queueXLSXExport">后台生成 XLSX</el-dropdown-item></el-dropdown-menu></template>
        </el-dropdown>
        <el-button v-if="auth.isGroupAdmin && !inventoryMode" @click="openAreaManagement">网络区域配置</el-button>
        <el-button v-if="!inventoryMode" @click="openImportHistory">导入记录</el-button>
        <el-button v-if="!inventoryMode" @click="openImportDialog">批量导入</el-button>
        <el-button type="primary" @click="openCreateDialog">新增资产</el-button>
      </div>
    </div>

    <div class="organization-summary">
      <div><span>符合条件资产</span><strong>{{ total }}</strong></div>
      <div><span>本页启用资产</span><strong>{{ activeCount }}</strong></div>
      <div><span>本页公网资产</span><strong>{{ publicCount }}</strong></div>
      <div><span>本页域名资产</span><strong>{{ domainCount }}</strong></div>
    </div>

    <div class="panel asset-filter-panel network-asset-filter-panel">
      <div class="network-asset-filter-controls" :class="{ 'with-organization-filter': auth.isGroupAdmin }">
        <el-input v-model="search" clearable placeholder="搜索资产值、名称、系统或负责人" @keyup.enter="resetAndLoadAssets" @clear="resetAndLoadAssets" />
        <el-select v-model="typeFilter" clearable placeholder="全部类型" @change="resetAndLoadAssets" @clear="resetAndLoadAssets">
          <el-option v-for="(label, value) in typeLabels" :key="value" :label="label" :value="value" />
        </el-select>
        <el-select v-model="statusFilter" clearable placeholder="全部状态" @change="resetAndLoadAssets" @clear="resetAndLoadAssets">
          <el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" />
        </el-select>
        <el-select v-if="auth.isGroupAdmin" v-model="organizationFilter" clearable filterable placeholder="全部组织" @change="resetAndLoadAssets" @clear="resetAndLoadAssets">
          <el-option v-for="item in enabledOrganizations" :key="item.id" :label="item.name" :value="item.id" />
        </el-select>
        <el-button @click="resetAndLoadAssets">查询</el-button>
        <span class="asset-filter-count">共 {{ total }} 条</span>
      </div>
      <SavedFilterViews v-if="!inventoryMode" ref="savedFilterViews" page-key="assets" :current-filters="currentAssetFilters" @apply="applySavedAssetFilters" />
    </div>

    <div v-if="inventoryMode" class="panel data-table-panel inventory-draft-panel" v-loading="loading">
      <div class="table-panel-heading">
        <div><strong>本次任务新增草稿</strong><span>共 {{ draftTotal }} 项；未提交时集团在正式台账中不可见</span></div>
        <el-tag type="warning" effect="plain">任务草稿</el-tag>
      </div>
      <el-table :data="draftAssets" row-key="id" empty-text="当前任务尚未新增资产草稿">
        <el-table-column label="资产名称" min-width="180"><template #default="scope"><el-button link type="primary" class="table-link" @click="openEditDialog(scope.row)">{{ scope.row.name || '未命名资产' }}</el-button></template></el-table-column>
        <el-table-column label="资产值" min-width="230"><template #default="scope"><span class="mono-text">{{ scope.row.value }}</span></template></el-table-column>
        <el-table-column label="类型" width="95"><template #default="scope">{{ typeLabel(scope.row.type) }}</template></el-table-column>
        <el-table-column prop="business_system_name" label="业务系统" min-width="150"><template #default="scope">{{ scope.row.business_system_name || '未绑定' }}</template></el-table-column>
        <el-table-column label="暴露范围" width="105"><template #default="scope">{{ scopeLabel(scope.row.network_scope) }}</template></el-table-column>
        <el-table-column label="草稿状态" width="105"><template #default="scope"><el-tag type="warning" effect="plain">{{ draftStatusLabels[scope.row.draft_status || 'editing'] || scope.row.draft_status }}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="140" fixed="right"><template #default="scope"><el-button link type="primary" :disabled="scope.row.draft_status === 'submitted'" @click="openEditDialog(scope.row)">编辑</el-button><el-button link type="danger" :disabled="scope.row.draft_status === 'submitted'" @click="removeDraft(scope.row)">删除</el-button></template></el-table-column>
      </el-table>
      <div v-if="draftTotal > 0" class="table-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="draftTotal" :current-page="draftPage" :page-size="draftPageSize" :page-sizes="[20, 50, 100]" @current-change="handleDraftPageChange" @size-change="handleDraftPageSizeChange" />
      </div>
    </div>

    <div class="panel data-table-panel" v-loading="loading">
      <div v-if="!inventoryMode" class="batch-toolbar">
        <div><strong>批量操作</strong><span>已选择 {{ selectedAssets.length }} 个资产；设置网络区域时需选择同一组织的资产</span></div>
        <div class="batch-toolbar-actions">
          <el-select v-model="batchStatusValue" aria-label="批量设置状态" :disabled="selectedAssets.length === 0 || batchLoading" placeholder="批量设置状态" style="width: 150px" @change="applyBatchStatus">
            <el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" />
          </el-select>
          <el-select v-model="batchNetworkAreaValue" aria-label="批量设置网络区域" :disabled="selectedAssets.length === 0 || !selectedOrganizationID || batchLoading" placeholder="批量设置网络区域" style="width: 210px" @change="applyBatchNetworkArea">
            <el-option label="清除网络区域" value="__clear__" />
            <el-option v-for="item in selectedNetworkAreas" :key="item.id" :label="item.code ? `${item.name}（${item.code}）` : item.name" :value="item.id" />
          </el-select>
        </div>
      </div>
      <el-table :data="assets" row-key="id" empty-text="暂无网络资产" @selection-change="handleSelectionChange">
        <el-table-column v-if="!inventoryMode" type="selection" width="46" />
        <el-table-column label="资产名称" min-width="180">
          <template #default="scope"><el-button link type="primary" class="table-link" @click="openDetail(scope.row)">{{ scope.row.name || '未命名资产' }}</el-button></template>
        </el-table-column>
        <el-table-column label="资产值" min-width="230">
          <template #default="scope"><span class="mono-text">{{ scope.row.value }}</span></template>
        </el-table-column>
        <el-table-column label="类型" width="95"><template #default="scope">{{ typeLabel(scope.row.type) }}</template></el-table-column>
        <el-table-column prop="organization_name" label="所属组织" min-width="150" />
        <el-table-column label="业务系统" min-width="150"><template #default="scope"><span v-if="scope.row.business_system_id">{{ scope.row.business_system_name }}</span><span v-else class="muted-text">未绑定</span></template></el-table-column>
        <el-table-column label="责任人账号" min-width="150"><template #default="scope"><div v-if="scope.row.owner_user_id" class="organization-name-cell"><strong>{{ scope.row.owner_user_display_name || scope.row.owner_username }}</strong><span>{{ scope.row.owner_username }}</span></div><span v-else class="muted-text">未绑定</span></template></el-table-column>
        <el-table-column label="环境 / 暴露范围" width="145"><template #default="scope">{{ environmentLabel(scope.row.environment) }} / {{ scopeLabel(scope.row.network_scope) }}</template></el-table-column>
        <el-table-column label="网络区域" min-width="135"><template #default="scope"><span>{{ scope.row.network_area_name || '未指定' }}</span></template></el-table-column>
        <el-table-column label="有效期" min-width="185"><template #default="scope"><el-tag :type="expiryTag(scope.row.expires_at)" effect="plain">{{ expiryLabel(scope.row.expires_at) }}</el-tag></template></el-table-column>
        <el-table-column label="状态" width="90"><template #default="scope"><el-tag :type="statusTag(scope.row.status)" effect="plain">{{ statusLabel(scope.row.status) }}</el-tag></template></el-table-column>
        <el-table-column label="重要程度" width="95"><template #default="scope"><el-tag :type="importanceTag(scope.row.importance)" effect="plain">{{ importanceLabel(scope.row.importance) }}</el-tag></template></el-table-column>
        <el-table-column label="来源" width="75"><template #default="scope">{{ sourceLabel(scope.row.source) }}</template></el-table-column>
        <el-table-column label="操作" width="235" fixed="right">
          <template #default="scope">
            <el-button link type="primary" @click="openChangeHistory(scope.row)">历史</el-button>
            <template v-if="!inventoryMode">
              <el-button link type="primary" @click="openEditDialog(scope.row)">编辑</el-button>
              <el-button link :type="scope.row.status === 'archived' ? 'success' : 'warning'" @click="toggleArchive(scope.row)">{{ scope.row.status === 'archived' ? '启用' : '归档' }}</el-button>
              <el-button link type="danger" @click="removeAsset(scope.row)">删除</el-button>
            </template>
            <span v-else class="muted-text">正式台账只读</span>
          </template>
        </el-table-column>
      </el-table>
      <div class="table-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="total" :current-page="page" :page-size="pageSize" :page-sizes="[20, 50, 100]" @current-change="handlePageChange" @size-change="handlePageSizeChange" />
      </div>
    </div>

    <el-dialog v-model="batchResultVisible" :title="batchResultTitle" width="720px">
      <template v-if="batchResult">
        <el-alert :title="`共处理 ${batchResult.total_count} 个，成功 ${batchResult.success_count} 个，失败 ${batchResult.failure_count} 个`" :type="batchResult.failure_count ? 'warning' : 'success'" :closable="false" show-icon />
        <el-table :data="batchResult.items" max-height="420" class="batch-result-table">
          <el-table-column prop="resource_name" label="网络资产" min-width="240" />
          <el-table-column label="结果" width="90"><template #default="scope"><el-tag :type="scope.row.success ? 'success' : 'danger'">{{ scope.row.success ? '成功' : '失败' }}</el-tag></template></el-table-column>
          <el-table-column prop="message" label="说明" min-width="230" />
        </el-table>
      </template>
      <template #footer><el-button type="primary" @click="batchResultVisible = false">关闭</el-button></template>
    </el-dialog>

    <el-drawer v-model="detailVisible" title="网络资产详情" size="620px">
      <div v-loading="detailLoading" class="detail-drawer">
        <template v-if="detail">
          <div class="detail-hero"><div><span class="eyebrow">NETWORK ASSET</span><h2>{{ detail.name || detail.value }}</h2><p>{{ typeLabel(detail.type) }} · {{ detail.value }}</p></div><el-tag :type="statusTag(detail.status)" effect="plain">{{ statusLabel(detail.status) }}</el-tag></div>
          <div class="detail-grid"><div><span>所属组织</span><strong>{{ detail.organization_name }}</strong></div><div><span>关联业务系统</span><strong>{{ detail.business_system_name || '未绑定' }}</strong></div><div><span>责任人账号</span><strong>{{ assetOwnerLabel(detail) }}</strong></div><div><span>负责人补充说明</span><strong>{{ detail.owner_name || '未填写' }}</strong></div><div><span>环境</span><strong>{{ environmentLabel(detail.environment) }}</strong></div><div><span>网络暴露范围</span><strong>{{ scopeLabel(detail.network_scope) }}</strong></div><div><span>网络区域</span><strong>{{ detail.network_area_name || '未指定' }}</strong></div><div><span>重要程度</span><strong>{{ importanceLabel(detail.importance) }}</strong></div><div><span>登记来源</span><strong>{{ sourceLabel(detail.source) }}</strong></div><div><span>有效期至</span><strong><el-tag :type="expiryTag(detail.expires_at)" effect="plain">{{ expiryLabel(detail.expires_at) }}</el-tag></strong></div><div><span>最近更新</span><strong>{{ formatDate(detail.updated_at) }}</strong></div></div>
          <template v-if="detailCustomFields.length">
            <div class="form-section-title">集团自定义字段</div>
            <div class="detail-grid"><div v-for="field in detailCustomFields" :key="field.id"><span>{{ field.field_name }}</span><strong>{{ customValueLabel(detailCustomValues[field.field_key]) }}</strong></div></div>
          </template>
          <div class="form-section-title">备注</div><p class="detail-description">{{ detail.notes || '暂无备注' }}</p>
          <div class="detail-actions"><el-button @click="openChangeHistory(detail)">变更历史</el-button><el-button @click="openEditDialog(detail)">编辑资产</el-button><el-button :type="detail.status === 'archived' ? 'success' : 'warning'" @click="toggleArchive(detail)">{{ detail.status === 'archived' ? '启用资产' : '归档资产' }}</el-button></div>
        </template>
      </div>
    </el-drawer>

    <el-dialog v-model="editDialogVisible" :title="dialogTitle" width="720px" destroy-on-close>
      <el-alert v-if="inventoryMode" title="此处保存的是任务草稿，不会立即进入集团正式台账。任务审核通过后系统会自动发布。" type="info" :closable="false" show-icon class="dialog-context-alert" /><el-form ref="formRef" :model="form" :rules="rules" label-position="top">
        <div class="form-grid two-columns">
          <el-form-item label="资产类型" prop="type"><el-select v-model="form.type" style="width: 100%"><el-option v-for="(label, value) in typeLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="资产值" prop="value"><el-input v-model="form.value" :placeholder="valuePlaceholder" /></el-form-item>
          <el-form-item label="资产名称"><el-input v-model="form.name" placeholder="便于识别的名称（可选）" /></el-form-item>
          <el-form-item label="所属组织" prop="organization_id"><el-select v-model="form.organization_id" filterable :disabled="!auth.isGroupAdmin || inventoryMode" style="width: 100%" @change="handleFormOrganizationChange"><el-option v-for="item in enabledOrganizations" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="关联业务系统"><el-select v-model="form.business_system_id" clearable filterable placeholder="可选" style="width: 100%"><el-option v-for="item in availableSystems" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="责任人账号" class="full-span">
            <el-select v-model="form.owner_user_id" clearable filterable remote reserve-keyword :remote-method="loadOwnerOptions" :loading="ownerOptionsLoading" placeholder="按姓名、账号或联系方式搜索所属组织用户" style="width: 100%">
              <el-option v-for="item in ownerOptions" :key="item.id" :label="ownerOptionLabel(item)" :value="item.id"><div class="owner-option"><strong>{{ ownerOptionLabel(item) }}</strong><span>{{ item.email || item.mobile_phone || '已启用账号' }}</span></div></el-option>
            </el-select>
            <div class="form-tip">绑定账号后，资产到期、数据质量问题和关键台账变更将优先通知该用户。</div>
          </el-form-item>
          <el-form-item label="负责人补充说明"><el-input v-model="form.owner_name" placeholder="外部联系人、团队或职责说明（可选）" /></el-form-item>
          <el-form-item label="环境"><el-select v-model="form.environment" style="width: 100%"><el-option v-for="(label, value) in environmentLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="网络暴露范围"><el-select v-model="form.network_scope" :disabled="inventoryMode && inventoryNetworkScope !== 'all'" style="width: 100%"><el-option v-for="(label, value) in scopeLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="网络区域"><el-select v-model="form.network_area_id" clearable filterable placeholder="如：上海办事处（可选）" style="width: 100%"><el-option v-for="item in availableNetworkAreas" :key="item.id" :label="item.code ? `${item.name}（${item.code}）` : item.name" :value="item.id" :disabled="item.status !== 'enabled'" /></el-select></el-form-item>
          <el-form-item label="重要程度"><el-select v-model="form.importance" style="width: 100%"><el-option v-for="(label, value) in importanceLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="资产状态"><el-select v-model="form.status" style="width: 100%"><el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="有效期至" class="full-span"><el-date-picker v-model="form.expires_at" type="datetime" placeholder="留空表示长期有效" style="width: 100%" /><div class="form-tip">适用于域名、临时 URL、公网 IP 租约等资产生命周期；该字段不是“最后更新时间”。</div></el-form-item>
          <el-form-item label="备注" class="full-span"><el-input v-model="form.notes" type="textarea" :rows="3" placeholder="补充资产用途、管理要求等信息" /></el-form-item><template v-if="customFields.length && !inventoryMode"><el-divider class="full-span" content-position="left">集团自定义字段</el-divider><el-form-item v-for="field in customFields" :key="field.id" :label="field.field_name" :required="field.is_required"><el-input v-if="field.field_type === 'text'" v-model="customValues[field.field_key]"/><el-input v-else-if="field.field_type === 'textarea'" v-model="customValues[field.field_key]" type="textarea" :rows="2"/><el-input-number v-else-if="field.field_type === 'number'" v-model="customValues[field.field_key]"/><el-date-picker v-else-if="field.field_type === 'date'" v-model="customValues[field.field_key]" type="date" value-format="YYYY-MM-DD"/><el-switch v-else-if="field.field_type === 'boolean'" v-model="customValues[field.field_key]"/><el-select v-else-if="field.field_type === 'multiselect'" v-model="customValues[field.field_key]" multiple><el-option v-for="option in field.options" :key="option" :label="option" :value="option"/></el-select><el-select v-else v-model="customValues[field.field_key]"><el-option v-for="option in field.options" :key="option" :label="option" :value="option"/></el-select><div v-if="field.description" class="form-tip">{{ field.description }}</div></el-form-item></template>
        </div>
      </el-form>
      <template #footer><el-button @click="editDialogVisible = false">取消</el-button><el-button type="primary" :loading="submitting" @click="submit">{{ submitLabel }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="areaManagementVisible" title="网络区域配置" width="980px">
      <el-alert title="“网络暴露范围”固定表示公网、内网和未知；此处配置的是资产所属的办公地点、园区、云区域或网络分区。子公司用户可选择，集团管理员统一维护。" type="info" :closable="false" show-icon />
      <div class="dialog-toolbar"><el-button type="primary" @click="openCreateNetworkArea">新增网络区域</el-button></div>
      <el-table :data="networkAreas" empty-text="暂无网络区域，请先按组织新增">
        <el-table-column prop="organization_name" label="所属组织" min-width="180" />
        <el-table-column prop="name" label="网络区域" min-width="170" />
        <el-table-column prop="code" label="标识 / 地址范围" min-width="150"><template #default="scope">{{ scope.row.code || '—' }}</template></el-table-column>
        <el-table-column prop="asset_count" label="关联资产" width="90" align="center" />
        <el-table-column label="状态" width="90"><template #default="scope"><el-tag :type="scope.row.status === 'enabled' ? 'success' : 'info'">{{ scope.row.status === 'enabled' ? '启用' : '停用' }}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="85" fixed="right"><template #default="scope"><el-button link type="primary" @click="openEditNetworkArea(scope.row)">编辑</el-button></template></el-table-column>
      </el-table>
    </el-dialog>

    <el-dialog v-model="areaFormVisible" :title="editingNetworkAreaID ? '编辑网络区域' : '新增网络区域'" width="620px" destroy-on-close>
      <el-form label-position="top"><div class="form-grid two-columns">
        <el-form-item label="所属组织" required><el-select v-model="networkAreaForm.organization_id" :disabled="Boolean(editingNetworkAreaID)" filterable style="width:100%"><el-option v-for="item in enabledOrganizations" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item>
        <el-form-item label="网络区域名称" required><el-input v-model="networkAreaForm.name" maxlength="128" placeholder="如：上海办事处、生产云 VPC" /></el-form-item>
        <el-form-item label="标识 / 地址范围"><el-input v-model="networkAreaForm.code" maxlength="64" placeholder="如：SH-OFFICE、10.20.0.0/16" /></el-form-item>
        <el-form-item label="状态"><el-select v-model="networkAreaForm.status" style="width:100%"><el-option label="启用" value="enabled" /><el-option label="停用" value="disabled" /></el-select></el-form-item>
        <el-form-item label="排序" class="full-span"><el-input-number v-model="networkAreaForm.sort_order" :min="0" :max="100000" /></el-form-item>
        <el-form-item label="说明" class="full-span"><el-input v-model="networkAreaForm.notes" type="textarea" :rows="3" maxlength="5000" placeholder="可说明地点、VPC、网段或管理要求" /></el-form-item>
      </div></el-form>
      <template #footer><el-button @click="areaFormVisible = false">取消</el-button><el-button type="primary" :loading="areaSubmitting" @click="submitNetworkArea">保存</el-button></template>
    </el-dialog>

    <el-dialog v-model="importDialogVisible" title="批量导入网络资产" width="760px" destroy-on-close>
      <el-alert title="单次最多导入 5000 行，资产类型和值在集团范围内去重；允许部分成功。请先选择所属组织再下载模板，模板会包含当前启用的集团自定义字段。网络区域按所属组织已配置的名称填写；有效期支持 YYYY-MM-DD、YYYY-MM-DD HH:mm:ss 或 RFC3339；无时区值按 UTC+8 解释，留空表示长期有效。" type="info" :closable="false" show-icon />
      <div class="import-toolbar"><el-button link type="primary" :loading="downloading" @click="downloadTemplate('xlsx')">下载 XLSX 模板</el-button><el-button link type="primary" :loading="downloading" @click="downloadTemplate('csv')">下载 CSV 模板</el-button></div>
      <el-form label-position="top">
        <el-form-item label="导入到所属组织" required><el-select v-model="importForm.organization_id" filterable :disabled="!auth.isGroupAdmin" @change="resetImportPreview" style="width: 100%"><el-option v-for="item in enabledOrganizations" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item>
        <el-form-item label="CSV / XLSX 文件" required><el-upload drag :auto-upload="false" :limit="1" accept=".csv,.xlsx" :on-change="handleFileChange" :on-remove="handleFileRemove"><div class="el-upload__text">拖入文件，或<em>点击选择</em></div><template #tip><div class="el-upload__tip">文件不超过 10 MB；业务系统使用同组织内的系统编码匹配；责任人账号按所属组织内的启用用户名匹配；有效期格式错误会在预检中逐行提示。</div></template></el-upload></el-form-item>
      </el-form>
      <div v-if="importResult" class="import-result" data-testid="import-result">
        <el-alert :title="importResultMode === 'preview' ? '预检结果：尚未写入台账，请确认后执行正式导入' : '正式导入结果'" :type="importResultMode === 'preview' ? 'info' : 'success'" :closable="false" show-icon />
        <div class="import-result-summary"><div><span>总行数</span><strong>{{ importResult.total_count }}</strong></div><div class="success"><span>{{ importResultMode === 'preview' ? '可导入' : '成功' }}</span><strong>{{ importResult.success_count }}</strong></div><div class="duplicate"><span>重复</span><strong>{{ importResult.duplicate_count }}</strong></div><div class="failure"><span>失败</span><strong>{{ importResult.failure_count }}</strong></div></div>
        <el-table v-if="importResult.errors.length" :data="importResult.errors" max-height="220" size="small"><el-table-column prop="row" label="行号" width="75" /><el-table-column prop="message" label="处理结果" /></el-table>
      </div>
      <template #footer><el-button @click="importDialogVisible = false">关闭</el-button><el-button :loading="importing" @click="runImport(true)">预检文件</el-button><el-button type="primary" :loading="importing" :disabled="importResultMode !== 'preview' || !importResult?.success_count" @click="runImport(false)">确认导入</el-button></template>
    </el-dialog>

    <el-dialog v-model="historyDialogVisible" title="资产导入记录" width="980px">
      <el-table v-loading="historyLoading" :data="importHistory" empty-text="暂无导入记录" max-height="560">
        <el-table-column label="文件" min-width="190"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.file_name }}</strong><span>{{ scope.row.file_type.toUpperCase() }}</span></div></template></el-table-column>
        <el-table-column prop="organization_name" label="所属组织" min-width="170" />
        <el-table-column label="状态" width="105"><template #default="scope"><el-tag :type="importStatusTag(scope.row.status)" effect="plain">{{ importStatusLabels[scope.row.status] ?? scope.row.status }}</el-tag></template></el-table-column>
        <el-table-column prop="total_count" label="总数" width="70" /><el-table-column prop="success_count" label="成功" width="70" /><el-table-column prop="duplicate_count" label="重复" width="70" /><el-table-column prop="failure_count" label="失败" width="70" />
        <el-table-column label="导入人 / 时间" min-width="160"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.created_by_name || '-' }}</strong><span>{{ formatDate(scope.row.created_at) }}</span></div></template></el-table-column>
      </el-table>
      <div class="table-pagination dialog-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="importTotal" :current-page="importPage" :page-size="importPageSize" :page-sizes="[20, 50, 100]" @current-change="handleImportPageChange" @size-change="handleImportPageSizeChange" />
      </div>
      <template #footer><el-button @click="historyDialogVisible = false">关闭</el-button></template>
    </el-dialog>

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
