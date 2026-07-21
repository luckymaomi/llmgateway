import { defineConfig, devices } from '@playwright/test'

const gatewayURL = requiredURL('LLMGATEWAY_REAL_GATEWAY_URL')

export default defineConfig({
  testDir: './e2e-real',
  fullyParallel: false,
  forbidOnly: true,
  retries: 0,
  workers: 1,
  reporter: 'line',
  timeout: 120_000,
  expect: {
    timeout: 15_000,
  },
  use: {
    baseURL: gatewayURL,
    headless: false,
    screenshot: 'off',
    trace: 'off',
    video: 'off',
  },
  projects: [
    {
      name: 'real-chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
})

function requiredURL(name: string): string {
  const value = process.env[name]
  if (!value) throw new Error(`${name} is required`)
  const parsed = new URL(value)
  if (parsed.protocol !== 'http:' || parsed.hostname !== '127.0.0.1') {
    throw new Error(`${name} must be a loopback HTTP URL`)
  }
  return parsed.origin
}
