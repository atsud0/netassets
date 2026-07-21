<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

interface Organization { id: string; code: string; name: string; type: string; status: string }
interface BusinessSystem {
  id: string; code: string; name: string; short_name?: string; organization_id: string; organization_name: string
  owner_name?: string; status: string; level: string; internet_exposure: string
  contains_personal_information: string; contains_sensitive_personal_information: string
  personal_information_record_count?: number; data_volume_over_100k: string
  contains_important_data: string; contains_core_data: string; created_at: string
}
interface ListResponse<T> { items: T[]; total: number }

const auth = useAuthStore()
const loading = ref(false)
const submitting = ref(false)
const dialogVisible = ref(false)
const formRef = ref<FormInstance>()
const systems = ref<BusinessSystem[]>([])
const organizations = ref<Organization[]>([])
const eligibleOrganizations = computed(() => organizations.value.filter((item) => item.status === 'enabled'))
const form = reactive({
  code: '', name: '', short_name: '', organization_id: '', owner_name: '', owner_contact: '',
  status: 'running', level: 'normal', deployment_type: '', internet_exposure: 'unknown', description: '',
  contains_personal_information: 'unknown', contains_sensitive_personal_information: 'unknown',
  personal_information_record_count: undefined as number | undefined, data_volume_over_100k: 'unknown',
  contains_important_data: 'unknown', contains_core_data: 'unknown', data_storage_location: '', security_notes: '',
})
const rules: FormRules = {
  code: [{ required: true, message: '请输入系统编码', trigger: 'blur' }, { pattern: /^[A-Za-z0-9][A-Za-z0-9_-]{1,63}$/, message: '使用 2-64 位字母、数字、下划线或横线', trigger: 'blur' }],
  name: [{ required: true, message: '请输入系统名称', trigger: 'blur' }],
  organization_id: [{ required: true, message: '请选择所属组织', trigger: 'change' }],
}
const yesNoLabels: Record<string, string> = { yes: '是', no: '否', unknown: '待确认', pending: '待认定' }
const statusLabels: Record<string, string> = { planning: '规划中', running: '运行中', suspended: '暂停', offline: '已下线', archived: '已归档' }
const levelLabels: Record<string, string> = { normal: '一般', important: '重要', core: '核心' }

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
async function load() {
  loading.value = true
  try {
    const [systemResponse, organizationResponse] = await Promise.all([
      apiClient.get<ListResponse<BusinessSystem>>('/business-systems'),
      apiClient.get<ListResponse<Organization>>('/organizations'),
    ])
    systems.value = systemResponse.data.items
    organizations.value = organizationResponse.data.items
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '读取业务系统失败')) }
  finally { loading.value = false }
}
function openCreateDialog() {
  Object.assign(form, {
    code: '', name: '', short_name: '', organization_id: auth.user?.organization?.id ?? eligibleOrganizations.value[0]?.id ?? '',
    owner_name: '', owner_contact: '', status: 'running', level: 'normal', deployment_type: '', internet_exposure: 'unknown', description: '',
    contains_personal_information: 'unknown', contains_sensitive_personal_information: 'unknown', personal_information_record_count: undefined,
    data_volume_over_100k: 'unknown', contains_important_data: 'unknown', contains_core_data: 'unknown', data_storage_location: '', security_notes: '',
  })
  dialogVisible.value = true
}
async function submit() {
  if (!formRef.value || !(await formRef.value.validate())) return
  submitting.value = true
  try {
    await apiClient.post('/business-systems', form)
    dialogVisible.value = false
    ElMessage.success('业务系统已新增')
    await load()
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '新增业务系统失败')) }
  finally { submitting.value = false }
}
function yesNo(value: string) { return yesNoLabels[value] ?? value }
function statusLabel(value: string) { return statusLabels[value] ?? value }
function levelLabel(value: string) { return levelLabels[value] ?? value }
onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div><span class="eyebrow">BUSINESS SYSTEMS</span><h1>业务系统</h1><p>登记子公司的业务系统、负责人、系统等级以及个人信息和重要数据属性。</p></div>
      <el-button type="primary" :disabled="eligibleOrganizations.length === 0" @click="openCreateDialog">新增业务系统</el-button>
    </div>
    <div class="organization-summary">
      <div><span>系统总数</span><strong>{{ systems.length }}</strong></div>
      <div><span>含个人信息</span><strong>{{ systems.filter((item) => item.contains_personal_information === 'yes').length }}</strong></div>
      <div><span>含敏感个人信息</span><strong>{{ systems.filter((item) => item.contains_sensitive_personal_information === 'yes').length }}</strong></div>
      <div><span>超过 10 万条</span><strong>{{ systems.filter((item) => item.data_volume_over_100k === 'yes').length }}</strong></div>
    </div>
    <div class="panel data-table-panel">
      <el-table v-loading="loading" :data="systems" row-key="id" empty-text="暂无业务系统">
        <el-table-column label="系统" min-width="220"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.name }}</strong><span>{{ scope.row.code }}</span></div></template></el-table-column>
        <el-table-column prop="organization_name" label="所属组织" min-width="180" />
        <el-table-column label="状态/等级" min-width="150"><template #default="scope"><el-tag effect="plain">{{ statusLabel(scope.row.status) }}</el-tag><span class="inline-note">{{ levelLabel(scope.row.level) }}</span></template></el-table-column>
        <el-table-column label="个人信息" min-width="120"><template #default="scope"><el-tag :type="scope.row.contains_personal_information === 'yes' ? 'warning' : 'info'">{{ yesNo(scope.row.contains_personal_information) }}</el-tag></template></el-table-column>
        <el-table-column label="敏感个人信息" min-width="140"><template #default="scope"><el-tag :type="scope.row.contains_sensitive_personal_information === 'yes' ? 'danger' : 'info'">{{ yesNo(scope.row.contains_sensitive_personal_information) }}</el-tag></template></el-table-column>
        <el-table-column label="数据量超 10 万" min-width="135"><template #default="scope">{{ yesNo(scope.row.data_volume_over_100k) }}</template></el-table-column>
        <el-table-column prop="owner_name" label="负责人" min-width="110"><template #default="scope">{{ scope.row.owner_name || '—' }}</template></el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="dialogVisible" title="新增业务系统" width="800px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
        <div class="form-section-title">基本信息</div>
        <div class="form-grid two-columns">
          <el-form-item label="所属组织" prop="organization_id"><el-select v-model="form.organization_id" :disabled="!auth.isGroupAdmin" style="width:100%"><el-option v-for="item in eligibleOrganizations" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="系统编码" prop="code"><el-input v-model="form.code" placeholder="如：CRM_001" maxlength="64" /></el-form-item>
          <el-form-item label="系统名称" prop="name"><el-input v-model="form.name" placeholder="请输入系统名称" maxlength="255" /></el-form-item>
          <el-form-item label="系统简称"><el-input v-model="form.short_name" maxlength="128" /></el-form-item>
          <el-form-item label="运行状态"><el-select v-model="form.status" style="width:100%"><el-option label="运行中" value="running" /><el-option label="规划中" value="planning" /><el-option label="暂停" value="suspended" /><el-option label="已下线" value="offline" /></el-select></el-form-item>
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
      </el-form>
      <template #footer><el-button @click="dialogVisible=false">取消</el-button><el-button type="primary" :loading="submitting" @click="submit">确认新增</el-button></template>
    </el-dialog>
  </section>
</template>
