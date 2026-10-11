const {chromium,expect}=require('@playwright/test');const fs=require('node:fs');
(async()=>{
 const baseURL=process.env.BASE_URL||'http://ui-test:8080';if(new URL(baseURL).hostname!=='ui-test')throw Error('Isolated UI application required');
 const browser=await chromium.launch({headless:true});const errors=[];const checks=[];
 try {
  const context=await browser.newContext({baseURL,viewport:{width:1440,height:1000},locale:'zh-CN'});
  const login=await context.request.post('/api/v1/auth/login',{data:{username:process.env.E2E_USERNAME,password:process.env.E2E_PASSWORD}});expect(login.status()).toBe(200);
  const page=await context.newPage();page.on('pageerror',e=>errors.push(e.message));page.on('response',r=>{if(r.status()>=400&&new URL(r.url()).pathname.startsWith('/api/'))errors.push(r.status()+' '+new URL(r.url()).pathname)});
  fs.mkdirSync('/screens',{recursive:true});
  for(const [name,path]of [['dashboard','/dashboard'],['assets','/assets'],['orders','/security-work-orders'],['tasks','/remediation'],['people','/remediation-people']]) {
   await page.goto(path);await page.waitForLoadState('networkidle');await expect(page.locator('h1')).toBeVisible();
   expect(await page.locator('.page-content').evaluate(el=>el.scrollWidth<=el.clientWidth+1),name+' desktop overflow').toBeTruthy();
   await page.mouse.move(0,0);await page.screenshot({path:'/screens/'+name+'.png'});checks.push(name);
  }
  await page.goto('/remediation');await page.waitForLoadState('networkidle');
  await page.getByLabel('搜索修复任务',{exact:true}).fill('不存在的修复任务-ui-check');await page.getByRole('button',{name:'查询',exact:true}).click();await expect(page.getByText('当前筛选下没有修复任务')).toBeVisible();
  await page.getByRole('button',{name:'保存视图',exact:true}).click();await page.getByRole('button',{name:'清除筛选',exact:true}).click();await page.getByRole('button',{name:'应用已存视图',exact:true}).click();await expect(page.getByLabel('搜索修复任务',{exact:true})).toHaveValue('不存在的修复任务-ui-check');checks.push('empty state and saved filters');
  await page.goto('/assets');await page.waitForLoadState('networkidle');await page.getByRole('button',{name:'更多操作',exact:true}).click();await expect(page.getByRole('menuitem',{name:'批量导入',exact:true})).toBeVisible();await page.getByRole('heading',{name:'网络资产',exact:true}).click();
  await page.getByRole('button',{name:'更多字段',exact:true}).click();await expect(page.getByRole('columnheader',{name:'网络区域',exact:true})).toBeVisible();checks.push('asset secondary actions and expanded fields');
  await page.setViewportSize({width:390,height:844});await page.goto('/remediation');await page.waitForLoadState('networkidle');
  await expect(page.getByRole('button',{name:'切换导航',exact:true})).toBeVisible();await page.getByRole('button',{name:'切换导航',exact:true}).click();await expect(page.locator('.sidebar')).toHaveClass(/mobile-open/);await page.getByRole('button',{name:'网络资产',exact:true}).click();await expect(page).toHaveURL(/\/assets$/);await expect(page.locator('.sidebar')).not.toHaveClass(/mobile-open/);
  await page.goto('/dashboard');await page.waitForLoadState('networkidle');expect(await page.locator('.page-content').evaluate(el=>el.scrollWidth<=el.clientWidth+1),'mobile dashboard overflow').toBeTruthy();await page.mouse.move(0,0);await page.screenshot({path:'/screens/mobile-dashboard.png'});checks.push('mobile navigation and dashboard');
  await page.setViewportSize({width:1280,height:900});await page.goto('/remediation');await page.waitForLoadState('networkidle');expect(await page.locator('.page-content').evaluate(el=>el.scrollWidth<=el.clientWidth+1)).toBeTruthy();await page.mouse.move(0,0);await page.screenshot({path:'/screens/tasks-1280.png'});checks.push('1280 task layout');
  for(const path of ['/systems','/inventory-tasks','/security-test-requests','/security-test-batches','/organizations','/users','/data-governance','/data-quality-issues','/data-quality-scan-history','/asset-candidates','/audit-logs','/backups','/notifications','/profile']) {
   await page.goto(path);await page.waitForLoadState('networkidle');await expect(page.locator('h1')).toBeVisible();expect(await page.locator('.page-content').evaluate(el=>el.scrollWidth<=el.clientWidth+1),path+' overflow').toBeTruthy();checks.push('route '+path);
  }
  expect(errors).toEqual([]);fs.writeFileSync('/screens/verification.json',JSON.stringify({checks,errors},null,2));console.log(JSON.stringify({checks,errors}));
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
