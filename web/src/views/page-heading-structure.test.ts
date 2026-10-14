import { describe, expect, it } from 'vitest'
import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'

const viewDir = join(__dirname)
const vueFiles = readdirSync(viewDir).filter((name) => name.endsWith('.vue'))

function headingBody(source: string, start: number): string {
  let index = start
  let depth = 1
  while (depth > 0 && index < source.length) {
    const open = source.indexOf('<div', index)
    const close = source.indexOf('</div>', index)
    if (close === -1) return ''
    if (open !== -1 && open < close) {
      depth += 1
      index = open + 4
      continue
    }
    depth -= 1
    index = close + 6
  }
  return source.slice(start, index - 6)
}

function headingBodies(name: string): string[] {
  const source = readFileSync(join(viewDir, name), 'utf8')
  const marker = '<div class="page-heading">'
  const bodies: string[] = []
  let cursor = source.indexOf(marker)
  while (cursor !== -1) {
    bodies.push(headingBody(source, cursor + marker.length))
    cursor = source.indexOf(marker, cursor + 1)
  }
  return bodies
}

describe('page heading structure', () => {
  it('never keeps obsolete empty span/p or redundant heading wrappers', () => {
    const offenders: string[] = []
    for (const name of vueFiles) {
      for (const body of headingBodies(name)) {
        if (/<(span|p)(\s[^>]*)?>\s*<\/\1>/.test(body)) offenders.push(`${name}: empty span/p`)
        if (/<div>\s*<div>/.test(body)) offenders.push(`${name}: nested heading wrapper`)
        if ((body.match(/<h1[\s>]/g) ?? []).length > 1) offenders.push(`${name}: multiple h1`)
      }
    }
    expect(offenders).toEqual([])
  })

  it('keeps a title in every page heading', () => {
    const missing: string[] = []
    for (const name of vueFiles) {
      for (const body of headingBodies(name)) {
        if (!/<h1[\s>]/.test(body)) missing.push(name)
      }
    }
    expect(missing).toEqual([])
  })
})
