import { devices, expect, type Browser, type Page } from '@playwright/test'

import { expectLocatorWidthToFit, expectPageWidthToFit } from './acceptance-helpers'
import type { PublishedCatalogFacts } from './catalog-flow'
import { acceptanceArtifactPath, type BrowserProblems } from './runtime'

export async function verifyPublishedCatalogOnMobile(
  administratorPage: Page,
  browser: Browser,
  browserProblems: BrowserProblems,
  catalog: PublishedCatalogFacts,
): Promise<void> {
  const context = await browser.newContext({
    ...devices['Pixel 7'],
    baseURL: new URL(administratorPage.url()).origin,
    storageState: await administratorPage.context().storageState(),
  })
  try {
    const page = await context.newPage()
    browserProblems.observe(page)
    await page.goto('/providers/models')
    const modelList = page.getByRole('list', { name: '模型列表' })
    await expect(modelList).toContainText(catalog.authorizedModelAlias)
    await expect(modelList).toContainText(catalog.ungrantedModelAlias)
    await expect(modelList).toContainText(catalog.draftOnlyModelAlias)
    await page.getByRole('button', { name: '添加模型' }).click()
    const modelDialog = page.getByRole('dialog')
    await expect(modelDialog.getByLabel('上下文 Token')).toBeVisible()
    await modelDialog.getByRole('checkbox', { name: '推理内容' }).check()
    await expect(modelDialog.getByLabel('推理控制')).toBeVisible()
    await modelDialog.getByLabel('推理控制').selectOption('toggle')
    await expectPageWidthToFit(page)
    await modelDialog.getByRole('button', { name: '取消' }).click()
    await page.goto('/credentials')
    const credentialList = page.getByRole('list', { name: '上游凭据列表' })
    await expect(credentialList).toContainText('Browser credential')
    await page.getByRole('button', { name: '添加凭据' }).click()
    const credentialDialog = page.getByRole('dialog')
    await credentialDialog.getByLabel('Provider').selectOption({ label: 'Browser Provider Mobile' })
    await expect(
      credentialDialog.getByRole('checkbox', { name: catalog.authorizedModelAlias }),
    ).toBeVisible()
    await expectPageWidthToFit(page)
    await credentialDialog.getByRole('button', { name: '取消' }).click()
    const credentialItem = credentialList.getByRole('listitem').filter({
      hasText: 'Browser credential',
    })
    await expect(credentialItem.getByRole('button', { name: '编辑凭据' })).toBeVisible()
    const probeResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname.endsWith('/probe') &&
        response.request().method() === 'POST',
    )
    await credentialItem.getByRole('button', { name: '测试连接' }).click()
    expect((await probeResponse).status()).toBe(200)
    await expect(page.getByRole('region', { name: '凭据连接测试' })).toContainText(
      '未消耗模型 Token',
    )
    await expectLocatorWidthToFit(credentialItem)
    await expectPageWidthToFit(page)
    await page.goto('/providers/revisions')
    await expect(page.getByLabel('当前生效配置')).toContainText(
      '版本 ' + String(catalog.revisionSequence),
    )
    const revisionCard = page
      .getByRole('list', { name: '配置版本列表' })
      .getByRole('listitem')
      .filter({ hasText: '版本 ' + String(catalog.revisionSequence) + ' ·' })
    await expect(revisionCard).toHaveCount(1)
    await expect(revisionCard.locator('strong').first()).toHaveText(catalog.revisionSummary)
    const revisionMetadata = revisionCard.locator('.mobile-summary > span').first()
    await expect(revisionMetadata).toContainText('版本 ' + String(catalog.revisionSequence) + ' ·')
    await expect(revisionMetadata).toContainText('Browser Administrator')
    await expectLocatorWidthToFit(revisionMetadata)
    await expectLocatorWidthToFit(revisionCard)
    const validationResponse = page.waitForResponse(
      (response) => response.url().endsWith('/validate') && response.request().method() === 'POST',
    )
    await revisionCard.getByRole('button', { name: '校验' }).click()
    expect((await validationResponse).status()).toBe(200)
    const resultDialog = page.getByRole('dialog')
    await expect(resultDialog.getByRole('heading', { name: '校验完成' })).toBeVisible()
    await resultDialog.getByRole('button', { name: '关闭' }).click()
    await expectPageWidthToFit(page)
    await page.screenshot({
      path: acceptanceArtifactPath('catalog-mobile.png'),
      fullPage: true,
      animations: 'disabled',
    })
  } finally {
    await context.close()
  }
}
