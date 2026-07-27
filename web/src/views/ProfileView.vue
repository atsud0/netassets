<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage, ElMessageBox, type FormInstance, type FormRules } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiClient, getAPIErrorMessage } from '../api/client'
import { useAuthStore } from '../stores/auth'

const auth = useAuthStore()
const router = useRouter()
const profileFormRef = ref<FormInstance>()
const passwordFormRef = ref<FormInstance>()
const savingProfile = ref(false)
const savingPassword = ref(false)
const loadingSessions = ref(false)
const revokingOthers = ref(false)
const sessions = ref<LoginSession[]>([])
const sessionPage = ref(1)
const sessionPageSize = ref(20)
const sessionTotal = ref(0)

const profileForm = reactive({ display_name: '', email: '', mobile_phone: '', office_phone: '' })
const passwordForm = reactive({ current_password: '', new_password: '', confirm_password: '' })

interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface LoginSession {
  id: string
  ip_address?: string
  user_agent?: string
  device: string
  browser: string
  operating_system: string
  created_at: string
  last_seen_at: string
  expires_at: string
  current: boolean
}

function formatDateTime(value: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}

async function loadSessions() {
  if (mustChangePassword.value) {
    sessions.value = []
    sessionTotal.value = 0
    return
  }
  loadingSessions.value = true
  try {
    const response = await apiClient.get<ListResponse<LoginSession>>('/auth/sessions', {
      params: { page: sessionPage.value, page_size: sessionPageSize.value },
    })
    sessions.value = response.data.items
    sessionTotal.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '获取登录设备失败'))
  } finally {
    loadingSessions.value = false
  }
}
async function refreshSessionsAfterMutation() {
  await loadSessions()
  if (sessions.value.length === 0 && sessionTotal.value > 0 && sessionPage.value > 1) {
    sessionPage.value -= 1
    await loadSessions()
  }
}
function handleSessionPageChange(value: number) {
  sessionPage.value = value
  void loadSessions()
}
function handleSessionPageSizeChange(value: number) {
  sessionPageSize.value = value
  sessionPage.value = 1
  void loadSessions()
}

async function revokeSession(session: LoginSession) {
  try {
    await ElMessageBox.confirm(`确定要退出“${session.device} · ${session.browser}”这台设备吗？`, '退出登录设备', { type: 'warning', confirmButtonText: '确认退出', cancelButtonText: '取消' })
    await apiClient.delete(`/auth/sessions/${session.id}`)
    ElMessage.success('该设备已退出登录')
    await refreshSessionsAfterMutation()
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '退出登录设备失败'))
  }
}

async function revokeOtherSessions() {
  const otherSessionCount = Math.max(sessionTotal.value - 1, 0)
  if (otherSessionCount === 0) {
    ElMessage.info('当前没有其他登录设备')
    return
  }
  try {
    await ElMessageBox.confirm(`将退出其他 ${otherSessionCount} 台登录设备，当前设备会保持登录。是否继续？`, '退出其他设备', { type: 'warning', confirmButtonText: '确认退出', cancelButtonText: '取消' })
    revokingOthers.value = true
    const response = await apiClient.delete<{ revoked_count: number }>('/auth/sessions/others')
    ElMessage.success(`已退出 ${response.data.revoked_count} 台其他设备`)
    await refreshSessionsAfterMutation()
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '退出其他登录设备失败'))
  } finally {
    revokingOthers.value = false
  }
}

watch(() => auth.user, (user) => {
  profileForm.display_name = user?.display_name ?? ''
  profileForm.email = user?.email ?? ''
  profileForm.mobile_phone = user?.mobile_phone ?? ''
  profileForm.office_phone = user?.office_phone ?? ''
}, { immediate: true })

const username = computed(() => auth.user?.username ?? '—')
const mustChangePassword = computed(() => auth.user?.must_change_password ?? false)

watch(mustChangePassword, (required) => {
  if (!required) void loadSessions()
  else {
    sessions.value = []
    sessionTotal.value = 0
  }
}, { immediate: true })
const profileRules: FormRules = {
  display_name: [{ required: true, message: '请输入显示名称', trigger: 'blur' }],
  email: [{ max: 255, message: '邮箱不能超过 255 个字符', trigger: 'blur' }],
  mobile_phone: [{ max: 64, message: '手机号码不能超过 64 个字符', trigger: 'blur' }],
  office_phone: [{ max: 64, message: '办公电话不能超过 64 个字符', trigger: 'blur' }],
}
const passwordRules: FormRules = {
  current_password: [{ required: true, message: '请输入当前密码', trigger: 'blur' }],
  new_password: [{ required: true, min: 10, message: '新密码至少需要 10 位', trigger: 'blur' }],
  confirm_password: [{ validator: (_rule, value, callback) => value === passwordForm.new_password ? callback() : callback(new Error('两次输入的新密码不一致')), trigger: 'blur' }],
}

async function saveProfile() {
  if (!profileFormRef.value || !(await profileFormRef.value.validate())) return
  savingProfile.value = true
  try {
    const response = await apiClient.put<{ user: typeof auth.user }>('/auth/profile', profileForm)
    if (response.data.user) auth.setCurrentUser(response.data.user)
    ElMessage.success('个人资料已更新')
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '更新个人资料失败'))
  } finally {
    savingProfile.value = false
  }
}

async function changePassword() {
  if (!passwordFormRef.value || !(await passwordFormRef.value.validate())) return
  savingPassword.value = true
  try {
    await apiClient.put('/auth/profile/password', {
      current_password: passwordForm.current_password,
      new_password: passwordForm.new_password,
    })
    Object.assign(passwordForm, { current_password: '', new_password: '', confirm_password: '' })
    await auth.fetchCurrentUser()
    ElMessage.success('密码已修改，其他登录设备已退出')
    if (!auth.user?.must_change_password) await router.replace('/dashboard')
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '修改密码失败'))
  } finally {
    savingPassword.value = false
  }
}
</script>

<template>
  <section class="profile-page">
    <div class="page-heading">
      <div>
        <span class="eyebrow">ACCOUNT SETTINGS</span>
        <h1>账户设置</h1>
        <p>在此维护您自己的显示名称、联系方式和登录密码。组织任职、角色及数据范围由有权限的管理员统一配置。</p>
      </div>
    </div>

    <el-alert v-if="mustChangePassword" class="password-required-alert" type="warning" :closable="false" show-icon title="首次登录必须修改临时密码" description="完成密码修改前，系统仅允许访问账户设置和退出登录。" />

    <div class="profile-grid">
      <div class="panel">
        <h2>个人资料</h2>
        <el-form ref="profileFormRef" :model="profileForm" :rules="profileRules" label-position="top">
          <el-form-item label="用户名"><el-input :model-value="username" disabled /></el-form-item>
          <el-form-item label="显示名称" prop="display_name"><el-input v-model="profileForm.display_name" maxlength="128" /></el-form-item>
          <el-form-item label="邮箱" prop="email"><el-input v-model="profileForm.email" maxlength="255" placeholder="如：name@example.com" /></el-form-item>
          <el-form-item label="手机号码" prop="mobile_phone"><el-input v-model="profileForm.mobile_phone" maxlength="64" placeholder="如：13800000000" /></el-form-item>
          <el-form-item label="办公电话" prop="office_phone"><el-input v-model="profileForm.office_phone" maxlength="64" placeholder="如：010-12345678" /></el-form-item>
          <el-button type="primary" :loading="savingProfile" @click="saveProfile">保存个人资料</el-button>
        </el-form>
      </div>
      <div class="panel">
        <h2>{{ mustChangePassword ? '设置正式密码' : '修改密码' }}</h2>
        <p class="form-hint">修改成功后，当前设备会保持登录，其他已登录设备将被安全退出。</p>
        <el-form ref="passwordFormRef" :model="passwordForm" :rules="passwordRules" label-position="top">
          <el-form-item label="当前密码" prop="current_password"><el-input v-model="passwordForm.current_password" type="password" show-password autocomplete="current-password" /></el-form-item>
          <el-form-item label="新密码" prop="new_password"><el-input v-model="passwordForm.new_password" type="password" show-password autocomplete="new-password" /></el-form-item>
          <el-form-item label="确认新密码" prop="confirm_password"><el-input v-model="passwordForm.confirm_password" type="password" show-password autocomplete="new-password" /></el-form-item>
          <el-button type="primary" :loading="savingPassword" @click="changePassword">修改密码</el-button>
        </el-form>
      </div>
    </div>

    <div v-if="!mustChangePassword" class="panel sessions-panel" v-loading="loadingSessions">
      <div class="sessions-heading">
        <div>
          <h2>登录设备</h2>
          <p class="form-hint">可查看当前有效登录会话，并安全退出不再使用的设备。当前设备请使用页面右上角的“退出登录”。</p>
        </div>
        <el-button :loading="revokingOthers" :disabled="loadingSessions || sessionTotal <= 1" @click="revokeOtherSessions">退出其他设备</el-button>
      </div>
      <el-empty v-if="!loadingSessions && sessions.length === 0" description="暂无有效登录设备" :image-size="72" />
      <div v-else class="session-list">
        <div v-for="session in sessions" :key="session.id" class="session-item">
          <div class="session-main">
            <div class="session-title">
              <strong>{{ session.device }} · {{ session.browser }}</strong>
              <el-tag v-if="session.current" type="success" size="small">当前设备</el-tag>
            </div>
            <p>{{ session.operating_system }}<span v-if="session.ip_address"> · {{ session.ip_address }}</span></p>
            <p>登录时间：{{ formatDateTime(session.created_at) }} · 最近活动：{{ formatDateTime(session.last_seen_at) }}</p>
            <p class="session-expiry">会话有效至：{{ formatDateTime(session.expires_at) }}</p>
          </div>
          <el-button v-if="!session.current" type="danger" plain @click="revokeSession(session)">退出此设备</el-button>
        </div>
      </div>
      <div v-if="sessionTotal > 0" class="table-pagination">
        <el-pagination
          background
          layout="total, sizes, prev, pager, next"
          :total="sessionTotal"
          :current-page="sessionPage"
          :page-size="sessionPageSize"
          :page-sizes="[20, 50, 100]"
          @current-change="handleSessionPageChange"
          @size-change="handleSessionPageSizeChange"
        />
      </div>
    </div>
  </section>
</template>

<style scoped>
.password-required-alert { margin-bottom: 18px; }
.profile-grid { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 18px; }
.panel h2 { margin: 0 0 18px; font-size: 17px; }
.sessions-panel { margin-top: 18px; }
.sessions-heading { display: flex; justify-content: space-between; gap: 16px; align-items: flex-start; }
.sessions-heading h2 { margin-bottom: 8px; }
.session-list { border-top: 1px solid var(--el-border-color-lighter); }
.session-item { display: flex; align-items: center; justify-content: space-between; gap: 18px; padding: 16px 0; border-bottom: 1px solid var(--el-border-color-lighter); }
.session-main { min-width: 0; }
.session-title { display: flex; align-items: center; gap: 8px; }
.session-main p { margin: 5px 0 0; color: var(--el-text-color-secondary); font-size: 13px; line-height: 1.6; }
.session-main .session-expiry { color: var(--el-text-color-placeholder); }
.form-hint { margin: -8px 0 16px; color: var(--el-text-color-secondary); font-size: 13px; line-height: 1.65; }
@media (max-width: 860px) { .profile-grid { grid-template-columns: 1fr; } .sessions-heading, .session-item { align-items: stretch; flex-direction: column; } .session-item .el-button { align-self: flex-start; } }
</style>
