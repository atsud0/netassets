<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

interface Organization {
  id: string
  parent_id?: string
  parent_name?: string
  code: string
  name: string
  short_name?: string
  type: 'group' | 'subsidiary' | 'department' | 'other'
  manager?: string
  contact?: string
  status: 'enabled' | 'disabled'
  notes?: string
  child_count: number
  business_system_count: number
  asset_count: number
  created_at: string
}

interface OrganizationListResponse { items: Organization[]; total: number }
interface OrganizationCreateResponse { item: Organization }

const auth = useAuthStore()
const loading = ref(false)
const submitting = ref(false)
const groupSubmitting = ref(false)
const dialogVisible = ref(false)
const groupDialogVisible = ref(false)
const organizations = ref<Organization[]>([])
const formRef = ref<FormInstance>()
const groupFormRef = ref<FormInstance>()
const form = reactive({
  parent_id: '',
  code: '',
  name: '',
  short_name: '',
  type: 'subsidiary' as 'subsidiary' | 'department' | 'other',
  manager: '',
  contact: '',
  notes: '',
})
const groupForm = reactive({ name: '', short_name: '', manager: '', contact: '', notes: '' })
const groupRules: FormRules = {
  name: [{ required: true, message: '请输入集团名称', trigger: 'blur' }],
}
const rules: FormRules = {
  parent_id: [{ required: true, message: '请选择上级组织', trigger: 'change' }],
  code: [
    { required: true, message: '请输入组织编码', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9][A-Za-z0-9_-]{1,63}$/, message: '使用 2-64 位字母、数字、下划线或横线', trigger: 'blur' },
  ],
  name: [{ required: true, message: '请输入组织名称', trigger: 'blur' }],
  type: [{ required: true, message: '请选择组织类型', trigger: 'change' }],
}

const enabledParents = computed(() => organizations.value.filter((item) => item.status === 'enabled'))
const rootOrganization = computed(() => organizations.value.find((item) => item.type === 'group'))
const typeLabels: Record<Organization['type'], string> = {
  group: '集团', subsidiary: '子公司', department: '部门', other: '其他',
}

async function loadOrganizations() {
  loading.value = true
  try {
    const response = await apiClient.get<OrganizationListResponse>('/organizations')
    organizations.value = response.data.items
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取组织机构失败'))
  } finally {
    loading.value = false
  }
}

function openCreateDialog() {
  Object.assign(form, {
    parent_id: rootOrganization.value?.id ?? '',
    code: '', name: '', short_name: '', type: 'subsidiary', manager: '', contact: '', notes: '',
  })
  dialogVisible.value = true
}

function openGroupEditDialog() {
  if (!rootOrganization.value) return
  Object.assign(groupForm, {
    name: rootOrganization.value.name,
    short_name: rootOrganization.value.short_name ?? '',
    manager: rootOrganization.value.manager ?? '',
    contact: rootOrganization.value.contact ?? '',
    notes: rootOrganization.value.notes ?? '',
  })
  groupDialogVisible.value = true
}

async function submitGroupInfo() {
  if (!rootOrganization.value || !groupFormRef.value || !(await groupFormRef.value.validate())) return
  groupSubmitting.value = true
  try {
    await apiClient.patch(`/organizations/${rootOrganization.value.id}`, groupForm)
    groupDialogVisible.value = false
    await Promise.all([loadOrganizations(), auth.fetchCurrentUser()])
    ElMessage.success('集团基本信息已更新')
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '更新集团基本信息失败'))
  } finally {
    groupSubmitting.value = false
  }
}

async function submit() {
  if (!formRef.value || !(await formRef.value.validate())) return
  submitting.value = true
  try {
    const response = await apiClient.post<OrganizationCreateResponse>('/organizations', form)
    organizations.value.push(response.data.item)
    dialogVisible.value = false
    ElMessage.success('组织机构已新增')
    await loadOrganizations()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '新增组织机构失败'))
  } finally {
    submitting.value = false
  }
}

onMounted(loadOrganizations)
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
        <span class="eyebrow">ORGANIZATIONS</span>
        <h1>组织机构</h1>
        <p>集团统一维护组织层级，子公司的业务系统和网络资产归属到对应组织。</p>
      </div>
      <div v-if="auth.isGroupAdmin" class="button-row">
        <el-button :disabled="!rootOrganization" @click="openGroupEditDialog">编辑集团信息</el-button>
        <el-button type="primary" @click="openCreateDialog">新增组织</el-button>
      </div>
    </div>

    <div class="organization-summary">
      <div><span>组织总数</span><strong>{{ organizations.length }}</strong></div>
      <div><span>子公司</span><strong>{{ organizations.filter((item) => item.type === 'subsidiary').length }}</strong></div>
      <div><span>业务系统</span><strong>{{ organizations.reduce((sum, item) => sum + item.business_system_count, 0) }}</strong></div>
      <div><span>网络资产</span><strong>{{ organizations.reduce((sum, item) => sum + item.asset_count, 0) }}</strong></div>
    </div>

    <div class="panel organization-table-panel">
      <el-table v-loading="loading" :data="organizations" row-key="id" empty-text="暂无组织数据">
        <el-table-column label="组织名称" min-width="220">
          <template #default="scope">
            <div class="organization-name-cell">
              <strong>{{ scope.row.name }}</strong>
              <span>{{ scope.row.code }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="parent_name" label="上级组织" min-width="170">
          <template #default="scope">{{ scope.row.parent_name || '—' }}</template>
        </el-table-column>
        <el-table-column label="类型" width="110">
          <template #default="scope"><el-tag effect="plain">{{ typeLabels[scope.row.type as Organization['type']] }}</el-tag></template>
        </el-table-column>
        <el-table-column prop="manager" label="负责人" min-width="120">
          <template #default="scope">{{ scope.row.manager || '—' }}</template>
        </el-table-column>
        <el-table-column prop="contact" label="联系方式" min-width="170">
          <template #default="scope">{{ scope.row.contact || '—' }}</template>
        </el-table-column>
        <el-table-column label="系统 / 资产" width="120" align="center">
          <template #default="scope">{{ scope.row.business_system_count }} / {{ scope.row.asset_count }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="scope">
            <el-tag :type="scope.row.status === 'enabled' ? 'success' : 'info'">{{ scope.row.status === 'enabled' ? '启用' : '停用' }}</el-tag>
          </template>
        </el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="groupDialogVisible" title="编辑集团基本信息" width="620px" destroy-on-close>
      <el-alert title="集团编码和组织类型属于系统根标识，不在此处修改；集团名称等信息保存后会同步显示在顶部导航。" type="info" :closable="false" show-icon />
      <el-form ref="groupFormRef" :model="groupForm" :rules="groupRules" label-position="top" class="dialog-form-with-alert">
        <div class="form-grid two-columns">
          <el-form-item label="集团名称" prop="name"><el-input v-model="groupForm.name" maxlength="255" placeholder="请输入集团完整名称" /></el-form-item>
          <el-form-item label="集团简称"><el-input v-model="groupForm.short_name" maxlength="128" placeholder="用于页面简短展示" /></el-form-item>
          <el-form-item label="集团负责人"><el-input v-model="groupForm.manager" maxlength="128" /></el-form-item>
          <el-form-item label="联系方式"><el-input v-model="groupForm.contact" maxlength="255" placeholder="邮箱、电话或内部通讯账号" /></el-form-item>
          <el-form-item label="集团说明" class="full-span"><el-input v-model="groupForm.notes" type="textarea" :rows="3" maxlength="5000" show-word-limit /></el-form-item>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="groupDialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="groupSubmitting" @click="submitGroupInfo">保存集团信息</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="dialogVisible" title="新增组织机构" width="620px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
        <div class="form-grid two-columns">
          <el-form-item label="上级组织" prop="parent_id">
            <el-select v-model="form.parent_id" placeholder="请选择" style="width: 100%">
              <el-option v-for="item in enabledParents" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" />
            </el-select>
          </el-form-item>
          <el-form-item label="组织类型" prop="type">
            <el-select v-model="form.type" style="width: 100%">
              <el-option label="子公司" value="subsidiary" />
              <el-option label="部门" value="department" />
              <el-option label="其他" value="other" />
            </el-select>
          </el-form-item>
          <el-form-item label="组织编码" prop="code">
            <el-input v-model="form.code" maxlength="64" placeholder="如：SUBSIDIARY_01" />
          </el-form-item>
          <el-form-item label="组织名称" prop="name">
            <el-input v-model="form.name" maxlength="255" placeholder="请输入完整名称" />
          </el-form-item>
          <el-form-item label="组织简称">
            <el-input v-model="form.short_name" maxlength="128" />
          </el-form-item>
          <el-form-item label="负责人">
            <el-input v-model="form.manager" maxlength="128" />
          </el-form-item>
          <el-form-item label="联系方式" class="full-span">
            <el-input v-model="form.contact" maxlength="255" placeholder="邮箱、电话或内部通讯账号" />
          </el-form-item>
          <el-form-item label="备注" class="full-span">
            <el-input v-model="form.notes" type="textarea" :rows="3" maxlength="1000" show-word-limit />
          </el-form-item>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">确认新增</el-button>
      </template>
    </el-dialog>
  </section>
</template>
