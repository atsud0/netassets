import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './tests',
  testMatch: 'toolbar-layout.spec.ts',
  fullyParallel: true,
  workers: 2,
  timeout: 120_000,
  expect: { timeout: 20_000 },
  use: {
    baseURL: process.env.BASE_URL ?? 'http://127.0.0.1:5174',
    launchOptions: { executablePath: process.env.CHROMIUM_PATH },
    trace: 'retain-on-failure',
  },
  webServer: process.env.BASE_URL ? undefined : {
    command: 'npm --prefix ../web run dev -- --port 5174',
    url: 'http://127.0.0.1:5174',
    reuseExistingServer: !process.env.CI,
  },
})
