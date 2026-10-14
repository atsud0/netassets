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

import { draftLastEditedHint, draftTakeoverSummary, normalizeDraftTakeoverReason } from './inventory-drafts'

describe('draftTakeoverSummary', () => {
  it('shows the takeover owner, time and reason', () => {
    const summary = draftTakeoverSummary({
      assigned_to: '张运维',
      takeover_reason: '原填报人休假',
      taken_over_at: '2026-10-10T02:00:00Z',
    })
    expect(summary).toContain('张运维')
    expect(summary).toContain('接管')
    expect(summary).toContain('原填报人休假')
  })

  it('renders nothing when the draft was never taken over', () => {
    expect(draftTakeoverSummary({ assigned_to: '李填报', last_edited_by: '李填报' })).toBe('')
    expect(draftTakeoverSummary(undefined)).toBe('')
  })
})

describe('draftLastEditedHint', () => {
  it('only warns when someone other than the owner edited last', () => {
    expect(draftLastEditedHint({ assigned_to: '李填报', last_edited_by: '王复核' })).toBe('最近由 王复核 编辑')
    expect(draftLastEditedHint({ assigned_to: '李填报', last_edited_by: '李填报' })).toBe('')
    expect(draftLastEditedHint({})).toBe('')
  })
})

describe('normalizeDraftTakeoverReason', () => {
  it('trims whitespace so blank reasons can be rejected', () => {
    expect(normalizeDraftTakeoverReason('  交接给值班同事 ')).toBe('交接给值班同事')
    expect(normalizeDraftTakeoverReason('   ')).toBe('')
  })
})

import { draftTakeoverSummary as summaryWithNames } from './inventory-drafts'

describe('draftTakeoverSummary name resolution', () => {
  it('prefers human readable names over raw user ids', () => {
    const summary = summaryWithNames({
      assigned_to: '0b6f0f6c-1111-2222-3333-444455556666',
      assigned_to_name: '王填报',
      takeover_reason: '换班交接',
      taken_over_at: '2026-10-11T03:00:00Z',
    })
    expect(summary).toContain('王填报')
    expect(summary).not.toContain('0b6f0f6c')
  })
})
