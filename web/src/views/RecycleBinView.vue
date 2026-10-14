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
const organizationTypeLabels: Record<string, string> = { group: '集团', subsidiary: '子公司', department: '部门', other: '其他' }

function formatTime(value: string) {
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}

function statusLabel(item: RecycleBinItem) {
  if (item.resource_type === 'asset') return assetStatusLabels[item.status] ?? item.status
  if (item.resource_type === 'business_system') return systemStatusLabels[item.status] ?? item.status
  return item.status === 'disabled' ? '停用' : item.status
}

function secondaryLabel(item: RecycleBinItem) {
  if (item.resource_type === 'business_system') return item.secondary_text
  if (item.resource_type === 'organization') {
    const [code, type] = item.secondary_text.split(' · ')
    return `${code} · ${organizationTypeLabels[type] ?? type}`
  }
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
  const isOrganization = item.resource_type === 'organization'
  try {
    await ElMessageBox.confirm(
      isSystem
        ? `确定恢复业务系统“${item.display_name}”吗？恢复后系统将保持“已归档”状态，请确认后再手动启用。`
        : isOrganization
          ? `确定恢复组织“${item.display_name}”吗？恢复后组织将保持停用状态，需在组织架构中确认层级后再启用。`
          : `确定恢复网络资产“${item.display_name}”吗？将恢复其原有归属、关联关系和网络区域。`,
      `恢复${isSystem ? '业务系统' : isOrganization ? '组织机构' : '网络资产'}`,
      { type: 'warning', confirmButtonText: '确认恢复', cancelButtonText: '取消' },
    )
  } catch {
    return
  }

  restoringID.value = item.id
  try {
    await restoreRecycleBinItem(item)
    ElMessage.success(isSystem ? '业务系统已恢复为归档状态' : isOrganization ? '组织已恢复为停用状态' : '网络资产已恢复')
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
    <div class="page-heading"><h1>回收站</h1>
      <div class="page-heading-actions">
        <el-button :loading="loading" @click="load">刷新</el-button>
      </div>
    </div>

    <div class="panel recycle-bin-notice">
      <strong>集团治理提示</strong>
      <span>恢复前会检查资源冲突和组织上级关系；组织恢复后不会自动启用，避免未经确认重新进入业务范围。</span>
    </div>

    <div class="panel data-table-panel" v-loading="loading">
      <el-tabs v-model="activeTab" class="recycle-bin-tabs" @tab-change="handleTabChange">
        <el-tab-pane name="asset">
          <template #label>网络资产</template>
        </el-tab-pane>
        <el-tab-pane name="business_system">
          <template #label>业务系统</template>
        </el-tab-pane>
        <el-tab-pane name="organization">
          <template #label>组织机构</template>
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
        <el-table-column :label="activeTab === 'organization' ? '组织名称' : '所属组织'" min-width="180">
          <template #default="scope">
            {{ scope.row.organization_name }}
          </template>
        </el-table-column>
        <el-table-column label="删除前状态" min-width="120">
          <template #default="scope"><el-tag effect="plain" type="info">{{ statusLabel(scope.row) }}</el-tag></template>
        </el-table-column>
        <el-table-column label="删除时间" min-width="178">
          <template #default="scope">{{ formatTime(scope.row.deleted_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="84" fixed="right" align="right" header-align="right" class-name="table-action-column">
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
.recycle-bin-notice { margin-bottom: 16px; }
.table-pagination { display: flex; justify-content: flex-end; margin-top: 18px; }
</style>
