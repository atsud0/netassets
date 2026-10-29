import { describe, expect, it } from 'vitest'
import { inventoryTaskStatus } from './inventory-task-status'
const task = { status: 'active', start_at: '2026-10-20T00:00:00Z', deadline_at: '2026-10-22T00:00:00Z' }
describe('inventory task status', () => {
  it('shows active tasks past their deadline as expired', () => {
    expect(inventoryTaskStatus(task, Date.parse('2026-10-23T00:00:00Z'))).toBe('overdue')
  })
  it('distinguishes scheduled and currently active tasks', () => {
    expect(inventoryTaskStatus(task, Date.parse('2026-10-19T00:00:00Z'))).toBe('scheduled')
    expect(inventoryTaskStatus(task, Date.parse(task.start_at))).toBe('active')
    expect(inventoryTaskStatus(task, Date.parse(task.deadline_at))).toBe('active')
  })
  it('does not replace completed or closed workflow states', () => {
    for (const status of ['draft', 'closed', 'completed', 'archived']) {
      expect(inventoryTaskStatus({ ...task, status }, Date.parse('2026-10-23T00:00:00Z'))).toBe(status)
    }
  })
})
