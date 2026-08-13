import { describe, expect, it } from 'vitest'
import { assetDraftEndpoint, batchAssetUpdateSuccessMessage } from './inventory-drafts'

describe('assetDraftEndpoint', () => {
  it('routes task-bound working assets through the inventory draft API', () => {
    expect(assetDraftEndpoint('task-1', 'organization-1', 'draft-1')).toBe(
      '/inventory-tasks/task-1/organizations/organization-1/asset-drafts/draft-1',
    )
  })
})

describe('batchAssetUpdateSuccessMessage', () => {
  it('explains that organization-side changes remain pending reporting', () => {
    expect(batchAssetUpdateSuccessMessage({
      success_count: 2,
      items: [
        { success: true, item: { is_working_copy: true } },
        { success: true, item: { is_working_copy: true } },
      ],
    })).toBe('已生成或更新 2 个待上报资产变更')
  })

  it('keeps the formal-ledger wording for headquarters updates', () => {
    expect(batchAssetUpdateSuccessMessage({
      success_count: 1,
      items: [{ success: true, item: { is_working_copy: false } }],
    })).toBe('已成功更新 1 个网络资产')
  })
})
