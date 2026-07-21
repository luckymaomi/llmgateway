import { defineConfig, devices } from '@playwright/test'

const baseURL = requiredHTTPSURL('LLMGATEWAY_DEPLOYMENT_URL')

export default defineConfig({
  testDir: './e2e-deployment',
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  workers: 1,
  reporter: 'line',
  timeout: 90_000,
  expect: { timeout: 15_000 },
  use: {
    baseURL,
    headless: false,
    ignoreHTTPSErrors: true,
    screenshot: 'off',
    trace: 'off',
    video: 'off',
  },
  projects: [{ name: 'deployment-chromium', use: { ...devices['Desktop Chrome'] } }],
})

function requiredHTTPSURL(name: string): string {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is required`)
  const parsed = new URL(value)
  if (parsed.protocol !== 'https:' || parsed.hostname !== 'localhost') {
    throw new Error(`${name} must be a localhost HTTPS URL`)
  }
  return parsed.origin
}
