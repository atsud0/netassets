import { defineConfig } from '@playwright/test'
import base from './playwright.config'

// Requires the file-transaction import implementation; retains all existing data.
export default defineConfig({ ...base, testDir: './probes', testMatch: 'finding-import-atomic.spec.ts', workers: 1 })
