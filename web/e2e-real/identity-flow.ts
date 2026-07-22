import { devices, expect, type Browser, type Page, type Request } from '@playwright/test'

import {
  administratorEmail,
  administratorPassword,
  clearClipboard,
  dataRecord,
  expectPageWidthToFit,
  gatewayEndpoint,
  memberEmail,
  memberPassword,
  memberReplacementPassword,
  problemCode,
} from './acceptance-helpers'
import type { PublishedCatalogFacts } from './catalog-flow'
import { createEntitlementAfterLostResponse } from './entitlement-flow'
import { createInvitationAfterLostResponse } from './invitation-flow'
import { createGatewayKeyAfterLostResponse, expectPublicModels } from './key-flow'
import { verifyGatewayKeyRequest } from './gateway-key-test-flow'
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
  await navigation.getByRole('link', { name: '成员与 API Key' }).click()
  await page.getByRole('link', { name: '邀请', exact: true }).click()
  await expect(page).toHaveURL(/\/access\/invitations$/)

  const invitation = await createInvitationAfterLostResponse(page, browserProblems, gateway)
  const invitationRowsAfterRestart = page
    .getByRole('table', { name: '邀请列表' })
    .getByRole('row')
    .filter({ hasText: `${invitation.prefix}…` })
  await expect(invitationRowsAfterRestart).toHaveCount(1)
  expect(
    await page.evaluate((code) => !document.body.innerText.includes(code), invitation.code),
  ).toBe(true)

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
    await expect(pendingProblem).toBeVisible()
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
  await expect(claimedInvitationRow).toContainText('Browser Member')
  await expectPageWidthToFit(page)

  await page.getByRole('link', { name: '成员', exact: true }).click()
  await page.reload()
  const memberRow = page.getByRole('row').filter({ hasText: memberEmail })
  const approvalResponse = page.waitForResponse(
    (response) =>
      response.url().includes('/api/control/users/') &&
      response.url().endsWith('/review') &&
      response.request().method() === 'POST',
  )
  await memberRow.getByRole('button', { name: '批准' }).click()
  expect((await approvalResponse).status()).toBe(200)

  const secondaryAdministratorContext = await browser.newContext({ baseURL: origin })
  try {
    const secondaryAdministratorPage = await secondaryAdministratorContext.newPage()
    browserProblems.observe(secondaryAdministratorPage)
    await secondaryAdministratorPage.goto('/login')
    await secondaryAdministratorPage.getByLabel('邮箱').fill(administratorEmail)
    await secondaryAdministratorPage.getByLabel('密码').fill(administratorPassword)
    await secondaryAdministratorPage.getByRole('button', { name: '登录' }).click()
    await expect(secondaryAdministratorPage).toHaveURL(/\/overview$/)

    const administratorRow = page.getByRole('row').filter({ hasText: administratorEmail })
    const revokeSessionsResponse = page.waitForResponse(
      (response) =>
        response.url().endsWith('/sessions/revoke') && response.request().method() === 'POST',
    )
    await administratorRow.getByRole('button', { name: '撤销会话' }).click()
    const revokeDialog = page.getByRole('alertdialog')
    await revokeDialog.getByRole('button', { name: '确认撤销' }).click()
    const revokeResult = await revokeSessionsResponse
    expect(revokeResult.status()).toBe(200)
    expect(dataRecord(await revokeResult.json())?.revokedSessions).toBeGreaterThanOrEqual(1)
    expect((await page.request.get('/api/control/session')).status()).toBe(200)
    const rejectedSecondarySession =
      await secondaryAdministratorPage.request.get('/api/control/session')
    expect(rejectedSecondarySession.status()).toBe(401)
  } finally {
    await secondaryAdministratorContext.close()
  }

  const memberRecoveryContext = await browser.newContext({ baseURL: origin })
  try {
    const memberRecoveryPage = await memberRecoveryContext.newPage()
    browserProblems.observe(memberRecoveryPage)
    await memberRecoveryPage.goto('/login')
    await memberRecoveryPage.getByLabel('邮箱').fill(memberEmail)
    await memberRecoveryPage.getByLabel('密码').fill(memberPassword)
    await memberRecoveryPage.getByRole('button', { name: '登录' }).click()
    await expect(memberRecoveryPage).toHaveURL(/\/overview$/)

    const passwordResetResponse = page.waitForResponse(
      (response) => response.url().endsWith('/password') && response.request().method() === 'POST',
    )
    await memberRow.getByRole('button', { name: '重置密码' }).click()
    const passwordDialog = page.getByRole('dialog')
    await passwordDialog.getByLabel('新密码', { exact: true }).fill(memberReplacementPassword)
    await passwordDialog.getByLabel('确认新密码').fill(memberReplacementPassword)
    await passwordDialog.getByRole('button', { name: '确认重置' }).click()
    const passwordReset = await passwordResetResponse
    expect(passwordReset.status()).toBe(200)
    expect(dataRecord(await passwordReset.json())?.revokedSessions).toBe(1)
    const storedState = await page.evaluate(() =>
      JSON.stringify({
        local: Object.fromEntries(Object.entries(localStorage)),
        session: Object.fromEntries(Object.entries(sessionStorage)),
      }),
    )
    expect(storedState).not.toContain(memberReplacementPassword)
    await passwordDialog.getByRole('button', { name: '完成' }).click()
    expect((await memberRecoveryPage.request.get('/api/control/session')).status()).toBe(401)
  } finally {
    await memberRecoveryContext.close()
  }

  await createEntitlementAfterLostResponse(page, browserProblems, gateway, catalog)
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

  await navigation.getByRole('link', { name: 'Provider 接入' }).click()
  await expect(page).toHaveURL(/\/providers\/providers$/)
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

  const oldPasswordLogin = await page.request.post('/api/control/session', {
    data: { email: memberEmail, password: memberPassword },
  })
  expect(oldPasswordLogin.status()).toBe(401)
  expect(problemCode(await oldPasswordLogin.json())).toBe('invalid_credential')

  await page.getByLabel('邮箱').fill(memberEmail)
  await page.getByLabel('密码').fill(memberReplacementPassword)
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
  await expect(page).toHaveURL(/\/overview$/)
  await page
    .getByRole('complementary', { name: '主导航' })
    .getByRole('link', { name: '我的 API Key' })
    .click()
  await expect(page.getByRole('table', { name: 'API Key 列表' })).toContainText(gatewayKey.name)
  await verifyGatewayKeyRequest(page, browserProblems, gatewayKey.name)

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
    await expect(page.getByText(administratorEmail)).toHaveCount(0)

    await page.goto('/access/invitations')
    await expect(page.getByText(administratorEmail)).toHaveCount(0)

    await page.goto('/ledger/entitlements')
    expect(managementRequests).toEqual([])
  } finally {
    page.off('request', observeManagementRequest)
  }

  const forbiddenEntitlements = await page.request.get('/api/control/entitlements')
  expect(forbiddenEntitlements.status()).toBe(403)
  expect(problemCode(await forbiddenEntitlements.json())).toBe('forbidden')

  await page.goto('/access/keys')
  const keyRow = page
    .getByRole('row')
    .filter({ has: page.getByText(gatewayKey.name, { exact: true }) })
  const replacementPath = '/api/control/keys/' + gatewayKey.id + '/replacement'
  let replacementResponseInterrupted = false
  let replacementIdempotencyKey = ''
  await page.route('**' + replacementPath, async (route) => {
    const request = route.request()
    if (replacementResponseInterrupted || request.method() !== 'POST') {
      await route.continue()
      return
    }
    replacementResponseInterrupted = true
    replacementIdempotencyKey = request.headers()['idempotency-key'] ?? ''
    const committed = await route.fetch()
    expect(committed.status()).toBe(201)
    await route.abort('failed')
  })
  browserProblems.allowRequestFailure('POST', replacementPath, 'net::ERR_FAILED')
  let replacementSecret: string
  try {
    const failedReplacement = page.waitForEvent(
      'requestfailed',
      (request) =>
        new URL(request.url()).pathname === replacementPath && request.method() === 'POST',
    )
    await keyRow.getByRole('button', { name: '更换' }).click()
    const replacementDialog = page.getByRole('dialog')
    await replacementDialog.getByRole('button', { name: '创建替换 Key' }).click()
    await failedReplacement
    expect(replacementIdempotencyKey).toMatch(/^[0-9a-f-]{36}$/)
    await expect(replacementDialog.getByRole('alert')).toBeVisible()
    const replayResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === replacementPath &&
        response.request().method() === 'POST',
    )
    await replacementDialog.getByRole('button', { name: '重试创建' }).click()
    const replayed = await replayResponse
    expect(replayed.status()).toBe(201)
    expect(replayed.request().headers()['idempotency-key']).toBe(replacementIdempotencyKey)
    await replacementDialog.getByRole('button', { name: '复制调用配置' }).click()
    const copiedConfiguration = await page.evaluate(() => navigator.clipboard.readText())
    expect(copiedConfiguration).toContain(`OPENAI_BASE_URL=${new URL(page.url()).origin}/v1`)
    replacementSecret =
      copiedConfiguration.match(/^OPENAI_API_KEY=(llmg_[A-Za-z0-9_-]+)$/m)?.[1] ?? ''
    expect(replacementSecret).toMatch(/^llmg_[A-Za-z0-9_-]+$/)
    const persistedState = await page.evaluate(() =>
      JSON.stringify({
        local: Object.fromEntries(Object.entries(localStorage)),
        session: Object.fromEntries(Object.entries(sessionStorage)),
      }),
    )
    expect(persistedState).not.toContain(replacementSecret)
    await replacementDialog.getByRole('button', { name: '完成' }).click()
  } finally {
    await clearClipboard(page)
    await page.unroute('**' + replacementPath)
  }
  await expectPublicModels(page, gatewayKey.secret, {
    included: [catalog.authorizedModelAlias],
    excluded: [catalog.ungrantedModelAlias, catalog.draftOnlyModelAlias],
  })
  await expectPublicModels(page, replacementSecret, {
    included: [catalog.authorizedModelAlias],
    excluded: [catalog.ungrantedModelAlias, catalog.draftOnlyModelAlias],
  })
  const revokeResponse = page.waitForResponse(
    (response) => response.url().endsWith('/revoke') && response.request().method() === 'POST',
  )
  await keyRow.getByRole('button', { name: '撤销' }).click()
  await page.getByRole('alertdialog').getByRole('button', { name: '确认撤销' }).click()
  expect((await revokeResponse).status()).toBe(200)
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
  await expectPublicModels(page, replacementSecret, {
    included: [catalog.authorizedModelAlias],
    excluded: [catalog.ungrantedModelAlias, catalog.draftOnlyModelAlias],
  })

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
    await expect(mobilePage).toHaveURL(/\/access\/keys$/)
    await expectPageWidthToFit(mobilePage)
    const mobileUsageResponse = mobilePage.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === '/api/control/usage' &&
        response.request().method() === 'GET',
    )
    await mobilePage.goto('/ledger/usage')
    expect((await mobileUsageResponse).status()).toBe(200)
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
  } finally {
    await mobileContext.close()
  }
  await clearClipboard(page)
}
