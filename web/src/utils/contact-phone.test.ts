import { describe, expect, it } from 'vitest'
import { isValidContactPhone, normalizeContactPhone } from './contact-phone'

describe('contact phone validation', () => {
  it('accepts the numbers a group ledger actually stores', () => {
    for (const value of ['13800000000', '138-0000-0000', '021 8888 6666', '0755-88886666-8001', '400-820-8820', '8008208820', '6688']) {
      expect(isValidContactPhone(value), value).toBe(true)
    }
  })

  it('rejects text that can never reach an owner', () => {
    for (const value of ['abc', '张三', '1380000000', '12345678901', '138000000001234', '转接行政', '12345678901234567890']) {
      expect(isValidContactPhone(value), value).toBe(false)
    }
  })

  it('treats an empty value as "not provided"', () => {
    expect(isValidContactPhone('')).toBe(true)
    expect(isValidContactPhone('   ')).toBe(true)
  })

  it('normalizes separators so stored values compare consistently', () => {
    expect(normalizeContactPhone(' 138-0000-0000 ')).toBe('13800000000')
    expect(normalizeContactPhone('(021) 8888 6666')).toBe('02188886666')
  })
})
