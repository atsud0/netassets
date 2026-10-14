// One shared rule for showing a long free-text reason inside a table cell.
//
// The reported defect: on the 待确认资产 page the 已忽略 / 无效 tabs print the
// review note in full inside a 120px-wide 状态 column. The cell wraps rather than
// clips, so one long reason pushes its row to dozens of lines, every column
// below it stops lining up, and reading it requires scrolling one single row.
//
// Two things must hold together, and neither alone is enough:
//   - the text is clipped to a couple of lines, so the row keeps its height;
//   - the full reason stays reachable, because it is the only thing that explains
//     why a lead was dismissed. Clipping without a tooltip would hide the decision
//     from the very person who has to review it.

/** Two lines matches the two-line treatment other secondary labels in tables get. */
export const REASON_PREVIEW_LINES = 2

/**
 * Characters that fit in the clamped area. A 120px column of 12px secondary text
 * holds roughly this many CJK glyphs; ASCII fits about twice as many, so the count
 * is deliberately conservative - clamping a little early is invisible, clamping a
 * little late reproduces the defect.
 */
const PREVIEW_CAPACITY = REASON_PREVIEW_LINES * 10

/** Inline style for a two-line clamp, applied without a stylesheet so a cell can carry it. */
export function clampToLines(lines: number = REASON_PREVIEW_LINES): Record<string, string> {
  return {
    display: '-webkit-box',
    WebkitLineClamp: String(lines),
    WebkitBoxOrient: 'vertical',
    overflow: 'hidden',
    wordBreak: 'break-word',
  }
}

/** True when the text is long enough to be clipped, so only those get a tooltip. */
export function isClampable(text?: string | null): boolean {
  const value = (text ?? '').trim()
  if (value === '') return false
  // Count code points, not UTF-16 units, so a CJK character or an emoji counts as
  // one column - matching how wide the glyph actually renders.
  return [...value].length > PREVIEW_CAPACITY
}

/** The full text for a tooltip, or undefined when there is nothing extra to show. */
export function clampTooltip(text?: string | null): string | undefined {
  const value = (text ?? '').trim()
  return value === '' ? undefined : value
}
