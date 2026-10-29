import { defineConfig } from '@playwright/test'
import base from './playwright.config'

export default defineConfig({ ...base, testDir: './probes', testMatch: 'security-startup.spec.ts', workers: 1 })
