import { apiClient } from './client'

export type RecycleBinResourceType = 'asset' | 'business_system'

export interface RecycleBinItem {
  id: string
  resource_type: RecycleBinResourceType
  display_name: string
  secondary_text: string
  organization_id: string
  organization_code: string
  organization_name: string
  status: string
  deleted_at: string
}

export interface RecycleBinListResponse {
  items: RecycleBinItem[]
  total: number
  page: number
  page_size: number
}

export async function listRecycleBin(params: {
  page: number
  page_size: number
  resource_type: RecycleBinResourceType
}) {
  const { data } = await apiClient.get<RecycleBinListResponse>('/recycle-bin', { params })
  return data
}

export async function restoreRecycleBinItem(item: RecycleBinItem) {
  const resourcePath = item.resource_type === 'asset' ? 'assets' : 'business-systems'
  await apiClient.post(`/recycle-bin/${resourcePath}/${item.id}/restore`)
}
