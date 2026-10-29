import { expect, test } from '@playwright/test'
import { writeFile } from 'node:fs/promises'

test('漏洞导入100/1000/5000行阶梯、混合结果及超行数/体积边界', async ({ request }, testInfo) => {
  test.setTimeout(240_000)
  const logged = await request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } })
  expect(logged.status(), await logged.text()).toBe(200)
  const stamp = `${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
  const roots = await request.get('/api/v1/organizations?page=1&page_size=100')
  expect(roots.status()).toBe(200)
  const group = (await roots.json()).items.find((o: { type: string }) => o.type === 'group')
  const createdOrg = await request.post('/api/v1/organizations', { data: { parent_id: group.id, code: `VOL_${stamp}`, name: `导入阶梯${stamp}`, type: 'subsidiary' } })
  expect(createdOrg.status(), await createdOrg.text()).toBe(201)
  const org = (await createdOrg.json()).item
  const createdAsset = await request.post('/api/v1/assets', { data: { type: 'domain', value: `volume-${stamp}.example.com`, organization_id: org.id, environment: 'production', network_scope: 'public', status: 'active', importance: 'normal', direct_change_reason: '隔离容量回归' } })
  expect(createdAsset.status(), await createdAsset.text()).toBe(201)
  const asset = (await createdAsset.json()).item
  const createdBatch = await request.post('/api/v1/security-test-batches', { data: { batch_name: `容量边界${stamp}`, test_type: 'penetration', provider: '隔离回归', authorization_note: '只测隔离环境', start_at: new Date(Date.now() - 3600000).toISOString(), end_at: new Date(Date.now() + 86400000).toISOString(), asset_ids: [asset.id] } })
  expect(createdBatch.status(), await createdBatch.text()).toBe(201)
  const batch = (await createdBatch.json()).item
  const authorized = await request.post(`/api/v1/security-test-batches/${batch.id}/authorize`, { data: { comment: '隔离容量验证' } })
  expect(authorized.status(), await authorized.text()).toBe(200)
  const header = '资产类型,资产值,漏洞标题,严重等级,漏洞描述,发现时间\n'
  const timings: { rows: number; bytes: number; duration_ms: number; status: number }[] = []
  const upload = (name: string, buffer: Buffer) => request.post('/api/v1/security-findings/import', {
    timeout: 120_000,
    multipart: { test_batch_id: batch.id, file: { name, mimeType: 'text/csv', buffer } },
  })
  for (const count of [100, 1000, 5000]) {
    const prefix = `阶梯${count}-${stamp}-`
    const csv = Buffer.from(header + Array.from({ length: count }, (_, i) => `domain,missing-${stamp}.invalid,${prefix}${i},high,隔离环境容量验证,2026-10-24 08:00:00`).join('\n'))
    const started = Date.now()
    const response = await upload(`volume-${count}-${stamp}.csv`, csv)
    timings.push({ rows: count, bytes: csv.length, duration_ms: Date.now() - started, status: response.status() })
    await writeFile(testInfo.outputPath('volume-summary.json'), JSON.stringify(timings, null, 2))
    await testInfo.attach(`volume-${count}`, { body: JSON.stringify(timings.at(-1)), contentType: 'application/json' })
    expect(response.status(), await response.text()).toBe(201)
    const result = (await response.json()).item
    expect(result).toMatchObject({ total_count: count, pending_count: count, success_count: 0, duplicate_count: 0, failure_count: 0 })
    const last = await request.get(`/api/v1/security-findings/import-candidates?test_batch_id=${batch.id}&keyword=${encodeURIComponent(prefix)}&page=${Math.ceil(count / 100)}&page_size=100`)
    expect(last.status(), await last.text()).toBe(200)
    const candidates = await last.json()
    expect(candidates.total).toBe(count)
    expect(candidates.items).toHaveLength(100)
    for (const candidate of candidates.items) expect(candidate.finding_import_id).toBe(result.id)
  }
  const targetBytes = (10 << 20) - 1024
  const nearLimitPrefix = `近上限-${stamp}-`
  const nearLimitFixedRows = Array.from({ length: 5000 }, (_, i) => `domain,near-limit-${stamp}.invalid,${nearLimitPrefix}${i},high,,2026-10-24 08:00:00`)
  const fixedBytes = Buffer.byteLength(header) + nearLimitFixedRows.reduce((total, row) => total + Buffer.byteLength(row), 0) + nearLimitFixedRows.length - 1
  const remainingDescriptionBytes = targetBytes - fixedBytes
  const descriptionSize = Math.floor(remainingDescriptionBytes / nearLimitFixedRows.length)
  const descriptionRemainder = remainingDescriptionBytes % nearLimitFixedRows.length
  expect(descriptionSize).toBeGreaterThan(0)
  expect(descriptionSize + 1).toBeLessThanOrEqual(20_000)
  const nearLimitCSV = Buffer.from(header + nearLimitFixedRows.map((row, i) => row.replace(',high,,', `,high,${'x'.repeat(descriptionSize + (i < descriptionRemainder ? 1 : 0))},`)).join('\n'))
  expect(nearLimitCSV.length).toBe(targetBytes)
  const nearLimitStarted = Date.now()
  const nearLimitResponse = await upload(`near-limit-${stamp}.csv`, nearLimitCSV)
  const nearLimitDuration = Date.now() - nearLimitStarted
  expect(nearLimitResponse.status(), await nearLimitResponse.text()).toBe(201)
  const nearLimitResult = (await nearLimitResponse.json()).item
  expect(nearLimitResult).toMatchObject({ total_count: 5000, pending_count: 5000, success_count: 0, duplicate_count: 0, failure_count: 0 })
  const nearLimitCandidates = await request.get(`/api/v1/security-findings/import-candidates?test_batch_id=${batch.id}&keyword=${encodeURIComponent(nearLimitPrefix)}&page=50&page_size=100`)
  expect(nearLimitCandidates.status(), await nearLimitCandidates.text()).toBe(200)
  const nearLimitPage = await nearLimitCandidates.json()
  expect(nearLimitPage.total).toBe(5000)
  expect(nearLimitPage.items).toHaveLength(100)
  expect(new Set(nearLimitPage.items.map((item: { id: string }) => item.id)).size).toBe(100)
  for (const item of nearLimitPage.items) {
    expect(item.finding_import_id).toBe(nearLimitResult.id)
    expect(item.title).toMatch(new RegExp(`^${nearLimitPrefix}\\d+$`))
  }
  timings.push({ rows: 5000, bytes: nearLimitCSV.length, duration_ms: nearLimitDuration, status: nearLimitResponse.status() })
  await writeFile(testInfo.outputPath('volume-summary.json'), JSON.stringify(timings, null, 2))

  const mixed = await upload(`mixed-${stamp}.csv`, Buffer.from(header + [
    `domain,${asset.value},精确匹配${stamp},high,应生成安全发现,2026-10-24 08:00:00`,
    `domain,missing-${stamp}.invalid,待关联${stamp},medium,应生成候选,2026-10-24 08:00:00`,
    `domain,missing-${stamp}.invalid,错误等级${stamp},invalid,应报告行级错误,2026-10-24 08:00:00`,
  ].join('\n')))
  expect(mixed.status(), await mixed.text()).toBe(201)
  const mixedResult = (await mixed.json()).item
  expect(mixedResult).toMatchObject({ total_count: 3, success_count: 1, pending_count: 1, failure_count: 1, duplicate_count: 0 })
  expect(mixedResult.errors).toEqual([expect.objectContaining({ row: 4, code: 'invalid_severity' })])

  const jsonName = `formats-${stamp}.json`
  const jsonBody = Buffer.from(JSON.stringify({ findings: [
    { name: `JSON精确${stamp}`, severity: 'high', summary: 'JSON精确命中', assets: [{ type: 'domain', value: asset.value }] },
    { name: `JSON待关联${stamp}`, severity: 'medium', summary: 'JSON候选待审', assets: [{ type: 'domain', value: `json-missing-${stamp}.invalid` }] },
  ] }))
  const jsonResponse = await upload(jsonName, jsonBody)
  expect(jsonResponse.status(), await jsonResponse.text()).toBe(201)
  const jsonResult = (await jsonResponse.json()).item
  expect(jsonResult).toMatchObject({ file_type: 'json', total_count: 2, success_count: 1, pending_count: 1, failure_count: 0, duplicate_count: 0 })
  const jsonCandidates = await request.get(`/api/v1/security-findings/import-candidates?test_batch_id=${batch.id}&keyword=${encodeURIComponent(`JSON待关联${stamp}`)}&page=1&page_size=100`)
  expect(jsonCandidates.status(), await jsonCandidates.text()).toBe(200)
  expect((await jsonCandidates.json()).items).toEqual([expect.objectContaining({ finding_import_id: jsonResult.id, title: `JSON待关联${stamp}`, description: 'JSON候选待审' })])

  const template = await request.get('/api/v1/security-findings/import-template?format=xlsx')
  expect(template.status(), await template.text()).toBe(200)
  expect(template.headers()['content-type']).toContain('spreadsheetml.sheet')
  const xlsxName = `template-${stamp}.xlsx`
  const xlsxResponse = await upload(xlsxName, await template.body())
  expect(xlsxResponse.status(), await xlsxResponse.text()).toBe(201)
  const xlsxResult = (await xlsxResponse.json()).item
  expect(xlsxResult).toMatchObject({ file_type: 'xlsx', total_count: 1, success_count: 0, pending_count: 1, failure_count: 0, duplicate_count: 0 })
  const xlsxCandidates = await request.get(`/api/v1/security-findings/import-candidates?test_batch_id=${batch.id}&keyword=${encodeURIComponent('登录接口未授权访问')}&page=1&page_size=100`)
  expect(xlsxCandidates.status(), await xlsxCandidates.text()).toBe(200)
  expect((await xlsxCandidates.json()).items).toEqual([expect.objectContaining({ finding_import_id: xlsxResult.id, title: '登录接口未授权访问', asset_value: 'https://example.com/login' })])

  const excessRows = await upload(`excess-rows-${stamp}.csv`, Buffer.from(header + Array.from({ length: 5001 }, (_, i) => `domain,missing-${stamp}.invalid,超行数${i},high,不应落库,2026-10-24 08:00:00`).join('\n')))
  expect(excessRows.status(), await excessRows.text()).toBe(400)
  expect((await excessRows.json()).error.code).toBe('invalid_finding_import')
  const excessBytes = await upload(`excess-bytes-${stamp}.csv`, Buffer.alloc((10 << 20) + 1, 'a'))
  expect(excessBytes.status(), await excessBytes.text()).toBe(400)
  expect((await excessBytes.json()).error.code).toBe('invalid_import_file')
  const history = await request.get('/api/v1/security-findings/imports?page=1&page_size=100')
  expect(history.status(), await history.text()).toBe(200)
  const records = (await history.json()).items.filter((item: { file_name: string }) => item.file_name.includes(stamp))
  expect(records).toHaveLength(7)
  expect(records.map((item: { total_count: number }) => item.total_count).sort((a: number, b: number) => a - b)).toEqual([1, 2, 3, 100, 1000, 5000, 5000])
  await testInfo.attach('volume-summary', { body: JSON.stringify(timings, null, 2), contentType: 'application/json' })
})
