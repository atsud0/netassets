import { defineConfig } from '@playwright/test'
import base from './playwright.config'

// Kept outside the normal suite until the paginated-tree image is deployed.
export default defineConfig({
  ...base,
  testDir: './probes',
  testMatch: 'organization-tree-render.spec.ts',
  workers: 1,
})
