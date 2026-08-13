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
