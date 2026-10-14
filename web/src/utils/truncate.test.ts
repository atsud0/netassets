import { describe, expect, it } from 'vitest'
import { REASON_PREVIEW_LINES, clampToLines, clampTooltip, isClampable } from './truncate'

describe('clampToLines', () => {
  it('pins the element to a fixed number of lines', () => {
    const style = clampToLines()
    expect(style.display).toBe('-webkit-box')
    expect(style.WebkitBoxOrient).toBe('vertical')
    expect(style.overflow).toBe('hidden')
    expect(style.WebkitLineClamp).toBe(String(REASON_PREVIEW_LINES))
  })

  it('breaks inside a long unbroken token rather than overflowing the cell', () => {
    // A reason with no spaces is the worst case: without word-break the text would
    // spill sideways out of the 120px column instead of wrapping.
    expect(clampToLines().wordBreak).toBe('break-word')
  })

  it('honours a different line count', () => {
    expect(clampToLines(3).WebkitLineClamp).toBe('3')
  })
})

describe('isClampable', () => {
  it('treats a long reason as clampable', () => {
    const long = '该线索由自动化扫描产生，但资产值已被合并到现有域名记录中，属于重复登记，无需单独入库。'
    expect(isClampable(long)).toBe(true)
  })

  it('leaves a short reason alone so it does not grow a pointless tooltip', () => {
    expect(isClampable('重复资产')).toBe(false)
    expect(isClampable('')).toBe(false)
    expect(isClampable('   ')).toBe(false)
    expect(isClampable(null)).toBe(false)
    expect(isClampable(undefined)).toBe(false)
  })

  it('counts CJK characters as one column each', () => {
    // UTF-16 length would be the same here, so use an astral character to prove
    // the count is by code point: an emoji is one glyph, not two.
    const elevenEmoji = '😀😀😀😀😀😀😀😀😀😀😀'
    expect([...elevenEmoji].length).toBe(11)
    expect(isClampable(elevenEmoji)).toBe(isClampable('😀'.repeat(11)))
  })
})

describe('clampTooltip', () => {
  it('returns the whole reason so the decision is still readable', () => {
    const long = '原因'.repeat(50)
    expect(clampTooltip(long)).toBe(long)
  })

  it('trims surrounding whitespace and reports nothing for an empty reason', () => {
    expect(clampTooltip('  重复资产  ')).toBe('重复资产')
    expect(clampTooltip('')).toBeUndefined()
    expect(clampTooltip('   ')).toBeUndefined()
    expect(clampTooltip(null)).toBeUndefined()
  })
})
