import { expect, type Page } from '@playwright/test'

import {
  administratorEmail,
  expectPageWidthToFit,
  memberEmail,
  problemCode,
} from './acceptance-helpers'
import { verifyGatewayKeyRequest } from './gateway-key-test-flow'
import { acceptanceArtifactPath, test, type BrowserProblems } from './runtime'

const replacementUpstreamSecret = 'browser-upstream-secret-replaced'

test('completes the administrator and member subscription journey against real services', async ({
  browserProblems,
  gateway,
  page,
}) => {
  const anonymousSession = page.waitForResponse(
    (response) => response.url().endsWith('/api/control/session') && response.status() === 401,
  )
  await page.goto('/')
  browserProblems.allow(await anonymousSession)
  await page.getByLabel('管理员邮箱').fill(administratorEmail)
  const setupResponse = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/setup') && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '创建管理员' }).click()
  expect((await setupResponse).status()).toBe(201)
  const initialAdministratorPassword =
    (await page.getByTestId('initial-administrator-password').textContent()) ?? ''
  expect(initialAdministratorPassword).toMatch(/^[A-Za-z0-9_-]{40,}$/)
  expect(
    await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage })),
  ).not.toContain(initialAdministratorPassword)
  await page.screenshot({
    path: acceptanceArtifactPath('administrator-setup-success-desktop.png'),
    mask: [page.getByTestId('initial-administrator-password')],
    maskColor: '#1d2a27',
  })
  await page.getByRole('button', { name: /^我已保存/ }).click()
  await expectPageWidthToFit(page)
  await page.screenshot({ path: acceptanceArtifactPath('getting-started-desktop.png') })

  await page.getByRole('button', { name: '引导我完成' }).click()
  await page.locator('.driver-popover').evaluate(async (element) => {
    await Promise.all(element.getAnimations().map((animation) => animation.finished))
  })
  await page.screenshot({ path: acceptanceArtifactPath('onboarding-tour-desktop.png') })
  await page.getByRole('button', { name: '带我前往' }).click()
  await expect(page).toHaveURL(/\/resource-pools$/)
  await page.getByRole('button', { name: '知道了' }).click()

  await page.getByRole('button', { name: '创建资源池' }).click()
  const poolDialog = page.getByRole('dialog', { name: '创建资源池' })
  await poolDialog.getByLabel('上游平台').selectOption({ label: '硅基流动' })
  await poolDialog.getByLabel('资源池名称').fill('Browser Pool')
  await poolDialog.getByRole('checkbox', { name: 'qwen3.5-9b' }).check()
  await page.screenshot({
    path: acceptanceArtifactPath('resource-pool-form-desktop.png'),
    fullPage: true,
  })
  const poolResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/control/resource-pools' &&
      response.request().method() === 'POST',
  )
  await poolDialog.getByRole('button', { name: '保存' }).click()
  expect((await poolResponse).status()).toBe(201)

  await page.goto('/provider-keys')
  await page.getByRole('button', { name: '添加上游 API Key' }).click()
  const credentialDialog = page.getByRole('dialog', { name: '添加上游 API Key' })
  await credentialDialog.getByLabel('资源池').selectOption({ label: 'Browser Pool' })
  await credentialDialog.getByLabel('逐行粘贴').fill('Browser Upstream Key,core-upstream-secret')
  await page.screenshot({
    path: acceptanceArtifactPath('upstream-key-form-desktop.png'),
    fullPage: true,
  })
  const credentialResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/control/credentials/batch' &&
      response.request().method() === 'POST',
  )
  await credentialDialog.getByRole('button', { name: '添加', exact: true }).click()
  expect((await credentialResponse).status()).toBe(200)
  const addedDialog = page.getByRole('dialog', { name: '添加结果' })
  await expect(addedDialog.getByText('已创建')).toBeVisible()
  await addedDialog.getByRole('button', { name: '完成' }).click()

  const credentialRow = page.getByRole('row').filter({ hasText: 'Browser Upstream Key' })
  await credentialRow.getByRole('button', { name: '编辑' }).click()
  const editCredential = page.getByRole('dialog', { name: '编辑上游 API Key' })
  await editCredential.getByLabel('上游 API Key').fill(replacementUpstreamSecret)
  const replaceSecretResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname.startsWith('/api/control/credentials/') &&
      response.request().method() === 'PUT',
  )
  await editCredential.getByRole('button', { name: '保存' }).click()
  expect((await replaceSecretResponse).status()).toBe(200)

  await credentialRow.getByRole('button', { name: '测试' }).click()
  const probeDialog = page.getByRole('dialog', { name: '测试上游 API Key' })
  const probeResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname.endsWith('/probe') && response.request().method() === 'POST',
  )
  await probeDialog.getByRole('button', { name: '开始测试' }).click()
  const completedProbeResponse = await probeResponse
  expect(completedProbeResponse.status()).toBe(200)
  const completedProbe = (await completedProbeResponse.json()) as {
    data: { execution: { request_id: string } }
  }
  expect(completedProbe.data.execution.request_id).toMatch(/\S+/)
  await expect(probeDialog.getByText('连接成功')).toBeVisible()
  await expect(
    probeDialog.getByText(completedProbe.data.execution.request_id, { exact: true }),
  ).toBeVisible()
  await probeDialog.getByRole('button', { name: '关闭', exact: true }).last().click()

  await page.goto('/costs')
  await page.getByRole('button', { name: '新增价格' }).click()
  const priceDialog = page.getByRole('dialog', { name: '新增价格版本' })
  await priceDialog.getByLabel('模型').selectOption({ label: 'qwen3.5-9b' })
  await priceDialog.getByLabel('输入价格 / 百万 Token').fill('0.1')
  await priceDialog.getByLabel('输出价格 / 百万 Token').fill('0.2')
  const priceResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/control/model-prices' &&
      response.request().method() === 'POST',
  )
  await priceDialog.getByRole('button', { name: '保存' }).click()
  expect((await priceResponse).status()).toBe(201)

  await page.goto('/plans')
  await page.getByRole('button', { name: '创建套餐' }).click()
  const planDialog = page.getByRole('dialog', { name: '创建并发布套餐' })
  await planDialog.getByLabel('套餐名称').fill('Browser Plan')
  await planDialog.getByLabel('每份订阅总额度（Token）').fill('50000')
  await planDialog.getByRole('checkbox', { name: 'qwen3.5-9b' }).check()
  await page.screenshot({ path: acceptanceArtifactPath('plan-form-desktop.png'), fullPage: true })
  const planResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/control/plans' &&
      response.request().method() === 'POST',
  )
  await planDialog.getByRole('button', { name: '发布版本' }).click()
  expect((await planResponse).status()).toBe(201)

  await page.goto('/members')
  await page.getByRole('button', { name: '创建成员' }).click()
  const memberDialog = page.getByRole('dialog', { name: '创建成员' })
  await memberDialog.getByLabel('显示名称').fill('Browser Member')
  await memberDialog.getByLabel('邮箱').fill(memberEmail)
  const memberResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/control/members' &&
      response.request().method() === 'POST',
  )
  await memberDialog.getByRole('button', { name: '保存' }).click()
  expect((await memberResponse).status()).toBe(201)
  const initialMemberPassword =
    (await page.getByRole('dialog').locator('.secret-reveal code').textContent()) ?? ''
  expect(initialMemberPassword).toMatch(/^\S{40,}$/)
  await page.getByRole('dialog').getByRole('button', { name: '完成' }).click()

  await page.goto('/subscriptions')
  await page.getByRole('button', { name: '分配订阅' }).click()
  const subscriptionDialog = page.getByRole('dialog', { name: '分配订阅' })
  await subscriptionDialog
    .getByLabel('成员')
    .selectOption({ label: `Browser Member · ${memberEmail}` })
  await subscriptionDialog
    .getByLabel('套餐', { exact: true })
    .selectOption({ label: 'Browser Plan · v1' })
  await page.screenshot({
    path: acceptanceArtifactPath('subscription-form-desktop.png'),
    fullPage: true,
  })
  const subscriptionResponse = page.waitForResponse(
    (response) =>
      new URL(response.url()).pathname === '/api/control/subscriptions' &&
      response.request().method() === 'POST',
  )
  await subscriptionDialog.getByRole('button', { name: '保存' }).click()
  expect((await subscriptionResponse).status()).toBe(201)

  const administratorKeySecret = await createKey(page, 'Browser Admin-Created Key')
  expect(administratorKeySecret).toMatch(/^llmg_[A-Za-z0-9_-]+$/)
  await verifyGatewayKeyRequest(page, 'Browser Admin-Created Key')

  await page.goto('/provider-keys')
  await expect(page.getByRole('row').filter({ hasText: 'Browser Upstream Key' })).toBeVisible()
  await expectPageWidthToFit(page)
  await page.screenshot({
    path: acceptanceArtifactPath('upstream-keys-desktop.png'),
    fullPage: true,
  })

  await page.goto('/members')
  await expect(page.getByRole('row').filter({ hasText: 'Browser Member' })).toBeVisible()
  await expectPageWidthToFit(page)
  await page.screenshot({ path: acceptanceArtifactPath('members-desktop.png'), fullPage: true })

  await page.goto('/operations')
  await page.waitForLoadState('networkidle')
  await expectPageWidthToFit(page)
  await page.screenshot({ path: acceptanceArtifactPath('operations-desktop.png'), fullPage: true })

  await page.goto('/api-logs')
  const completedRequestRow = page.getByRole('row').filter({ hasText: 'qwen3.5-9b' }).first()
  await expect(completedRequestRow).toBeVisible()
  await completedRequestRow.click()
  await expect(page.getByRole('dialog', { name: '请求详情' })).toBeVisible()
  await expectPageWidthToFit(page)
  await page.screenshot({
    path: acceptanceArtifactPath('api-log-detail-desktop.png'),
    fullPage: true,
  })
  await page.getByRole('button', { name: '关闭详情' }).click()

  await page.goto('/quota-records')
  await expect(page.getByRole('row').filter({ hasText: 'Browser Plan' }).first()).toBeVisible()
  await expectPageWidthToFit(page)
  await page.screenshot({
    path: acceptanceArtifactPath('quota-records-desktop.png'),
    fullPage: true,
  })

  await page.goto('/costs')
  await expect(page.getByRole('row').filter({ hasText: 'Browser Member' })).toBeVisible()
  await expectPageWidthToFit(page)
  await page.screenshot({ path: acceptanceArtifactPath('costs-desktop.png'), fullPage: true })

  await page.goto('/site-settings')
  await expectPageWidthToFit(page)
  await page.screenshot({
    path: acceptanceArtifactPath('site-settings-desktop.png'),
    fullPage: true,
  })

  await page.goto('/dashboard')
  await expectPageWidthToFit(page)
  await page.screenshot({
    path: acceptanceArtifactPath('administrator-dashboard-desktop.png'),
    fullPage: true,
  })

  await page.goto('about:blank')
  await gateway.restart()
  await page.goto('/subscriptions')
  await expect(page.getByRole('row').filter({ hasText: 'Browser Plan' })).toBeVisible()
  await logout(page, browserProblems)
  await login(page, memberEmail, initialMemberPassword)
  await page.goto('/subscriptions')
  await expect(page.getByRole('row').filter({ hasText: 'Browser Plan' })).toBeVisible()
  const memberKeySecret = await createKey(page, 'Browser Member-Created Key')
  expect(memberKeySecret).toMatch(/^llmg_[A-Za-z0-9_-]+$/)
  await page.goto('/account')
  await expect(
    page.locator('#main-content').getByText('Browser Member', { exact: true }),
  ).toBeVisible()
  await page.goto('/dashboard')
  await expect(page.getByText('可用', { exact: true })).toBeVisible()
  await expectPageWidthToFit(page)
  await page.screenshot({
    path: acceptanceArtifactPath('member-dashboard-desktop.png'),
    fullPage: true,
  })

  await page.goto('/api-logs')
  await expect(page.getByRole('row').filter({ hasText: 'qwen3.5-9b' }).first()).toBeVisible()
  await expectPageWidthToFit(page)
  await page.screenshot({
    path: acceptanceArtifactPath('member-api-logs-desktop.png'),
    fullPage: true,
  })

  await page.goto('/quota-records')
  await expect(page.getByRole('row').filter({ hasText: 'Browser Plan' }).first()).toBeVisible()
  await expectPageWidthToFit(page)
  await page.screenshot({
    path: acceptanceArtifactPath('member-quota-records-desktop.png'),
    fullPage: true,
  })

  const forbiddenMembers = await page.request.get('/api/control/members')
  expect(forbiddenMembers.status()).toBe(403)
  expect(problemCode(await forbiddenMembers.json())).toBe('forbidden')
  await logout(page, browserProblems)
})

async function createKey(page: Page, name: string): Promise<string> {
  await page.goto('/api-keys')
  await page.getByRole('button', { name: '创建 API 密钥' }).click()
  const dialog = page.getByRole('dialog', { name: '创建 API 密钥' })
  const owner = dialog.getByLabel('所属成员')
  if (await owner.locator('option').count())
    await owner.selectOption({ label: `Browser Member · ${memberEmail}` })
  await dialog.getByLabel('名称').fill(name)
  await dialog.getByRole('checkbox', { name: 'qwen3.5-9b' }).check()
  if (name === 'Browser Admin-Created Key') {
    await page.screenshot({
      path: acceptanceArtifactPath('api-key-form-desktop.png'),
      fullPage: true,
    })
  }
  const response = page.waitForResponse(
    (candidate) =>
      new URL(candidate.url()).pathname === '/api/control/keys' &&
      candidate.request().method() === 'POST',
  )
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  expect((await response).status()).toBe(201)
  const result = page.getByRole('dialog', { name: 'API 密钥已创建' })
  const secret = (await result.locator('.secret-reveal code').textContent()) ?? ''
  await result.getByRole('button', { name: '完成' }).click()
  return secret
}

async function login(page: Page, email: string, password: string) {
  await page.goto('/login')
  await page.getByLabel('邮箱').fill(email)
  await page.getByLabel('密码').fill(password)
  const response = page.waitForResponse(
    (candidate) =>
      candidate.url().endsWith('/api/control/session') && candidate.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '登录' }).click()
  expect((await response).status()).toBe(200)
}

async function logout(page: Page, browserProblems: BrowserProblems) {
  const response = page.waitForResponse(
    (candidate) =>
      candidate.url().endsWith('/api/control/session') && candidate.request().method() === 'DELETE',
  )
  const anonymousSession = page.waitForResponse(
    (candidate) =>
      candidate.url().endsWith('/api/control/session') &&
      candidate.request().method() === 'GET' &&
      candidate.status() === 401,
  )
  await page.getByRole('button', { name: '退出登录' }).click()
  expect((await response).status()).toBe(204)
  browserProblems.allow(await anonymousSession)
}
