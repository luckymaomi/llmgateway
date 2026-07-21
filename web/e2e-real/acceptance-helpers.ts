import { expect, type Locator, type Page } from '@playwright/test'

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

export async function visitDesktopNavigation(page: Page): Promise<void> {
  const navigation = page.getByRole('complementary', { name: '主导航' })
  await expect(navigation.getByRole('link')).toHaveCount(5)
  await expect(navigation.getByRole('link', { name: 'Provider 与模型' })).toBeVisible()
  await expect(navigation.getByRole('link', { name: '上游凭据池' })).toBeVisible()
  await expect(navigation.getByRole('link', { name: '用户与网关 Key' })).toBeVisible()
  await expect(navigation.getByRole('link', { name: '用量与账本' })).toBeVisible()
  await expect(navigation.getByRole('link', { name: 'Playground' })).toBeVisible()
  const usersResponse = page.waitForResponse(
    (candidate) =>
      candidate.url().includes('/api/control/users') && candidate.request().method() === 'GET',
  )
  await navigation.getByRole('link', { name: '用户与网关 Key' }).click()
  expect((await usersResponse).status()).toBe(200)
  await expect(page.getByRole('heading', { name: '用户与网关 Key' })).toBeVisible()
  await expectPageWidthToFit(page)
  await navigation.getByRole('link', { name: 'Provider 与模型' }).click()
  await expect(page).toHaveURL(/\/providers\/providers$/)
}

export async function visitMobileNavigation(page: Page): Promise<void> {
  await page.getByRole('button', { name: '打开导航' }).click()
  const dialog = page.getByRole('dialog', { name: 'LLMGateway' })
  await expect(dialog.getByRole('link')).toHaveCount(5)
  await expect(dialog.getByRole('link', { name: '上游凭据池' })).toBeVisible()
  await expect(dialog.getByRole('link', { name: '用量与账本' })).toBeVisible()
  await expect(dialog.getByRole('link', { name: 'Playground' })).toBeVisible()
  await expectPageWidthToFit(page)
  const usersResponse = page.waitForResponse(
    (candidate) =>
      candidate.url().includes('/api/control/users') && candidate.request().method() === 'GET',
  )
  await dialog.getByRole('link', { name: '用户与网关 Key' }).click()
  expect((await usersResponse).status()).toBe(200)
  await expect(page.getByRole('heading', { name: '用户与网关 Key' })).toBeVisible()
  await expectPageWidthToFit(page)
  await page.getByRole('button', { name: '打开导航' }).click()
  await page
    .getByRole('dialog', { name: 'LLMGateway' })
    .getByRole('link', { name: 'Provider 与模型' })
    .click()
  await expect(page).toHaveURL(/\/providers\/providers$/)
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

export async function expectLocatorWidthToFit(locator: Locator): Promise<void> {
  await expect
    .poll(() => locator.evaluate((element) => element.scrollWidth <= element.clientWidth + 1))
    .toBe(true)
}
