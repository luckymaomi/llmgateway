import { expect, type Page } from '@playwright/test'

import { dataID, expectPageWidthToFit, uuidPattern } from './acceptance-helpers'
import type { PublishedCatalogFacts } from './catalog-flow'
import type { BrowserProblems, GatewayRuntime } from './runtime'

export async function createEntitlementAfterLostResponse(
  page: Page,
  browserProblems: BrowserProblems,
  gateway: GatewayRuntime,
  catalog: PublishedCatalogFacts,
): Promise<void> {
  const navigation = page.getByRole('complementary', { name: '主导航' })
  await navigation.getByRole('link', { name: '用量与额度' }).click()
  await expect(page).toHaveURL(/\/ledger\/entitlements$/)
  await page.getByRole('button', { name: '分配额度' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('用户').selectOption({ label: 'Browser Member' })
  await dialog.getByLabel('模型范围').selectOption(catalog.authorizedModelID)
  await dialog.getByLabel('Token 额度').fill('50000')
  await dialog.getByLabel('RPM').fill('60')
  await dialog.getByLabel('TPM').fill('50000')
  await dialog.getByLabel('并发上限').fill('2')
  await dialog.getByLabel('分配原因').fill('Browser production acceptance allocation')
  const entitlementPath = '/api/control/entitlements'
  let interrupted = false
  let originalKey = ''
  let originalBody = ''
  let committedID = ''
  await page.route('**' + entitlementPath, async (route) => {
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
    committedID = dataID(await committed.json()) ?? ''
    await route.abort('failed')
  })
  browserProblems.allowRequestFailure('POST', entitlementPath, 'net::ERR_FAILED')
  try {
    const failedRequest = page.waitForEvent(
      'requestfailed',
      (request) =>
        new URL(request.url()).pathname === entitlementPath && request.method() === 'POST',
    )
    await dialog.getByRole('button', { name: '分配', exact: true }).click()
    await failedRequest
    expect(originalKey).toMatch(uuidPattern)
    expect(committedID).toMatch(uuidPattern)
    await expect(dialog.getByRole('alert')).toBeVisible()
    const pendingOperation = await page.evaluate(() => {
      for (let index = 0; index < sessionStorage.length; index += 1) {
        const key = sessionStorage.key(index)
        if (key?.startsWith('llmgateway:pending-entitlement:')) return sessionStorage.getItem(key)
      }
      return null
    })
    expect(pendingOperation).not.toBeNull()
    const pending = JSON.parse(pendingOperation ?? '{}') as {
      idempotencyKey?: unknown
      input?: { modelId?: unknown; grantedTokens?: unknown; reason?: unknown }
    }
    expect(pending.idempotencyKey).toBe(originalKey)
    expect(pending.input?.modelId).toBe(catalog.authorizedModelID)
    expect(pending.input?.grantedTokens).toBe(50_000)
    expect(pending.input?.reason).toBe('Browser production acceptance allocation')
    await gateway.restart()
    await page.reload()
    await page.getByRole('button', { name: '分配额度' }).click()
    const recoveryDialog = page.getByRole('dialog')
    await expect(recoveryDialog.getByRole('alert')).toBeVisible()
    const replayResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === entitlementPath &&
        response.request().method() === 'POST',
    )
    await recoveryDialog.getByRole('button', { name: '确认原操作' }).click()
    const replayed = await replayResponse
    expect(replayed.status()).toBe(201)
    expect(replayed.request().headers()['idempotency-key']).toBe(originalKey)
    expect(replayed.request().postData()).toBe(originalBody)
    expect(dataID(await replayed.json())).toBe(committedID)
    const row = page.getByRole('row').filter({ hasText: 'Browser Member' })
    await expect(row).toHaveCount(1)
    await expect(row).toContainText(catalog.authorizedModelAlias)
    await expect(row).toContainText('50.0K / 50.0K')
    await expectPageWidthToFit(page)
    expect(
      await page.evaluate(
        () =>
          Object.keys(sessionStorage).filter((key) =>
            key.startsWith('llmgateway:pending-entitlement:'),
          ).length,
      ),
    ).toBe(0)
  } finally {
    if (!page.isClosed()) await page.unroute('**' + entitlementPath)
  }
  await page.goto('/access/users')
}
