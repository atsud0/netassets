import { apiClient } from './client'

export interface BackupItem {
  id: string
  created_at: string
  database_size: number
  data_size: number
  verified: boolean
}

export interface BackupListResponse {
  items: BackupItem[]
  total: number
  page: number
  page_size: number
}

export async function listBackups(params: { page: number; page_size: number }) {
  const { data } = await apiClient.get<BackupListResponse>('/backups', { params })
  return data
}

export async function createBackup() {
  const { data } = await apiClient.post<{ item: BackupItem }>('/backups', {}, { timeout: 300_000 })
  return data.item
}

export async function verifyBackup(id: string) {
  const { data } = await apiClient.get<{ item: BackupItem }>(`/backups/${encodeURIComponent(id)}/verify`, { timeout: 300_000 })
  return data.item
}

export async function deleteBackup(id: string) {
  await apiClient.delete(`/backups/${encodeURIComponent(id)}`)
}

export function backupDownloadURL(id: string, file: 'database' | 'attachments' | 'manifest') {
  return `/api/v1/backups/${encodeURIComponent(id)}/files/${file}`
}
