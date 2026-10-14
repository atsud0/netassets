<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface ListResponse<T> { items: T[]; total: number; page: number; page_size: number }
interface ScanBatch {
  id: string
  organization_name?: string
  resource_type?: string
  severity?: string
  asset_count: number
  system_count: number
  enabled_rule_count: number
  total_issue_count: number
  error_count: number
  warning_count: number
  auto_closed_count: number
  trigger_type: 'manual' | 'scheduled'
  scheduled_for?: string
  created_by_name?: string
  created_at: string
}

const router = useRouter()
const loading = ref(false)
const items = ref<ScanBatch[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

function formatTime(value?: string) {
  if (!value) return '—'
  return new Intl.DateTimeFormat('zh-CN', {
    year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hour12: false,
  }).format(new Date(value))
}

function resourceTypeLabel(value?: string) {
  return value === 'asset' ? '网络资产' : value === 'business_system' ? '业务系统' : '全部对象'
}

function severityLabel(value?: string) {
  return value === 'error' ? '错误' : value === 'warning' ? '警告' : '全部级别'
}

async function loadHistory() {
  loading.value = true
  try {
    const response = await apiClient.get<ListResponse<ScanBatch>>('/data-quality-scan-batches', {
      params: { page: page.value, page_size: pageSize.value },
    })
    items.value = response.data.items
    total.value = response.data.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取质量巡检历史失败'))
  } finally {
    loading.value = false
  }
}

function handlePageChange(value: number) {
  page.value = value
  void loadHistory()
}

function handlePageSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  void loadHistory()
}

onMounted(() => { void loadHistory() })
</script>

<template>
  <section class="page-stack">
    <div class="page-heading"><h1>巡检历史</h1>
      <div class="page-heading-actions">
        <el-button :loading="loading" @click="loadHistory">刷新</el-button>
        <el-button @click="router.push('/data-quality-issues')">返回质量整改</el-button>
        <el-button type="primary" @click="router.push('/data-governance')">执行质量巡检</el-button>
      </div>
    </div>

    <div class="panel inspection-history-panel" v-loading="loading">
      <div class="table-panel-heading">
        <div>
          <strong>质量巡检批次</strong>
          <span>每次巡检均完整保存执行范围、规则数量、问题数量和自动关闭结果</span>
        </div>
      </div>
      <div class="inspection-history-table-scroll">
        <el-table :data="items" stripe empty-text="尚未执行持久化质量巡检">
          <el-table-column label="执行时间" width="155"><template #default="scope">{{ formatTime(scope.row.created_at) }}</template></el-table-column>
          <el-table-column label="触发方式" width="100"><template #default="scope"><el-tag :type="scope.row.trigger_type === 'scheduled' ? 'success' : 'info'">{{ scope.row.trigger_type === 'scheduled' ? '系统自动' : '人工执行' }}</el-tag></template></el-table-column>
          <el-table-column label="巡检范围" min-width="165"><template #default="scope"><strong>{{ scope.row.organization_name || '全集团' }}</strong><div class="muted-text">{{ resourceTypeLabel(scope.row.resource_type) }} · {{ severityLabel(scope.row.severity) }}</div></template></el-table-column>
          <el-table-column prop="enabled_rule_count" label="启用规则" width="90" />
          <el-table-column label="扫描资源" width="90"><template #default="scope">{{ scope.row.asset_count + scope.row.system_count }}</template></el-table-column>
          <el-table-column label="命中问题" width="150"><template #default="scope">共 {{ scope.row.total_issue_count }} 项<span class="list-secondary">错误 {{ scope.row.error_count }} · 警告 {{ scope.row.warning_count }}</span></template></el-table-column>


          <el-table-column prop="auto_closed_count" label="自动关闭" width="90" />
          <el-table-column label="执行人" min-width="120"><template #default="scope">{{ scope.row.trigger_type === 'scheduled' ? '系统调度器' : (scope.row.created_by_name || '未知') }}</template></el-table-column>
        </el-table>
      </div>
      <div class="table-pagination">
        <el-pagination background layout="total, sizes, prev, pager, next" :total="total" :current-page="page" :page-size="pageSize" :page-sizes="[20, 50, 100]" @current-change="handlePageChange" @size-change="handlePageSizeChange" />
      </div>
    </div>
  </section>
</template>

<style scoped>
.inspection-history-panel { min-width:0; padding-top:14px; }
.inspection-history-table-scroll { width:100%; min-width:0; overflow-x:auto; overscroll-behavior-x:contain; }
.inspection-history-table-scroll :deep(.el-table) { min-width:1080px; }
.table-pagination { display:flex; justify-content:flex-end; margin-top:18px; }
@media (max-width:760px) {
  .table-pagination { justify-content:flex-start; overflow-x:auto; padding-bottom:2px; }
}
</style>
