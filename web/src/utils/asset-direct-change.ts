// A group administrator writing the formal asset ledger bypasses the
// working-version reporting path, so the backend demands a change reason and
// raises a recheck item for the owning organization. These helpers keep the
// "is this a direct ledger write" decision in one place so the create, edit,
// archive, delete, batch and import paths cannot drift apart.

interface DirectWriteContext {
  isGroupAdmin: boolean
  inventoryMode: boolean
}

export function needsDirectChangeReason(context: DirectWriteContext) {
  return context.isGroupAdmin && !context.inventoryMode
}

export function assetDirectBatchEndpointQuery(context: DirectWriteContext, reason: string) {
  const trimmed = reason.trim()
  if (!needsDirectChangeReason(context) || !trimmed) return {}
  return { direct_change_reason: trimmed }
}
