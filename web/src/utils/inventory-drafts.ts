export function assetDraftEndpoint(taskID: string, organizationID: string, draftID: string) {
  return `/inventory-tasks/${taskID}/organizations/${organizationID}/asset-drafts/${draftID}`
}
