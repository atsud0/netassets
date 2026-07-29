<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { getAPIErrorMessage } from '../api/client'
import {
  listNotifications,
  markAllNotificationsRead,
  markNotificationRead,
  type NotificationItem,
} from '../api/notifications'

const router = useRouter()
const loading = ref(false)
const markingAll = ref(false)
const items = ref<NotificationItem[]>([])
const unreadOnly = ref(false)
const category = ref('')
const summary = ref({ total: 0, unread_count: 0 })
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

const categoryOptions = [
  { value: 'asset_change', label: '资产变更' },
  { value: 'business_system_change', label: '系统变更' },
  { value: 'finding_received', label: '安全发现' },
  { value: 'inventory_task_review', label: '填报审核' },
  { value: 'inventory_task_received', label: '填报任务' },
  { value: 'inventory_task_returned', label: '填报退回' },
  { value: 'inventory_task_due_soon', label: '填报临期' },
  { value: 'inventory_task_overdue', label: '填报逾期' },
  { value: 'work_order_review', label: '待集团复测' },
  { value: 'work_order_received', label: '新工单' },
  { value: 'work_order_returned', label: '退回整改' },
  { value: 'work_order_overdue', label: '工单逾期' },
  { value: 'work_order_due_soon', label: '工单临期' },
  { value: 'risk_review', label: '风险复核' },
  { value: 'quality_review', label: '质量复核' },
  { value: 'quality_received', label: '质量整改' },
  { value: 'quality_returned', label: '质量退回' },
  { value: 'quality_exemption', label: '豁免到期' },
]

const displayedItems = computed(() => items.value)
const levelLabel: Record<string, string> = { info: '提醒', warning: '重要', danger: '紧急' }
const categoryLabel = (value: string) => categoryOptions.find((item) => item.value === value)?.label ?? value

function formatTime(value?: string) {
  if (!value) return '—'
  return new Date(value).toLocaleString('zh-CN', { hour12: false })
}

function formatDue(value?: string) {
  if (!value) return ''
  return `截止：${formatTime(value)}`
}

async function load() {
  loading.value = true
  try {
    const data = await listNotifications({ unread_only: unreadOnly.value, category: category.value || undefined, page: page.value, page_size: pageSize.value })
    items.value = data.items
    total.value = data.total
    summary.value = data.summary
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '读取消息失败'))
  } finally {
    loading.value = false
  }
}

async function markRead(item: NotificationItem) {
  if (item.read) return
  try {
    await markNotificationRead(item.key)
    item.read = true
    summary.value.unread_count = Math.max(0, summary.value.unread_count - 1)
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '标记消息失败'))
  }
}

async function openItem(item: NotificationItem) {
  await markRead(item)
  if (item.action_path) await router.push(item.action_path)
}

async function markAllRead() {
  if (!summary.value.unread_count) return
  markingAll.value = true
  try {
    await markAllNotificationsRead()
    items.value.forEach((item) => { item.read = true })
    summary.value.unread_count = 0
    ElMessage.success('已全部标记为已读')
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '全部标记已读失败'))
  } finally {
    markingAll.value = false
  }
}

async function changeFilter() {
  page.value = 1
  await load()
}

function changePage(nextPage: number) {
  page.value = nextPage
  void load()
}

function changePageSize(nextPageSize: number) {
  pageSize.value = nextPageSize
  page.value = 1
  void load()
}

onMounted(load)
</script>

<template>
  <section>
    <div class="page-heading">
      <div>
        <p class="eyebrow">IN-APP NOTIFICATIONS</p>
        <h1>消息中心</h1>
        <p>集中查看填报任务、台账变更、安全发现、安全工单和数据质量整改等组织协作消息。当前仅提供站内提醒，不发送外部通知。</p>
      </div>
      <div class="page-heading-actions">
        <el-button :loading="markingAll" :disabled="!summary.unread_count" @click="markAllRead">全部标记已读</el-button>
      </div>
    </div>

    <div class="panel notification-summary-panel">
      <div class="notification-stat"><span>当前消息</span><strong>{{ summary.total }}</strong></div>
      <div class="notification-stat unread"><span>未读消息</span><strong>{{ summary.unread_count }}</strong></div>
      <el-switch v-model="unreadOnly" active-text="只看未读" @change="changeFilter" />
      <el-select v-model="category" clearable placeholder="全部分类" @change="changeFilter">
        <el-option v-for="item in categoryOptions" :key="item.value" :label="item.label" :value="item.value" />
      </el-select>
    </div>

    <div v-loading="loading" class="notification-list">
      <div v-if="!displayedItems.length && !loading" class="panel empty-state">当前没有需要关注的消息。</div>
      <article
        v-for="item in displayedItems"
        :key="item.key"
        class="panel notification-card"
        :class="[{ unread: !item.read }, `level-${item.level}`]"
      >
        <div class="notification-icon">{{ item.level === 'danger' ? '!' : item.level === 'warning' ? '!' : 'i' }}</div>
        <div class="notification-body">
          <div class="notification-card-heading">
            <div>
              <span class="notification-category">{{ categoryLabel(item.category) }}</span>
              <h2>{{ item.title }}</h2>
            </div>
            <el-tag :type="item.level === 'danger' ? 'danger' : item.level === 'warning' ? 'warning' : 'info'" effect="plain">
              {{ levelLabel[item.level] }}
            </el-tag>
          </div>
          <p>{{ item.message }}</p>
          <div class="notification-meta">
            <span>{{ formatTime(item.occurred_at) }}</span>
            <span v-if="item.due_at">{{ formatDue(item.due_at) }}</span>
            <span v-if="item.read" class="read-label">已读</span>
            <span v-else class="unread-label">未读</span>
          </div>
        </div>
        <div class="notification-actions">
          <el-button v-if="!item.read" link @click="markRead(item)">标记已读</el-button>
          <el-button type="primary" link @click="openItem(item)">查看处理</el-button>
        </div>
      </article>
    </div>
    <div class="table-pagination" v-if="total > 0">
      <el-pagination
        background
        layout="total, sizes, prev, pager, next"
        :total="total"
        :current-page="page"
        :page-size="pageSize"
        :page-sizes="[20, 50, 100]"
        @current-change="changePage"
        @size-change="changePageSize"
      />
    </div>
  </section>
</template>
