interface TaskTiming { status: string; start_at: string; deadline_at: string }

// Preserve persisted workflow state for actions; derive the time-sensitive label
// separately so an active task is not shown as in progress after its deadline.
export function inventoryTaskStatus(task: TaskTiming, now = Date.now()): string {
  if (task.status !== 'active') return task.status
  if (now > new Date(task.deadline_at).getTime()) return 'overdue'
  if (now < new Date(task.start_at).getTime()) return 'scheduled'
  return 'active'
}
