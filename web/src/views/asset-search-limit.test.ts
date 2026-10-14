import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, expect, it } from 'vitest'

// The asset search box feeds two consumers with different limits: the list endpoint
// and the saved-view endpoint, which caps the keyword at 200 characters. The box had no
// limit of its own, so a user could type a long phrase, see an empty list, and then be
// refused when saving that filter as a view - with an error naming nothing they had
// touched. The box now carries the same bound, so the input cannot exceed what can be
// saved.

const source = readFileSync(join(process.cwd(), 'src/views/AssetsView.vue'), 'utf8')

describe('资产搜索框', () => {
  it('限制输入长度与保存视图的 q 上限一致', () => {
    const searchInput = source.match(/<el-input[^>]*placeholder="搜索资产值、名称、系统或负责人"[^>]*\/>/)
    expect(searchInput, '未找到资产搜索输入框').not.toBeNull()
    expect(searchInput![0]).toMatch(/maxlength="200"/)
  })

  it('限制的是同一个搜索框，而不是别处的输入', () => {
    // A maxlength added to some other input would satisfy a looser check but leave the
    // search box able to outgrow the saved-view limit again.
    const withLimit = source.match(/<el-input[^>]*maxlength="200"[^>]*\/>/g) ?? []
    expect(withLimit.length).toBe(1)
    expect(withLimit[0]).toContain('搜索资产值、名称、系统或负责人')
  })
})
