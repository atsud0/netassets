import { apiClient } from './client'

export type ExportJobResourceType = 'asset' | 'business_system' | 'inventory_task'
export type ExportJobStatus = 'queued' | 'running' | 'completed' | 'failed' | 'expired'

export interface ExportJob {
  id: string
  resource_type: ExportJobResourceType
  format: 'xlsx'
  filter: Record<string, unknown>
  status: ExportJobStatus
  total_rows: number
  processed_rows: number
  file_name?: string
  content_type?: string
  file_size: number
  error_message?: string
  started_at?: string
  completed_at?: string
  expires_at?: string
  created_at: string
  updated_at: string
}

export interface ExportJobPage {
  items: ExportJob[]
  total: number
  page: number
  page_size: number
}

export async function listExportJobs(params: { page?: number; page_size?: number; status?: string; resource_type?: string } = {}) {
  const { data } = await apiClient.get<ExportJobPage>('/export-jobs', { params })
  return data
}

export async function getExportJob(id: string) {
  const { data } = await apiClient.get<{ item: ExportJob }>(`/export-jobs/${id}`)
  return data.item
}

export async function createExportJob(resourceType: ExportJobResourceType, filter: Record<string, unknown> = {}) {
  const { data } = await apiClient.post<{ item: ExportJob }>('/export-jobs', {
    resource_type: resourceType,
    format: 'xlsx',
    filter,
  })
  return data.item
}

export async function downloadExportJob(item: ExportJob) {
  const response = await apiClient.get<Blob>(`/export-jobs/${item.id}/download`, { responseType: 'blob', timeout: 10 * 60_000 })
  const href = URL.createObjectURL(response.data)
  const link = document.createElement('a')
  link.href = href
  link.download = item.file_name || '后台导出.xlsx'
  link.click()
  URL.revokeObjectURL(href)
}

export async function deleteExportJob(id: string) {
  await apiClient.delete(`/export-jobs/${id}`)
}
