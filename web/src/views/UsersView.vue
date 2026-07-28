<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

interface Organization { id: string; code: string; name: string; type: string; status: string }
interface Permission { key: string; name: string; description?: string }
interface Role { id: string; key: string; name: string; scope: string; description?: string; is_system: boolean; status: 'enabled' | 'disabled'; permissions: Permission[]; user_count: number }
interface RoleAssignment { id: string; role_id: string; role_key: string; role_name: string; role_scope: string; organization_id?: string; data_scope: string }
interface Appointment { organization: Organization; is_primary: boolean; assignments: RoleAssignment[] }
interface UserItem {
  id: string
  username: string
  display_name: string
  email?: string
  mobile_phone?: string
  office_phone?: string
  must_change_password: boolean
  status: 'enabled' | 'disabled' | 'locked'
  last_login_at?: string
  created_at: string
  appointments: Appointment[]
  global_roles: RoleAssignment[]
}
interface ListResponse<T> { items: T[]; total: number; page?: number; page_size?: number }
interface ItemResponse<T> { item: T }
interface AppointmentForm { organization_id: string; is_primary: boolean; assignments: { role_id: string; data_scope: string }[] }

const auth = useAuthStore()
const loading = ref(false)
const listLoading = ref(false)
const submitting = ref(false)
const roleSubmitting = ref(false)
const detailLoading = ref(false)
const userDialogVisible = ref(false)
const roleDialogVisible = ref(false)
const activeTab = ref('users')
const detailVisible = ref(false)
const users = ref<UserItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const organizations = ref<Organization[]>([])
const roles = ref<Role[]>([])
const roleOptions = ref<Role[]>([])
const rolePage = ref(1)
const rolePageSize = ref(20)
const roleTotal = ref(0)
const permissions = ref<Permission[]>([])
const editingUser = ref<UserItem | null>(null)
const detailUser = ref<UserItem | null>(null)
const editingRole = ref<Role | null>(null)
const userFormRef = ref<FormInstance>()
const roleFormRef = ref<FormInstance>()
const filters = reactive({ keyword: '', status: '', organization_id: '', role_id: '' })
const userForm = reactive({ username: '', password: '', display_name: '', email: '', mobile_phone: '', office_phone: '', global_role_ids: [] as string[], appointments: [] as AppointmentForm[] })
const roleForm = reactive({ key: '', name: '', description: '', status: 'enabled' as 'enabled' | 'disabled', permission_keys: [] as string[] })
const userRules: FormRules = {
  username: [{ required: true, message: '请输入用户名', trigger: 'blur' }, { pattern: /^[A-Za-z0-9][A-Za-z0-9_.-]{2,63}$/, message: '用户名为 3-64 位字母、数字、._-', trigger: 'blur' }],
  password: [{ validator: (_rule, value, callback) => editingUser.value || String(value).length >= 10 ? callback() : callback(new Error('初始密码至少 10 位')), trigger: 'blur' }],
  display_name: [{ required: true, message: '请输入姓名', trigger: 'blur' }],
}
const roleRules: FormRules = {
  key: [{ required: true, message: '请输入角色编码', trigger: 'blur' }, { pattern: /^[a-z][a-z0-9_]{2,63}$/, message: '使用 3-64 位小写字母、数字或下划线', trigger: 'blur' }],
  name: [{ required: true, message: '请输入角色名称', trigger: 'blur' }],
}

const isGroupAdmin = computed(() => auth.isGroupAdmin)
const eligibleOrganizations = computed(() => organizations.value.filter((item) => item.status === 'enabled' && item.type !== 'group'))
const organizationRoles = computed(() => roleOptions.value.filter((item) => item.scope === 'organization' && item.status === 'enabled'))
const groupRoles = computed(() => isGroupAdmin.value ? roleOptions.value.filter((item) => item.scope === 'group' && item.status === 'enabled') : [])
const canOmitAppointments = computed(() => isGroupAdmin.value && userForm.global_role_ids.length > 0)
const userDialogTitle = computed(() => editingUser.value ? `编辑用户：${editingUser.value.display_name}` : '新增用户')
const roleDialogTitle = computed(() => editingRole.value ? `编辑角色：${editingRole.value.name}` : '新增自定义角色')
const pageDescription = computed(() => isGroupAdmin.value
  ? '维护集团统一账号、多个组织任职、主组织和组织范围角色。角色或任职变更后，受影响用户会被强制重新登录。'
  : '您可管理已授权组织及下级组织范围内的用户、任职和组织角色；集团角色与范围外用户由集团管理员统一管理。')

async function loadUsers(showLoading = true) {
  if (showLoading) listLoading.value = true
  try {
    const response = await apiClient.get<ListResponse<UserItem>>('/users', {
      params: {
        keyword: filters.keyword || undefined,
        status: filters.status || undefined,
        organization_id: filters.organization_id || undefined,
        role_id: filters.role_id || undefined,
        page: page.value,
        page_size: pageSize.value,
      },
    })
    users.value = response.data.items
    total.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取用户列表失败'))
  } finally {
    if (showLoading) listLoading.value = false
  }
}

async function loadRolePage() {
  if (!isGroupAdmin.value) return
  const response = await apiClient.get<ListResponse<Role>>('/roles', {
    params: { page: rolePage.value, page_size: rolePageSize.value },
  })
  roles.value = response.data.items
  roleTotal.value = response.data.total
}

async function loadRoleOptions() {
  const endpoint = isGroupAdmin.value ? '/roles' : '/roles/assignable'
  const response = await apiClient.get<ListResponse<Role>>(endpoint, {
    params: { page: 1, page_size: 100 },
  })
  roleOptions.value = response.data.items
}

async function refreshRolesAfterMutation() {
  await Promise.all([loadRolePage(), loadRoleOptions()])
  if (roles.value.length === 0 && roleTotal.value > 0 && rolePage.value > 1) {
    rolePage.value -= 1
    await loadRolePage()
  }
}

function changeRolePage(value: number) {
  rolePage.value = value
  void loadRolePage()
}

function changeRolePageSize(value: number) {
  rolePageSize.value = value
  rolePage.value = 1
  void loadRolePage()
}

async function load() {
  loading.value = true
  try {
    const requests: Promise<unknown>[] = [
      apiClient.get<ListResponse<Organization>>('/users/manageable-organizations', { params: { page: 1, page_size: 100 } })
        .then((response) => { organizations.value = response.data.items }),
      loadRoleOptions(),
    ]
    permissions.value = []
    if (isGroupAdmin.value) {
      requests.push(
        loadRolePage(),
        apiClient.get<ListResponse<Permission>>('/roles/permissions', { params: { page: 1, page_size: 100 } })
          .then((response) => { permissions.value = response.data.items }),
      )
    }
    await Promise.all(requests)
    await loadUsers(false)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取用户与权限配置失败'))
  } finally {
    loading.value = false
  }
}

async function searchUsers() { page.value = 1; await loadUsers() }
async function resetFilters() {
  Object.assign(filters, { keyword: '', status: '', organization_id: '', role_id: '' })
  page.value = 1
  await loadUsers()
}
async function changePage(value: number) { page.value = value; await loadUsers() }
async function changePageSize(value: number) { pageSize.value = value; page.value = 1; await loadUsers() }

function resetUserForm() {
  Object.assign(userForm, { username: '', password: '', display_name: '', email: '', mobile_phone: '', office_phone: '', global_role_ids: [], appointments: [] })
  if (eligibleOrganizations.value.length > 0) addAppointment()
}
function addAppointment() {
  const selected = new Set(userForm.appointments.map((item) => item.organization_id))
  const organization = eligibleOrganizations.value.find((item) => !selected.has(item.id))
  if (!organization) { ElMessage.warning('没有可新增的可管理组织'); return }
  userForm.appointments.push({ organization_id: organization.id, is_primary: userForm.appointments.length === 0, assignments: [{ role_id: organizationRoles.value[0]?.id ?? '', data_scope: 'organization' }] })
}
function removeAppointment(index: number) {
  if (userForm.appointments.length <= 1 && !canOmitAppointments.value) {
    ElMessage.warning('普通账号至少需要一个组织任职；如需仅保留集团角色，请先选择集团范围角色')
    return
  }
  const removedPrimary = userForm.appointments[index].is_primary
  userForm.appointments.splice(index, 1)
  if (removedPrimary && userForm.appointments.length > 0) userForm.appointments[0].is_primary = true
}
function setPrimary(index: number) { userForm.appointments.forEach((item, current) => { item.is_primary = current === index }) }
function addRoleAssignment(appointment: AppointmentForm) { appointment.assignments.push({ role_id: organizationRoles.value[0]?.id ?? '', data_scope: 'organization' }) }
function removeRoleAssignment(appointment: AppointmentForm, index: number) {
  if (appointment.assignments.length <= 1) { ElMessage.warning('每个任职组织至少需要一个组织角色'); return }
  appointment.assignments.splice(index, 1)
}
function openCreateUser() { editingUser.value = null; resetUserForm(); userDialogVisible.value = true }
function openEditUser(item: UserItem) {
  if (item.id === auth.user?.id) return
  editingUser.value = item
  Object.assign(userForm, {
    username: item.username,
    password: '',
    display_name: item.display_name,
    email: item.email ?? '',
    mobile_phone: item.mobile_phone ?? '',
    office_phone: item.office_phone ?? '',
    global_role_ids: isGroupAdmin.value ? item.global_roles.map((role) => role.role_id) : [],
    appointments: item.appointments.map((appointment) => ({ organization_id: appointment.organization.id, is_primary: appointment.is_primary, assignments: appointment.assignments.map((assignment) => ({ role_id: assignment.role_id, data_scope: assignment.data_scope })) })),
  })
  userDialogVisible.value = true
}
async function openUserDetail(item: UserItem) {
  detailUser.value = item
  detailVisible.value = true
  detailLoading.value = true
  try {
    const response = await apiClient.get<ItemResponse<UserItem>>(`/users/${item.id}`)
    detailUser.value = response.data.item
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取用户详情失败'))
    detailVisible.value = false
  } finally {
    detailLoading.value = false
  }
}
function validAppointments() {
  if (!userForm.appointments.length) return canOmitAppointments.value
  if (userForm.appointments.filter((item) => item.is_primary).length !== 1) return false
  const ids = new Set<string>()
  return userForm.appointments.every((item) => item.organization_id && !ids.has(item.organization_id) && (ids.add(item.organization_id), item.assignments.length > 0 && item.assignments.every((assignment) => assignment.role_id && assignment.data_scope)))
}
async function submitUser() {
  if (!userFormRef.value || !(await userFormRef.value.validate()) || !validAppointments()) { ElMessage.warning('请完整设置集团角色，或设置组织任职、主组织和组织角色'); return }
  submitting.value = true
  try {
    const payload = { display_name: userForm.display_name, email: userForm.email, mobile_phone: userForm.mobile_phone, office_phone: userForm.office_phone, global_role_ids: isGroupAdmin.value ? userForm.global_role_ids : [], appointments: userForm.appointments }
    if (editingUser.value) await apiClient.put(`/users/${editingUser.value.id}`, payload)
    else await apiClient.post('/users', { username: userForm.username, password: userForm.password, ...payload })
    userDialogVisible.value = false
    ElMessage.success(editingUser.value ? '用户任职已更新，旧会话已失效' : '用户已新增')
    await loadUsers()
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '保存用户失败')) }
  finally { submitting.value = false }
}
async function changeStatus(item: UserItem) {
  const next = item.status === 'enabled' ? 'disabled' : 'enabled'
  try {
    await ElMessageBox.confirm(`确定要${next === 'enabled' ? '启用' : '停用'}用户“${item.display_name}”吗？该用户当前会话会立即失效。`, '确认状态变更', { type: 'warning' })
    await apiClient.patch(`/users/${item.id}/status`, { status: next })
    ElMessage.success(next === 'enabled' ? '用户已启用' : '用户已停用')
    await loadUsers()
  } catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '更新用户状态失败')) }
}
async function resetPassword(item: UserItem) {
  try {
    const result = await ElMessageBox.prompt(`请设置“${item.display_name}”的临时密码（至少 10 位）。重置后原会话立即失效，用户下次登录必须修改密码。`, '重置密码', { inputType: 'password', inputPattern: /^.{10,}$/, inputErrorMessage: '密码至少 10 位' })
    await apiClient.post(`/users/${item.id}/reset-password`, { password: result.value })
    ElMessage.success('临时密码已设置，用户下次登录必须修改密码')
  } catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '重置密码失败')) }
}

function openCreateRole() { editingRole.value = null; Object.assign(roleForm, { key: '', name: '', description: '', status: 'enabled', permission_keys: [] }); roleDialogVisible.value = true }
function openEditRole(item: Role) { if (item.is_system) return; editingRole.value = item; Object.assign(roleForm, { key: item.key, name: item.name, description: item.description ?? '', status: item.status, permission_keys: item.permissions.map((permission) => permission.key) }); roleDialogVisible.value = true }
async function submitRole() {
  if (!roleFormRef.value || !(await roleFormRef.value.validate()) || !roleForm.permission_keys.length) { ElMessage.warning('请至少选择一项权限'); return }
  roleSubmitting.value = true
  try {
    const payload = { ...roleForm }
    if (editingRole.value) await apiClient.put(`/roles/${editingRole.value.id}`, payload)
    else await apiClient.post('/roles', payload)
    roleDialogVisible.value = false
    ElMessage.success(editingRole.value ? '角色已更新，相关用户需重新登录' : '自定义角色已新增')
    await refreshRolesAfterMutation()
  } catch (error) { ElMessage.error(getAPIErrorMessage(error, '保存角色失败')) }
  finally { roleSubmitting.value = false }
}
async function deleteRole(item: Role) {
  try {
    await ElMessageBox.confirm(`确定删除角色“${item.name}”吗？已被任职使用的角色不能删除。`, '删除角色', { type: 'warning' })
    await apiClient.delete(`/roles/${item.id}`)
    ElMessage.success('角色已删除')
    await refreshRolesAfterMutation()
  } catch (error) { if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '删除角色失败')) }
}
function appointmentSummary(item: UserItem) { return item.appointments.map((appointment) => `${appointment.organization.name}${appointment.is_primary ? '（主）' : ''}`).join('、') || '—' }
function roleSummary(item: UserItem) { return item.appointments.flatMap((appointment) => appointment.assignments.map((assignment) => assignment.role_name)).concat(item.global_roles.map((role) => role.role_name)).join('、') || '未分配' }
function statusLabel(status: UserItem['status']) { return status === 'enabled' ? '启用' : status === 'locked' ? '锁定' : '停用' }
function statusType(status: UserItem['status']) { return status === 'enabled' ? 'success' : status === 'locked' ? 'warning' : 'info' }
function dataScopeLabel(value: string) { return ({ all: '全部组织', organization_and_descendants: '组织及下级', organization: '仅本组织', owned: '仅本人创建' } as Record<string, string>)[value] ?? value }
function formatTime(value?: string, fallback = '尚未登录') { return value ? new Date(value).toLocaleString('zh-CN', { hour12: false }) : fallback }
onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div><span class="eyebrow">IDENTITY & ACCESS</span><h1>{{ isGroupAdmin ? '用户与权限' : '用户管理' }}</h1><p>{{ pageDescription }}</p></div>
      <el-button type="primary" :disabled="!isGroupAdmin && eligibleOrganizations.length === 0" @click="openCreateUser">新增用户</el-button>
    </div>

    <el-tabs v-model="activeTab" v-loading="loading" class="access-tabs">
      <el-tab-pane label="用户与任职" name="users">
        <div class="panel filter-panel">
          <el-input v-model="filters.keyword" clearable placeholder="姓名、用户名、联系方式或组织" @keyup.enter="searchUsers" />
          <el-select v-model="filters.status" clearable placeholder="全部状态">
            <el-option label="启用" value="enabled" /><el-option label="停用" value="disabled" /><el-option label="锁定" value="locked" />
          </el-select>
          <el-select v-model="filters.organization_id" clearable filterable placeholder="全部组织">
            <el-option v-for="organization in eligibleOrganizations" :key="organization.id" :label="`${organization.name}（${organization.code}）`" :value="organization.id" />
          </el-select>
          <el-select v-model="filters.role_id" clearable filterable placeholder="全部角色">
            <el-option v-for="role in roleOptions" :key="role.id" :label="role.name" :value="role.id" />
          </el-select>
          <div class="filter-actions"><el-button type="primary" @click="searchUsers">查询</el-button><el-button @click="resetFilters">重置</el-button></div>
        </div>

        <div class="panel data-table-panel" v-loading="listLoading">
          <!-- 用户列表可切换至每页 100 条，使用表格内部滚动避免拉长内容页面。 -->
          <el-table :data="users" row-key="id" empty-text="暂无符合条件的可管理用户" :max-height="480">
            <el-table-column label="用户" min-width="180"><template #default="scope"><div class="organization-name-cell"><strong>{{ scope.row.display_name }}</strong><span>{{ scope.row.username }}</span></div></template></el-table-column>
            <el-table-column label="联系方式" min-width="210"><template #default="scope"><div>{{ scope.row.mobile_phone || scope.row.office_phone || scope.row.email || '—' }}</div><div v-if="scope.row.email && scope.row.email !== scope.row.mobile_phone" class="form-hint">{{ scope.row.email }}</div></template></el-table-column>
            <el-table-column label="任职组织" min-width="210"><template #default="scope">{{ appointmentSummary(scope.row) }}</template></el-table-column>
            <el-table-column label="角色" min-width="210"><template #default="scope">{{ roleSummary(scope.row) }}</template></el-table-column>
            <el-table-column label="状态" width="120"><template #default="scope"><el-tag :type="statusType(scope.row.status)">{{ statusLabel(scope.row.status) }}</el-tag><el-tag v-if="scope.row.must_change_password" class="temporary-tag" size="small" type="warning">待改密</el-tag></template></el-table-column>
            <el-table-column label="最近登录" min-width="165"><template #default="scope">{{ formatTime(scope.row.last_login_at) }}</template></el-table-column>
            <el-table-column label="操作" width="270" fixed="right">
              <template #default="scope">
                <el-button link @click="openUserDetail(scope.row)">详情</el-button>
                <template v-if="scope.row.id !== auth.user?.id">
                  <el-button link type="primary" @click="openEditUser(scope.row)">编辑</el-button>
                  <el-button link type="warning" @click="changeStatus(scope.row)">{{ scope.row.status === 'enabled' ? '停用' : '启用' }}</el-button>
                  <el-button link @click="resetPassword(scope.row)">重置密码</el-button>
                </template>
                <span v-else class="self-hint">本人资料请到账户设置维护</span>
              </template>
            </el-table-column>
          </el-table>
          <div class="pagination-row"><span>共 {{ total }} 个用户</span><el-pagination background layout="sizes, prev, pager, next" :total="total" :current-page="page" :page-size="pageSize" :page-sizes="[10, 20, 50, 100]" @current-change="changePage" @size-change="changePageSize" /></div>
        </div>
      </el-tab-pane>

      <el-tab-pane v-if="isGroupAdmin" label="角色与权限" name="roles">
        <div class="tab-toolbar"><p>系统角色不可编辑或删除；可创建、维护组织范围的自定义角色。</p><el-button type="primary" @click="openCreateRole">新增自定义角色</el-button></div>
        <div class="panel data-table-panel">
          <el-table :data="roles" row-key="id"><el-table-column prop="name" label="角色名称" min-width="170"><template #default="scope"><strong>{{ scope.row.name }}</strong><div class="form-hint">{{ scope.row.key }}</div></template></el-table-column><el-table-column label="范围" width="110"><template #default="scope">{{ scope.row.scope === 'group' ? '集团' : '组织' }}</template></el-table-column><el-table-column label="状态" width="90"><template #default="scope"><el-tag :type="scope.row.status === 'enabled' ? 'success' : 'info'">{{ scope.row.status === 'enabled' ? '启用' : '停用' }}</el-tag></template></el-table-column><el-table-column label="权限" min-width="300"><template #default="scope"><el-tag v-for="permission in scope.row.permissions" :key="permission.key" class="permission-tag" size="small">{{ permission.name }}</el-tag></template></el-table-column><el-table-column label="使用人数" width="100"><template #default="scope">{{ scope.row.user_count }}</template></el-table-column><el-table-column label="操作" width="130" fixed="right"><template #default="scope"><span v-if="scope.row.is_system" class="self-hint">系统角色</span><template v-else><el-button link type="primary" @click="openEditRole(scope.row)">编辑</el-button><el-button link type="danger" @click="deleteRole(scope.row)">删除</el-button></template></template></el-table-column></el-table>
          <div class="pagination-row"><span>共 {{ roleTotal }} 个角色</span><el-pagination background layout="sizes, prev, pager, next" :total="roleTotal" :current-page="rolePage" :page-size="rolePageSize" :page-sizes="[20, 50, 100]" @current-change="changeRolePage" @size-change="changeRolePageSize" /></div>
        </div>
      </el-tab-pane>
    </el-tabs>

    <el-drawer v-model="detailVisible" title="用户详情" size="640px" destroy-on-close>
      <div v-loading="detailLoading">
        <template v-if="detailUser">
          <el-descriptions :column="2" border>
            <el-descriptions-item label="姓名">{{ detailUser.display_name }}</el-descriptions-item>
            <el-descriptions-item label="用户名">{{ detailUser.username }}</el-descriptions-item>
            <el-descriptions-item label="状态"><el-tag :type="statusType(detailUser.status)">{{ statusLabel(detailUser.status) }}</el-tag><el-tag v-if="detailUser.must_change_password" class="temporary-tag" size="small" type="warning">临时密码</el-tag></el-descriptions-item>
            <el-descriptions-item label="创建时间">{{ formatTime(detailUser.created_at, '—') }}</el-descriptions-item>
            <el-descriptions-item label="手机号码">{{ detailUser.mobile_phone || '—' }}</el-descriptions-item>
            <el-descriptions-item label="办公电话">{{ detailUser.office_phone || '—' }}</el-descriptions-item>
            <el-descriptions-item label="邮箱" :span="2">{{ detailUser.email || '—' }}</el-descriptions-item>
            <el-descriptions-item label="最近登录" :span="2">{{ formatTime(detailUser.last_login_at) }}</el-descriptions-item>
          </el-descriptions>
          <h3 class="detail-section-title">集团范围角色</h3>
          <div v-if="detailUser.global_roles.length"><el-tag v-for="role in detailUser.global_roles" :key="role.id" class="permission-tag">{{ role.role_name }} · {{ dataScopeLabel(role.data_scope) }}</el-tag></div><el-empty v-else :image-size="54" description="未分配集团范围角色" />
          <h3 class="detail-section-title">组织任职与角色</h3>
          <div v-for="appointment in detailUser.appointments" :key="appointment.organization.id" class="detail-appointment-card">
            <div class="detail-appointment-title"><strong>{{ appointment.organization.name }}</strong><el-tag v-if="appointment.is_primary" size="small" type="success">主组织</el-tag><span>{{ appointment.organization.code }}</span></div>
            <div><el-tag v-for="assignment in appointment.assignments" :key="assignment.id" class="permission-tag" type="info">{{ assignment.role_name }} · {{ dataScopeLabel(assignment.data_scope) }}</el-tag></div>
          </div>
        </template>
      </div>
    </el-drawer>

    <el-dialog v-model="userDialogVisible" :title="userDialogTitle" width="780px" destroy-on-close>
      <el-form ref="userFormRef" :model="userForm" :rules="userRules" label-position="top">
        <div class="form-grid two-columns"><el-form-item label="用户名" prop="username"><el-input v-model="userForm.username" :disabled="!!editingUser" /></el-form-item><el-form-item v-if="!editingUser" label="临时密码" prop="password"><el-input v-model="userForm.password" type="password" show-password /></el-form-item><el-form-item label="姓名" prop="display_name"><el-input v-model="userForm.display_name" maxlength="128" /></el-form-item><el-form-item label="邮箱"><el-input v-model="userForm.email" maxlength="255" /></el-form-item><el-form-item label="手机号码"><el-input v-model="userForm.mobile_phone" maxlength="64" /></el-form-item><el-form-item label="办公电话"><el-input v-model="userForm.office_phone" maxlength="64" /></el-form-item></div>
        <el-form-item v-if="isGroupAdmin && groupRoles.length" label="集团范围角色"><el-checkbox-group v-model="userForm.global_role_ids"><el-checkbox v-for="role in groupRoles" :key="role.id" :label="role.id">{{ role.name }}</el-checkbox></el-checkbox-group><div class="form-hint">集团角色适用于全集团。仅承担集团安全运营等集团职责的账号可不配置组织任职，避免额外获得子公司岗位权限。</div></el-form-item>
        <div class="appointment-header"><strong>组织任职与角色{{ isGroupAdmin ? '（集团角色账号可选）' : '' }}</strong><el-button size="small" plain type="primary" :disabled="eligibleOrganizations.length === 0" @click="addAppointment">新增任职组织</el-button></div>
        <el-alert v-if="userForm.appointments.length === 0" :title="canOmitAppointments ? '当前账号仅授予集团范围角色，不附加任何组织岗位权限。' : '未选择集团范围角色时，至少需要配置一个组织任职。'" :type="canOmitAppointments ? 'info' : 'warning'" :closable="false" show-icon class="appointment-empty-alert" />
        <div v-for="(appointment, index) in userForm.appointments" :key="`${appointment.organization_id}-${index}`" class="appointment-card"><div class="appointment-topline"><el-select v-model="appointment.organization_id" filterable placeholder="请选择组织" style="width: 320px"><el-option v-for="organization in eligibleOrganizations" :key="organization.id" :label="`${organization.name}（${organization.code}）`" :value="organization.id" /></el-select><el-radio :model-value="appointment.is_primary" :label="true" @change="setPrimary(index)">主组织</el-radio><el-button link type="danger" @click="removeAppointment(index)">移除任职</el-button></div><div v-for="(assignment, roleIndex) in appointment.assignments" :key="`${assignment.role_id}-${roleIndex}`" class="assignment-row"><el-select v-model="assignment.role_id" placeholder="选择组织角色" style="width: 320px"><el-option v-for="role in organizationRoles" :key="role.id" :label="role.name" :value="role.id" /></el-select><el-select v-model="assignment.data_scope" style="width: 190px"><el-option value="organization" label="仅本组织" /><el-option value="organization_and_descendants" label="组织及下级" /><el-option value="owned" label="仅本人创建" /></el-select><el-button link type="danger" @click="removeRoleAssignment(appointment, roleIndex)">移除角色</el-button></div><el-button size="small" text type="primary" @click="addRoleAssignment(appointment)">+ 添加角色</el-button></div>
      </el-form>
      <template #footer><el-button @click="userDialogVisible=false">取消</el-button><el-button type="primary" :loading="submitting" @click="submitUser">保存</el-button></template>
    </el-dialog>

    <el-dialog v-if="isGroupAdmin" v-model="roleDialogVisible" :title="roleDialogTitle" width="700px" destroy-on-close><el-form ref="roleFormRef" :model="roleForm" :rules="roleRules" label-position="top"><div class="form-grid two-columns"><el-form-item label="角色编码" prop="key"><el-input v-model="roleForm.key" :disabled="!!editingRole" placeholder="如：asset_operator" /></el-form-item><el-form-item label="角色名称" prop="name"><el-input v-model="roleForm.name" /></el-form-item><el-form-item label="状态"><el-radio-group v-model="roleForm.status"><el-radio value="enabled">启用</el-radio><el-radio value="disabled">停用</el-radio></el-radio-group></el-form-item></div><el-form-item label="角色说明"><el-input v-model="roleForm.description" type="textarea" :rows="2" maxlength="5000" /></el-form-item><el-form-item label="权限项"><el-checkbox-group v-model="roleForm.permission_keys" class="permission-checkboxes"><el-checkbox v-for="permission in permissions" :key="permission.key" :label="permission.key">{{ permission.name }} <small>{{ permission.key }}</small></el-checkbox></el-checkbox-group></el-form-item></el-form><template #footer><el-button @click="roleDialogVisible=false">取消</el-button><el-button type="primary" :loading="roleSubmitting" @click="submitRole">保存角色</el-button></template></el-dialog>
  </section>
</template>

<style scoped>
.access-tabs { margin-top: 18px; }
.filter-panel { display: grid; grid-template-columns: minmax(220px, 1.5fr) repeat(3, minmax(160px, 1fr)) auto; gap: 12px; align-items: center; margin-bottom: 14px; padding: 16px; }
.filter-actions, .tab-toolbar, .appointment-header, .appointment-topline, .assignment-row, .pagination-row, .detail-appointment-title { display: flex; align-items: center; gap: 12px; }
.filter-actions { justify-content: flex-end; }
.pagination-row { justify-content: space-between; padding: 16px 4px 2px; color: var(--el-text-color-secondary); }
.tab-toolbar { justify-content: space-between; margin: 0 0 14px; }
.tab-toolbar p { color: var(--el-text-color-regular); margin: 0; }
.appointment-header { justify-content: space-between; margin: 12px 0; }
.form-hint, .self-hint { color: var(--el-text-color-secondary); font-size: 12px; }
.self-hint { line-height: 1.4; margin-left: 6px; }
.appointment-empty-alert { margin-bottom: 12px; }
.appointment-card, .detail-appointment-card { border: 1px solid var(--el-border-color-lighter); border-radius: 8px; padding: 14px; margin-bottom: 12px; background: var(--el-fill-color-lighter); }
.assignment-row { margin: 10px 0; }
.permission-tag { margin: 2px 6px 2px 0; }
.temporary-tag { margin-left: 6px; }
.permission-checkboxes { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px; }
.permission-checkboxes small { color: var(--el-text-color-secondary); margin-left: 4px; }
.detail-section-title { margin: 24px 0 12px; font-size: 16px; }
.detail-appointment-title { margin-bottom: 10px; }
.detail-appointment-title span:last-child { color: var(--el-text-color-secondary); font-size: 12px; }
@media (max-width: 1100px) { .filter-panel { grid-template-columns: repeat(2, minmax(0, 1fr)); } .filter-actions { justify-content: flex-start; } }
</style>
