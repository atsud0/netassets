<script setup lang="ts">
import RowActions from '../components/RowActions.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRoute, useRouter } from 'vue-router'
import { getAPIErrorMessage, getDownloadAPIErrorMessage } from '../api/client'
import { deleteExportJob, downloadExportJob, listExportJobs, type ExportJob } from '../api/export-jobs'

const route = useRoute()
const router = useRouter()
const loading = ref(false)
const downloadingID = ref('')
const deletingID = ref('')
const items = ref<ExportJob[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const status = ref('')
const resourceType = ref('')
let timer: number | undefined

const hasActiveJobs = computed(() => items.value.some((item) => item.status === 'queued' || item.status === 'running'))
const resourceLabels: Record<string, string> = { asset: '网络资产', business_system: '业务系统', inventory_task: '填报任务' }
const statusLabels: Record<string, string> = { queued: '等待处理', running: '正在生成', completed: '已完成', failed: '生成失败', expired: '文件已过期' }

function statusTag(value: string) {
  if (value === 'completed') return 'success'
  if (value === 'failed') return 'danger'
  if (value === 'expired') return 'info'
  return 'warning'
}
function formatTime(value?: string) {
  return value ? new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'medium', hour12: false }).format(new Date(value)) : '—'
}
function formatSize(value: number) {
  if (!value) return '—'
  const units = ['B', 'KB', 'MB', 'GB']
  let size = value
  let unit = 0
  while (size >= 1024 && unit < units.length - 1) { size /= 1024; unit += 1 }
  return `${unit === 0 || size >= 10 ? size.toFixed(0) : size.toFixed(1)} ${units[unit]}`
}
function progress(item: ExportJob) {
  if (item.status === 'completed') return 100
  if (!item.total_rows) return item.status === 'running' ? 10 : 0
  return Math.min(99, Math.round(item.processed_rows * 100 / item.total_rows))
}

async function load(silent = false) {
  if (!silent) loading.value = true
  try {
    const data = await listExportJobs({ page: page.value, page_size: pageSize.value, status: status.value || undefined, resource_type: resourceType.value || undefined })
    items.value = data.items
    total.value = data.total
  } catch (error) {
    if (!silent) ElMessage.error(getAPIErrorMessage(error, '读取导出任务失败'))
  } finally {
    if (!silent) loading.value = false
  }
}
async function changeFilter() { page.value = 1; await load() }
function changePage(value: number) { page.value = value; void load() }
function changePageSize(value: number) { pageSize.value = value; page.value = 1; void load() }

async function download(item: ExportJob) {
  downloadingID.value = item.id
  try {
    await downloadExportJob(item)
    ElMessage.success('导出文件已开始下载')
  } catch (error) {
    ElMessage.error(await getDownloadAPIErrorMessage(error, '下载导出文件失败'))
  } finally {
    downloadingID.value = ''
  }
}
async function remove(item: ExportJob) {
  try {
    await ElMessageBox.confirm(`确认删除“${item.file_name || resourceLabels[item.resource_type] + '导出任务'}”吗？已生成的文件也会一并清理。`, '删除导出任务', {
      type: 'warning', confirmButtonText: '删除', cancelButtonText: '取消',
    })
  } catch { return }
  deletingID.value = item.id
  try {
    await deleteExportJob(item.id)
    ElMessage.success('导出任务已删除')
    await load()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '删除导出任务失败'))
  } finally { deletingID.value = '' }
}

function focusJob() {
  const id = typeof route.query.job_id === 'string' ? route.query.job_id : ''
  if (!id) return
  window.setTimeout(() => document.querySelector(`[data-job-id="${CSS.escape(id)}"]`)?.scrollIntoView({ behavior: 'smooth', block: 'center' }), 100)
}
watch(() => route.query.job_id, focusJob)
watch(hasActiveJobs, (active) => {
  if (timer !== undefined) window.clearInterval(timer)
  timer = active ? window.setInterval(() => { void load(true) }, 2_000) : undefined
}, { immediate: true })
onMounted(async () => { await load(); focusJob() })
onBeforeUnmount(() => { if (timer !== undefined) window.clearInterval(timer) })
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
        <h1>导出任务</h1>
      </div>
      <div class="page-heading-actions">
        <el-button :loading="loading" @click="load()">刷新</el-button>
        <el-button @click="router.push('/assets')">返回网络资产</el-button>
      </div>
    </div>

    <div class="panel export-filter-panel">
      <el-select v-model="resourceType" clearable placeholder="全部资源" @change="changeFilter">
        <el-option label="网络资产" value="asset" /><el-option label="业务系统" value="business_system" /><el-option label="填报任务" value="inventory_task" />
      </el-select>
      <el-select v-model="status" clearable placeholder="全部状态" @change="changeFilter">
        <el-option v-for="(label, value) in statusLabels" :key="value" :label="label" :value="value" />
      </el-select>
      <span>共 {{ total }} 个任务</span>
    </div>

    <div class="panel data-table-panel" v-loading="loading">
      <el-table :data="items" row-key="id" empty-text="暂无后台导出任务">
        <el-table-column label="导出内容" min-width="190">
          <template #default="scope"><div class="job-name" :data-job-id="scope.row.id"><strong>{{ resourceLabels[scope.row.resource_type] }}</strong><span>{{ scope.row.file_name || 'XLSX 后台导出' }}</span></div><span class="list-secondary">{{ formatSize(scope.row.file_size) }}</span></template>
        </el-table-column>
        <el-table-column label="状态" width="100"><template #default="scope"><el-tag :type="statusTag(scope.row.status)" effect="plain">{{ statusLabels[scope.row.status] }}</el-tag></template></el-table-column>
        <el-table-column label="进度" min-width="150">
          <template #default="scope"><el-progress :percentage="progress(scope.row)" :status="scope.row.status === 'failed' ? 'exception' : scope.row.status === 'completed' ? 'success' : undefined" /><span class="progress-copy">{{ scope.row.processed_rows }} / {{ scope.row.total_rows }} 条</span></template>
        </el-table-column>

        <el-table-column label="创建 / 完成时间" min-width="175"><template #default="scope"><div class="job-name"><strong>{{ formatTime(scope.row.created_at) }}</strong><span>{{ scope.row.completed_at ? `完成：${formatTime(scope.row.completed_at)}` : '尚未完成' }}</span></div></template></el-table-column>
        <el-table-column label="有效期 / 失败原因" min-width="175"><template #default="scope"><span v-if="scope.row.error_message" class="error-copy">{{ scope.row.error_message }}</span><span v-else>{{ scope.row.expires_at ? `保留至 ${formatTime(scope.row.expires_at)}` : '—' }}</span></template></el-table-column>
        <el-table-column label="操作" width="88" fixed="right" align="right" header-align="right" class-name="table-action-column">
          <template #default="scope"><RowActions :label="`操作：${scope.row.name || scope.row.title || scope.row.code || scope.row.display_name || scope.row.id}`"><el-button link type="primary" :disabled="scope.row.status !== 'completed'" :loading="downloadingID === scope.row.id" @click="download(scope.row)">下载</el-button><el-button link type="danger" :disabled="scope.row.status === 'running'" :loading="deletingID === scope.row.id" @click="remove(scope.row)">删除</el-button></RowActions></template>
        </el-table-column>
      </el-table>
      <div v-if="total > 0" class="table-pagination"><el-pagination background layout="total, sizes, prev, pager, next" :total="total" :current-page="page" :page-size="pageSize" :page-sizes="[20, 50, 100]" @current-change="changePage" @size-change="changePageSize" /></div>
    </div>
  </section>
</template>

<style scoped>
.export-filter-panel { display:flex; align-items:center; gap:12px; margin-bottom:18px; }
.export-filter-panel .el-select { width:180px; }
.export-filter-panel span { color:#64748b; font-size:13px; }
.job-name { display:flex; flex-direction:column; gap:4px; }
.job-name span,.progress-copy { color:#64748b; font-size:12px; }
.error-copy { color:#b42318; font-size:12px; line-height:1.5; }
.table-pagination { display:flex; justify-content:flex-end; margin-top:18px; }
</style>
