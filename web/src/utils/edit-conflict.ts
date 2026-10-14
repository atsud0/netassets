import { ElMessage, ElMessageBox } from 'element-plus'
import { describeEditConflict, isEditConflictError } from '../api/client'

// A rejected optimistic-concurrency write must not look like a validation
// failure: the server kept its own copy and nothing was overwritten. The user is
// told exactly that and offered an explicit reload, so nobody retries blindly
// against a stale version.
export async function reportEditConflict(error: unknown, fallbackTitle: string, reload: () => void | Promise<void>) {
  if (!isEditConflictError(error)) return false
  ElMessage.warning(describeEditConflict())
  try {
    await ElMessageBox.confirm('刷新会重新读取服务器上的最新内容，你当前页面上尚未提交的修改会丢失。是否现在刷新？', fallbackTitle, {
      type: 'warning', confirmButtonText: '刷新数据', cancelButtonText: '保留我的输入',
    })
  } catch {
    return true
  }
  await reload()
  return true
}
