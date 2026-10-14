import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// The list and the detail drawer both render the expiry state through
// expiryLabel/expiryTag. Unit-testing the shared rule proves the arithmetic; this
// proves the page actually uses it, because the reported defect lived in the view
// rounding the days itself, not in any API response.
describe('asset list and detail read the expiry state from the shared rule', () => {
  const source = readFileSync(join(__dirname, 'AssetsView.vue'), 'utf8')

  it('no longer rounds remaining days inside the view', () => {
    expect(source, 'AssetsView must not recompute remaining days').not.toMatch(/Math\.ceil\(\s*\(?[a-zA-Z]*expiresAt/)
    expect(source, 'AssetsView must not branch on a locally rounded day count').not.toMatch(/remainingDays\s*</)
  })

  it('derives both the label and the tag from the shared rule', () => {
    expect(source).toContain("import { expiryState, expiryTagType } from '../utils/asset-expiry'")
    expect(source).toContain('switch (expiryState(value))')
    expect(source).toContain('return expiryTagType(value)')
  })

  it('shows an explicit 已到期 branch rather than falling through to 即将到期', () => {
    expect(source).toContain(`case 'expired': return \`已到期`)
    expect(source).toContain(`case 'expiring': return \`即将到期`)
  })
})
