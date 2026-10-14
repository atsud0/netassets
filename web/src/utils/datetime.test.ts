import { describe, expect, it } from 'vitest'
import { formatDateTime, formatDateTimeShort } from './datetime'

describe('timestamp formatting', () => {
  it('renders an RFC 3339 UTC timestamp in the browser time zone', () => {
    // 2026-10-14T01:41:29Z is 09:41 in Asia/Shanghai, i.e. still the 14th at
    // 01:41 UTC. Showing the raw string is what made the history look wrong.
    const formatted = formatDateTime('2026-10-14T01:41:29.125837Z')
    expect(formatted).not.toBe('2026-10-14T01:41:29.125837Z')
    expect(formatted).toContain('2026')
    expect(formatted).toMatch(/\d{2}:\d{2}:\d{2}/)
  })

  it('keeps the year and adds the seconds the raw string lacked', () => {
    const formatted = formatDateTime('2026-07-20T15:31:40.512852Z')
    expect(formatted).toContain('2026')
    expect(formatted).toMatch(/^\d{4}\/\d{2}\/\d{2}\s/)
  })

  it('returns a dash for missing values and echoes unparsable ones', () => {
    expect(formatDateTime(undefined)).toBe('—')
    expect(formatDateTime(null)).toBe('—')
    expect(formatDateTime('')).toBe('—')
    expect(formatDateTime('not-a-date')).toBe('not-a-date')
  })

  it('exposes a short variant for dense tables', () => {
    expect(formatDateTimeShort('2026-10-14T01:41:29Z')).toMatch(/\d{2}\/\d{2}\/\d{2}/)
    expect(formatDateTimeShort('')).toBe('—')
  })
})
