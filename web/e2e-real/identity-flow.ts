import { devices, expect, type Browser, type Page, type Request } from '@playwright/test'

import {
  administratorEmail,
  clearClipboard,
  dataRecord,
  expectLocatorWidthToFit,
  expectPageWidthToFit,
  gatewayEndpoint,
  memberEmail,
  memberPassword,
  problemCode,
} from './acceptance-helpers'
import type { PublishedCatalogFacts } from './catalog-flow'
import { createEntitlementAfterLostResponse } from './entitlement-flow'
import {
  createInvitationAfterLostResponse,
  verifyInvitationCreationOnMobile,
} from './invitation-flow'
import { createGatewayKeyAfterLostResponse, expectPublicModels } from './key-flow'
import { verifyRealPlayground } from './playground-flow'
import { acceptanceArtifactPath, type BrowserProblems, type GatewayRuntime } from './runtime'

export async function completeIdentityBoundary(
  page: Page,
  browser: Browser,
  browserProblems: BrowserProblems,
  gateway: GatewayRuntime,
  catalog: PublishedCatalogFacts,
): Promise<void> {
  const origin = new URL(page.url()).origin
  await page.context().grantPermissions(['clipboard-read', 'clipboard-write'], { origin })
  const navigation = page.getByRole('complementary', { name: '主导航' })
  await navigation.getByRole('link', { name: '用户与网关 Key' }).click()
  await expect(page.getByRole('table', { name: '用户列表' })).toContainText(administratorEmail)
  await page.getByRole('link', { name: '邀请', exact: true }).click()
  await expect(page).toHaveURL(/\/access\/invitations$/)

  const invitation = await createInvitationAfterLostResponse(page, browserProblems, gateway)
  const invitationRowsAfterRestart = page
    .getByRole('table', { name: '邀请列表' })
    .getByRole('row')
    .filter({ hasText: `${invitation.prefix}…` })
  await expect(invitationRowsAfterRestart).toHaveCount(1)
  await expect(invitationRowsAfterRestart).toContainText('Browser Administrator')
  expect(
    await page.evaluate((code) => !document.body.innerText.includes(code), invitation.code),
  ).toBe(true)

  await verifyInvitationCreationOnMobile(page, browser, browserProblems)

  const registrationContext = await browser.newContext({ baseURL: origin })
  try {
    const registrationPage = await registrationContext.newPage()
    browserProblems.observe(registrationPage)
    await registrationPage.goto('/register')
    await registrationPage.getByLabel('邀请码').fill(invitation.code)
    await registrationPage.getByLabel('显示名称').fill('Browser Member')
    await registrationPage.getByLabel('邮箱').fill(memberEmail)
    await registrationPage.getByLabel('密码').fill(memberPassword)
    const registrationResponse = registrationPage.waitForResponse(
      (response) =>
        response.url().endsWith('/api/control/registrations') &&
        response.request().method() === 'POST',
    )
    await registrationPage.getByRole('button', { name: '提交注册' }).click()
    expect((await registrationResponse).status()).toBe(202)
    await expect(registrationPage.getByRole('heading', { name: '等待审核' })).toBeVisible()

    await registrationPage.getByRole('link', { name: '返回登录' }).click()
    await registrationPage.getByLabel('邮箱').fill(memberEmail)
    await registrationPage.getByLabel('密码').fill(memberPassword)
    const pendingLoginResponse = registrationPage.waitForResponse(
      (response) =>
        response.url().endsWith('/api/control/session') && response.request().method() === 'POST',
    )
    await registrationPage.getByRole('button', { name: '登录' }).click()
    const pendingLogin = await pendingLoginResponse
    browserProblems.allow(pendingLogin)
    expect(pendingLogin.status()).toBe(403)
    expect(problemCode(await pendingLogin.json())).toBe('approval_required')
    const pendingProblem = registrationPage.getByRole('alert')
    await expect(pendingProblem).toContainText('账号正在等待管理员审核。')
    await expect(pendingProblem).toContainText('Request ID：')
  } finally {
    await registrationContext.close()
  }
  invitation.code = ''

  await page.reload()
  const claimedInvitationRow = page
    .getByRole('table', { name: '邀请列表' })
    .getByRole('row')
    .filter({ hasText: `${invitation.prefix}…` })
  await expect(claimedInvitationRow).toHaveCount(1)
  await expect(claimedInvitationRow).toContainText('Browser Administrator')
  await expect(claimedInvitationRow).toContainText('Browser Member')
  await expectLocatorWidthToFit(claimedInvitationRow)
  await expectPageWidthToFit(page)

  await page.getByRole('link', { name: '用户', exact: true }).click()
  await page.reload()
  const memberRow = page.getByRole('row').filter({ hasText: memberEmail })
  await expect(memberRow).toContainText('待审核')
  const approvalResponse = page.waitForResponse(
    (response) =>
      response.url().includes('/api/control/users/') &&
      response.url().endsWith('/review') &&
      response.request().method() === 'POST',
  )
  await memberRow.getByRole('button', { name: '批准' }).click()
  expect((await approvalResponse).status()).toBe(200)
  await expect(memberRow).toContainText('可用')

  await createEntitlementAfterLostResponse(page, browser, browserProblems, gateway, catalog)
  const gatewayKey = await createGatewayKeyAfterLostResponse(page, browserProblems, catalog)
  await expectPublicModels(page, gatewayKey.secret, {
    included: [catalog.authorizedModelAlias],
    excluded: [catalog.ungrantedModelAlias, catalog.draftOnlyModelAlias],
  })
  await gateway.restart()
  await expectPublicModels(page, gatewayKey.secret, {
    included: [catalog.authorizedModelAlias],
    excluded: [catalog.ungrantedModelAlias, catalog.draftOnlyModelAlias],
  })

  await navigation.getByRole('link', { name: 'Provider 与模型' }).click()
  await expect(page.getByRole('heading', { name: 'Provider 与模型' })).toBeVisible()
  const logoutResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/session') && response.request().method() === 'DELETE',
  )
  const rejectedAdministratorSession = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/session') &&
      response.request().method() === 'GET' &&
      response.status() === 401,
  )
  await navigation.getByRole('button', { name: '退出登录' }).click()
  expect((await logoutResponse).status()).toBe(204)
  browserProblems.allow(await rejectedAdministratorSession)
  await expect(page.getByRole('heading', { name: '登录' })).toBeVisible()

  await page.getByLabel('邮箱').fill(memberEmail)
  await page.getByLabel('密码').fill(memberPassword)
  const memberLoginResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/session') && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '登录' }).click()
  const memberLogin = await memberLoginResponse
  expect(memberLogin.status()).toBe(200)
  const memberSession = dataRecord(await memberLogin.json())
  const memberCSRFToken =
    typeof memberSession?.csrfToken === 'string' ? memberSession.csrfToken : ''
  expect(memberCSRFToken).not.toBe('')
  await expect(page.getByRole('heading', { name: '我的网关 Key' })).toBeVisible()
  await expect(page.getByRole('table', { name: '网关 Key 列表' })).toContainText(gatewayKey.name)
  await verifyRealPlayground(page, browser, browserProblems)

  const usageResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/control/usage' &&
      response.request().method() === 'GET',
  )
  await page
    .getByRole('complementary', { name: '主导航' })
    .getByRole('link', { name: '我的用量' })
    .click()
  expect((await usageResponse).status()).toBe(200)
  await expect(page.getByRole('heading', { name: '我的用量' })).toBeVisible()
  const usageTable = page.getByRole('table', { name: '请求用量列表' })
  await expect(usageTable).toContainText('browser-chat')
  await expect(usageTable).toContainText('4')
  await expect(usageTable).toContainText('2')
  await expectPageWidthToFit(page)

  const managementRequests: string[] = []
  const observeManagementRequest = (request: Request) => {
    const pathname = new URL(request.url()).pathname
    if (
      request.method() === 'GET' &&
      (pathname === '/api/control/users' ||
        pathname === '/api/control/invitations' ||
        pathname === '/api/control/entitlements')
    ) {
      managementRequests.push(pathname)
    }
  }
  page.on('request', observeManagementRequest)
  try {
    await page.goto('/access/users')
    await expect(page.getByRole('heading', { name: '当前会话无权执行此任务' })).toBeVisible()
    await expect(page.getByText(administratorEmail)).toHaveCount(0)

    await page.goto('/access/invitations')
    await expect(page.getByRole('heading', { name: '当前会话无权执行此任务' })).toBeVisible()
    await expect(page.getByText(administratorEmail)).toHaveCount(0)

    await page.goto('/ledger/entitlements')
    await expect(page.getByRole('heading', { name: '当前会话无权执行此任务' })).toBeVisible()
    expect(managementRequests).toEqual([])
  } finally {
    page.off('request', observeManagementRequest)
  }

  const forbiddenEntitlements = await page.request.get('/api/control/entitlements')
  expect(forbiddenEntitlements.status()).toBe(403)
  expect(problemCode(await forbiddenEntitlements.json())).toBe('forbidden')

  await page.goto('/access/keys')
  const keyRow = page.getByRole('row').filter({ hasText: gatewayKey.name })
  const revokeResponse = page.waitForResponse(
    (response) => response.url().endsWith('/revoke') && response.request().method() === 'POST',
  )
  await keyRow.getByRole('button', { name: '撤销' }).click()
  expect((await revokeResponse).status()).toBe(200)
  await expect(keyRow).toContainText('已撤销')
  await gateway.restart()
  await page.reload()
  const repeatedRevocation = await page.request.post(
    '/api/control/keys/' + gatewayKey.id + '/revoke',
    { headers: { 'X-CSRF-Token': memberCSRFToken } },
  )
  expect(repeatedRevocation.status()).toBe(200)
  const rejectedModels = await page.request.get(gatewayEndpoint('/v1/models'), {
    headers: { Authorization: 'Bearer ' + gatewayKey.secret },
  })
  expect(rejectedModels.status()).toBe(401)
  expect(problemCode(await rejectedModels.json())).toBe('invalid_api_key')

  const memberState = await page.context().storageState()
  const mobileContext = await browser.newContext({
    ...devices['Pixel 7'],
    baseURL: origin,
    storageState: memberState,
  })
  try {
    const mobilePage = await mobileContext.newPage()
    browserProblems.observe(mobilePage)
    await mobilePage.goto('/access/keys')
    await expect(mobilePage.getByRole('heading', { name: '我的网关 Key' })).toBeVisible()
    await expect(mobilePage.getByRole('list', { name: '网关 Key 列表' })).toContainText(
      gatewayKey.name,
    )
    await expect(mobilePage.getByRole('list', { name: '网关 Key 列表' })).toContainText('已撤销')
    await mobilePage.getByRole('button', { name: '打开导航' }).click()
    const mobileNavigation = mobilePage.getByRole('dialog', { name: 'LLMGateway' })
    await expect(mobileNavigation.getByRole('link')).toHaveCount(3)
    await expect(mobileNavigation.getByRole('link', { name: '我的网关 Key' })).toBeVisible()
    await expect(mobileNavigation.getByRole('link', { name: '我的用量' })).toBeVisible()
    await expect(mobileNavigation).toContainText('Browser Member')
    await expectPageWidthToFit(mobilePage)
    const mobileUsageResponse = mobilePage.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/control/usage' &&
        response.request().method() === 'GET',
    )
    await mobileNavigation.getByRole('link', { name: '我的用量' }).click()
    expect((await mobileUsageResponse).status()).toBe(200)
    await expect(mobilePage.getByRole('heading', { name: '我的用量' })).toBeVisible()
    await expect(mobilePage.getByRole('list', { name: '请求用量列表' })).toContainText(
      'browser-chat',
    )
    await expectPageWidthToFit(mobilePage)
    await mobilePage.screenshot({
      path: acceptanceArtifactPath('member-usage-mobile.png'),
      fullPage: true,
      animations: 'disabled',
    })

    await mobilePage.getByRole('button', { name: '打开导航' }).click()
    const logoutNavigation = mobilePage.getByRole('dialog', { name: 'LLMGateway' })
    const mobileLogoutResponse = mobilePage.waitForResponse(
      (response) =>
        response.url().endsWith('/api/control/session') && response.request().method() === 'DELETE',
    )
    const rejectedMemberSession = mobilePage.waitForResponse(
      (response) =>
        response.url().endsWith('/api/control/session') &&
        response.request().method() === 'GET' &&
        response.status() === 401,
    )
    await logoutNavigation.getByRole('button', { name: '退出登录' }).click()
    expect((await mobileLogoutResponse).status()).toBe(204)
    browserProblems.allow(await rejectedMemberSession)
    await expect(mobilePage.getByRole('heading', { name: '登录' })).toBeVisible()
  } finally {
    await mobileContext.close()
  }
  await clearClipboard(page)
}
