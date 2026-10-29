import { defineConfig } from '@playwright/test'
import base from './playwright.config'

// Post-v11 source: run only after the candidate-field/policy image is deployed.
export default defineConfig({ ...base, testDir: './probes', testMatch: 'candidate-validation.spec.ts', workers: 1 })
