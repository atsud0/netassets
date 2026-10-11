// Run only against the separately restored test application. Never a live environment.
const { chromium } = require('@playwright/test')
const { readFileSync } = require('node:fs')
const assert = require('node:assert/strict')
;(async () => {
 const baseURL = process.env.BASE_URL
 assert.equal(new URL(baseURL).hostname, 'repair-restored', 'requires isolated restored app')
 const fixture = JSON.parse(readFileSync(process.env.RESTORE_FIXTURE, 'utf8'))
 const browser = await chromium.launch({ headless: true })
 try {
  const context = await browser.newContext({ baseURL })
  const login = await context.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  assert.equal(login.status(), 200)
  let attachments = 0, reviews = 0
  const statuses = []
  for (const task of fixture.tasks) {
   const detail = await context.request.get(`/api/v1/remediation-tasks/${task.task_id}`)
   assert.equal(detail.status(), 200)
   const { item } = await detail.json()
   assert.equal(item.work_order_id, fixture.order_id)
   statuses.push(item.status)
   const submissions = await context.request.get(`/api/v1/remediation-tasks/${task.task_id}/submissions`)
   const versions = (await submissions.json()).items
   assert.equal(versions.length, 1)
   assert.ok(JSON.stringify(versions).includes(task.attachment_id), 'restored submission references proof')
   assert.ok(JSON.stringify(versions).includes('生产环境'), 'verification environment restored')
   reviews += versions[0].reviews.length
   const materials = await context.request.get(`/api/v1/remediation-tasks/${task.task_id}/materials`)
   assert.ok((await materials.json()).items.length > 0, 'material history restored')
   const download = await context.request.get(`/api/v1/remediation-tasks/${task.task_id}/attachments/${task.attachment_id}/download`)
   assert.equal(download.status(), 200)
   assert.equal((await download.body()).toString('utf8'), task.content, 'proof bytes match backup fixture')
   const deletion = await context.request.delete(`/api/v1/remediation-tasks/${task.task_id}/attachments/${task.attachment_id}`)
   assert.ok([404, 405].includes(deletion.status()), 'historical evidence cannot be deleted')
   attachments++
  }
  assert.deepEqual(statuses.sort(), ['completed','in_progress'])
  assert.equal(reviews, 4)
  const page = await context.newPage()
  await page.goto(`/remediation?work_order_id=${fixture.order_id}`)
  await page.getByRole('heading', {name:'修复任务',exact:true}).waitFor()
  await page.screenshot({ path: process.env.RESTORE_SCREENSHOT || 'test-results/remediation-restored.png', fullPage: true })
  console.log(JSON.stringify({ restoredTasks: statuses.length, restoredProofs: attachments, restoredReviews: reviews, exactAttachmentContents: true, immutableEvidence: true, statuses }))
 } finally { await browser.close() }
})().catch(error => { console.error(error); process.exitCode = 1 })
