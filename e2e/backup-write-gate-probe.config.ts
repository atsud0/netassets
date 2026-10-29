import { defineConfig } from '@playwright/test'
import base from './playwright.config'

export default defineConfig({ ...base, testDir: './probes', testMatch: 'backup-write-gate.spec.ts', workers: 1 })
