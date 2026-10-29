import { defineConfig } from '@playwright/test'
import base from './playwright.config'

export default defineConfig({ ...base, testDir: './probes', testMatch: 'inventory-review-unicode.spec.ts', workers: 1 })
