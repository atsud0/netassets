const { chromium } = require('@playwright/test');
(async () => {
  const browser = await chromium.launch();
  try {
    const context = await browser.newContext({ baseURL: process.env.BASE_URL || 'http://app:8080', viewport: { width: 1280, height: 1000 } });
    const login = await context.request.post('/api/v1/auth/login', { data: { username: process.env.E2E_USERNAME, password: process.env.E2E_PASSWORD } });
    if (!login.ok()) throw Error('Login failed');
    const page = await context.newPage();
    await page.goto('/security-test-batches');
    await page.waitForLoadState('networkidle');
    const draft = page.locator('.el-table__row').filter({ hasText: '草稿' }).first();
    await draft.getByRole('button', { name: /^操作：/ }).click();
    await page.locator('.row-actions-panel:visible').waitFor();
    await page.waitForFunction(() => document.activeElement?.closest('.row-actions-panel'));
    await page.screenshot({ path: '/audit/security-test-batches-actions-1280.png', animations: 'disabled' });
    console.log('Captured read-only action preview');
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
