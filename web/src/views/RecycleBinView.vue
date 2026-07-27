<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getAPIErrorMessage } from '../api/client'
import { listRecycleBin, restoreRecycleBinItem, type RecycleBinItem, type RecycleBinResourceType } from '../api/recycle-bin'

const loading = ref(false)
const restoringID = ref('')
const activeTab = ref<RecycleBinResourceType>('asset')
const items = ref<RecycleBinItem[]>([])
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

const assetTypeLabels: Record<string, string> = {
  domain: '域名', ip: 'IP 地址', url: 'URL', service: '服务', other: '其他',
}
const assetStatusLabels: Record<string, string> = {
  active: '启用', pending: '待确认', disabled: '停用', archived: '归档',
}
const systemStatusLabels: Record<string, string> = {
  planning: '规划中', running: '运行中', suspended: '已暂停', offline: '已下线', archived: '已归档',
}

function formatTime(value: string) {
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}

function statusLabel(item: RecycleBinItem) {
  return item.resource_type === 'asset'
    ? (assetStatusLabels[item.status] ?? item.status)
    : (systemStatusLabels[item.status] ?? item.status)
}

function secondaryLabel(item: RecycleBinItem) {
  if (item.resource_type === 'business_system') return item.secondary_text
  const [assetType, ...rest] = item.secondary_text.split(' · ')
  const value = rest.join(' · ')
  return `${assetTypeLabels[assetType] ?? assetType} · ${value}`
}

async function load() {
  loading.value = true
  try {
    const result = await listRecycleBin({
      page: page.value,
      page_size: pageSize.value,
      resource_type: activeTab.value,
    })
    items.value = result.items
    total.value = result.total
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取回收站失败'))
  } finally {
    loading.value = false
  }
}

async function refreshAfterMutation() {
  await load()
  if (items.value.length === 0 && total.value > 0 && page.value > 1) {
    page.value -= 1
    await load()
  }
}

function handleTabChange(value: string | number) {
  activeTab.value = value as RecycleBinResourceType
  page.value = 1
  void load()
}

function handlePageChange(value: number) {
  page.value = value
  void load()
}

function handlePageSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  void load()
}

async function restore(item: RecycleBinItem) {
  const isSystem = item.resource_type === 'business_system'
  try {
    await ElMessageBox.confirm(
      isSystem
        ? `确定恢复业务系统“${item.display_name}”吗？恢复后系统将保持“已归档”状态，请确认后再手动启用。`
        : `确定恢复网络资产“${item.display_name}”吗？将恢复其原有归属、关联关系和网络区域。`,
      `恢复${isSystem ? '业务系统' : '网络资产'}`,
      { type: 'warning', confirmButtonText: '确认恢复', cancelButtonText: '取消' },
    )
  } catch {
    return
  }

  restoringID.value = item.id
  try {
    await restoreRecycleBinItem(item)
    ElMessage.success(isSystem ? '业务系统已恢复为归档状态' : '网络资产已恢复')
    await refreshAfterMutation()
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '恢复失败，请稍后重试'))
  } finally {
    restoringID.value = ''
  }
}

onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
        <p class="eyebrow">RECYCLE BIN</p>
        <h1>回收站</h1>
        <p>仅展示已软删除的网络资产和业务系统。恢复会保留原所属组织与关联信息；业务系统恢复后始终保持归档状态，需人工确认后再启用。</p>
      </div>
      <div class="page-heading-actions">
        <el-button :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="panel recycle-bin-notice">
      <strong>集团治理提示</strong>
      <span>恢复前会自动检查是否已有相同的在用资产或系统编码，避免重复台账；若存在冲突，请先处理重复数据。</span>
    </div>

    <div class="panel data-table-panel" v-loading="loading">
      <el-tabs v-model="activeTab" class="recycle-bin-tabs" @tab-change="handleTabChange">
        <el-tab-pane name="asset">
          <template #label>网络资产</template>
        </el-tab-pane>
        <el-tab-pane name="business_system">
          <template #label>业务系统</template>
        </el-tab-pane>
      </el-tabs>

      <el-table :data="items" row-key="id" empty-text="当前分类没有已删除项目">
        <el-table-column label="资源" min-width="230">
          <template #default="scope">
            <div class="audit-action-cell">
              <strong>{{ scope.row.display_name }}</strong>
              <span>{{ secondaryLabel(scope.row) }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="所属组织" min-width="180">
          <template #default="scope">
            <div class="audit-action-cell">
              <strong>{{ scope.row.organization_name }}</strong>
              <span>{{ scope.row.organization_code }}</span>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="删除前状态" min-width="120">
          <template #default="scope"><el-tag effect="plain" type="info">{{ statusLabel(scope.row) }}</el-tag></template>
        </el-table-column>
        <el-table-column label="删除时间" min-width="178">
          <template #default="scope">{{ formatTime(scope.row.deleted_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="118" fixed="right">
          <template #default="scope">
            <el-button type="primary" link :loading="restoringID === scope.row.id" @click="restore(scope.row)">恢复</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="table-pagination">
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
    </div>
  </section>
</template>

<style scoped>
.table-pagination { display: flex; justify-content: flex-end; margin-top: 18px; }
</style>
