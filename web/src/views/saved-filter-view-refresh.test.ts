import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// Deleting a saved view used to reload nothing outside the component: the view
// selector updated and the table kept rendering the previous result set, so the page
// stayed filtered by a name that no longer existed. The component cannot reload a list
// it does not own, so the fix is a 'changed' event that every host page must handle.
// These tests exist because the event is easy to drop: adding a new host page, or
// removing the handler from one of the three existing ones, reintroduces the bug with
// no compile error and no failing behaviour test until a user deletes a view.

const component = readFileSync(join(process.cwd(), 'src/components/SavedFilterViews.vue'), 'utf8')

describe('SavedFilterViews', () => {
  it('emits changed after the view set is modified', () => {
    expect(component).toContain('changed: [views: SavedFilterView[]]')
    // Deletion, creation and default reassignment all change the set.
    const emissions = component.match(/emit\('changed'/g) ?? []
    expect(emissions.length).toBeGreaterThanOrEqual(3)
  })

  it('emits changed after the deletion request has been reloaded, not before', () => {
    const deleteIndex = component.indexOf('async function deleteSelected')
    const deleteBody = component.slice(deleteIndex, component.indexOf('defineExpose'))
    expect(deleteBody).toContain('emit(\'changed\'')
    // The host page reloads on the event, so the list must already reflect the
    // deletion; emitting before loadViews() would restore a view that is gone.
    expect(deleteBody.indexOf('await loadViews()')).toBeLessThan(deleteBody.indexOf("emit('changed'"))
  })
})

describe('saved view hosts', () => {
  const viewDir = join(process.cwd(), 'src/views')
  // This file quotes the component name in its own sources, so it has to exclude
  // itself or it would assert that a test file listens for a Vue event.
  const hosts = readdirSync(viewDir).filter(file => {
    if (!file.endsWith('.vue')) return false
    return readFileSync(join(viewDir, file), 'utf8').includes('<SavedFilterViews')
  })

  it('finds every page that renders saved views', () => {
    expect(hosts.length).toBeGreaterThan(0)
  })

  it.each(hosts)('%s reloads its list when the saved view set changes', file => {
    const source = readFileSync(join(viewDir, file), 'utf8')
    const tags = source.match(/<SavedFilterViews[^>]*>/g) ?? []
    expect(tags.length).toBeGreaterThan(0)
    for (const tag of tags) {
      expect(tag, `${file} does not listen for @changed`).toMatch(/@changed="(\w+)"/)
      const handler = tag.match(/@changed="(\w+)"/)![1]
      expect(source, `${file} binds @changed to ${handler} but never defines it`).toContain(`function ${handler}(`)
    }
  })
})
