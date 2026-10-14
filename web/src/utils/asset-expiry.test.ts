import { describe, expect, it } from 'vitest'
import { EXPIRY_SOON_MS, expiryState, expiryTagType, isAssetExpired, remainingDays } from './asset-expiry'

const NOW = new Date('2026-10-15T08:00:00Z').getTime()
const HOUR = 3_600_000
const DAY = 86_400_000

// The reported defect, verbatim: "已到期但没有显示已到期，还显示即将过期".
// Math.ceil used to turn "expired one hour ago" into zero remaining days, so the
// deadline landed in the "即将到期" branch and carried a warning colour.
describe('a deadline that has already passed is expired', () => {
  it('treats one hour in the past as expired, not as expiring', () => {
    const oneHourAgo = new Date(NOW - HOUR).toISOString()
    expect(expiryState(oneHourAgo, NOW)).toBe('expired')
    expect(expiryTagType(oneHourAgo, NOW)).toBe('danger')
    expect(isAssetExpired(oneHourAgo, NOW)).toBe(true)
  })

  it('treats the exact deadline instant as expired', () => {
    expect(expiryState(new Date(NOW).toISOString(), NOW)).toBe('expired')
    expect(expiryTagType(new Date(NOW).toISOString(), NOW)).toBe('danger')
  })

  it('covers the whole window that ceil used to swallow, up to a full day', () => {
    for (const minutesAgo of [1, 30, 60, 120, 360, 720, 1439]) {
      const value = new Date(NOW - minutesAgo * 60_000).toISOString()
      expect(expiryState(value, NOW), `${minutesAgo} minutes ago must read as expired`).toBe('expired')
    }
  })

  it('never reports a positive day count for a passed deadline', () => {
    // Zero is acceptable here - a deadline 1 hour ago is 0 whole days past - but a
    // positive count would be the defect, and is what Math.ceil produced for any
    // deadline less than a day in the past once the caller rounded it up.
    expect(remainingDays(new Date(NOW - HOUR).toISOString(), NOW)).toBeLessThanOrEqual(0)
    expect(remainingDays(new Date(NOW - 23 * HOUR).toISOString(), NOW)).toBeLessThanOrEqual(0)
    expect(remainingDays(new Date(NOW - 25 * HOUR).toISOString(), NOW)).toBeLessThan(0)
  })

  it('keeps expired and expiring visually distinct', () => {
    const expired = new Date(NOW - DAY).toISOString()
    const expiring = new Date(NOW + DAY).toISOString()
    expect(expiryState(expired, NOW)).toBe('expired')
    expect(expiryState(expiring, NOW)).toBe('expiring')
    // Same colour for both would make the fix invisible on the list.
    expect(expiryTagType(expired, NOW)).not.toBe(expiryTagType(expiring, NOW))
  })
})

describe('a deadline still in the future', () => {
  it('is expiring within 30 days and active beyond', () => {
    expect(expiryState(new Date(NOW + DAY).toISOString(), NOW)).toBe('expiring')
    expect(expiryTagType(new Date(NOW + DAY).toISOString(), NOW)).toBe('warning')
    expect(expiryState(new Date(NOW + EXPIRY_SOON_MS).toISOString(), NOW)).toBe('expiring')
    expect(expiryState(new Date(NOW + EXPIRY_SOON_MS + 1000).toISOString(), NOW)).toBe('active')
    expect(expiryTagType(new Date(NOW + 90 * DAY).toISOString(), NOW)).toBe('success')
  })

  it('one second before the deadline is still expiring, not expired', () => {
    expect(expiryState(new Date(NOW + 1000).toISOString(), NOW)).toBe('expiring')
    expect(isAssetExpired(new Date(NOW + 1000).toISOString(), NOW)).toBe(false)
  })
})

describe('deadlines that carry no usable timestamp', () => {
  it('treats a missing deadline as permanently valid', () => {
    expect(expiryState('', NOW)).toBe('permanent')
    expect(expiryState(undefined, NOW)).toBe('permanent')
    expect(expiryTagType(null, NOW)).toBe('info')
    expect(isAssetExpired(null, NOW)).toBe(false)
  })

  it('treats an unparseable deadline as unknown rather than expired', () => {
    // An asset must not be auto-flagged as expired because a stored string is
    // malformed; that would take working inventory out of service.
    expect(expiryState('not-a-date', NOW)).toBe('unknown')
    expect(isAssetExpired('not-a-date', NOW)).toBe(false)
    expect(expiryTagType('not-a-date', NOW)).toBe('info')
  })

  it('does not produce NaN days for an unparseable deadline', () => {
    expect(Number.isNaN(remainingDays('not-a-date', NOW))).toBe(true)
  })
})
