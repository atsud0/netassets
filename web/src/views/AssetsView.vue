<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules, type UploadFile, type UploadFiles } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

interface Organization { id: string; code: string; name: string; type: string; status: string }
interface BusinessSystem { id: string; code: string; name: string; organization_id: string; organization_name: string }
interface Asset {
  id: string; type: string; value: string; normalized_value: string; name?: string
  organization_id: string; organization_code: string; organization_name: string
  business_system_id?: string; business_system_code?: string; business_system_name?: string
  environment: string; network_scope: string; status: string; importance: string; source: string
  owner_name?: string; notes?: string; created_at: string; updated_at: string
}
interface ImportError { row: number; code: string; message: string }
interface ImportResult {
  id: string; file_name: string; file_type: string; status: string; total_count: number
  success_count: number; failure_count: number; duplicate_count: number; errors: ImportError[]
}
interface ImportHistory extends ImportResult {
  organization_id: string; organization_code: string; organization_name: string
  created_by_name?: string; created_at: string
}
interface ListResponse<T> { items: T[]; total: number }

const auth = useAuthStore()
const loading = ref(false)
const submitting = ref(false)
const importing = ref(false)
const downloading = ref(false)
const historyLoading = ref(false)
const editDialogVisible = ref(false)
const importDialogVisible = ref(false)
const historyDialogVisible = ref(false)
const editingAssetID = ref('')
const formRef = ref<FormInstance>()
const assets = ref<Asset[]>([])
const organizations = ref<Organization[]>([])
const systems = ref<BusinessSystem[]>([])
const importHistory = ref<ImportHistory[]>([])
const importFile = ref<File>()
const importResult = ref<ImportResult>()
const search = ref('')
const typeFilter = ref('')
const statusFilter = ref('')
const organizationFilter = ref('')

const form = reactive({
  type: 'domain', value: '', name: '', organization_id: '', business_system_id: '',
  environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', owner_name: '', notes: '',
})
const importForm = reactive({ organization_id: '' })
const rules: FormRules = {
  type: [{ required: true, message: '请选择资产类型', trigger: 'change' }],
  value: [{ required: true, message: '请输入资产值', trigger: 'blur' }],
  organization_id: [{ required: true, message: '请选择所属组织', trigger: 'change' }],
}

const enabledOrganizations = computed(() => organizations.value.filter((item) => item.status === 'enabled'))
const availableSystems = computed(() => systems.value.filter((item) => item.organization_id === form.organization_id))
const dialogTitle = computed(() => editingAssetID.value ? '编辑网络资产' : '新增网络资产')
const submitLabel = computed(() => editingAssetID.value ? '保存修改' : '确认新增')
const filteredAssets = computed(() => {
  const keyword = search.value.trim().toLowerCase()
  return assets.value.filter((item) => {
    if (typeFilter.value && item.type !== typeFilter.value) return false
    if (statusFilter.value && item.status !== statusFilter.value) return false
    if (organizationFilter.value && item.organization_id !== organizationFilter.value) return false
    if (!keyword) return true
    return [item.value, item.name, item.organization_name, item.business_system_name, item.business_system_code, item.owner_name]
      .some((value) => value?.toLowerCase().includes(keyword))
  })
})
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

watch(() => form.organization_id, () => {
  if (!availableSystems.value.some((item) => item.id === form.business_system_id)) form.business_system_id = ''
})

async function load() {
  loading.value = true
  try {
    const [assetResponse, organizationResponse, systemResponse] = await Promise.all([
      apiClient.get<ListResponse<Asset>>('/assets'),
      apiClient.get<ListResponse<Organization>>('/organizations'),
      apiClient.get<ListResponse<BusinessSystem>>('/business-systems'),
    ])
    assets.value = assetResponse.data.items
    organizations.value = organizationResponse.data.items
    systems.value = systemResponse.data.items
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取网络资产失败'))
  } finally {
    loading.value = false
  }
}
function defaultOrganizationID() {
  return auth.user?.organization?.id ?? enabledOrganizations.value[0]?.id ?? ''
}
function resetForm() {
  Object.assign(form, {
    type: 'domain', value: '', name: '', organization_id: defaultOrganizationID(), business_system_id: '',
    environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', owner_name: '', notes: '',
  })
}
function openCreateDialog() {
  editingAssetID.value = ''
  resetForm()
  editDialogVisible.value = true
}
function openEditDialog(item: Asset) {
  editingAssetID.value = item.id
  Object.assign(form, {
    type: item.type, value: item.value, name: item.name ?? '', organization_id: item.organization_id,
    business_system_id: item.business_system_id ?? '', environment: item.environment, network_scope: item.network_scope,
    status: item.status, importance: item.importance, owner_name: item.owner_name ?? '', notes: item.notes ?? '',
  })
  editDialogVisible.value = true
}
function assetPayload(item: Asset, overrides: Partial<typeof form> = {}) {
  return {
    type: item.type, value: item.value, name: item.name ?? '', organization_id: item.organization_id,
    business_system_id: item.business_system_id ?? '', environment: item.environment, network_scope: item.network_scope,
    status: item.status, importance: item.importance, owner_name: item.owner_name ?? '', notes: item.notes ?? '', ...overrides,
  }
}
async function submit() {
  if (!formRef.value || !(await formRef.value.validate())) return
  submitting.value = true
  try {
    if (editingAssetID.value) {
      await apiClient.patch(`/assets/${editingAssetID.value}`, form)
      ElMessage.success('网络资产已更新')
    } else {
      await apiClient.post('/assets', form)
      ElMessage.success('网络资产已新增')
    }
    editDialogVisible.value = false
    await load()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, editingAssetID.value ? '编辑网络资产失败' : '新增网络资产失败'))
  } finally {
    submitting.value = false
  }
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
    await load()
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
    await load()
  } catch (error) {
    if (error === 'cancel' || error === 'close') return
    ElMessage.error(getAPIErrorMessage(error, '删除网络资产失败'))
  }
}
function openImportDialog() {
  importForm.organization_id = defaultOrganizationID()
  importFile.value = undefined
  importResult.value = undefined
  importDialogVisible.value = true
}
function handleFileChange(uploadFile: UploadFile, _uploadFiles: UploadFiles) { importFile.value = uploadFile.raw }
function handleFileRemove() { importFile.value = undefined }
async function runImport() {
  if (!importForm.organization_id) return ElMessage.warning('请选择所属组织')
  if (!importFile.value) return ElMessage.warning('请选择 CSV 或 XLSX 文件')
  importing.value = true
  try {
    const data = new FormData()
    data.append('organization_id', importForm.organization_id)
    data.append('file', importFile.value)
    const response = await apiClient.post<{ result: ImportResult }>('/assets/import', data, {
      headers: { 'Content-Type': 'multipart/form-data' }, timeout: 60_000,
    })
    importResult.value = response.data.result
    ElMessage.success('导入处理完成')
    await load()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '批量导入失败'))
  } finally {
    importing.value = false
  }
}
async function openImportHistory() {
  historyDialogVisible.value = true
  historyLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<ImportHistory>>('/assets/imports')
    importHistory.value = response.data.items
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取导入记录失败'))
  } finally {
    historyLoading.value = false
  }
}
async function downloadTemplate(format: 'csv' | 'xlsx') {
  downloading.value = true
  try {
    const response = await apiClient.get(`/assets/import-template?format=${format}`, { responseType: 'blob' })
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

onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
        <span class="eyebrow">NETWORK ASSETS</span>
        <h1>网络资产</h1>
        <p>集团统一维护域名、IP、URL 和网络服务，子公司仅管理授权组织范围内的数据。</p>
      </div>
      <div class="button-row">
        <el-button @click="openImportHistory">导入记录</el-button>
        <el-dropdown split-button :loading="downloading" @click="openImportDialog">
          批量导入
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item @click="downloadTemplate('xlsx')">下载 XLSX 模板</el-dropdown-item>
              <el-dropdown-item @click="downloadTemplate('csv')">下载 CSV 模板</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
        <el-button type="primary" @click="openCreateDialog">新增资产</el-button>
      </div>
    </div>

    <div class="organization-summary">
      <div><span>资产总数</span><strong>{{ assets.length }}</strong></div>
      <div><span>启用资产</span><strong>{{ activeCount }}</strong></div>
      <div><span>公网资产</span><strong>{{ publicCount }}</strong></div>
      <div><span>域名资产</span><strong>{{ domainCount }}</strong></div>
    </div>

    <div class="panel asset-filter-panel">
      <el-input v-model="search" clearable placeholder="搜索资产值、名称、系统或负责人" />
      <el-select v-model="typeFilter" clearable placeholder="全部类型">
        <el-option v-for="(label, value) in typeLabels" :key="value" :label="label" :value="value" />
      </el-select>
      <el-select v-model="statusFilter" clearable placeholder="全部状态">
        <el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" />
      </el-select>
      <el-select v-if="auth.isGroupAdmin" v-model="organizationFilter" clearable filterable placeholder="全部组织">
        <el-option v-for="item in enabledOrganizations" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" />
      </el-select>
      <span class="asset-filter-count">当前 {{ filteredAssets.length }} 条</span>
    </div>

    <div class="panel data-table-panel" v-loading="loading">
      <el-table :data="filteredAssets" empty-text="暂无网络资产">
        <el-table-column label="资产" min-width="230">
          <template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.name || scope.row.value }}</strong><span v-if="scope.row.name">{{ scope.row.value }}</span></div></template>
        </el-table-column>
        <el-table-column label="类型" width="95"><template #default="scope">{{ typeLabel(scope.row.type) }}</template></el-table-column>
        <el-table-column label="所属组织" min-width="150"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.organization_name }}</strong><span>{{ scope.row.organization_code }}</span></div></template></el-table-column>
        <el-table-column label="业务系统" min-width="150"><template #default="scope"><div v-if="scope.row.business_system_id" class="organization-name-cell"><strong>{{ scope.row.business_system_name }}</strong><span>{{ scope.row.business_system_code }}</span></div><span v-else class="muted-text">未绑定</span></template></el-table-column>
        <el-table-column label="环境 / 范围" width="120"><template #default="scope">{{ environmentLabel(scope.row.environment) }} / {{ scopeLabel(scope.row.network_scope) }}</template></el-table-column>
        <el-table-column label="状态" width="90"><template #default="scope"><el-tag :type="statusTag(scope.row.status)" effect="plain">{{ statusLabel(scope.row.status) }}</el-tag></template></el-table-column>
        <el-table-column label="重要程度" width="95"><template #default="scope"><el-tag :type="importanceTag(scope.row.importance)" effect="plain">{{ importanceLabel(scope.row.importance) }}</el-tag></template></el-table-column>
        <el-table-column label="来源" width="75"><template #default="scope">{{ sourceLabel(scope.row.source) }}</template></el-table-column>
        <el-table-column label="操作" width="180" fixed="right">
          <template #default="scope">
            <el-button link type="primary" @click="openEditDialog(scope.row)">编辑</el-button>
            <el-button link :type="scope.row.status === 'archived' ? 'success' : 'warning'" @click="toggleArchive(scope.row)">{{ scope.row.status === 'archived' ? '启用' : '归档' }}</el-button>
            <el-button link type="danger" @click="removeAsset(scope.row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="editDialogVisible" :title="dialogTitle" width="720px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
        <div class="form-grid two-columns">
          <el-form-item label="资产类型" prop="type"><el-select v-model="form.type" style="width: 100%"><el-option v-for="(label, value) in typeLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="资产值" prop="value"><el-input v-model="form.value" :placeholder="valuePlaceholder" /></el-form-item>
          <el-form-item label="资产名称"><el-input v-model="form.name" placeholder="便于识别的名称（可选）" /></el-form-item>
          <el-form-item label="所属组织" prop="organization_id"><el-select v-model="form.organization_id" filterable :disabled="!auth.isGroupAdmin" style="width: 100%"><el-option v-for="item in enabledOrganizations" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="关联业务系统"><el-select v-model="form.business_system_id" clearable filterable placeholder="可选" style="width: 100%"><el-option v-for="item in availableSystems" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="负责人"><el-input v-model="form.owner_name" placeholder="姓名或团队" /></el-form-item>
          <el-form-item label="环境"><el-select v-model="form.environment" style="width: 100%"><el-option v-for="(label, value) in environmentLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="网络范围"><el-select v-model="form.network_scope" style="width: 100%"><el-option v-for="(label, value) in scopeLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="重要程度"><el-select v-model="form.importance" style="width: 100%"><el-option v-for="(label, value) in importanceLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="资产状态"><el-select v-model="form.status" style="width: 100%"><el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" /></el-select></el-form-item>
          <el-form-item label="备注" class="full-span"><el-input v-model="form.notes" type="textarea" :rows="3" placeholder="补充资产用途、管理要求等信息" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="editDialogVisible = false">取消</el-button><el-button type="primary" :loading="submitting" @click="submit">{{ submitLabel }}</el-button></template>
    </el-dialog>

    <el-dialog v-model="importDialogVisible" title="批量导入网络资产" width="760px" destroy-on-close>
      <el-alert title="单次最多导入 5000 行，资产类型和值在集团范围内去重；允许部分成功。" type="info" :closable="false" show-icon />
      <div class="import-toolbar"><el-button link type="primary" @click="downloadTemplate('xlsx')">下载 XLSX 模板</el-button><el-button link type="primary" @click="downloadTemplate('csv')">下载 CSV 模板</el-button></div>
      <el-form label-position="top">
        <el-form-item label="导入到所属组织" required><el-select v-model="importForm.organization_id" filterable :disabled="!auth.isGroupAdmin" style="width: 100%"><el-option v-for="item in enabledOrganizations" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
        <el-form-item label="CSV / XLSX 文件" required><el-upload drag :auto-upload="false" :limit="1" accept=".csv,.xlsx" :on-change="handleFileChange" :on-remove="handleFileRemove"><div class="el-upload__text">拖入文件，或<em>点击选择</em></div><template #tip><div class="el-upload__tip">文件不超过 10 MB；业务系统使用同组织内的系统编码匹配。</div></template></el-upload></el-form-item>
      </el-form>
      <div v-if="importResult" class="import-result" data-testid="import-result">
        <div class="import-result-summary"><div><span>总行数</span><strong>{{ importResult.total_count }}</strong></div><div class="success"><span>成功</span><strong>{{ importResult.success_count }}</strong></div><div class="duplicate"><span>重复</span><strong>{{ importResult.duplicate_count }}</strong></div><div class="failure"><span>失败</span><strong>{{ importResult.failure_count }}</strong></div></div>
        <el-table v-if="importResult.errors.length" :data="importResult.errors" max-height="220" size="small"><el-table-column prop="row" label="行号" width="75" /><el-table-column prop="message" label="处理结果" /></el-table>
      </div>
      <template #footer><el-button @click="importDialogVisible = false">关闭</el-button><el-button type="primary" :loading="importing" @click="runImport">开始导入</el-button></template>
    </el-dialog>

    <el-dialog v-model="historyDialogVisible" title="资产导入记录" width="980px">
      <el-table v-loading="historyLoading" :data="importHistory" empty-text="暂无导入记录" max-height="560">
        <el-table-column label="文件" min-width="190"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.file_name }}</strong><span>{{ scope.row.file_type.toUpperCase() }}</span></div></template></el-table-column>
        <el-table-column label="所属组织" min-width="170"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.organization_name }}</strong><span>{{ scope.row.organization_code }}</span></div></template></el-table-column>
        <el-table-column label="状态" width="105"><template #default="scope"><el-tag :type="importStatusTag(scope.row.status)" effect="plain">{{ importStatusLabels[scope.row.status] ?? scope.row.status }}</el-tag></template></el-table-column>
        <el-table-column prop="total_count" label="总数" width="70" /><el-table-column prop="success_count" label="成功" width="70" /><el-table-column prop="duplicate_count" label="重复" width="70" /><el-table-column prop="failure_count" label="失败" width="70" />
        <el-table-column label="导入人 / 时间" min-width="160"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.created_by_name || '-' }}</strong><span>{{ formatDate(scope.row.created_at) }}</span></div></template></el-table-column>
      </el-table>
      <template #footer><el-button @click="historyDialogVisible = false">关闭</el-button></template>
    </el-dialog>
  </section>
</template>
