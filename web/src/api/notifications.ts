import { apiClient } from './client'

export interface NotificationItem {
  key: string
  category: string
  title: string
  message: string
  level: 'info' | 'warning' | 'danger'
  resource_type: string
  resource_id: string
  action_path: string
  occurred_at: string
  due_at?: string
  read: boolean
}

export interface NotificationSummary {
  total: number
  unread_count: number
}

export interface NotificationPage {
  items: NotificationItem[]
  summary: NotificationSummary
  total: number
  page: number
  page_size: number
}

export async function listNotifications(params: { unread_only?: boolean; category?: string; page?: number; page_size?: number } = {}) {
  const { data } = await apiClient.get<NotificationPage>('/notifications', { params })
  return data
}

export async function loadNotificationSummary() {
  const { data } = await apiClient.get<{ summary: NotificationSummary }>('/notifications/summary')
  return data.summary
}

export async function markNotificationRead(key: string) {
  await apiClient.post('/notifications/read', { key })
}

export async function markAllNotificationsRead() {
  await apiClient.post('/notifications/read-all')
}
