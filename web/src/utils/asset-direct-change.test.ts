import { describe, expect, it } from 'vitest'
import { assetDirectBatchEndpointQuery, needsDirectChangeReason } from './asset-direct-change'

describe('needsDirectChangeReason', () => {
  it('requires a reason when headquarters writes the formal ledger directly', () => {
    expect(needsDirectChangeReason({ isGroupAdmin: true, inventoryMode: false })).toBe(true)
  })

  it('does not require a reason on the subsidiary reporting path', () => {
    expect(needsDirectChangeReason({ isGroupAdmin: false, inventoryMode: false })).toBe(false)
    expect(needsDirectChangeReason({ isGroupAdmin: true, inventoryMode: true })).toBe(false)
  })

  it('does not require a reason while acting inside an inventory task', () => {
    expect(needsDirectChangeReason({ isGroupAdmin: false, inventoryMode: true })).toBe(false)
  })
})

describe('assetDirectBatchEndpointQuery', () => {
  it('omits the reason when headquarters is not the writer', () => {
    expect(assetDirectBatchEndpointQuery({ isGroupAdmin: false, inventoryMode: false }, '')).toEqual({})
  })

  it('carries the reason for a direct bulk ledger write', () => {
    expect(assetDirectBatchEndpointQuery({ isGroupAdmin: true, inventoryMode: false }, '集团统一停用')).toEqual({
      direct_change_reason: '集团统一停用',
    })
  })

  it('never sends a blank reason that the backend would reject as invalid', () => {
    expect(assetDirectBatchEndpointQuery({ isGroupAdmin: true, inventoryMode: false }, '   ')).toEqual({})
  })
})
