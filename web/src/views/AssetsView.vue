<script setup lang="ts">
import RowActions from '../components/RowActions.vue'
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules, type UploadFile, type UploadFiles } from 'element-plus'
import { apiClient, getAPIErrorMessage, getDownloadAPIError } from '../api/client'
import { createExportJob } from '../api/export-jobs'
import { useAuthStore } from '../stores/auth'
import SavedFilterViews from '../components/SavedFilterViews.vue'
import AssetExternalObservations from '../components/AssetExternalObservations.vue'
import WorkingVersionDiffDrawer from '../components/WorkingVersionDiffDrawer.vue'
import ResourceSubmissionHistoryDrawer from '../components/ResourceSubmissionHistoryDrawer.vue'
import { assetDraftEndpoint, batchAssetUpdateSuccessMessage, draftLastEditedHint, draftTakeoverSummary } from '../utils/inventory-drafts'
import { reportEditConflict } from '../utils/edit-conflict'
import { formatDateTime } from '../utils/datetime'
import { assetDirectBatchEndpointQuery, needsDirectChangeReason } from '../utils/asset-direct-change'
import { createSuggestedColumnMapping, getImportColumnMappingError, importColumnLabel, isImportColumnUsed, serializeImportColumnMapping, type ImportColumn, type ImportColumnMapping, type ImportField } from '../utils/import-column-mapping'

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
  source_asset_id?: string; operation?: 'create' | 'update' | 'delete'; version?: number
  reporting_status?: 'unreported' | 'pending_review' | 'reported' | 'changed' | 'returned' | 'pending_offline'; is_working_copy?: boolean; published_id?: string
  assigned_to?: string; assigned_to_name?: string; last_edited_by?: string; last_edited_by_name?: string
  takeover_reason?: string; taken_over_at?: string; takeover_by_name?: string
  custom_values?: Array<{ field_key: string; value: unknown }>
}
interface NetworkArea {
  id: string; organization_id: string; organization_code: string; organization_name: string
  name: string; code?: string; status: 'enabled' | 'disabled'; sort_order: number; notes?: string; asset_count: number
}
interface CustomField { id: string; field_key: string; field_name: string; description?: string; field_type: string; options: string[]; is_required: boolean }
interface ImportError { row: number; code: string; message: string }
interface ImportResult {
  id: string; file_name: string; file_type: string; import_mode: 'skip_duplicates' | 'update_working'; status: string; total_count: number
  success_count: number; created_count: number; updated_count: number; failure_count: number; duplicate_count: number; errors: ImportError[]
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
interface DirectChangeRecheckItem {
  id: string; asset_id: string; asset_type: string; asset_value: string; asset_name?: string
  organization_id: string; organization_name: string
  action: 'create' | 'update' | 'delete'; reason: string
  snapshot?: { before?: Partial<Asset>; after?: Partial<Asset> }
  changed_fields?: Array<{ field: string; before?: unknown; after?: unknown }>
  status: 'open' | 'confirmed' | 'disputed'
  created_by: string; created_by_name?: string; created_at: string
  confirmed_by?: string; confirmed_by_name?: string; confirmed_at?: string; confirmation_note?: string
  request_id?: string; request_ip?: string
}
interface BatchOperationItem { id: string; success: boolean; code?: string; message: string; item?: Asset; resource_name?: string }
interface BatchOperationResult { total_count: number; success_count: number; failure_count: number; items: BatchOperationItem[] }
interface DetailResponse { item: Asset }

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()
const extendedColumns = ref(false)
const loading = ref(false)
const submitting = ref(false)
const importing = ref(false)
const downloading = ref(false)
const exporting = ref(false)
const selectedExporting = ref(false)
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
const workingVersionDiffRef = ref<InstanceType<typeof WorkingVersionDiffDrawer>>()
const resourceSubmissionHistoryRef = ref<InstanceType<typeof ResourceSubmissionHistoryDrawer>>()
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
// Captured when the edit dialog opens so the backend can reject a save that
// overwrites a change made by someone else in the meantime.
const editingExpectedUpdatedAt = ref<string>('')
const editingExpectedVersion = ref(0)
const formRef = ref<FormInstance>()
const assets = ref<Asset[]>([])
const draftAssets = ref<Asset[]>([])
const draftTotal = ref(0)
const draftPage = ref(1)
const draftPageSize = ref(20)
const editingIsDraft = ref(false)
const editingDraftTaskID = ref('')
const editingDraftOrganizationID = ref('')
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
const importColumns = ref<ImportColumn[]>([])
const importFields = ref<ImportField[]>([])
const importColumnMapping = reactive<ImportColumnMapping>({})
const importColumnsLoading = ref(false)
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
const importForm = reactive<{ organization_id: string; import_mode: 'skip_duplicates' | 'update_working'; direct_change_reason: string }>({ organization_id: '', import_mode: 'skip_duplicates', direct_change_reason: '' })

// 集团管理员直写正式台账会绕过填报复核流程，后端强制要求变更原因。这里集中判定
// 「本次操作是否直接写正式台账」，只有真正直写时才向用户索要原因，避免把填报
// 路径也塞进不必要的输入框。
const directLedgerWrite = computed(() => needsDirectChangeReason({ isGroupAdmin: auth.isGroupAdmin, inventoryMode: inventoryMode.value }))
const directChangeReasonRequired = ref(false)
const directChangeReason = ref('')
function resetDirectChangeReason() {
  directChangeReasonRequired.value = false
  directChangeReason.value = ''
}
// 用户取消原因输入时不区分 cancel/close：返回空串表示「未提供原因」，调用方
// 静默退出，避免把「取消」当成错误弹出来。
async function requestDirectChangeReason(title: string, message: string): Promise<string> {
  const { value } = await ElMessageBox.prompt(message, title, {
    confirmButtonText: '确认提交', cancelButtonText: '取消',
    inputType: 'textarea', inputPlaceholder: '例如：集团资产清查中确认的存量服务器',
    inputValidator: (input) => Boolean(input?.trim()) || '请填写变更原因，该原因会同步给资产所属组织复核',
  })
  return value.trim()
}

const recheckVisible = ref(false)
const recheckLoading = ref(false)
const recheckItems = ref<DirectChangeRecheckItem[]>([])
const recheckTotal = ref(0)
const recheckPage = ref(1)
const recheckPageSize = ref(20)
const recheckStatusFilter = ref('')
const recheckDetail = ref<DirectChangeRecheckItem>()
const recheckDetailVisible = ref(false)
const recheckSubmitting = ref(false)
const recheckActionLabels: Record<string, string> = { create: '集团直接新增', update: '集团直接修改', delete: '集团直接删除' }
const recheckStatusLabels: Record<string, string> = { open: '待复核', confirmed: '已认可', disputed: '有异议' }
async function openRecheckDrawer() {
  recheckVisible.value = true
  recheckPage.value = 1
  await loadRechecks()
}
async function loadRechecks() {
  recheckLoading.value = true
  try {
    const { data } = await apiClient.get<ListResponse<DirectChangeRecheckItem>>('/assets/direct-change-rechecks', {
      params: { page: recheckPage.value, page_size: recheckPageSize.value, status: recheckStatusFilter.value || undefined },
    })
    recheckItems.value = data.items
    recheckTotal.value = data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取集团直改复核列表失败'))
  } finally {
    recheckLoading.value = false
  }
}
function openRecheckDetail(item: DirectChangeRecheckItem) {
  recheckDetail.value = item
  recheckDetailVisible.value = true
}
async function decideRecheck(item: DirectChangeRecheckItem, status: 'confirmed' | 'disputed') {
  const prompt = status === 'confirmed' ? '认可后该变更视为归属确认无误，请填写确认说明' : '提出异议不会自动回滚已写入的资产，但会记录在台账审计中，请说明异议内容'
  try {
    const { value } = await ElMessageBox.prompt(prompt, status === 'confirmed' ? '认可集团直改' : '对直改提出异议', {
      confirmButtonText: '确认', cancelButtonText: '取消',
      inputValidator: (input) => Boolean(input?.trim()) || '请填写说明',
    })
    recheckSubmitting.value = true
    await apiClient.post(`/assets/direct-change-rechecks/${item.id}/confirm`, { status, note: value.trim() })
    ElMessage.success(status === 'confirmed' ? '已认可该变更' : '已提交异议')
    recheckDetailVisible.value = false
    await loadRechecks()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, '提交复核结论失败'))
  } finally {
    recheckSubmitting.value = false
  }
}
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
const draftOperationLabels: Record<string, string> = { create: '新增', update: '修改', delete: '删除' }

const enabledOrganizations = computed(() => organizations.value.filter((item) => item.status === 'enabled'))
const availableSystems = computed(() => systems.value.filter((item) => item.organization_id === form.organization_id))
const availableNetworkAreas = computed(() => networkAreas.value.filter((item) => item.organization_id === form.organization_id))
const dialogTitle = computed(() => editingAssetID.value ? '编辑网络资产' : '新增网络资产')
const submitLabel = computed(() => editingAssetID.value ? '保存修改' : '确认新增')
const importMappingError = computed(() => getImportColumnMappingError(importFields.value, importColumnMapping))
const selectedOrganizationID = computed(() => {
  const ids = new Set(selectedAssets.value.map((item) => item.organization_id))
  return ids.size === 1 ? [...ids][0] : ''
})
const selectedNetworkAreas = computed(() => networkAreas.value.filter((item) => item.organization_id === selectedOrganizationID.value && item.status === 'enabled'))
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
const reportingStatusLabels: Record<string, string> = { unreported: '未上报', pending_review: '待审核', reported: '已上报', changed: '有变更待上报', returned: '已退回', pending_offline: '待下线' }
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

// A saved view was created, changed or deleted. The list itself did not change, but
// the name the page is filtering by may have, so re-render from the server instead of
// trusting what is already in the table. Deleting the view that is currently applied
// is the case that matters: without this the user keeps reading a result set whose
// only name has just been removed.
function reloadAssetsAfterViewChange() {
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
  if (!inventoryMode.value && inventoryOrganizationID.value) organizationFilter.value = inventoryOrganizationID.value
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
function defaultCustomValues() {
  Object.keys(customValues).forEach((key) => delete customValues[key])
  customFields.value.forEach((field) => { customValues[field.field_key] = field.field_type === 'multiselect' ? [] : field.field_type === 'boolean' ? false : '' })
}
function defaultCustomValueMap() {
  return Object.fromEntries(customFields.value.map((field) => [field.field_key, field.field_type === 'multiselect' ? [] : field.field_type === 'boolean' ? false : '']))
}
function resetForm() {
  defaultCustomValues()
  Object.assign(form, {
    type: 'domain', value: '', name: '', organization_id: defaultOrganizationID(), business_system_id: '', network_area_id: '',
    environment: 'production', network_scope: inventoryMode.value && inventoryNetworkScope.value !== 'all' ? inventoryNetworkScope.value : 'public', status: 'active', importance: 'normal', owner_user_id: '', owner_name: '', notes: '', expires_at: null,
  })
  ownerOptions.value = []
}
function openCreateDialog() {
  editingAssetID.value = ''
  editingExpectedUpdatedAt.value = ''
  editingExpectedVersion.value = 0
  editingIsDraft.value = false
  editingDraftTaskID.value = ''
  editingDraftOrganizationID.value = ''
  resetForm()
  resetDirectChangeReason()
  editDialogVisible.value = true
  void loadOwnerOptions()
}
function openWorkingVersionDiff(row: { id: string }) {
  void workingVersionDiffRef.value?.open(row.id)
}
function openResourceSubmissionHistory(row: { id: string }) {
  void resourceSubmissionHistoryRef.value?.open(row.id)
}

function openEditDialog(item: Asset) {
  editingAssetID.value = item.id
  editingExpectedUpdatedAt.value = item.updated_at ?? ''
  editingExpectedVersion.value = item.version ?? 0
  const draftTaskID = item.task_id || (inventoryMode.value && item.is_draft ? inventoryTaskID.value : '')
  editingIsDraft.value = Boolean(draftTaskID)
  editingDraftTaskID.value = draftTaskID
  editingDraftOrganizationID.value = draftTaskID ? item.organization_id : ''
  Object.assign(form, {
    type: item.type, value: item.value, name: item.name ?? '', organization_id: item.organization_id,
    business_system_id: item.business_system_id ?? '', network_area_id: item.network_area_id ?? '', environment: item.environment, network_scope: item.network_scope,
    status: item.status, importance: item.importance, owner_user_id: item.owner_user_id ?? '', owner_name: item.owner_name ?? '', notes: item.notes ?? '', expires_at: item.expires_at ? new Date(item.expires_at) : null,
  })
  ownerOptions.value = item.owner_user_id && item.owner_username ? [{ id: item.owner_user_id, username: item.owner_username, display_name: item.owner_user_display_name ?? item.owner_username }] : []
  void loadOwnerOptions()
  defaultCustomValues()
  if (item.is_draft || item.is_working_copy) item.custom_values?.forEach((value) => { customValues[value.field_key] = value.value })
  else void loadCustomValues(item.id)
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
    if (row.is_draft || row.is_working_copy) {
      detail.value = row
      detailCustomFields.value = customFields.value
      detailCustomValues.value = { ...defaultCustomValueMap(), ...Object.fromEntries((row.custom_values || []).map((item) => [item.field_key, item.value])) }
      return
    }
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
    status: item.status, importance: item.importance, owner_user_id: item.owner_user_id ?? '', owner_name: item.owner_name ?? '', notes: item.notes ?? '', expires_at: item.expires_at ?? null,
    custom_values: item.custom_values ?? [], ...overrides,
    // 集团管理员归档/启用同样是直改正式台账，必须带上变更原因。
    ...(directLedgerWrite.value && directChangeReasonRequired.value ? { direct_change_reason: directChangeReason.value } : {}),
  }
}
async function submit() {
  if (!formRef.value || !(await formRef.value.validate())) return
  submitting.value = true
  try {
    const missing = customFields.value.find((field) => field.is_required && isEmptyCustomValue(customValues[field.field_key]))
    if (missing) { ElMessage.warning(`请填写自定义字段：${missing.field_name}`); return }
    const customPayload = customFields.value.map((field) => ({ field_key: field.field_key, value: customValues[field.field_key] }))
    if (directLedgerWrite.value && !directChangeReasonRequired.value) {
      // 台账直写必须说明原因，后端会拒绝没有原因的提交；在请求之前先要一次，
      // 免得用户填完一整张表单才被 400 打回。
      directChangeReason.value = await requestDirectChangeReason('集团直接变更正式台账', `确认以集团名义直接${editingAssetID.value ? '修改' : '新增'}正式台账资产，请填写变更原因：所属组织会收到一条待复核记录，且集团不能自行确认该变更。`)
      directChangeReasonRequired.value = true
    }
    // Draft saves use the same optimistic-lock contract as the backend. A
    // newly created draft has no version yet; existing drafts must echo the
    // version returned by the last read/save.
    const payload: Record<string, unknown> = {
      ...form,
      organization_id: inventoryMode.value ? inventoryOrganizationID.value : form.organization_id,
      expires_at: form.expires_at?.toISOString() ?? null,
      custom_values: customPayload,
      // Working versions (task-bound or not) are version-locked; only rows in
      // the published ledger still use the timestamp comparison.
      ...(directLedgerWrite.value && directChangeReasonRequired.value ? { direct_change_reason: directChangeReason.value } : {}),
      ...(editingAssetID.value && editingExpectedVersion.value > 0 ? { expected_version: editingExpectedVersion.value } : {}),
      ...(editingAssetID.value && !editingExpectedVersion.value && !editingIsDraft.value && editingExpectedUpdatedAt.value ? { expected_updated_at: editingExpectedUpdatedAt.value } : {}),
    }
    let response
    if (inventoryMode.value) {
      const draftTaskID = editingDraftTaskID.value || inventoryTaskID.value
      const draftOrganizationID = editingDraftOrganizationID.value || inventoryOrganizationID.value
      if (editingAssetID.value && editingIsDraft.value) response = await apiClient.patch<{ item: Asset }>(assetDraftEndpoint(draftTaskID, draftOrganizationID, editingAssetID.value), payload)
      else response = editingAssetID.value
        ? await apiClient.patch<{ item: Asset }>(`/assets/${editingAssetID.value}`, payload)
        : await apiClient.post<{ item: Asset }>('/assets', payload)
    } else {
      response = editingAssetID.value && editingIsDraft.value
        ? await apiClient.patch<{ item: Asset }>(assetDraftEndpoint(editingDraftTaskID.value, editingDraftOrganizationID.value, editingAssetID.value), payload)
        : editingAssetID.value
          ? await apiClient.patch<{ item: Asset }>(`/assets/${editingAssetID.value}`, payload)
          : await apiClient.post<{ item: Asset }>('/assets', payload)
    }
    const id = editingAssetID.value || response.data.item.id
    if (!inventoryMode.value && customFields.value.length && !response.data.item.is_working_copy) await apiClient.put(`/assets/${id}/custom-fields`, { values: customFields.value.map((field) => ({ field_key: field.field_key, value: customValues[field.field_key] })) })
    ElMessage.success(inventoryMode.value ? (editingIsDraft.value ? '本次上报内容已更新' : (editingAssetID.value ? '资产变更已保存，请选择后纳入本次上报' : '资产已保存为未上报状态，请选择后纳入本次上报')) : (response.data.item.is_working_copy ? (editingAssetID.value ? '网络资产已更新，当前仍为未上报状态' : '网络资产已新增，默认未上报') : (editingAssetID.value ? '网络资产已更新' : '网络资产已新增')))
    editDialogVisible.value = false
    resetDirectChangeReason()
    await refreshAfterMutation()
  } catch (error) {
    if (error === 'cancel' || error === 'close') { editDialogVisible.value = false; return }
    if (await reportEditConflict(error, '网络资产已被其他人更新', refreshAfterMutation)) return
    ElMessage.error(getAPIErrorMessage(error, inventoryMode.value ? '保存待上报网络资产失败' : (editingAssetID.value ? '编辑网络资产失败' : '新增网络资产失败')))
  } finally {
    submitting.value = false
  }
}

async function selectWorkspaceAsset(item: Asset) {
  try {
    await apiClient.post(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/asset-drafts/${item.id}/select`, { expected_version: item.version })
    ElMessage.success('已选择该网络资产纳入本次上报')
    await refreshAfterMutation()
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '选择上报网络资产失败')) }
}

function canSelectAsset(row: Asset) {
  return inventoryMode.value ? Boolean(row.is_working_copy && !row.task_id && !auth.isGroupAdmin) : !row.is_working_copy
}

async function batchSelectWorkspaceAssets() {
  if (!selectedAssets.value.length) return ElMessage.warning('请先选择需要上报的网络资产')
  if (selectedAssets.value.length > 200) return ElMessage.warning('单次最多选择上报 200 个网络资产')
  try {
    await ElMessageBox.confirm(`确认将选中的 ${selectedAssets.value.length} 个未上报网络资产纳入本次填报任务吗？关联的未上报业务系统会按任务范围自动一并纳入。`, '批量选择上报', {
      type: 'warning', confirmButtonText: '确认纳入', cancelButtonText: '取消',
    })
    batchLoading.value = true
    const targets = [...selectedAssets.value]
    const response = await apiClient.post<{ result: BatchOperationResult }>(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/asset-drafts/batch-select`, {
      items: targets.map((item) => ({ id: item.id, expected_version: item.version })),
    })
    const names = new Map(targets.map((item) => [item.id, item.name || item.value]))
    batchResult.value = {
      ...response.data.result,
      items: response.data.result.items.map((item) => ({ ...item, resource_name: names.get(item.id) || item.id })),
    }
    batchResultTitle.value = '批量选择上报网络资产结果'
    batchResultVisible.value = true
    if (response.data.result.failure_count === 0) ElMessage.success(`已将 ${response.data.result.success_count} 个网络资产纳入本次上报`)
    await refreshAfterMutation()
    selectedAssets.value = []
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '批量选择上报网络资产失败'))
  } finally { batchLoading.value = false }
}

async function requestAssetDeletion(item: Asset) {
  try {
    await ElMessageBox.confirm(`确认将网络资产“${item.name || item.value}”加入本次任务的删除草稿吗？提交并发布前不会删除正式台账。`, '申请删除网络资产', { type: 'warning', confirmButtonText: '加入删除草稿', cancelButtonText: '取消' })
    await apiClient.post(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/assets/${item.id}/delete-draft`)
    ElMessage.success('已加入删除草稿，提交前正式台账保持不变')
    await loadAssetDrafts()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, '创建资产删除草稿失败'))
  }
}

function canTakeOverDraft(row: Asset) {
  if (!inventoryMode.value) return false
  if (!row.assigned_to) return false
  if (row.assigned_to === auth.user?.id) return false
  return row.draft_status === 'editing' || row.draft_status === 'returned'
}

async function takeoverDraft(item: Asset) {
  const reason = await requestDirectChangeReason(
    '接管填报草稿',
    `确认接管“${item.name || item.value}”的填报草稿吗？接管原因会记入审计日志，原填报人会看到该提示。`,
  ).catch(() => '')
  if (!reason) return
  try {
    if (!item.version || item.version <= 0) throw new Error('草稿版本信息缺失，请刷新后重试')
    await apiClient.post(`${assetDraftEndpoint(inventoryTaskID.value, inventoryOrganizationID.value, item.id)}/takeover`, { expected_version: item.version, reason })
    ElMessage.success('已接管该草稿，接管原因已记入审计日志')
    await refreshAfterMutation()
  } catch (error) {
    if (await reportEditConflict(error, '草稿已被其他人更新', loadAssetDrafts)) return
    ElMessage.error(getAPIErrorMessage(error, '接管填报资产草稿失败'))
  }
}

async function removeDraft(item: Asset) {
  try {
    await ElMessageBox.confirm(`确认取消任务中的“${draftOperationLabels[item.operation || 'create']}”变更“${item.name || item.value}”吗？`, '取消任务变更', { type: 'warning', confirmButtonText: '确认取消', cancelButtonText: '返回' })
    if (!item.version || item.version <= 0) throw new Error('草稿版本信息缺失，请刷新后重试')
    await apiClient.delete(`/inventory-tasks/${inventoryTaskID.value}/organizations/${inventoryOrganizationID.value}/asset-drafts/${item.id}`, { params: { expected_version: item.version } })
    ElMessage.success('任务变更已取消')
    await loadAssetDrafts()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    if (await reportEditConflict(error, '草稿已被其他人更新', loadAssetDrafts)) return
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
    const directBatch = directLedgerWrite.value && selectedAssets.value.every((item) => !item.is_working_copy && !item.task_id)
    let directReason = ''
    if (directBatch) directReason = await requestDirectChangeReason('集团批量变更正式台账', `确认对 ${selectedAssets.value.length} 个正式台账资产执行“${action}”，请填写变更原因：每个资产都会在所属组织生成待复核记录。`)
    batchLoading.value = true
    const targets = [...selectedAssets.value]
    const response = await apiClient.patch<{ result: BatchOperationResult }>('/assets/batch', {
      // 后端批量更新按资产 ID 列表处理（ids），不是按带版本号的条目；版本由
      // 服务端在事务里读取，条目形式会被当成空选择直接 400。
      ids: targets.map((item) => item.id), ...payload,
      ...assetDirectBatchEndpointQuery({ isGroupAdmin: auth.isGroupAdmin, inventoryMode: inventoryMode.value }, directReason),
    })
    const names = new Map(targets.map((item) => [item.id, item.name || item.value]))
    batchResult.value = {
      ...response.data.result,
      items: response.data.result.items.map((item) => ({ ...item, resource_name: item.item?.name || item.item?.value || names.get(item.id) || item.id })),
    }
    batchResultTitle.value = `批量${action}结果`
    batchResultVisible.value = true
    if (response.data.result.failure_count === 0) ElMessage.success(batchAssetUpdateSuccessMessage(response.data.result))
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
    if (directLedgerWrite.value) {
      directChangeReason.value = await requestDirectChangeReason(`${action}正式台账资产`, `确认${action}正式台账资产“${item.name || item.value}”，请填写变更原因：所属组织会收到一条待复核记录。`)
      directChangeReasonRequired.value = true
    }
    await apiClient.patch(`/assets/${item.id}`, assetPayload(item, { status: archived ? 'active' : 'archived' }))
    ElMessage.success(`网络资产已${action}`)
    resetDirectChangeReason()
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
    const formalDelete = !item.is_working_copy && !item.task_id
    if (directLedgerWrite.value && formalDelete) {
      directChangeReason.value = await requestDirectChangeReason('删除正式台账资产', `确认以集团名义删除正式台账资产“${item.name || item.value}”，请填写变更原因：所属组织会收到一条待复核记录。`)
      directChangeReasonRequired.value = true
    }
    if (item.is_working_copy && item.task_id) {
      if (!item.version || item.version <= 0) throw new Error('草稿版本信息缺失，请刷新后重试')
      await apiClient.delete(assetDraftEndpoint(item.task_id, item.organization_id, item.id), { params: { expected_version: item.version } })
    } else {
      await apiClient.delete(`/assets/${item.id}`, { params: {
        ...(item.version && item.version > 0 ? { expected_version: item.version } : {}),
        ...(formalDelete && directChangeReasonRequired.value ? { direct_change_reason: directChangeReason.value } : {}),
      } })
    }
    ElMessage.success('网络资产已删除')
    resetDirectChangeReason()
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
    const response = await apiClient.post<{ columns: ImportColumn[]; fields: ImportField[] }>('/assets/import/columns', data, {
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
  importForm.organization_id = defaultOrganizationID()
  importForm.import_mode = 'skip_duplicates'
  importForm.direct_change_reason = ''
  importFile.value = undefined
  clearImportColumnMapping()
  resetImportPreview()
  importDialogVisible.value = true
}
async function handleFileChange(uploadFile: UploadFile, _uploadFiles: UploadFiles) {
  importFile.value = uploadFile.raw
  await analyzeImportColumns()
}
function handleFileRemove() {
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
  if (!preview && auth.isGroupAdmin && !importForm.direct_change_reason.trim()) return ElMessage.warning('导入正式台账属于集团直接变更，请填写变更原因')
  if (!preview && (importResultMode.value !== 'preview' || !importResult.value?.success_count)) {
    return ElMessage.warning('请先完成预检，并确认至少有一行可以导入')
  }
  importing.value = true
  try {
    const data = new FormData()
    data.append('organization_id', importForm.organization_id)
    data.append('import_mode', importForm.import_mode)
    if (auth.isGroupAdmin) data.append('direct_change_reason', importForm.direct_change_reason)
    data.append('column_mapping', serializeImportColumnMapping(importFields.value, importColumnMapping))
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
      ElMessage.success(auth.isGroupAdmin
        ? `成功导入 ${response.data.result.success_count} 条网络资产到正式台账`
        : `已新增 ${response.data.result.created_count} 条、更新 ${response.data.result.updated_count} 条未上报工作版本`)
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

async function exportSelectedAssets(format: 'csv' | 'xlsx') {
  if (!selectedAssets.value.length) return ElMessage.warning('请先选择需要导出的网络资产')
  if (selectedAssets.value.length > 200) return ElMessage.warning('单次最多导出 200 个网络资产')
  selectedExporting.value = true
  try {
    const selectedIDs = selectedAssets.value.map((item) => item.id)
    const response = await apiClient.get('/assets/export', {
      params: { format, selected_ids: selectedIDs.join(',') },
      responseType: 'blob',
      timeout: 60_000,
    })
    const href = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = href
    link.download = `网络资产选中项.${format}`
    link.click()
    URL.revokeObjectURL(href)
    const count = response.headers['x-export-count'] ?? selectedIDs.length
    ElMessage.success(`已生成并开始下载 ${count} 条选中网络资产`)
  } catch (error) {
    const detail = await getDownloadAPIError(error, '导出选中网络资产失败')
    ElMessage.error(detail.message)
  } finally {
    selectedExporting.value = false
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

watch(() => route.query.organization_id, (value) => {
  if (inventoryMode.value) return
  const nextOrganizationID = typeof value === 'string' ? value : ''
  if (organizationFilter.value === nextOrganizationID) return
  organizationFilter.value = nextOrganizationID
  page.value = 1
  void loadAssets()
})

onMounted(() => {
  if (inventoryMode.value) organizationFilter.value = inventoryOrganizationID.value
  void load(!inventoryMode.value)
  // 通知里的“集团直改复核”深链直接打开复核抽屉，省去用户自己在更多操作里找。
  if (!inventoryMode.value && route.query.recheck === '1') void openRecheckDrawer()
})
</script>

<template>
  <section>
    <el-alert
      v-if="inventoryMode"
      :title="`当前为“${inventoryTaskName}”选择上报模式`"
      :description="`这里展示 ${inventoryOrganizationName} 的工作台账。新增或修改先保存为未上报版本，选择纳入本次任务并提交审核后，才会发布到集团正式台账。`"
      type="warning"
      :closable="false"
      show-icon
      class="inventory-draft-alert"
    >
      <template #default><el-button size="small" @click="router.push('/inventory-tasks')">返回填报任务</el-button></template>
    </el-alert>
    <div class="page-heading"><h1>网络资产</h1>
      <div class="button-row ledger-page-actions asset-page-actions">
        <el-dropdown v-if="!inventoryMode">
          <el-button :loading="exporting">导出台账</el-button>
          <template #dropdown><el-dropdown-menu><el-dropdown-item @click="exportLedger('xlsx')">导出 XLSX</el-dropdown-item><el-dropdown-item @click="exportLedger('csv')">导出 CSV</el-dropdown-item><el-dropdown-item divided @click="queueXLSXExport">后台生成 XLSX</el-dropdown-item></el-dropdown-menu></template>
        </el-dropdown>
        <el-dropdown v-if="!inventoryMode" trigger="click"><el-button>更多操作</el-button><template #dropdown><el-dropdown-menu><el-dropdown-item @click="openImportDialog">批量导入</el-dropdown-item><el-dropdown-item @click="openImportHistory">导入记录</el-dropdown-item><el-dropdown-item v-if="auth.hasPermission('asset.manage')" divided @click="openRecheckDrawer">直改复核</el-dropdown-item><el-dropdown-item v-if="auth.isGroupAdmin" divided @click="openAreaManagement">网络区域配置</el-dropdown-item></el-dropdown-menu></template></el-dropdown>
        <el-button type="primary" @click="openCreateDialog">新增资产</el-button>
      </div>
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
        <el-button text :aria-pressed="extendedColumns" @click="extendedColumns=!extendedColumns">{{ extendedColumns?'精简字段':'更多字段' }}</el-button><span class="asset-filter-count">共 {{ total }} 条</span>
      </div>
      <SavedFilterViews v-if="!inventoryMode" ref="savedFilterViews" page-key="assets" :current-filters="currentAssetFilters" @apply="applySavedAssetFilters" @changed="reloadAssetsAfterViewChange" />
    </div>

    <div v-if="inventoryMode" class="panel data-table-panel inventory-draft-panel" v-loading="loading">
      <div class="table-panel-heading">
        <div><strong>本次上报内容</strong><span>共 {{ draftTotal }} 项；首次上报、变更上报和下线申请均在审核发布后影响集团正式台账</span></div>
        <el-tag type="warning" effect="plain">待提交</el-tag>
      </div>
      <el-table :data="draftAssets" row-key="id" empty-text="尚未选择需要上报的网络资产">
        <el-table-column label="资产 / 访问标识" min-width="240"><template #default="scope"><el-button link type="primary" class="table-link" @click="openEditDialog(scope.row)">{{ scope.row.name || '未命名资产' }}</el-button><span class="list-secondary mono-text">{{ scope.row.value }}</span></template></el-table-column>

        <el-table-column label="类型" width="95"><template #default="scope">{{ typeLabel(scope.row.type) }}</template></el-table-column>
        <el-table-column prop="business_system_name" label="业务系统" min-width="150"><template #default="scope">{{ scope.row.business_system_name || '未绑定' }}</template></el-table-column>
        <el-table-column label="暴露范围" width="105"><template #default="scope">{{ scopeLabel(scope.row.network_scope) }}</template></el-table-column>
        <el-table-column label="变更类型" width="90"><template #default="scope"><el-tag :type="scope.row.operation === 'delete' ? 'danger' : scope.row.operation === 'update' ? 'warning' : 'success'" effect="plain">{{ draftOperationLabels[scope.row.operation || 'create'] }}</el-tag></template></el-table-column>
        <el-table-column label="上报状态" width="105"><template #default="scope"><el-tag type="warning" effect="plain">{{ draftStatusLabels[scope.row.draft_status || 'editing'] || scope.row.draft_status }}</el-tag></template></el-table-column>
        <el-table-column label="填报归属" min-width="180"><template #default="scope"><span v-if="!scope.row.assigned_to" class="list-secondary">尚未分配填报人</span><template v-else><span>{{ scope.row.assigned_to }}</span><span v-if="draftTakeoverSummary(scope.row)" class="list-secondary">{{ draftTakeoverSummary(scope.row) }}</span><span v-else-if="draftLastEditedHint(scope.row)" class="list-secondary">{{ draftLastEditedHint(scope.row) }}</span></template></template></el-table-column>
        <el-table-column label="操作" width="120" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="scope"><RowActions :label="`操作：${scope.row.name || scope.row.title || scope.row.code || scope.row.display_name || scope.row.id}`"><el-button link type="primary" @click="openWorkingVersionDiff(scope.row)">查看变更</el-button><el-button v-if="scope.row.operation !== 'delete'" link type="primary" :disabled="scope.row.draft_status === 'submitted'" @click="openEditDialog(scope.row)">编辑</el-button><el-button v-if="canTakeOverDraft(scope.row)" link type="warning" :disabled="scope.row.draft_status === 'submitted'" @click="takeoverDraft(scope.row)">接管</el-button><el-button link type="danger" :disabled="scope.row.draft_status === 'submitted'" @click="removeDraft(scope.row)">取消上报</el-button></RowActions></template></el-table-column>
      </el-table>
      <div v-if="draftTotal > 0" class="table-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="draftTotal" :current-page="draftPage" :page-size="draftPageSize" :page-sizes="[20, 50, 100]" @current-change="handleDraftPageChange" @size-change="handleDraftPageSizeChange" />
      </div>
    </div>

    <div class="panel data-table-panel" v-loading="loading">
      <div v-if="inventoryMode" class="batch-toolbar">
        <div><strong>选择上报</strong><span>已选择 {{ selectedAssets.length }} 个未上报网络资产，单次最多 200 个</span></div>
        <div class="batch-toolbar-actions"><el-button type="primary" :loading="batchLoading" :disabled="selectedAssets.length === 0" @click="batchSelectWorkspaceAssets">批量纳入本次上报</el-button></div>
      </div>
      <div v-else-if="selectedAssets.length" class="batch-toolbar">
        <div><strong>批量操作</strong><span>已选择 {{ selectedAssets.length }} 个资产；设置网络区域时需选择同一组织的资产</span></div>
        <div class="batch-toolbar-actions">
          <el-select v-model="batchStatusValue" aria-label="批量设置状态" :disabled="selectedAssets.length === 0 || batchLoading" placeholder="批量设置状态" style="width: 150px" @change="applyBatchStatus">
            <el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" />
          </el-select>
          <el-select v-model="batchNetworkAreaValue" aria-label="批量设置网络区域" :disabled="selectedAssets.length === 0 || !selectedOrganizationID || batchLoading" placeholder="批量设置网络区域" style="width: 210px" @change="applyBatchNetworkArea">
            <el-option label="清除网络区域" value="__clear__" />
            <el-option v-for="item in selectedNetworkAreas" :key="item.id" :label="item.code ? `${item.name}（${item.code}）` : item.name" :value="item.id" />
          </el-select>
          <el-dropdown :disabled="selectedAssets.length === 0 || selectedExporting">
            <el-button :loading="selectedExporting" :disabled="selectedAssets.length === 0">导出选中</el-button>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item @click="exportSelectedAssets('xlsx')">导出选中 XLSX</el-dropdown-item>
                <el-dropdown-item @click="exportSelectedAssets('csv')">导出选中 CSV</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </div>
      <el-table :data="assets" row-key="id" empty-text="暂无网络资产" @selection-change="handleSelectionChange">
        <el-table-column type="selection" width="46" :selectable="canSelectAsset" />
        <el-table-column label="资产 / 访问标识" min-width="240">
          <template #default="scope"><el-button link type="primary" class="table-link" @click="openDetail(scope.row)">{{ scope.row.name || '未命名资产' }}</el-button><div class="table-secondary mono-text">{{ scope.row.value }}</div></template>
        </el-table-column>
        <el-table-column label="类型" width="95"><template #default="scope">{{ typeLabel(scope.row.type) }}</template></el-table-column>

        <el-table-column label="业务归属" min-width="175"><template #default="scope"><span v-if="scope.row.business_system_id">{{ scope.row.business_system_name }}</span><el-tag v-else type="warning" size="small">待补全业务系统</el-tag><div class="table-secondary">{{ scope.row.organization_name }}</div></template></el-table-column>
        <el-table-column label="责任人" min-width="110"><template #default="scope"><div v-if="scope.row.owner_user_id" class="organization-name-cell"><strong>{{ scope.row.owner_user_display_name || scope.row.owner_username }}</strong><span>{{ scope.row.owner_username }}</span></div><span v-else class="muted-text">未绑定</span></template></el-table-column>
        <el-table-column v-if="extendedColumns" label="环境 / 暴露范围" width="145"><template #default="scope">{{ environmentLabel(scope.row.environment) }} / {{ scopeLabel(scope.row.network_scope) }}</template></el-table-column>
        <el-table-column v-if="extendedColumns" label="网络区域" min-width="135"><template #default="scope"><span>{{ scope.row.network_area_name || '未指定' }}</span></template></el-table-column>
        <el-table-column v-if="extendedColumns" label="有效期" min-width="185"><template #default="scope"><el-tag :type="expiryTag(scope.row.expires_at)" effect="plain">{{ expiryLabel(scope.row.expires_at) }}</el-tag></template></el-table-column>
        <el-table-column label="状态" width="90"><template #default="scope"><el-tag :type="statusTag(scope.row.status)" effect="plain">{{ statusLabel(scope.row.status) }}</el-tag><span v-if="inventoryMode && scope.row.is_working_copy && scope.row.task_id" class="list-secondary">{{ scope.row.task_id === inventoryTaskID ? '已纳入本次上报' : '已纳入其他任务' }}</span></template></el-table-column>
        <el-table-column v-if="!auth.isGroupAdmin && !inventoryMode" label="上报状态" width="130"><template #default="scope"><el-tag :type="scope.row.reporting_status === 'reported' ? 'success' : 'warning'" effect="plain">{{ reportingStatusLabels[scope.row.reporting_status || 'reported'] }}</el-tag></template></el-table-column>
        <el-table-column v-if="extendedColumns" label="重要程度" width="95"><template #default="scope"><el-tag :type="importanceTag(scope.row.importance)" effect="plain">{{ importanceLabel(scope.row.importance) }}</el-tag></template></el-table-column>
        <el-table-column v-if="extendedColumns" label="来源" width="75"><template #default="scope">{{ sourceLabel(scope.row.source) }}</template></el-table-column>
        <el-table-column label="操作" width="88" fixed="right" align="right" header-align="right" class-name="table-action-column">
          <template #default="scope"><RowActions :label="`操作：${scope.row.name || scope.row.display_name || scope.row.id}`">
            <template v-if="!inventoryMode"><el-button link type="primary" @click="openEditDialog(scope.row)">编辑</el-button><el-button link type="primary" @click="openDetail(scope.row)">查看详情</el-button><el-button link type="primary" v-if="scope.row.is_working_copy" @click="openWorkingVersionDiff(scope.row)">查看变更</el-button><el-button link type="primary" v-else @click="openChangeHistory(scope.row)">变更历史</el-button><el-button link type="primary" v-if="!scope.row.is_working_copy" @click="toggleArchive(scope.row)">{{ scope.row.status === 'archived' ? '启用' : '归档' }}</el-button><el-button link type="danger" @click="removeAsset(scope.row)">删除资产</el-button></template>
            <template v-else><template v-if="scope.row.is_working_copy"><el-button link type="primary" @click="openWorkingVersionDiff(scope.row)">查看变更</el-button><el-button v-if="!scope.row.task_id" link type="primary" @click="selectWorkspaceAsset(scope.row)">选择上报</el-button></template><template v-else><el-button link type="primary" @click="openEditDialog(scope.row)">修改后上报</el-button><el-button link type="danger" @click="requestAssetDeletion(scope.row)">申请下线</el-button></template></template>
          </RowActions></template>
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
          <AssetExternalObservations v-if="detail && !detail.is_draft && !detail.is_working_copy && ['domain', 'url'].includes(detail.type)" :asset-id="detail.id" :asset-type="detail.type" :can-refresh="auth.hasPermission('asset.manage')" />
          <template v-if="detailCustomFields.length">
            <div class="form-section-title">集团自定义字段</div>
            <div class="detail-grid"><div v-for="field in detailCustomFields" :key="field.id"><span>{{ field.field_name }}</span><strong>{{ customValueLabel(detailCustomValues[field.field_key]) }}</strong></div></div>
          </template>
          <div class="form-section-title">备注</div><p class="detail-description">{{ detail.notes || '暂无备注' }}</p>
          <div class="detail-actions"><el-button type="primary" plain @click="openResourceSubmissionHistory(detail)">上报历史</el-button><el-button v-if="!detail.is_working_copy" @click="openChangeHistory(detail)">变更历史</el-button><el-button @click="openEditDialog(detail)">编辑资产</el-button><el-button v-if="!detail.is_working_copy" :type="detail.status === 'archived' ? 'success' : 'warning'" @click="toggleArchive(detail)">{{ detail.status === 'archived' ? '启用资产' : '归档资产' }}</el-button></div>
        </template>
      </div>
    </el-drawer>

    <WorkingVersionDiffDrawer ref="workingVersionDiffRef" resource-type="network_asset" />
    <ResourceSubmissionHistoryDrawer ref="resourceSubmissionHistoryRef" resource-type="network_asset" :allow-restore="!auth.isGroupAdmin && auth.hasPermission('inventory.manage') && auth.hasPermission('asset.manage')" @restored="load()" />

    <el-dialog v-model="editDialogVisible" :title="dialogTitle" width="720px" destroy-on-close>
      <el-alert v-if="inventoryMode" title="这里保存的是组织工作版本；只有选择纳入本次上报并通过审核后，才会更新集团正式台账。" type="info" :closable="false" show-icon class="dialog-context-alert" /><el-form ref="formRef" :model="form" :rules="rules" label-position="top">
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
          <el-form-item label="备注" class="full-span"><el-input v-model="form.notes" type="textarea" :rows="3" placeholder="补充资产用途、管理要求等信息" /></el-form-item><template v-if="customFields.length"><el-divider class="full-span" content-position="left">集团自定义字段</el-divider><el-form-item v-for="field in customFields" :key="field.id" :label="field.field_name" :required="field.is_required"><el-input v-if="field.field_type === 'text'" v-model="customValues[field.field_key]"/><el-input v-else-if="field.field_type === 'textarea'" v-model="customValues[field.field_key]" type="textarea" :rows="2"/><el-input-number v-else-if="field.field_type === 'number'" v-model="customValues[field.field_key]"/><el-date-picker v-else-if="field.field_type === 'date'" v-model="customValues[field.field_key]" type="date" value-format="YYYY-MM-DD"/><el-switch v-else-if="field.field_type === 'boolean'" v-model="customValues[field.field_key]"/><el-select v-else-if="field.field_type === 'multiselect'" v-model="customValues[field.field_key]" multiple><el-option v-for="option in field.options" :key="option" :label="option" :value="option"/></el-select><el-select v-else v-model="customValues[field.field_key]"><el-option v-for="option in field.options" :key="option" :label="option" :value="option"/></el-select><div v-if="field.description" class="form-tip">{{ field.description }}</div></el-form-item></template>
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
        <el-table-column label="操作" width="85" fixed="right" align="right" header-align="right" class-name="table-action-column"><template #default="scope"><el-button link type="primary" @click="openEditNetworkArea(scope.row)">编辑</el-button></template></el-table-column>
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

    <el-dialog v-model="importDialogVisible" title="批量导入网络资产" width="920px" destroy-on-close>
      <el-alert :title="auth.isGroupAdmin ? '单次最多导入 5000 行，成功项直接进入集团正式台账；资产类型和值在集团范围内去重，允许部分成功。请先选择所属组织再下载模板，模板会包含当前启用的集团自定义字段。网络区域按所属组织已配置的名称填写；有效期支持 YYYY-MM-DD、YYYY-MM-DD HH:mm:ss 或 RFC3339。' : '单次最多导入 5000 行，成功项保存为本组织未上报工作版本，集团暂不可见；后续需在填报任务中选择并提交上报。可关联本组织尚未上报或已经发布的业务系统；资产类型和值在集团范围内去重，允许部分成功。'" type="info" :closable="false" show-icon />
      <div class="import-toolbar"><el-button link type="primary" :loading="downloading" @click="downloadTemplate('xlsx')">下载 XLSX 模板</el-button><el-button link type="primary" :loading="downloading" @click="downloadTemplate('csv')">下载 CSV 模板</el-button></div>
      <el-form label-position="top">
        <el-form-item label="导入到所属组织" required><el-select v-model="importForm.organization_id" filterable :disabled="!auth.isGroupAdmin" @change="handleImportOrganizationChange" style="width: 100%"><el-option v-for="item in enabledOrganizations" :key="item.id" :label="item.name" :value="item.id" /></el-select></el-form-item>
        <el-form-item v-if="!auth.isGroupAdmin" label="重复记录处理方式">
          <div class="import-mode-field">
            <el-radio-group v-model="importForm.import_mode" @change="resetImportPreview"><el-radio value="skip_duplicates">重复记录跳过（默认）</el-radio><el-radio value="update_working">覆盖为未上报工作版本</el-radio></el-radio-group>
            <p class="muted-text">覆盖模式不会直接修改集团正式台账：已上报资产会生成更新工作版本，已有可编辑工作版本会被文件内容更新；已纳入任务、已提交或待下线版本不可覆盖，导入后仍需通过填报任务提交上报。</p>
          </div>
        </el-form-item>
        <el-form-item v-if="auth.isGroupAdmin" label="直改原因" required>
          <el-input v-model="importForm.direct_change_reason" type="textarea" :rows="2" maxlength="1000" show-word-limit placeholder="例如：集团资产清查中确认的存量服务器" @input="resetImportPreview" />
          <p class="muted-text">导入成功项直接进入集团正式台账，会在每个资产所属组织生成一条待复核记录；集团管理员不能自行确认该变更。</p>
        </el-form-item>
        <el-form-item label="CSV / XLSX 文件" required><el-upload drag :auto-upload="false" :limit="1" accept=".csv,.xlsx" :on-change="handleFileChange" :on-remove="handleFileRemove"><div class="el-upload__text">拖入文件，或<em>点击选择</em></div><template #tip><div class="el-upload__tip">文件不超过 10 MB；业务系统使用同组织内的系统编码匹配；责任人账号按所属组织内的启用用户名匹配；有效期格式错误会在预检中逐行提示。</div></template></el-upload></el-form-item>
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
      <div v-if="importResult" class="import-result" data-testid="import-result">
        <el-alert :title="importResultMode === 'preview' ? '预检结果：尚未写入台账，请确认后执行导入' : (auth.isGroupAdmin ? '正式台账导入结果' : '组织工作台账导入结果：成功项当前为未上报')" :type="importResultMode === 'preview' ? 'info' : 'success'" :closable="false" show-icon />
        <div class="import-result-summary"><div><span>总行数</span><strong>{{ importResult.total_count }}</strong></div><div class="success"><span>{{ importResultMode === 'preview' ? '可导入' : '成功' }}</span><strong>{{ importResult.success_count }}</strong></div><div class="duplicate"><span>重复</span><strong>{{ importResult.duplicate_count }}</strong></div><div class="failure"><span>失败</span><strong>{{ importResult.failure_count }}</strong></div></div>
        <p class="muted-text import-result-breakdown">成功项：新增 {{ importResult.created_count }} 条，更新 {{ importResult.updated_count }} 条。</p>
        <el-table v-if="importResult.errors.length" :data="importResult.errors" max-height="220" size="small"><el-table-column prop="row" label="行号" width="75" /><el-table-column prop="message" label="处理结果" /></el-table>
      </div>
      <template #footer><el-button @click="importDialogVisible = false">关闭</el-button><el-button :loading="importing || importColumnsLoading" :disabled="Boolean(importMappingError) || !importFields.length" @click="runImport(true)">预检文件</el-button><el-button type="primary" :loading="importing" :disabled="importResultMode !== 'preview' || !importResult?.success_count" @click="runImport(false)">确认导入</el-button></template>
    </el-dialog>

    <el-dialog v-model="historyDialogVisible" title="资产导入记录" width="980px">
      <el-table v-loading="historyLoading" :data="importHistory" empty-text="暂无导入记录" max-height="560">
        <el-table-column label="文件" min-width="190"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.file_name }}</strong><span>{{ scope.row.file_type.toUpperCase() }}</span></div></template></el-table-column>
        <el-table-column prop="organization_name" label="所属组织" min-width="170" />
        <el-table-column label="模式" width="130"><template #default="scope">{{ scope.row.import_mode === 'update_working' ? '覆盖为工作版本' : '重复跳过' }}</template></el-table-column>
        <el-table-column label="状态" width="105"><template #default="scope"><el-tag :type="importStatusTag(scope.row.status)" effect="plain">{{ importStatusLabels[scope.row.status] ?? scope.row.status }}</el-tag></template></el-table-column>
        <el-table-column prop="total_count" label="总数" width="70" /><el-table-column label="成功" width="130"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.success_count }}</strong><span>新增 {{ scope.row.created_count }} / 更新 {{ scope.row.updated_count }}</span></div></template></el-table-column><el-table-column prop="duplicate_count" label="重复" width="70" /><el-table-column prop="failure_count" label="失败" width="70" />
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
          <el-timeline-item v-for="item in changeHistoryItems" :key="item.id" :timestamp="formatDateTime(item.created_at)" placement="top">
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

    <el-drawer v-model="recheckVisible" title="集团直改复核" size="880px">
      <el-alert title="集团管理员绕过填报复核流程直接写入正式台账的资产会列在这里，由资产所属组织认可或提出异议。集团管理员不能确认自己写的变更。" type="info" :closable="false" show-icon />
      <div class="recheck-toolbar">
        <el-select v-model="recheckStatusFilter" clearable placeholder="全部状态" style="width: 150px" @change="recheckPage = 1; loadRechecks()">
          <el-option v-for="(label, value) in recheckStatusLabels" :key="value" :label="label" :value="value" />
        </el-select>
        <span class="asset-filter-count">共 {{ recheckTotal }} 条</span>
      </div>
      <el-table :data="recheckItems" v-loading="recheckLoading" max-height="520">
        <el-table-column label="资产" min-width="200"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.asset_name || scope.row.asset_value }}</strong><span>{{ scope.row.asset_type }} · {{ scope.row.asset_value }}</span></div></template></el-table-column>
        <el-table-column label="所属组织" min-width="140" prop="organization_name" />
        <el-table-column label="变更" width="120"><template #default="scope">{{ recheckActionLabels[scope.row.action] || scope.row.action }}</template></el-table-column>
        <el-table-column label="变更原因" min-width="200"><template #default="scope">{{ scope.row.reason }}</template></el-table-column>
        <el-table-column label="操作人 / 时间" width="160"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.created_by_name || '-' }}</strong><span>{{ formatDate(scope.row.created_at) }}</span></div></template></el-table-column>
        <el-table-column label="状态" width="96"><template #default="scope"><el-tag :type="scope.row.status === 'confirmed' ? 'success' : scope.row.status === 'disputed' ? 'danger' : 'warning'" effect="plain">{{ recheckStatusLabels[scope.row.status] || scope.row.status }}</el-tag></template></el-table-column>
        <el-table-column label="操作" width="140" fixed="right"><template #default="scope"><el-button link type="primary" @click="openRecheckDetail(scope.row)">查看</el-button><template v-if="scope.row.status === 'open' && !auth.isGroupAdmin"><el-button link type="primary" @click="decideRecheck(scope.row, 'confirmed')">认可</el-button><el-button link type="danger" @click="decideRecheck(scope.row, 'disputed')">有异议</el-button></template></template></el-table-column>
      </el-table>
      <el-pagination
        v-model:current-page="recheckPage"
        v-model:page-size="recheckPageSize"
        layout="total, sizes, prev, pager, next"
        :total="recheckTotal"
        :page-sizes="[20, 50, 100]"
        @current-change="loadRechecks"
        @size-change="recheckPage = 1; loadRechecks()"
      />
    </el-drawer>

    <el-dialog v-model="recheckDetailVisible" title="直改复核详情" width="760px">
      <template v-if="recheckDetail">
        <el-descriptions :column="2" border>
          <el-descriptions-item label="资产">{{ recheckDetail.asset_name || recheckDetail.asset_value }}</el-descriptions-item>
          <el-descriptions-item label="所属组织">{{ recheckDetail.organization_name }}</el-descriptions-item>
          <el-descriptions-item label="变更类型">{{ recheckActionLabels[recheckDetail.action] || recheckDetail.action }}</el-descriptions-item>
          <el-descriptions-item label="状态">{{ recheckStatusLabels[recheckDetail.status] || recheckDetail.status }}</el-descriptions-item>
          <el-descriptions-item label="变更原因" :span="2">{{ recheckDetail.reason }}</el-descriptions-item>
          <el-descriptions-item label="操作人">{{ recheckDetail.created_by_name || '-' }}</el-descriptions-item>
          <el-descriptions-item label="操作时间">{{ formatDate(recheckDetail.created_at) }}</el-descriptions-item>
          <el-descriptions-item label="来源 IP">{{ recheckDetail.request_ip || '-' }}</el-descriptions-item>
          <el-descriptions-item label="请求 ID">{{ recheckDetail.request_id || '-' }}</el-descriptions-item>
          <el-descriptions-item v-if="recheckDetail.status !== 'open'" label="复核人">{{ recheckDetail.confirmed_by_name || '-' }}</el-descriptions-item>
          <el-descriptions-item v-if="recheckDetail.status !== 'open'" label="复核说明">{{ recheckDetail.confirmation_note || '-' }}</el-descriptions-item>
        </el-descriptions>
        <h4 class="recheck-section-title">字段变更</h4>
        <el-table :data="recheckDetail.changed_fields || []" max-height="240">
          <el-table-column prop="field" label="字段" min-width="160" />
          <el-table-column label="变更前" min-width="180"><template #default="scope">{{ scope.row.before ?? '-' }}</template></el-table-column>
          <el-table-column label="变更后" min-width="180"><template #default="scope">{{ scope.row.after ?? '-' }}</template></el-table-column>
        </el-table>
        <el-alert v-if="!auth.isGroupAdmin && recheckDetail.status === 'open'" title="请确认该变更与本组织资产事实一致；提出异议不会自动回滚资产，但会记录在台账审计中。" type="warning" :closable="false" show-icon />
      </template>
      <template #footer>
        <el-button @click="recheckDetailVisible = false">关闭</el-button>
        <template v-if="recheckDetail && recheckDetail.status === 'open' && !auth.isGroupAdmin">
          <el-button type="primary" :loading="recheckSubmitting" @click="decideRecheck(recheckDetail, 'confirmed')">认可</el-button>
          <el-button type="danger" :loading="recheckSubmitting" @click="decideRecheck(recheckDetail, 'disputed')">有异议</el-button>
        </template>
      </template>
    </el-dialog>
  </section>
</template>

<style scoped>
.network-asset-filter-controls,.network-asset-filter-controls.with-organization-filter { display:flex;flex-wrap:wrap;gap:10px; }.network-asset-filter-controls>.el-input { flex:1;min-width:220px; }.network-asset-filter-controls>.el-select { width:140px; }.network-asset-filter-controls>.el-select:last-of-type { width:170px; }.network-asset-filter-controls .el-button { margin-left:0; }.mono-text { overflow-wrap:anywhere; }.recheck-toolbar { display:flex;align-items:center;gap:12px;margin:12px 0; }.recheck-section-title { margin:16px 0 8px; }@media(max-width:760px) { .network-asset-filter-controls>.el-input { flex-basis:100%; }.asset-page-actions { flex-wrap:wrap; }.batch-toolbar { align-items:flex-start; } }
</style>
