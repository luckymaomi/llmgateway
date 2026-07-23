import { expect, type Page } from '@playwright/test'

export const administratorEmail = 'browser-admin@example.test'
export const administratorPassword = 'browser-acceptance-password'
export const memberEmail = 'browser-member@example.test'
export const memberPassword = 'browser-member-password'
export const memberReplacementPassword = 'browser-member-replacement-password'
export const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

export function gatewayEndpoint(pathname: string): string {
  const baseURL = process.env.LLMGATEWAY_REAL_GATEWAY_URL
  if (!baseURL) throw new Error('LLMGATEWAY_REAL_GATEWAY_URL is required')
  return new URL(pathname, baseURL).toString()
}

export async function clearClipboard(page: Page): Promise<void> {
  expect(
    await page.evaluate(async () => {
      await navigator.clipboard.writeText('')
      return (await navigator.clipboard.readText()) === ''
    }),
  ).toBe(true)
}

export async function clearClipboardBestEffort(page: Page): Promise<void> {
  try {
    await page.evaluate(() => navigator.clipboard.writeText(''))
  } catch {
    // Automatic artifacts stay disabled; cleanup must not expose the clipboard value on failure.
  }
}

export function problemCode(body: unknown): string | undefined {
  if (typeof body !== 'object' || body === null || !('error' in body)) return undefined
  const error = body.error
  if (typeof error !== 'object' || error === null || !('code' in error)) return undefined
  return typeof error.code === 'string' ? error.code : undefined
}

export function dataID(body: unknown): string | undefined {
  if (typeof body !== 'object' || body === null || !('data' in body)) return undefined
  const data = body.data
  if (typeof data !== 'object' || data === null || !('id' in data)) return undefined
  return typeof data.id === 'string' ? data.id : undefined
}

export function dataRecord(body: unknown): Record<string, unknown> | undefined {
  if (!isRecord(body) || !('data' in body) || !isRecord(body.data)) return undefined
  return body.data
}

export function dataItems(body: unknown): Record<string, unknown>[] {
  const data = dataRecord(body)
  return Array.isArray(data?.items) ? data.items.filter(isRecord) : []
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

export async function expectPageWidthToFit(page: Page): Promise<void> {
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
      ),
    )
    .toBe(true)
}
