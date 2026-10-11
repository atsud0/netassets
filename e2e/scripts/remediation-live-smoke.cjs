// Read-only business checks: login and GET endpoints, no fixture creation.
const { chromium } = require('@playwright/test')
const assert = require('node:assert/strict')
;(async () => {
 const browser = await chromium.launch({headless:true})
 try {
  const context = await browser.newContext({baseURL:process.env.BASE_URL || 'http://app:8080'})
  const page = await context.newPage()
  const failures=[]
  page.on('pageerror',error=>failures.push(error.message))
  page.on('response',response=>{if(response.url().includes('/api/') && response.status()>=400) failures.push(`${response.status()} ${new URL(response.url()).pathname}`)})
  await page.goto('/login')
  await page.getByPlaceholder('请输入用户名').fill(process.env.E2E_USERNAME)
  await page.getByPlaceholder('请输入密码').fill(process.env.E2E_PASSWORD)
  await page.getByRole('button',{name:'登录',exact:true}).click()
  await page.waitForURL(url=>!url.pathname.includes('/login'))
  failures.length=0 // Initial anonymous auth/me 401 is expected before login.
  const pages=[['/dashboard','今天，先处理关键事项'],['/assets','网络资产'],['/security-work-orders','安全工单'],['/remediation','修复任务'],['/remediation-people','人员与厂商']]
  for(const [path,heading] of pages) {
   await page.goto(path)
   await page.getByRole('heading',{name:heading,exact:true}).waitFor()
   await page.waitForLoadState('networkidle')
  }
  await page.getByRole('tab',{name:'厂商目录与规则',exact:true}).click()
  await page.getByText('集团统一维护厂商身份；合作组织、联系方式与合作状态分别在“合作厂商”中管理。停用集团厂商将使该厂商的执行授权立即失效。',{exact:true}).waitFor()
  const paths=['/remediation-tasks','/remediation-tasks/statistics','/remediation-tasks/systems','/remediation-grants','/vendors','/vendor-catalog','/vendor-memberships','/remediation-settings','/security-work-orders']
  for(const path of paths){const response=await context.request.get(`/api/v1${path}`);assert.equal(response.status(),200,path)}
  assert.deepEqual(failures,[])
  console.log(JSON.stringify({pages:pages.length,groupAdministration:true,readAPIs:paths.length,businessWrites:0,browserErrors:failures}))
 }finally{await browser.close()}
})().catch(error=>{console.error(error);process.exitCode=1})
