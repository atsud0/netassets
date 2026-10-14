export function assetDraftEndpoint(taskID: string, organizationID: string, draftID: string) {
  return `/inventory-tasks/${taskID}/organizations/${organizationID}/asset-drafts/${draftID}`
}

interface BatchAssetUpdateItem {
  success: boolean
  item?: { is_working_copy?: boolean }
}

interface BatchAssetUpdateSummary {
  success_count: number
  items: BatchAssetUpdateItem[]
}

export function batchAssetUpdateSuccessMessage(result: BatchAssetUpdateSummary) {
  const workingCount = result.items.filter((entry) => entry.success && entry.item?.is_working_copy).length
  if (workingCount === result.success_count && workingCount > 0) {
    return `已生成或更新 ${workingCount} 个待上报资产变更`
  }
  if (workingCount > 0) {
    return `已处理 ${result.success_count} 个网络资产，其中 ${workingCount} 个进入待上报工作版本`
  }
  return `已成功更新 ${result.success_count} 个网络资产`
}

export interface DraftTakeoverMeta {
  assigned_to?: string
  assigned_to_name?: string
  last_edited_by?: string
  last_edited_by_name?: string
  takeover_by_name?: string
  takeover_reason?: string
  taken_over_at?: string
}

export function draftTakeoverSummary(draft: DraftTakeoverMeta | undefined | null) {
  const reason = (draft?.takeover_reason ?? '').trim()
  const takenOverAt = (draft?.taken_over_at ?? '').trim()
  const assignedTo = (draft?.takeover_by_name ?? draft?.assigned_to_name ?? draft?.assigned_to ?? '').trim()
  if (!reason && !takenOverAt) return ''
  const who = assignedTo || '其他填报人'
  const when = takenOverAt ? new Date(takenOverAt).toLocaleString() : ''
  const head = when ? `${who} 于 ${when} 接管` : `${who} 已接管`
  return reason ? `${head}：${reason}` : head
}

export function draftLastEditedHint(draft: DraftTakeoverMeta | undefined | null) {
  const lastEditedBy = (draft?.last_edited_by_name ?? draft?.last_edited_by ?? '').trim()
  const assignedTo = (draft?.assigned_to_name ?? draft?.assigned_to ?? '').trim()
  if (!lastEditedBy || lastEditedBy === assignedTo) return ''
  return `最近由 ${lastEditedBy} 编辑`
}

export function normalizeDraftTakeoverReason(reason: string | undefined | null) {
  return (reason ?? '').trim()
}
