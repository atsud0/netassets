const { chromium }=require('@playwright/test'); const fs=require('node:fs');
(async()=>{const browser=await chromium.launch({headless:true});try{
 const context=await browser.newContext({baseURL:process.env.BASE_URL||'http://app:8080',viewport:{width:1440,height:1000}});
 const login=await context.request.post('/api/v1/auth/login',{data:{username:process.env.E2E_USERNAME,password:process.env.E2E_PASSWORD}});if(login.status()!==200)throw Error('Login failed');
 const page=await context.newPage();const results=[];
 const steps=process.env.AUDIT_RESTORED ? [['05-task-detail','/remediation?task_id='+JSON.parse(fs.readFileSync('/backup/fixture.json')).tasks[1].task_id]] : [['01-dashboard','/'],['02-assets','/assets'],['03-work-orders','/security-work-orders'],['04-remediation','/remediation'],['06-people','/remediation-people']];
 for(const [name,path]of steps){await page.goto(path);await page.waitForLoadState('networkidle');await page.screenshot({path:'/audit/'+name+'.png',fullPage:true});results.push({step:name,path,title:await page.locator('h1').allTextContents(),headings:await page.locator('h2,h3').allTextContents(),buttons:await page.getByRole('button').allTextContents(),horizontalOverflow:await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth)});
 if(process.env.AUDIT_RESTORED){await page.getByRole('tab',{name:'提交与审核',exact:true}).click();await page.screenshot({path:'/audit/07-review.png',fullPage:true});results.push({step:'07-review',path,buttons:await page.getByRole('button').allTextContents()});}}
 fs.writeFileSync('/audit/'+(process.env.AUDIT_RESTORED?'detail':'pages')+'-observations.json',JSON.stringify(results,null,2));console.log(JSON.stringify(results.map(x=>({step:x.step,path:x.path,heading:x.title,overflow:x.horizontalOverflow}))));
 }finally{await browser.close()}})().catch(e=>{console.error(e);process.exitCode=1})
