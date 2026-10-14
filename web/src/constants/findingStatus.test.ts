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

// The labels below are the vocabulary the external scanner export uses and that
// reviewers read. They are asserted literally so a refactor cannot quietly
// rename a state; resolved (已处理) and fixed (已修复) in particular are two
// different facts about the same vulnerability.
describe('finding status label contract', () => {
  it('maps every value to its external label', () => {
    expect(FINDING_STATUSES.map(s => [s.value, s.label])).toEqual([
      ['pending', '待处理'],
      ['in_progress', '处理中'],
      ['confirmed', '已确认'],
      ['resolved', '已处理'],
      ['fixed', '已修复'],
      ['false_positive', '误报'],
      ['ignored', '忽略'],
      ['duplicate', '重复'],
      ['risk_accepted', '风险接受'],
    ])
  })

  it('keeps resolved and fixed distinguishable', () => {
    expect(findingStatusLabels.resolved).not.toBe(findingStatusLabels.fixed)
    expect(findingStatusLabels.resolved).toBe('已处理')
    expect(findingStatusLabels.fixed).toBe('已修复')
  })
})
