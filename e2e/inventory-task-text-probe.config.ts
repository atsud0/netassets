import { defineConfig } from '@playwright/test'
import base from './playwright.config'

export default defineConfig({ ...base, testDir: './probes', testMatch: 'inventory-task-text.spec.ts', workers: 1 })
