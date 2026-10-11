import { describe, expect, it } from 'vitest'
import {
  CLOSED_FINDING_STATUS_VALUES,
  FINDING_STATUSES,
  findingStatusLabels,
  findingStatusTagType,
  isFindingStatusClosed,
  isFindingStatusOpen,
} from './findingStatus'

describe('findingStatus', () => {
  it('keeps the backend vocabulary in presentation order', () => {
    expect(FINDING_STATUSES.map(status => status.value)).toEqual([
      'pending', 'in_progress', 'confirmed', 'resolved', 'fixed', 'false_positive', 'ignored', 'duplicate', 'risk_accepted',
    ])
    expect(FINDING_STATUSES.map(status => status.sortOrder)).toEqual([1, 2, 3, 4, 5, 6, 7, 8, 9])
    expect(findingStatusLabels.false_positive).toBe('误报')
    expect(findingStatusLabels.risk_accepted).toBe('风险接受')
  })

  it('maps statuses to tag types', () => {
    expect(findingStatusTagType('fixed')).toBe('success')
    expect(findingStatusTagType('risk_accepted')).toBe('danger')
    expect(findingStatusTagType('unknown')).toBe('primary')
  })

  it('treats the backend closed set as closed', () => {
    expect(CLOSED_FINDING_STATUS_VALUES).toEqual(['resolved', 'fixed', 'false_positive', 'ignored', 'duplicate', 'risk_accepted'])
    expect(CLOSED_FINDING_STATUS_VALUES.every(isFindingStatusClosed)).toBe(true)
    expect(isFindingStatusClosed('pending')).toBe(false)
    expect(isFindingStatusOpen('confirmed')).toBe(true)
    expect(isFindingStatusOpen('unknown')).toBe(false)
  })
})
