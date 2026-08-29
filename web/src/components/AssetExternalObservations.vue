<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { apiClient, getAPIErrorMessage } from '../api/client'

interface Observation {
  id: string
  asset_id: string
  observation_type: 'domain_registration' | 'tls_certificate'
  endpoint_key: string
  status: 'pending' | 'success' | 'failed' | 'unsupported'
  source?: string
  source_reference?: string
  not_before?: string
  expires_at?: string
  last_attempt_at: string
  last_success_at?: string
  next_refresh_at: string
  consecutive_failures: number
  error_code?: string
  error_message?: string
}

const props = defineProps<{
  assetId?: string
  assetType?: string
  canRefresh?: boolean
}>()

const loading = ref(false)
const refreshing = ref(false)
const loadError = ref('')
const items = ref<Observation[]>([])

const typeLabels: Record<Observation['observation_type'], string> = {
  domain_registration: '域名注册到期',
  tls_certificate: 'TLS 证书',
}
const statusLabels: Record<Observation['status'], string> = {
  pending: '待采集',
  success: '正常',
  failed: '采集失败',
  unsupported: '不支持',
}
const statusTypes: Record<Observation['status'], 'success' | 'warning' | 'danger' | 'info'> = {
  pending: 'info',
  success: 'success',
  failed: 'danger',
  unsupported: 'warning',
}

const supported = computed(() => props.assetType === 'domain' || props.assetType === 'url')

watch(() => props.assetId, (value) => {
  if (value && supported.value) void load()
  else {
    items.value = []
    loadError.value = ''
  }
}, { immediate: true })

async function load() {
  if (!props.assetId) return
  loading.value = true
  loadError.value = ''
  try {
    const { data } = await apiClient.get<{ items: Observation[] }>(`/assets/${props.assetId}/external-observations`)
    items.value = data.items
  } catch (error) {
    loadError.value = getAPIErrorMessage(error, '读取域名与证书信息失败')
  } finally {
    loading.value = false
  }
}

async function refresh() {
  if (!props.assetId || refreshing.value) return
  refreshing.value = true
  try {
    const { data } = await apiClient.post<{ result: { items: Observation[] } }>(`/assets/${props.assetId}/external-observations/refresh`)
    items.value = data.result.items
    ElMessage.success('域名与证书信息已刷新')
  } catch (error) {
    ElMessage.error(getAPIErrorMessage(error, '刷新域名与证书信息失败'))
  } finally {
    refreshing.value = false
  }
}

function typeLabel(item: Observation) {
  return typeLabels[item.observation_type] ?? item.observation_type
}

function statusLabel(item: Observation) {
  return statusLabels[item.status] ?? item.status
}

function formatDateTime(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleString()
}

function formatDate(value?: string) {
  if (!value) return '—'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '—' : date.toLocaleDateString()
}

function remainingText(item: Observation) {
  if (!item.expires_at) return '—'
  const date = new Date(item.expires_at)
  if (Number.isNaN(date.getTime())) return '—'
  const today = new Date()
  today.setHours(0, 0, 0, 0)
  const expiry = new Date(date)
  expiry.setHours(0, 0, 0, 0)
  const days = Math.round((expiry.getTime() - today.getTime()) / 86_400_000)
  if (days < 0) return `已过期 ${Math.abs(days)} 天`
  if (days === 0) return '今天到期'
  return `剩余 ${days} 天`
}

function expiryTag(item: Observation) {
  if (item.status !== 'success' || !item.expires_at) return 'info'
  const date = new Date(item.expires_at)
  if (Number.isNaN(date.getTime())) return 'info'
  const days = Math.ceil((date.getTime() - Date.now()) / 86_400_000)
  if (days < 0) return 'danger'
  if (days <= 30) return 'warning'
  return 'success'
}

function hasStoredExpiry(item: Observation) {
  return Boolean(item.expires_at)
}
</script>

<template>
  <section class="external-observations">
    <div class="section-heading">
      <div>
        <h3>域名与证书信息</h3>
        <p>来自 RDAP 与公网 TLS 握手，不会覆盖人工维护的资产有效期。</p>
      </div>
      <el-button v-if="canRefresh" size="small" type="primary" plain :loading="refreshing" @click="refresh">立即刷新</el-button>
    </div>

    <el-alert v-if="loadError" :title="loadError" type="error" :closable="false" show-icon />

    <div v-loading="loading" class="observation-body">
      <el-empty v-if="!loadError && items.length === 0" description="暂无外部采集记录" :image-size="72" />
      <article v-for="item in items" :key="item.id" class="observation-card">
        <header>
          <strong>{{ typeLabel(item) }}</strong>
          <el-tag :type="statusTypes[item.status] ?? 'info'" effect="plain" size="small">{{ statusLabel(item) }}</el-tag>
        </header>

        <dl>
          <div><dt>采集对象</dt><dd>{{ item.endpoint_key }}</dd></div>
          <div><dt>剩余有效期</dt><dd>{{ remainingText(item) }}</dd></div>
          <div><dt>到期时间</dt><dd>{{ formatDate(item.expires_at) }}</dd></div>
          <div v-if="item.observation_type === 'tls_certificate'"><dt>证书生效时间</dt><dd>{{ formatDate(item.not_before) }}</dd></div>
          <div><dt>数据来源</dt><dd>{{ item.source || '—' }}</dd></div>
          <div><dt>最近尝试</dt><dd>{{ formatDateTime(item.last_attempt_at) }}</dd></div>
          <div><dt>最近成功</dt><dd>{{ formatDateTime(item.last_success_at) }}</dd></div>
          <div><dt>下次刷新</dt><dd>{{ formatDateTime(item.next_refresh_at) }}</dd></div>
        </dl>

        <p v-if="item.status === 'failed' && hasStoredExpiry(item)" class="observation-note warning">
          本次刷新失败，以上到期时间来自最近一次成功采集。
        </p>
        <p v-if="item.error_message" class="observation-note danger">{{ item.error_message }}</p>
      </article>
    </div>
  </section>
</template>

<style scoped>
.external-observations { margin-top: 22px; }
.section-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.section-heading h3 { margin: 0; font-size: 16px; }
.section-heading p { margin: 4px 0 0; color: #64748b; font-size: 12px; line-height: 1.5; }
.observation-body { min-height: 72px; margin-top: 12px; display: grid; gap: 12px; }
.observation-card { padding: 14px; border: 1px solid #dbe3ef; border-radius: 10px; background: #f8fafc; }
.observation-card header { display: flex; align-items: center; justify-content: space-between; margin-bottom: 10px; }
.observation-card header strong { color: #172b4d; }
.observation-card dl { margin: 0; display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 10px 14px; }
.observation-card dt { color: #64748b; font-size: 12px; margin-bottom: 2px; }
.observation-card dd { margin: 0; color: #1f2937; font-size: 13px; overflow-wrap: anywhere; }
.observation-note { margin: 10px 0 0; font-size: 12px; line-height: 1.5; }
.observation-note.warning { color: #9a6700; }
.observation-note.danger { color: #b42318; overflow-wrap: anywhere; }
@media (max-width: 560px) { .observation-card dl { grid-template-columns: 1fr; } }
</style>
