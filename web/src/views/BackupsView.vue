<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ArrowDown } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { backupDownloadURL, createBackup, deleteBackup, listBackups, type BackupItem, verifyBackup } from '../api/backups'
import { getAPIErrorMessage } from '../api/client'

const loading = ref(false)
const creating = ref(false)
const verifyingID = ref('')
const deletingID = ref('')
const backups = ref<BackupItem[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

function formatDateTime(value: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium', hour12: false }).format(new Date(value))
}

function formatSize(value: number) {
  if (!Number.isFinite(value) || value < 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index += 1
  }
  return `${size >= 10 || index === 0 ? size.toFixed(0) : size.toFixed(1)} ${units[index]}`
}

async function loadBackups() {
  loading.value = true
  try {
    const result = await listBackups({ page: page.value, page_size: pageSize.value })
    backups.value = result.items
    total.value = result.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '加载备份列表失败'))
  } finally {
    loading.value = false
  }
}

async function createNewBackup() {
  try {
    await ElMessageBox.confirm('创建期间，系统会暂时拒绝新的保存、导入、下发等写入操作；读取不受影响。确认现在创建备份吗？', '创建系统备份', {
      type: 'warning',
      confirmButtonText: '开始备份',
      cancelButtonText: '取消',
      closeOnClickModal: false,
    })
  } catch {
    return
  }
  creating.value = true
  try {
    await createBackup()
    ElMessage.success('系统备份已创建并完成校验')
    page.value = 1
    await loadBackups()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '创建备份失败，请检查服务器日志后重试'))
  } finally {
    creating.value = false
  }
}

async function verify(item: BackupItem) {
  verifyingID.value = item.id
  try {
    const verified = await verifyBackup(item.id)
    backups.value = backups.value.map((backup) => backup.id === verified.id ? verified : backup)
    ElMessage.success('备份文件校验通过')
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '备份校验失败，请勿用于恢复'))
  } finally {
    verifyingID.value = ''
  }
}

async function refreshAfterMutation() {
  await loadBackups()
  if (backups.value.length === 0 && total.value > 0 && page.value > 1) {
    page.value -= 1
    await loadBackups()
  }
}

function handlePageChange(value: number) {
  page.value = value
  void loadBackups()
}

function handlePageSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  void loadBackups()
}

async function remove(item: BackupItem) {
  try {
    await ElMessageBox.confirm(`将永久删除备份“${item.id}”及其数据库、附件归档，且无法从回收站找回。确认删除吗？`, '删除系统备份', {
      type: 'warning',
      confirmButtonText: '永久删除',
      cancelButtonText: '取消',
      confirmButtonClass: 'el-button--danger',
    })
  } catch {
    return
  }
  deletingID.value = item.id
  try {
    await deleteBackup(item.id)
    ElMessage.success('备份已删除')
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '删除备份失败'))
  } finally {
    deletingID.value = ''
  }
}

onMounted(() => { void loadBackups() })
</script>

<template>
  <section class="page backups-page">
    <div class="page-header">
      <div>
        <h1>备份管理</h1>
        <p>创建并管理数据库及附件的一致性备份，仅集团管理员可操作。</p>
      </div>
      <div class="header-actions">
        <el-button :loading="loading" @click="loadBackups">刷新</el-button>
        <el-button type="primary" :loading="creating" @click="createNewBackup">创建备份</el-button>
      </div>
    </div>

    <el-alert class="restore-alert" type="warning" :closable="false" show-icon>
      <template #title>恢复操作仅能在部署宿主机执行</template>
      <p>为避免浏览器在线恢复带来的高风险，当前版本不提供在线恢复。请由具备宿主机权限的运维人员使用 <code>scripts/restore.sh</code>，并按脚本要求显式确认后执行。</p>
    </el-alert>

    <div class="panel" v-loading="loading">
      <el-empty v-if="!loading && backups.length === 0" description="尚未创建系统备份" :image-size="84">
        <el-button type="primary" @click="createNewBackup">创建首个备份</el-button>
      </el-empty>
      <el-table v-else :data="backups" stripe class="backup-table">
        <el-table-column label="备份编号" min-width="280">
          <template #default="{ row }"><code class="backup-id">{{ row.id }}</code></template>
        </el-table-column>
        <el-table-column label="创建时间" min-width="180">
          <template #default="{ row }">{{ formatDateTime(row.created_at) }}</template>
        </el-table-column>
        <el-table-column label="数据库" width="120">
          <template #default="{ row }">{{ formatSize(row.database_size) }}</template>
        </el-table-column>
        <el-table-column label="附件归档" width="120">
          <template #default="{ row }">{{ formatSize(row.data_size) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }"><el-tag :type="row.verified ? 'success' : 'info'">{{ row.verified ? '已校验' : '未校验' }}</el-tag></template>
        </el-table-column>
        <el-table-column label="操作" width="190" fixed="right" align="right" header-align="right" class-name="table-action-column">
          <template #default="{ row }">
            <el-button link type="primary" :loading="verifyingID === row.id" @click="verify(row)">校验</el-button>
            <el-dropdown trigger="click">
              <el-button link type="primary">下载<el-icon class="el-icon--right"><ArrowDown /></el-icon></el-button>
              <template #dropdown>
                <el-dropdown-menu>
                  <el-dropdown-item><a :href="backupDownloadURL(row.id, 'database')">数据库备份</a></el-dropdown-item>
                  <el-dropdown-item><a :href="backupDownloadURL(row.id, 'attachments')">附件归档</a></el-dropdown-item>
                  <el-dropdown-item><a :href="backupDownloadURL(row.id, 'manifest')">校验清单</a></el-dropdown-item>
                </el-dropdown-menu>
              </template>
            </el-dropdown>
            <el-button link type="danger" :loading="deletingID === row.id" @click="remove(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div v-if="total > 0" class="table-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="total" :current-page="page" :page-size="pageSize" :page-sizes="[20, 50, 100]" @current-change="handlePageChange" @size-change="handlePageSizeChange" />
      </div>
    </div>
  </section>
</template>

<style scoped>
.page-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 18px; margin-bottom: 18px; }
.page-header h1 { margin: 0 0 8px; font-size: 24px; }
.page-header p { margin: 0; color: var(--el-text-color-secondary); }
.header-actions { display: flex; gap: 10px; flex-shrink: 0; }
.restore-alert { margin-bottom: 18px; }
.restore-alert p { margin: 8px 0 0; line-height: 1.7; }
.restore-alert code, .backup-id { font-family: var(--el-font-family-monospace); }
.table-pagination { display: flex; justify-content: flex-end; margin-top: 18px; }
.backup-id { color: var(--el-text-color-regular); font-size: 12px; }
:deep(.el-dropdown-menu a) { color: inherit; display: block; text-decoration: none; }
@media (max-width: 720px) { .page-header { flex-direction: column; } .header-actions { width: 100%; } }
</style>
