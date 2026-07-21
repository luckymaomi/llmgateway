import { expect, type Locator, type Page } from '@playwright/test'

import { expectPageWidthToFit, uuidPattern } from './acceptance-helpers'
import type { BrowserProblems } from './runtime'

export async function fillProviderForm(
  dialog: Locator,
  values: { slug: string; name: string; baseURL: string },
): Promise<void> {
  await dialog.getByLabel('标识').fill(values.slug)
  await dialog.getByLabel('名称').fill(values.name)
  await dialog.getByLabel('类型').selectOption('openai-compatible')
  await dialog.getByLabel('Base URL').fill(values.baseURL)
}

export async function editProvider(
  page: Page,
  name: string,
  baseURL: string,
  kind?: 'openai-compatible' | 'zhipu' | 'agnes' | 'gemini',
): Promise<void> {
  await page.getByRole('button', { name: '编辑 Provider' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByLabel('标识')).toHaveAttribute('readonly', '')
  await expectPageWidthToFit(page)
  await expect(dialog.getByRole('button', { name: '保存' })).toBeVisible()
  await dialog.getByLabel('名称').fill(name)
  if (kind) await dialog.getByLabel('类型').selectOption(kind)
  await dialog.getByLabel('Base URL').fill(baseURL)
  const response = page.waitForResponse(
    (candidate) =>
      candidate.url().includes('/api/control/providers/') && candidate.request().method() === 'PUT',
  )
  await dialog.getByRole('button', { name: '保存' }).click()
  expect((await response).status()).toBe(200)
}

export async function renameEnabledProvider(page: Page, name: string): Promise<void> {
  await page.getByRole('button', { name: '编辑 Provider' }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByLabel('类型')).toBeDisabled()
  await expect(dialog.getByLabel('Base URL')).toHaveAttribute('readonly', '')
  await expectPageWidthToFit(page)
  await expect(dialog.getByRole('button', { name: '保存' })).toBeVisible()
  await dialog.getByLabel('名称').fill(name)
  const response = page.waitForResponse(
    (candidate) =>
      candidate.url().includes('/api/control/providers/') && candidate.request().method() === 'PUT',
  )
  await dialog.getByRole('button', { name: '保存' }).click()
  expect((await response).status()).toBe(200)
}

export async function setProviderEnabled(page: Page, enabled: boolean): Promise<void> {
  const response = page.waitForResponse(
    (candidate) => candidate.url().endsWith('/status') && candidate.request().method() === 'PUT',
  )
  await page.getByRole('button', { name: enabled ? '启用 Provider' : '停用 Provider' }).click()
  expect((await response).status()).toBe(200)
}

export async function enableProviderAfterLostResponse(
  page: Page,
  browserProblems: BrowserProblems,
  providerID: string,
): Promise<void> {
  const statusPath = `/api/control/providers/${providerID}/status`
  const routePattern = `**${statusPath}`
  let interrupted = false
  let originalKey = ''
  let originalBody = ''
  await page.route(routePattern, async (route) => {
    const request = route.request()
    if (interrupted || request.method() !== 'PUT') {
      await route.continue()
      return
    }
    interrupted = true
    originalKey = request.headers()['idempotency-key'] ?? ''
    originalBody = request.postData() ?? ''
    const committed = await route.fetch()
    expect(committed.status()).toBe(200)
    await route.abort('failed')
  })
  browserProblems.allowRequestFailure('PUT', statusPath, 'net::ERR_FAILED')
  try {
    const failedRequest = page.waitForEvent(
      'requestfailed',
      (request) => new URL(request.url()).pathname === statusPath && request.method() === 'PUT',
    )
    await page.getByRole('button', { name: '启用 Provider' }).click()
    await failedRequest
    expect(originalKey).toMatch(uuidPattern)
    expect(JSON.parse(originalBody)).toMatchObject({ enabled: true })
    const retryButton = page.getByRole('button', { name: '重试原操作' })
    await expect(retryButton).toBeVisible()
    await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText('已启用')
    const replayResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === statusPath && response.request().method() === 'PUT',
    )
    const refreshedProviders = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/control/providers' &&
        response.request().method() === 'GET',
    )
    await retryButton.click()
    const replayed = await replayResponse
    expect(replayed.status()).toBe(200)
    expect(replayed.request().headers()['idempotency-key']).toBe(originalKey)
    expect(replayed.request().postData()).toBe(originalBody)
    expect((await refreshedProviders).status()).toBe(200)
    await expect(retryButton).toBeHidden()
  } finally {
    await page.unroute(routePattern)
  }
}
