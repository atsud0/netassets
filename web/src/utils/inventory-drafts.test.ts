import { describe, expect, it } from 'vitest'
import { assetDraftEndpoint } from './inventory-drafts'

describe('assetDraftEndpoint', () => {
  it('routes task-bound working assets through the inventory draft API', () => {
    expect(assetDraftEndpoint('task-1', 'organization-1', 'draft-1')).toBe(
      '/inventory-tasks/task-1/organizations/organization-1/asset-drafts/draft-1',
    )
  })
})
