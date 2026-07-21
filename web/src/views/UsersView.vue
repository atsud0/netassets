<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, type FormInstance, type FormRules } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface Organization { id: string; code: string; name: string; type: string; status: string }
interface UserItem {
  id: string; username: string; display_name: string; email?: string; status: string
  organization_id?: string; organization_code?: string; organization_name?: string
  role_key?: string; role_name?: string; last_login_at?: string; created_at: string
}
interface ListResponse<T> { items: T[]; total: number }

const loading = ref(false)
const submitting = ref(false)
const dialogVisible = ref(false)
const formRef = ref<FormInstance>()
const users = ref<UserItem[]>([])
const organizations = ref<Organization[]>([])
const eligibleOrganizations = computed(() => organizations.value.filter((item) => item.status === 'enabled' && item.type !== 'group'))
const form = reactive({ username: '', password: '', display_name: '', email: '', organization_id: '', role_key: 'organization_admin' })
const rules: FormRules = {
  username: [
    { required: true, message: '请输入用户名', trigger: 'blur' },
    { pattern: /^[A-Za-z0-9][A-Za-z0-9_.-]{2,63}$/, message: '使用 3-64 位字母、数字、点、下划线或横线', trigger: 'blur' },
  ],
  password: [{ required: true, min: 10, message: '初始密码至少 10 位', trigger: 'blur' }],
  display_name: [{ required: true, message: '请输入姓名', trigger: 'blur' }],
  organization_id: [{ required: true, message: '请选择所属组织', trigger: 'change' }],
  role_key: [{ required: true, message: '请选择角色', trigger: 'change' }],
}

async function load() {
  loading.value = true
  try {
    const [userResponse, organizationResponse] = await Promise.all([
      apiClient.get<ListResponse<UserItem>>('/users'),
      apiClient.get<ListResponse<Organization>>('/organizations'),
    ])
    users.value = userResponse.data.items
    organizations.value = organizationResponse.data.items
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取用户数据失败'))
  } finally { loading.value = false }
}
function openCreateDialog() {
  Object.assign(form, { username: '', password: '', display_name: '', email: '', organization_id: eligibleOrganizations.value[0]?.id ?? '', role_key: 'organization_admin' })
  dialogVisible.value = true
}
async function submit() {
  if (!formRef.value || !(await formRef.value.validate())) return
  submitting.value = true
  try {
    await apiClient.post('/users', form)
    dialogVisible.value = false
    ElMessage.success('子公司用户已新增')
    await load()
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '新增用户失败')) }
  finally { submitting.value = false }
}
function formatTime(value?: string) { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : '尚未登录' }
onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div><span class="eyebrow">USER ACCESS</span><h1>用户与权限</h1><p>集团为子公司创建账号并绑定所属组织，子公司账号只能访问授权范围内的数据。</p></div>
      <el-button type="primary" :disabled="eligibleOrganizations.length === 0" @click="openCreateDialog">新增子公司用户</el-button>
    </div>
    <div class="panel data-table-panel">
      <el-table v-loading="loading" :data="users" row-key="id" empty-text="暂无用户">
        <el-table-column label="用户" min-width="180"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.display_name }}</strong><span>{{ scope.row.username }}</span></div></template></el-table-column>
        <el-table-column prop="organization_name" label="所属组织" min-width="190"><template #default="scope">{{ scope.row.organization_name || '—' }}</template></el-table-column>
        <el-table-column prop="role_name" label="角色" min-width="140"><template #default="scope"><el-tag effect="plain">{{ scope.row.role_name || '未分配' }}</el-tag></template></el-table-column>
        <el-table-column prop="email" label="邮箱" min-width="190"><template #default="scope">{{ scope.row.email || '—' }}</template></el-table-column>
        <el-table-column label="最近登录" min-width="170"><template #default="scope">{{ formatTime(scope.row.last_login_at) }}</template></el-table-column>
        <el-table-column label="状态" width="90"><template #default="scope"><el-tag :type="scope.row.status === 'enabled' ? 'success' : 'info'">{{ scope.row.status === 'enabled' ? '启用' : '停用' }}</el-tag></template></el-table-column>
      </el-table>
    </div>

    <el-dialog v-model="dialogVisible" title="新增子公司用户" width="620px" destroy-on-close>
      <el-form ref="formRef" :model="form" :rules="rules" label-position="top">
        <div class="form-grid two-columns">
          <el-form-item label="所属组织" prop="organization_id"><el-select v-model="form.organization_id" placeholder="请选择" style="width:100%"><el-option v-for="item in eligibleOrganizations" :key="item.id" :label="`${item.name}（${item.code}）`" :value="item.id" /></el-select></el-form-item>
          <el-form-item label="角色" prop="role_key"><el-select v-model="form.role_key" style="width:100%"><el-option label="子公司管理员" value="organization_admin" /><el-option label="子公司填报员" value="organization_editor" /></el-select></el-form-item>
          <el-form-item label="用户名" prop="username"><el-input v-model="form.username" placeholder="如：subsidiary_admin" maxlength="64" /></el-form-item>
          <el-form-item label="姓名" prop="display_name"><el-input v-model="form.display_name" placeholder="请输入姓名" maxlength="128" /></el-form-item>
          <el-form-item label="初始密码" prop="password"><el-input v-model="form.password" type="password" show-password placeholder="至少 10 位" /></el-form-item>
          <el-form-item label="邮箱"><el-input v-model="form.email" placeholder="可选" maxlength="255" /></el-form-item>
        </div>
      </el-form>
      <template #footer><el-button @click="dialogVisible=false">取消</el-button><el-button type="primary" :loading="submitting" @click="submit">确认新增</el-button></template>
    </el-dialog>
  </section>
</template>
