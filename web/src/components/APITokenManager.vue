<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface APITokenItem {
  id: string
  name: string
  token_prefix: string
  organization_id: string
  organization_name: string
  expires_at: string
  last_used_at?: string
  created_at: string
  revoked_at?: string
}

interface ListResponse<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}

interface CreateResponse {
  item: APITokenItem
  token: string
}

const loading = ref(false)
const creating = ref(false)
const items = ref<APITokenItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = ref(20)
const createDialogVisible = ref(false)
const secretDialogVisible = ref(false)
const createdToken = ref('')
const createdItem = ref<APITokenItem>()
const createForm = reactive({ name: '', expires_in_days: 90 })
const canCreate = computed(() => createForm.name.trim().length > 0 && createForm.name.trim().length <= 100)

function formatDateTime(value?: string) {
  if (!value) return '从未使用'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString('zh-CN', { hour12: false })
}

function tokenStatus(item: APITokenItem) {
  if (item.revoked_at) return { label: '已撤销', type: 'info' as const }
  if (new Date(item.expires_at).getTime() <= Date.now()) return { label: '已过期', type: 'danger' as const }
  return { label: '有效', type: 'success' as const }
}

async function loadTokens() {
  loading.value = true
  try {
    const response = await apiClient.get<ListResponse<APITokenItem>>('/auth/api-tokens', {
      params: { page: page.value, page_size: pageSize.value },
    })
    items.value = response.data.items
    total.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '获取 API Token 失败'))
  } finally {
    loading.value = false
  }
}

function openCreateDialog() {
  Object.assign(createForm, { name: '', expires_in_days: 90 })
  createDialogVisible.value = true
}

async function createToken() {
  if (!canCreate.value) {
    ElMessage.warning('请输入不超过 100 个字符的 Token 名称')
    return
  }
  creating.value = true
  try {
    const response = await apiClient.post<CreateResponse>('/auth/api-tokens', {
      name: createForm.name.trim(),
      expires_in_days: createForm.expires_in_days,
    })
    createdToken.value = response.data.token
    createdItem.value = response.data.item
    createDialogVisible.value = false
    secretDialogVisible.value = true
    page.value = 1
    await loadTokens()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '创建 API Token 失败'))
  } finally {
    creating.value = false
  }
}

async function copyToken() {
  try {
    await navigator.clipboard.writeText(createdToken.value)
    ElMessage.success('API Token 已复制')
  } catch {
    ElMessage.warning('浏览器无法自动复制，请手动选择并复制 Token')
  }
}

function closeSecretDialog() {
  secretDialogVisible.value = false
  createdToken.value = ''
  createdItem.value = undefined
}

async function revokeToken(item: APITokenItem) {
  try {
    await ElMessageBox.confirm(`撤销“${item.name}”后，使用该 Token 的脚本会立即认证失败。是否继续？`, '撤销 API Token', {
      type: 'warning',
      confirmButtonText: '确认撤销',
      cancelButtonText: '取消',
    })
    await apiClient.delete(`/auth/api-tokens/${item.id}`)
    ElMessage.success('API Token 已撤销')
    await loadTokens()
    if (items.value.length === 0 && total.value > 0 && page.value > 1) {
      page.value -= 1
      await loadTokens()
    }
  } catch (error) {
    if (error !== 'cancel' && error !== 'close') ElMessage.error(getAPIErrorMessage(error, '撤销 API Token 失败'))
  }
}

function handlePageChange(value: number) {
  page.value = value
  void loadTokens()
}

function handlePageSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  void loadTokens()
}

onMounted(() => { void loadTokens() })

defineExpose({ loadTokens, createForm, createToken, createdToken, secretDialogVisible })
</script>

<template>
  <div class="panel api-token-panel" v-loading="loading">
    <div class="token-heading">
      <div>
        <h2>API Token</h2>
        <p class="form-hint">用于脚本或外部工具调用接口。Token 继承您的账号权限，并固定到创建时的当前工作组织；每人最多同时保留 10 个有效 Token。</p>
      </div>
      <el-button type="primary" @click="openCreateDialog">创建 Token</el-button>
    </div>

    <el-table :data="items" max-height="440" empty-text="暂无 API Token">
      <el-table-column prop="name" label="名称" min-width="150" />
      <el-table-column label="Token 前缀" min-width="140"><template #default="{ row }"><code>{{ row.token_prefix }}…</code></template></el-table-column>
      <el-table-column prop="organization_name" label="绑定组织" min-width="160" />
      <el-table-column label="状态" width="90"><template #default="{ row }"><el-tag :type="tokenStatus(row).type" size="small">{{ tokenStatus(row).label }}</el-tag></template></el-table-column>
      <el-table-column label="最后使用" min-width="170"><template #default="{ row }">{{ formatDateTime(row.last_used_at) }}</template></el-table-column>
      <el-table-column label="有效期至" min-width="170"><template #default="{ row }">{{ formatDateTime(row.expires_at) }}</template></el-table-column>
      <el-table-column label="操作" width="84" fixed="right" align="right" header-align="right" class-name="table-action-column">
        <template #default="{ row }"><el-button v-if="!row.revoked_at" type="danger" link @click="revokeToken(row)">撤销</el-button><span v-else>—</span></template>
      </el-table-column>
    </el-table>
    <div v-if="total > 0" class="table-pagination">
      <el-pagination
        background
        layout="total, sizes, prev, pager, next"
        :total="total"
        :current-page="page"
        :page-size="pageSize"
        :page-sizes="[20, 50, 100]"
        @current-change="handlePageChange"
        @size-change="handlePageSizeChange"
      />
    </div>

    <el-dialog v-model="createDialogVisible" title="创建 API Token" width="480px">
      <el-form label-position="top" @submit.prevent="createToken">
        <el-form-item label="名称" required><el-input v-model="createForm.name" maxlength="100" show-word-limit placeholder="如：资产同步脚本" /></el-form-item>
        <el-form-item label="有效期" required>
          <el-select v-model="createForm.expires_in_days" style="width: 100%">
            <el-option label="30 天" :value="30" />
            <el-option label="90 天" :value="90" />
            <el-option label="180 天" :value="180" />
            <el-option label="365 天" :value="365" />
          </el-select>
        </el-form-item>
        <el-alert type="info" :closable="false" show-icon title="权限说明" description="Token 使用创建时的当前工作组织，并继承您当时及后续有效的角色权限；撤销任职、停用账号或撤销 Token 后会立即失效。" />
      </el-form>
      <template #footer><el-button @click="createDialogVisible = false">取消</el-button><el-button type="primary" :loading="creating" :disabled="!canCreate" @click="createToken">创建</el-button></template>
    </el-dialog>

    <el-dialog :model-value="secretDialogVisible" title="请立即保存 API Token" width="620px" :close-on-click-modal="false" :close-on-press-escape="false" :show-close="false">
      <el-alert type="warning" :closable="false" show-icon title="该 Token 只显示这一次" description="系统仅保存不可逆摘要。关闭窗口后无法再次查看，如遗失请撤销并重新创建。" />
      <div class="secret-box">
        <div><strong>{{ createdItem?.name }}</strong><span>{{ createdItem?.organization_name }}</span></div>
        <el-input :model-value="createdToken" readonly type="textarea" :autosize="{ minRows: 2, maxRows: 4 }" />
        <el-button type="primary" plain @click="copyToken">复制 Token</el-button>
      </div>
      <template #footer><el-button type="primary" @click="closeSecretDialog">我已保存，关闭</el-button></template>
    </el-dialog>
  </div>
</template>

<style scoped>
.api-token-panel { margin-top: 18px; }
.token-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 18px; }
.token-heading h2 { margin: 0 0 8px; font-size: 17px; }
.form-hint { margin: 0 0 16px; color: var(--el-text-color-secondary); font-size: 13px; line-height: 1.65; }
.secret-box { display: grid; gap: 14px; margin-top: 18px; }
.secret-box > div { display: flex; align-items: center; justify-content: space-between; gap: 16px; color: var(--el-text-color-secondary); }
.secret-box strong { color: var(--el-text-color-primary); }
code { color: var(--el-color-primary); }
@media (max-width: 720px) { .token-heading { align-items: stretch; flex-direction: column; } .token-heading .el-button { align-self: flex-start; } }
</style>
