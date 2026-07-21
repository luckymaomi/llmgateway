import { expect, type Page } from '@playwright/test'

import {
  clearClipboard,
  clearClipboardBestEffort,
  dataRecord,
  gatewayEndpoint,
  isRecord,
  uuidPattern,
} from './acceptance-helpers'
import type { PublishedCatalogFacts } from './catalog-flow'
import type { BrowserProblems } from './runtime'

export interface GatewayKeyFacts {
  id: string
  name: string
  secret: string
}

export async function createGatewayKeyAfterLostResponse(
  page: Page,
  browserProblems: BrowserProblems,
  catalog: PublishedCatalogFacts,
): Promise<GatewayKeyFacts> {
  const name = 'Browser member Key'
  await page.getByRole('link', { name: '网关 Key', exact: true }).click()
  await page.getByRole('button', { name: '创建 Key' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('所属用户').selectOption({ label: 'Browser Member' })
  await dialog.getByLabel('名称').fill(name)
  await dialog.getByRole('checkbox', { name: new RegExp(catalog.authorizedModelAlias) }).check()
  await expect(
    dialog.getByRole('checkbox', { name: new RegExp(catalog.ungrantedModelAlias) }),
  ).toBeVisible()
  await expect(dialog.getByText(catalog.draftOnlyModelAlias)).toHaveCount(0)

  const keyPath = '/api/control/keys'
  let interrupted = false
  let originalKey = ''
  let originalBody = ''
  await page.route('**' + keyPath, async (route) => {
    const request = route.request()
    if (interrupted || request.method() !== 'POST') {
      await route.continue()
      return
    }
    interrupted = true
    originalKey = request.headers()['idempotency-key'] ?? ''
    originalBody = request.postData() ?? ''
    const committed = await route.fetch()
    expect(committed.status()).toBe(201)
    await route.abort('failed')
  })
  browserProblems.allowRequestFailure('POST', keyPath, 'net::ERR_FAILED')
  try {
    const failedRequest = page.waitForEvent(
      'requestfailed',
      (request) => new URL(request.url()).pathname === keyPath && request.method() === 'POST',
    )
    await dialog.getByRole('button', { name: '创建', exact: true }).click()
    await failedRequest
    expect(originalKey).toMatch(uuidPattern)
    const originalInput = JSON.parse(originalBody) as {
      authorizedModelIds?: unknown
      name?: unknown
    }
    expect(originalInput.name).toBe(name)
    expect(originalInput.authorizedModelIds).toEqual([catalog.authorizedModelID])
    await expect(dialog.getByRole('alert')).toContainText('创建结果暂时无法确认')

    await page.reload()
    await page.getByRole('button', { name: '创建 Key' }).click()
    const recoveryDialog = page.getByRole('dialog')
    await expect(recoveryDialog.getByRole('alert')).toContainText('创建结果暂时无法确认')
    const replayResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === keyPath && response.request().method() === 'POST',
    )
    await recoveryDialog.getByRole('button', { name: '重试原操作' }).click()
    const replayed = await replayResponse
    expect(replayed.status()).toBe(201)
    expect(replayed.request().headers()['idempotency-key']).toBe(originalKey)
    expect(replayed.request().postData()).toBe(originalBody)
    const replayData = dataRecord(await replayed.json())
    const createdKey = isRecord(replayData?.key) ? replayData.key : undefined
    const id = typeof createdKey?.id === 'string' ? createdKey.id : ''
    expect(id).toMatch(uuidPattern)

    const acknowledgement = page.getByRole('dialog')
    await acknowledgement.getByRole('button', { name: '复制 Key' }).click()
    await expect(acknowledgement.getByRole('button', { name: '已复制' })).toBeVisible()
    const secret = await page.evaluate(() => navigator.clipboard.readText())
    expect(/^llmg_[A-Za-z0-9_-]+$/.test(secret)).toBe(true)
    await acknowledgement.getByRole('button', { name: '完成' }).click()
    await expect(page.getByTestId('created-key-secret')).toHaveCount(0)
    await clearClipboard(page)
    await expect(page.getByRole('table', { name: '网关 Key 列表' })).toContainText(name)
    return { id, name, secret }
  } finally {
    await clearClipboardBestEffort(page)
    await page.unroute('**' + keyPath)
  }
}

export async function expectPublicModels(
  page: Page,
  secret: string,
  expected: { included: string[]; excluded: string[] },
): Promise<void> {
  const response = await page.request.get(gatewayEndpoint('/v1/models'), {
    headers: { Authorization: 'Bearer ' + secret },
  })
  expect(response.status()).toBe(200)
  const body = (await response.json()) as { object?: unknown; data?: unknown }
  expect(body.object).toBe('list')
  const aliases = Array.isArray(body.data)
    ? body.data
        .map((item) => (isRecord(item) && typeof item.id === 'string' ? item.id : ''))
        .filter(Boolean)
    : []
  expect(aliases.sort()).toEqual([...expected.included].sort())
  for (const alias of expected.excluded) expect(aliases).not.toContain(alias)
}
