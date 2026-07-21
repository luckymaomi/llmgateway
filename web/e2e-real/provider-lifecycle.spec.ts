import { devices, expect } from '@playwright/test'

import {
  administratorEmail,
  administratorPassword,
  dataID,
  expectPageWidthToFit,
  problemCode,
  uuidPattern,
  visitDesktopNavigation,
  visitMobileNavigation,
} from './acceptance-helpers'
import { completePublishedCatalog } from './catalog-flow'
import { verifyPublishedCatalogOnMobile } from './catalog-mobile'
import { completeIdentityBoundary } from './identity-flow'
import {
  editProvider,
  enableProviderAfterLostResponse,
  fillProviderForm,
  renameEnabledProvider,
  setProviderEnabled,
} from './provider-operations'
import { acceptanceArtifactPath, requiredEnvironment, test } from './runtime'

const providerSlug = 'browser-fixture'
const providerBaseURL = requiredEnvironment('LLMGATEWAY_REAL_PROVIDER_BASE_URL')

test('walks an administrator and member through a published Key catalog across failures', async ({
  browser,
  browserProblems,
  gateway,
  page,
}) => {
  await page.goto('/setup')
  await expect(page.locator('script[src*="/@vite/client"]')).toHaveCount(0)
  await expect(page.locator('script[type="module"][src^="/assets/"]')).toHaveCount(1)
  await expect(page.getByRole('heading', { name: '初始化 LLMGateway' })).toBeVisible()
  await page.getByLabel('管理员名称').fill('Browser Administrator')
  await page.getByLabel('邮箱').fill(administratorEmail)
  await page.getByLabel('密码', { exact: true }).fill(administratorPassword)
  await page.getByLabel('确认密码').fill(administratorPassword)

  const setupResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/setup') && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '创建管理员' }).click()
  expect((await setupResponse).status()).toBe(201)
  await expect(page).toHaveURL(/\/providers\/providers$/)
  await expect(page.getByRole('heading', { name: 'Provider 与模型' })).toBeVisible()
  await visitDesktopNavigation(page)

  const administratorNavigation = page.getByRole('complementary', { name: '主导航' })
  await expect(administratorNavigation.getByRole('link', { name: '用量与账本' })).toBeVisible()
  await administratorNavigation.getByRole('link', { name: '用量与账本' }).click()
  await expect(page).toHaveURL(/\/ledger\/entitlements$/)
  await expect(page.getByRole('heading', { name: '用量与账本' })).toBeVisible()
  await expect(page.getByRole('table', { name: '额度与套餐列表' })).toBeVisible()
  await page.goto('/providers/providers')

  await page.getByRole('button', { name: '添加 Provider' }).click()
  await fillProviderForm(page.getByRole('dialog'), {
    slug: providerSlug,
    name: 'Browser Provider',
    baseURL: 'https://198.18.0.1/v1',
  })
  const createdResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/providers') && response.request().method() === 'POST',
  )
  await page.getByRole('dialog').getByRole('button', { name: '保存' }).click()
  const created = await createdResponse
  expect(created.status()).toBe(201)
  const providerID = dataID(await created.json())
  expect(providerID).toMatch(uuidPattern)
  if (!providerID) throw new Error('created Provider response did not contain an ID')
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText('Browser Provider')

  const stalePage = await page.context().newPage()
  browserProblems.observe(stalePage)
  await stalePage.goto('/providers/providers')
  await stalePage.getByRole('button', { name: '编辑 Provider' }).click()
  const staleDialog = stalePage.getByRole('dialog')
  await staleDialog.getByLabel('名称').fill('Browser Provider Reconciled')
  await staleDialog.getByLabel('类型').selectOption('zhipu')

  await editProvider(page, 'Browser Provider Winner', 'https://198.18.0.3/v1')
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText(
    'Browser Provider Winner',
  )

  const conflictResponsePromise = stalePage.waitForResponse(
    (response) =>
      response.url().includes('/api/control/providers/') && response.request().method() === 'PUT',
  )
  await staleDialog.getByRole('button', { name: '保存' }).click()
  const conflictResponse = await conflictResponsePromise
  browserProblems.allow(conflictResponse)
  expect(conflictResponse.status()).toBe(409)
  expect(problemCode(await conflictResponse.json())).toBe('conflict')
  await expect(staleDialog.getByRole('alert')).toContainText('数据已被其他操作更新')
  await expect(staleDialog).toContainText('Request ID：')
  await expect(staleDialog.getByRole('heading', { name: '合并并发修改' })).toBeVisible()
  await expect(staleDialog.getByLabel('名称')).toHaveValue('Browser Provider Reconciled')
  await expect(staleDialog.getByLabel('类型')).toHaveValue('zhipu')
  await expect(staleDialog.getByLabel('Base URL')).toHaveValue('https://198.18.0.3/v1')
  await expect(
    staleDialog.getByRole('group', { name: '类型' }).getByText(/保留你的草稿/),
  ).toBeVisible()
  await expect(
    staleDialog.getByRole('group', { name: 'Base URL' }).getByText(/采用当前最新值/),
  ).toBeVisible()
  const nameConflict = staleDialog.getByRole('group', { name: '名称' })
  await expect(nameConflict.getByText('Browser Provider Reconciled')).toBeVisible()
  await expect(nameConflict.getByText('Browser Provider Winner')).toBeVisible()
  await expect(staleDialog.getByRole('button', { name: '保存合并结果' })).toBeDisabled()

  await nameConflict.getByRole('radio', { name: '采用最新' }).click()

  await expect(staleDialog.getByLabel('名称')).toHaveValue('Browser Provider Winner')
  await expect(staleDialog.getByRole('button', { name: '保存合并结果' })).toBeEnabled()
  const reconciledResponse = stalePage.waitForResponse(
    (response) =>
      response.url().includes('/api/control/providers/') && response.request().method() === 'PUT',
  )
  await staleDialog.getByRole('button', { name: '保存合并结果' }).click()
  const mergedResponse = await reconciledResponse
  expect(mergedResponse.status()).toBe(200)
  expect(mergedResponse.request().postDataJSON()).toMatchObject({
    name: 'Browser Provider Winner',
    kind: 'zhipu',
    baseUrl: 'https://198.18.0.3/v1',
  })
  await stalePage.close()

  await page.reload()
  await expect(page).toHaveURL(/\/providers\/providers$/)
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText(
    'Browser Provider Winner',
  )
  await expectPageWidthToFit(page)

  await enableProviderAfterLostResponse(page, browserProblems, providerID)
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText('已启用')

  await gateway.restart()
  await page.reload()
  await expect(page).toHaveURL(/\/providers\/providers$/)
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText(
    'Browser Provider Winner',
  )
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText('已启用')

  await renameEnabledProvider(page, 'Browser Provider Restarted')
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText(
    'Browser Provider Restarted',
  )
  await setProviderEnabled(page, false)
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText('已停用')

  const desktopState = await page.context().storageState()
  const pixel = devices['Pixel 7']
  const mobileContext = await browser.newContext({
    ...pixel,
    baseURL: new URL(page.url()).origin,
    storageState: desktopState,
  })
  try {
    const mobilePage = await mobileContext.newPage()
    browserProblems.observe(mobilePage)
    await mobilePage.goto('/providers/providers')
    await expect(mobilePage.getByRole('heading', { name: 'Provider 与模型' })).toBeVisible()
    await visitMobileNavigation(mobilePage)
    await expect(mobilePage.getByRole('list', { name: 'Provider 列表' })).toContainText(
      'Browser Provider Restarted',
    )
    await editProvider(mobilePage, 'Browser Provider Mobile', providerBaseURL, 'openai-compatible')
    await expect(mobilePage.getByRole('list', { name: 'Provider 列表' })).toContainText(
      'Browser Provider Mobile',
    )
    await setProviderEnabled(mobilePage, true)
    await expect(mobilePage.getByRole('list', { name: 'Provider 列表' })).toContainText('已启用')
    await expectPageWidthToFit(mobilePage)
    await mobilePage.screenshot({
      path: acceptanceArtifactPath('provider-mobile.png'),
      fullPage: true,
      animations: 'disabled',
    })
  } finally {
    await mobileContext.close()
  }

  await page.reload()
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText(
    'Browser Provider Mobile',
  )
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText('已启用')
  await page.screenshot({
    path: acceptanceArtifactPath('provider-desktop.png'),
    fullPage: true,
    animations: 'disabled',
  })

  const catalog = await completePublishedCatalog(page, browserProblems)
  await verifyPublishedCatalogOnMobile(page, browser, browserProblems, catalog)
  await completeIdentityBoundary(page, browser, browserProblems, gateway, catalog)
})
