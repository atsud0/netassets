<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules, type UploadFile, type UploadFiles } from 'element-plus'
import { UploadFilled } from '@element-plus/icons-vue'
import { apiClient, getAPIErrorMessage, getDownloadAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

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
}
interface FieldChange { field: string; label: string; before: string; after: string }
interface ChangeHistoryItem {
  id: number; action: string; action_label: string; summary?: string; display_name?: string; username?: string
  created_at: string; details?: { changes?: FieldChange[] }
}
interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface ImportError { row: number; code: string; message: string }
interface ImportResult { id: string; file_name: string; file_type: string; status: string; total_count: number; success_count: number; failure_count: number; duplicate_count: number; errors: ImportError[] }
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
const formRef = ref<FormInstance>()
const systems = ref<BusinessSystem[]>([])
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
const form = reactive<FormData>(emptyForm())
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
const importLoading = ref(false)
const downloading = ref(false)
const exporting = ref(false)
const historyLoading = ref(false)
const importFile = ref<File>()
const importResult = ref<ImportResult>()
const importResultMode = ref<'preview' | 'completed'>()
const importHistory = ref<ImportHistory[]>([])
const importPage = ref(1)
const importPageSize = ref(20)
const importTotal = ref(0)
const importForm = reactive({ organization_id: '' })
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
const submitLabel = computed(() => editingID.value ? '保存修改' : '确认新增')

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
  await loadSystems()
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
function openCreateDialog() {
  Object.assign(form, emptyForm(), { organization_id: auth.user?.organization?.id ?? eligibleOrganizations.value[0]?.id ?? '' })
  defaultCustomValues()
  editingID.value = ''
  dialogVisible.value = true
}

async function openEditDialog(row: BusinessSystem) {
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
    const response = editingID.value
      ? await apiClient.patch<{ item: BusinessSystem }>(`/business-systems/${editingID.value}`, form)
      : await apiClient.post<{ item: BusinessSystem }>('/business-systems', form)
    const id = editingID.value || response.data.item.id
    if (customFields.value.length) await apiClient.put(`/business-systems/${id}/custom-fields`, { values: customFields.value.map((field) => ({ field_key: field.field_key, value: customValues[field.field_key] })) })
    ElMessage.success(editingID.value ? '业务系统已更新' : '业务系统已新增')
    dialogVisible.value = false
    await refreshAfterMutation()
    if (detailVisible.value && detail.value?.id === id) await openDetail(detail.value as BusinessSystem)
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, editingID.value ? '编辑业务系统失败' : '新增业务系统失败')) }
  finally { submitting.value = false }
}

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

async function openDetail(row: BusinessSystem) {
  detailVisible.value = true
  detailLoading.value = true
  detailAssetPage.value = 1
  detailAssets.value = []
  detailAssetTotal.value = 0
  try {
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
      ids: targets.map((item) => item.id), archived: archive,
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
    await apiClient.delete(`/business-systems/${row.id}`)
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
function openImportDialog() {
  importForm.organization_id = auth.user?.organization?.id ?? eligibleOrganizations.value[0]?.id ?? ''
  importFile.value = undefined
  resetImportPreview()
  importDialogVisible.value = true
}
function handleImportFileChange(file: UploadFile, _files: UploadFiles) {
  importFile.value = file.raw
  resetImportPreview()
}
function handleImportFileRemove() {
  importFile.value = undefined
  resetImportPreview()
}
async function runImport(preview = false) {
  if (!importForm.organization_id) return ElMessage.warning('请选择所属组织')
  if (!importFile.value) return ElMessage.warning('请选择 CSV 或 XLSX 文件')
  if (!preview && (importResultMode.value !== 'preview' || !importResult.value?.success_count)) {
    return ElMessage.warning('请先完成预检，并确认至少有一行可以导入')
  }
  importLoading.value = true
  try {
    const data = new FormData()
    data.append('organization_id', importForm.organization_id)
    data.append('file', importFile.value)
    const endpoint = preview ? '/business-systems/import/preview' : '/business-systems/import'
    const response = await apiClient.post<{ result: ImportResult }>(endpoint, data, { headers: { 'Content-Type': 'multipart/form-data' }, timeout: 60_000 })
    importResult.value = response.data.result
    importResultMode.value = preview ? 'preview' : 'completed'
    if (preview) {
      ElMessage.success('文件预检完成，尚未写入台账')
    } else {
      ElMessage.success('导入处理完成')
      await refreshAfterMutation()
    }
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, preview ? '导入文件预检失败' : '批量导入业务系统失败')) }
  finally { importLoading.value = false }
}
async function exportLedger(format: 'csv' | 'xlsx') {
  exporting.value = true
  try {
    const response = await apiClient.get(`/business-systems/export?format=${format}`, { responseType: 'blob', timeout: 60_000 })
    const href = URL.createObjectURL(response.data)
    const link = document.createElement('a')
    link.href = href
    link.download = `业务系统台账.${format}`
    link.click()
    URL.revokeObjectURL(href)
    const count = response.headers['x-export-count'] ?? systems.value.length
    ElMessage.success(`已生成并开始下载 ${count} 个业务系统`)
  } catch (error) {
    ElMessage.error(await getDownloadAPIErrorMessage(error, '导出业务系统台账失败'))
  } finally {
    exporting.value = false
  }
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
onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div><span class="eyebrow">BUSINESS SYSTEMS</span><h1>业务系统</h1><p>登记子公司的业务系统、负责人、系统等级以及个人信息和重要数据属性。</p></div>
      <div class="button-row ledger-page-actions business-system-page-actions"><el-dropdown><el-button :loading="exporting">导出台账</el-button><template #dropdown><el-dropdown-menu><el-dropdown-item @click="exportLedger('xlsx')">导出 XLSX</el-dropdown-item><el-dropdown-item @click="exportLedger('csv')">导出 CSV</el-dropdown-item></el-dropdown-menu></template></el-dropdown><el-button @click="openImportHistory">导入记录</el-button><el-button @click="openImportDialog">批量导入</el-button><el-button type="primary" :disabled="eligibleOrganizations.length === 0" @click="openCreateDialog">新增业务系统</el-button></div>
    </div>
    <div class="organization-summary">
      <div><span>符合条件系统</span><strong>{{ total }}</strong></div>
      <div><span>本页含个人信息</span><strong>{{ systems.filter((item) => item.contains_personal_information === 'yes').length }}</strong></div>
      <div><span>本页含敏感个人信息</span><strong>{{ systems.filter((item) => item.contains_sensitive_personal_information === 'yes').length }}</strong></div>
      <div><span>本页超过 10 万条</span><strong>{{ systems.filter((item) => item.data_volume_over_100k === 'yes').length }}</strong></div>
    </div>
    <div class="panel asset-filter-panel">
      <el-input v-model="search" clearable placeholder="搜索系统编码、名称、组织或负责人" @keyup.enter="resetAndLoadSystems" @clear="resetAndLoadSystems" />
      <el-select v-model="statusFilter" clearable placeholder="全部状态" @change="resetAndLoadSystems" @clear="resetAndLoadSystems">
        <el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" />
      </el-select>
      <el-select v-if="auth.isGroupAdmin" v-model="organizationFilter" clearable filterable placeholder="全部组织" @change="resetAndLoadSystems" @clear="resetAndLoadSystems">
        <el-option v-for="item in eligibleOrganizations" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" />
      </el-select>
      <el-button @click="resetAndLoadSystems">查询</el-button>
      <span class="asset-filter-count">共 {{ total }} 条</span>
    </div>
    <div class="panel data-table-panel">
      <div class="batch-toolbar">
        <div><strong>批量操作</strong><span>已选择 {{ selectedSystems.length }} 个业务系统，单次最多 200 个</span></div>
        <div class="button-row"><el-button :disabled="selectedSystems.length === 0" :loading="batchLoading" @click="runBatchArchive(false)">批量恢复</el-button><el-button type="warning" plain :disabled="selectedSystems.length === 0" :loading="batchLoading" @click="runBatchArchive(true)">批量归档</el-button></div>
      </div>
      <el-table v-loading="loading" :data="systems" row-key="id" empty-text="暂无业务系统" @selection-change="handleSelectionChange">
        <el-table-column type="selection" width="46" />
        <el-table-column label="系统" min-width="220"><template #default="scope"><div class="organization-name-cell"><el-button link type="primary" class="table-link" @click="openDetail(scope.row)">{{ scope.row.name }}</el-button><span>{{ scope.row.code }}</span></div></template></el-table-column>
        <el-table-column prop="organization_name" label="所属组织" min-width="180" />
        <el-table-column label="状态/等级" min-width="150"><template #default="scope"><el-tag effect="plain">{{ statusLabel(scope.row.status) }}</el-tag><span class="inline-note">{{ levelLabel(scope.row.level) }}</span></template></el-table-column>
        <el-table-column label="个人信息" min-width="120"><template #default="scope"><el-tag :type="scope.row.contains_personal_information === 'yes' ? 'warning' : 'info'">{{ yesNo(scope.row.contains_personal_information) }}</el-tag></template></el-table-column>
        <el-table-column label="敏感个人信息" min-width="140"><template #default="scope"><el-tag :type="scope.row.contains_sensitive_personal_information === 'yes' ? 'danger' : 'info'">{{ yesNo(scope.row.contains_sensitive_personal_information) }}</el-tag></template></el-table-column>
        <el-table-column label="数据量超 10 万" min-width="135"><template #default="scope">{{ yesNo(scope.row.data_volume_over_100k) }}</template></el-table-column>
        <el-table-column prop="owner_name" label="负责人" min-width="110"><template #default="scope">{{ scope.row.owner_name || '—' }}</template></el-table-column>
        <el-table-column label="操作" fixed="right" width="280"><template #default="scope"><el-button link type="primary" @click="openChangeHistory(scope.row)">历史</el-button><el-button link type="primary" @click="openEditDialog(scope.row)">编辑</el-button><el-button link :type="scope.row.status === 'archived' ? 'success' : 'warning'" @click="changeArchive(scope.row, scope.row.status !== 'archived')">{{ scope.row.status === 'archived' ? '恢复' : '归档' }}</el-button><el-button link type="danger" @click="remove(scope.row)">删除</el-button></template></el-table-column>
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

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="800px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
        <div class="form-section-title">基本信息</div>
        <div class="form-grid two-columns">
          <el-form-item label="所属组织" prop="organization_id"><el-select v-model="form.organization_id" :disabled="!auth.isGroupAdmin" style="width:100%"><el-option v-for="item in eligibleOrganizations" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="系统编码" prop="code"><el-input v-model="form.code" placeholder="如：CRM_001" maxlength="64" /></el-form-item>
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
      <el-alert title="所属组织由当前表单统一指定，导入文件不需要填写组织列；请先选择所属组织再下载模板，模板会包含当前启用的集团自定义字段；系统编码已存在的行会跳过，不覆盖原记录。" type="info" :closable="false" show-icon />
      <div class="import-toolbar"><el-button link type="primary" :loading="downloading" @click="downloadTemplate('xlsx')">下载 XLSX 模板</el-button><el-button link type="primary" :loading="downloading" @click="downloadTemplate('csv')">下载 CSV 模板</el-button></div>
      <el-form label-position="top">
        <el-form-item label="导入到所属组织" required><el-select v-model="importForm.organization_id" filterable :disabled="!auth.isGroupAdmin" @change="resetImportPreview" style="width: 100%"><el-option v-for="item in eligibleOrganizations" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
        <el-form-item label="CSV / XLSX 文件" required><el-upload drag :auto-upload="false" :limit="1" accept=".csv,.xlsx" :on-change="handleImportFileChange" :on-remove="handleImportFileRemove"><el-icon><UploadFilled /></el-icon><div class="el-upload__text">拖入文件，或<em>点击选择</em></div><template #tip><div class="el-upload__tip">文件不超过 10 MB，单次最多 5000 行；允许部分成功。</div></template></el-upload></el-form-item>
      </el-form>
      <div v-if="importResult" class="import-result" data-testid="business-system-import-result">
        <el-alert :title="importResultMode === 'preview' ? '预检结果：尚未写入台账，请确认后执行正式导入' : '正式导入结果'" :type="importResultMode === 'preview' ? 'info' : 'success'" :closable="false" show-icon />
        <div class="import-result-summary"><div><span>总行数</span><strong>{{ importResult.total_count }}</strong></div><div class="success"><span>{{ importResultMode === 'preview' ? '可导入' : '成功' }}</span><strong>{{ importResult.success_count }}</strong></div><div class="duplicate"><span>重复</span><strong>{{ importResult.duplicate_count }}</strong></div><div class="failure"><span>失败</span><strong>{{ importResult.failure_count }}</strong></div></div>
        <el-table v-if="importResult.errors.length" :data="importResult.errors" size="small" max-height="220"><el-table-column prop="row" label="行号" width="70" /><el-table-column prop="code" label="错误代码" width="150" /><el-table-column prop="message" label="处理说明" min-width="260" /></el-table>
      </div>
      <template #footer><el-button @click="importDialogVisible=false">关闭</el-button><el-button :loading="importLoading" @click="runImport(true)">预检文件</el-button><el-button type="primary" :loading="importLoading" :disabled="importResultMode !== 'preview' || !importResult?.success_count" @click="runImport(false)">确认导入</el-button></template>
    </el-dialog>

    <el-dialog v-model="historyDialogVisible" title="业务系统导入记录" width="980px">
      <el-table v-loading="historyLoading" :data="importHistory" size="small" empty-text="暂无导入记录" max-height="520"><el-table-column prop="created_at" label="时间" width="175" /><el-table-column prop="organization_name" label="所属组织" min-width="150" /><el-table-column prop="file_name" label="文件名" min-width="190" /><el-table-column label="结果" width="230"><template #default="scope">总数 {{ scope.row.total_count }} · 成功 {{ scope.row.success_count }} · 重复 {{ scope.row.duplicate_count }} · 失败 {{ scope.row.failure_count }}</template></el-table-column><el-table-column prop="created_by_name" label="操作人" width="110" /></el-table>
      <div class="table-pagination dialog-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="importTotal" :current-page="importPage" :page-size="importPageSize" :page-sizes="[20, 50, 100]" @current-change="handleImportPageChange" @size-change="handleImportPageSizeChange" />
      </div>
      <template #footer><el-button @click="historyDialogVisible = false">关闭</el-button></template>
    </el-dialog>

    <el-drawer v-model="detailVisible" title="业务系统详情" size="620px">
      <div v-loading="detailLoading" class="detail-drawer">
        <template v-if="detail">
          <div class="detail-hero"><div><span class="eyebrow">SYSTEM PROFILE</span><h2>{{ detail.name }}</h2><p>{{ detail.code }} · {{ detail.organization_name }}</p></div><el-tag effect="plain">{{ statusLabel(detail.status) }}</el-tag></div>
          <div class="detail-grid"><div><span>系统等级</span><strong>{{ levelLabel(detail.level) }}</strong></div><div><span>负责人</span><strong>{{ detail.owner_name || '未填写' }}</strong></div><div><span>联系方式</span><strong>{{ detail.owner_contact || '未填写' }}</strong></div><div><span>互联网暴露</span><strong>{{ yesNo(detail.internet_exposure) }}</strong></div><div><span>个人信息</span><strong>{{ yesNo(detail.contains_personal_information) }}</strong></div><div><span>敏感个人信息</span><strong>{{ yesNo(detail.contains_sensitive_personal_information) }}</strong></div><div><span>记录数</span><strong>{{ detail.personal_information_record_count ?? '未填写' }}</strong></div><div><span>超过 10 万条</span><strong>{{ yesNo(detail.data_volume_over_100k) }}</strong></div><div><span>重要数据</span><strong>{{ yesNo(detail.contains_important_data) }}</strong></div><div><span>核心数据</span><strong>{{ yesNo(detail.contains_core_data) }}</strong></div><div><span>部署方式</span><strong>{{ detail.deployment_type || '未填写' }}</strong></div><div><span>存储位置</span><strong>{{ detail.data_storage_location || '未填写' }}</strong></div></div>
          <template v-if="detailCustomFields.length">
            <div class="form-section-title">集团自定义字段</div>
            <div class="detail-grid"><div v-for="field in detailCustomFields" :key="field.id"><span>{{ field.field_name }}</span><strong>{{ customValueLabel(detailCustomValues[field.field_key]) }}</strong></div></div>
          </template>
          <div class="form-section-title">系统说明</div><p class="detail-description">{{ detail.description || '暂无说明' }}</p>
          <div class="form-section-title">关联网络资产（{{ detailAssetTotal }}）</div>
          <el-table v-loading="detailAssetLoading" :data="detailAssets" size="small" empty-text="暂未关联网络资产" max-height="360"><el-table-column label="资产" min-width="210"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.value }}</strong><span>{{ scope.row.name || assetTypeLabel(scope.row.type) }}</span></div></template></el-table-column><el-table-column label="范围" width="80"><template #default="scope">{{ scope.row.network_scope === 'public' ? '公网' : scope.row.network_scope === 'private' ? '内网' : '待确认' }}</template></el-table-column><el-table-column label="状态" width="90"><template #default="scope">{{ assetStatusLabel(scope.row.status) }}</template></el-table-column><el-table-column prop="owner_name" label="负责人" min-width="90" /></el-table>
          <div v-if="detailAssetTotal > 0" class="table-pagination dialog-pagination"><el-pagination background small layout="total, sizes, prev, pager, next" :total="detailAssetTotal" :current-page="detailAssetPage" :page-size="detailAssetPageSize" :page-sizes="[20, 50, 100]" @current-change="handleDetailAssetPageChange" @size-change="handleDetailAssetPageSizeChange" /></div>
          <div class="detail-actions"><el-button @click="openChangeHistory(detail as BusinessSystem)">变更历史</el-button><el-button @click="openEditDialog(detail as BusinessSystem)">编辑系统</el-button><el-button :type="detail.status === 'archived' ? 'success' : 'warning'" @click="changeArchive(detail, detail.status !== 'archived')">{{ detail.status === 'archived' ? '恢复系统' : '归档系统' }}</el-button></div>
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
