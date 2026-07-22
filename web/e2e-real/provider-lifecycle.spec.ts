import { expect } from '@playwright/test'

import {
  administratorEmail,
  administratorPassword,
  dataID,
  expectPageWidthToFit,
  problemCode,
  uuidPattern,
} from './acceptance-helpers'
import { completePublishedCatalog } from './catalog-flow'
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
  const initialAnonymousSessionResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/session') &&
      response.request().method() === 'GET' &&
      response.status() === 401,
  )
  await page.goto('/')
  browserProblems.allow(await initialAnonymousSessionResponse)
  await expect(page).toHaveURL(/\/setup$/)
  await page.getByLabel('管理员邮箱').fill(administratorEmail)

  const setupResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/setup') && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '创建管理员' }).click()
  const setup = await setupResponse
  expect(setup.status()).toBe(201)
  expect(setup.headers()['cache-control']).toBe('no-store')
  expect(setup.request().postDataJSON()).toEqual({ email: administratorEmail })
  const initialAdministratorPassword =
    (await page.getByTestId('initial-administrator-password').textContent()) ?? ''
  expect(initialAdministratorPassword).toMatch(/^[A-Za-z0-9_-]{40,}$/)
  const browserStorage = await page.evaluate(() =>
    JSON.stringify({
      local: Object.fromEntries(Object.entries(localStorage)),
      session: Object.fromEntries(Object.entries(sessionStorage)),
    }),
  )
  expect(browserStorage).not.toContain(initialAdministratorPassword)
  await page.getByRole('button', { name: '我已保存，进入控制面' }).click()
  await expect(page).toHaveURL(/\/overview$/)
  await page.reload()
  expect(await page.locator('body').innerText()).not.toContain(initialAdministratorPassword)

  await page
    .getByRole('complementary', { name: '主导航' })
    .getByRole('button', { name: '更换密码' })
    .click()
  const passwordDialog = page.getByRole('dialog', { name: '更换密码' })
  await passwordDialog.getByLabel('当前密码').fill(initialAdministratorPassword)
  await passwordDialog.getByLabel('新密码', { exact: true }).fill(administratorPassword)
  await passwordDialog.getByLabel('确认新密码').fill(administratorPassword)
  const passwordChangeResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/password') && response.request().method() === 'POST',
  )
  await passwordDialog.getByRole('button', { name: '确认更换' }).click()
  expect((await passwordChangeResponse).status()).toBe(200)
  const changedPasswordDialog = page.getByRole('dialog', { name: '密码已更换' })
  await changedPasswordDialog.getByRole('button', { name: '完成' }).click()
  expect((await page.request.get('/api/control/session')).status()).toBe(200)

  const passwordVerificationContext = await browser.newContext({
    baseURL: new URL(page.url()).origin,
  })
  try {
    const passwordVerificationPage = await passwordVerificationContext.newPage()
    browserProblems.observe(passwordVerificationPage)
    await passwordVerificationPage.goto('/login')
    await passwordVerificationPage.getByLabel('邮箱').fill(administratorEmail)
    await passwordVerificationPage.getByLabel('密码').fill(initialAdministratorPassword)
    const rejectedLoginResponse = passwordVerificationPage.waitForResponse(
      (response) =>
        response.url().endsWith('/api/control/session') && response.request().method() === 'POST',
    )
    await passwordVerificationPage.getByRole('button', { name: '登录' }).click()
    const rejectedLogin = await rejectedLoginResponse
    browserProblems.allow(rejectedLogin)
    expect(rejectedLogin.status()).toBe(401)
    await passwordVerificationPage.getByLabel('密码').fill(administratorPassword)
    const acceptedLoginResponse = passwordVerificationPage.waitForResponse(
      (response) =>
        response.url().endsWith('/api/control/session') && response.request().method() === 'POST',
    )
    await passwordVerificationPage.getByRole('button', { name: '登录' }).click()
    expect((await acceptedLoginResponse).status()).toBe(200)
    await expect(passwordVerificationPage).toHaveURL(/\/overview$/)
  } finally {
    await passwordVerificationContext.close()
  }
  await page.goto('/providers/providers')

  await page.getByRole('button', { name: '自定义 Provider' }).click()
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

  const stalePage = await page.context().newPage()
  browserProblems.observe(stalePage)
  await stalePage.goto('/providers/providers')
  await stalePage.getByRole('button', { name: '编辑 Provider' }).click()
  const staleDialog = stalePage.getByRole('dialog')
  await staleDialog.getByLabel('名称').fill('Browser Provider Reconciled')
  await staleDialog.getByLabel('类型').selectOption('zhipu')

  await editProvider(page, 'Browser Provider Winner', 'https://198.18.0.3/v1')

  const conflictResponsePromise = stalePage.waitForResponse(
    (response) =>
      response.url().includes('/api/control/providers/') && response.request().method() === 'PUT',
  )
  await staleDialog.getByRole('button', { name: '保存' }).click()
  const conflictResponse = await conflictResponsePromise
  browserProblems.allow(conflictResponse)
  expect(conflictResponse.status()).toBe(409)
  expect(problemCode(await conflictResponse.json())).toBe('conflict')
  await expect(staleDialog.getByRole('alert')).toBeVisible()
  await expect(staleDialog.getByLabel('名称')).toHaveValue('Browser Provider Reconciled')
  await expect(staleDialog.getByLabel('类型')).toHaveValue('zhipu')
  await expect(staleDialog.getByLabel('Base URL')).toHaveValue('https://198.18.0.3/v1')
  const nameConflict = staleDialog.getByRole('group', { name: '名称' })
  await nameConflict.getByRole('radio', { name: '采用最新' }).click()

  await expect(staleDialog.getByLabel('名称')).toHaveValue('Browser Provider Winner')
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

  await gateway.restart()
  await page.reload()
  await expect(page).toHaveURL(/\/providers\/providers$/)
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText(
    'Browser Provider Winner',
  )

  await renameEnabledProvider(page, 'Browser Provider Restarted')
  await setProviderEnabled(page, false)
  await editProvider(page, 'Browser Provider Ready', providerBaseURL, 'openai-compatible')
  await setProviderEnabled(page, true)

  await page.reload()
  await expect(page.getByRole('table', { name: 'Provider 列表' })).toContainText(
    'Browser Provider Ready',
  )
  await page.screenshot({
    path: acceptanceArtifactPath('provider-desktop.png'),
    fullPage: true,
    animations: 'disabled',
  })

  const catalog = await completePublishedCatalog(page, browserProblems, providerID)
  await completeIdentityBoundary(page, browser, browserProblems, gateway, catalog)
})
