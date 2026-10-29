import { defineConfig } from '@playwright/test'
import base from './playwright.config'

// Optional-field boundaries require the post-v12 image; not in the default suite.
export default defineConfig({ ...base, testDir: './probes', testMatch: 'business-system-fields.spec.ts', workers: 1 })
