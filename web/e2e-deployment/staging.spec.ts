import { expect, test, type Page } from '@playwright/test'

import { problemCode } from '../e2e-real/acceptance-helpers'

const administratorEmail = 'deployment-admin@example.test'
const administratorPassword = 'deployment-administrator-password'
const memberEmail = 'deployment-member@example.test'
const memberPassword = 'deployment-member-password'

test('preserves administrator and member boundaries through the production TLS topology', async ({
  browser,
  page,
}) => {
  const mode = process.env.LLMGATEWAY_DEPLOYMENT_MODE
  if (mode === 'restored') {
    await verifyRestoredIdentities(page)
    return
  }
  expect(mode).toBe('setup')

  await page.goto('/')
  await page.getByLabel('管理员邮箱').fill(administratorEmail)
  const setupResponsePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/setup') && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '创建管理员' }).click()
  const setupResponse = await setupResponsePromise
  expect(setupResponse.status()).toBe(201)
  expect(setupResponse.headers()['cache-control']).toBe('no-store')
  expect(setupResponse.request().postDataJSON()).toEqual({ email: administratorEmail })
  const setupPayload = (await setupResponse.json()) as { data?: { csrfToken?: unknown } }
  const csrfToken =
    typeof setupPayload.data?.csrfToken === 'string' ? setupPayload.data.csrfToken : ''
  expect(csrfToken).not.toBe('')
  const initialAdministratorPassword =
    (await page.getByTestId('initial-administrator-password').textContent()) ?? ''
  expect(initialAdministratorPassword).toMatch(/^[A-Za-z0-9_-]{40,}$/)
  await page.getByRole('button', { name: '我已保存，进入控制面' }).click()

  await page
    .getByRole('complementary', { name: '管理员导航' })
    .getByRole('button', { name: '更换密码' })
    .click()
  const passwordDialog = page.getByRole('dialog', { name: '更换密码' })
  await passwordDialog.getByLabel('当前密码').fill(initialAdministratorPassword)
  await passwordDialog.getByLabel('新密码', { exact: true }).fill(administratorPassword)
  await passwordDialog.getByLabel('确认新密码').fill(administratorPassword)
  const passwordChangeResponsePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/password') && response.request().method() === 'POST',
  )
  await passwordDialog.getByRole('button', { name: '确认更换' }).click()
  expect((await passwordChangeResponsePromise).status()).toBe(200)
  await page
    .getByRole('dialog', { name: '密码已更换' })
    .getByRole('button', { name: '完成' })
    .click()

  const passwordVerificationContext = await browser.newContext({
    baseURL: new URL(page.url()).origin,
    ignoreHTTPSErrors: true,
  })
  try {
    const rejectedLogin = await passwordVerificationContext.request.post('/api/control/session', {
      data: { email: administratorEmail, password: initialAdministratorPassword },
    })
    expect(rejectedLogin.status()).toBe(401)
    const acceptedLogin = await passwordVerificationContext.request.post('/api/control/session', {
      data: { email: administratorEmail, password: administratorPassword },
    })
    expect(acceptedLogin.status()).toBe(200)
  } finally {
    await passwordVerificationContext.close()
  }

  const invitationResponse = await page.request.post('/api/control/invitations', {
    data: { expiresAt: new Date(Date.now() + 24 * 60 * 60 * 1000).toISOString() },
    headers: { 'Idempotency-Key': crypto.randomUUID(), 'X-CSRF-Token': csrfToken },
  })
  expect(invitationResponse.status()).toBe(201)
  const invitationPayload = (await invitationResponse.json()) as { data?: { code?: unknown } }
  const invitationCode =
    typeof invitationPayload.data?.code === 'string' ? invitationPayload.data.code : ''
  expect(invitationCode).toMatch(/^invite_[A-Za-z0-9_-]{20,}$/)

  const registrationContext = await browser.newContext({
    baseURL: new URL(page.url()).origin,
    ignoreHTTPSErrors: true,
  })
  const registrationPage = await registrationContext.newPage()
  await registrationPage.goto('/register')
  await registrationPage.getByLabel('邀请码').fill(invitationCode)
  await registrationPage.getByLabel('显示名称').fill('Deployment Member')
  await registrationPage.getByLabel('邮箱').fill(memberEmail)
  await registrationPage.getByLabel('密码').fill(memberPassword)
  const registrationResponsePromise = registrationPage.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/registrations') &&
      response.request().method() === 'POST',
  )
  await registrationPage.getByRole('button', { name: '提交注册' }).click()
  expect((await registrationResponsePromise).status()).toBe(202)
  await registrationContext.close()

  await page.goto('/members')
  const memberRow = page.getByRole('row').filter({ hasText: memberEmail })
  const approvalResponsePromise = page.waitForResponse(
    (response) =>
      response.url().includes('/api/control/users/') &&
      response.url().endsWith('/review') &&
      response.request().method() === 'POST',
  )
  await memberRow.getByRole('button', { name: '批准' }).click()
  expect((await approvalResponsePromise).status()).toBe(200)

  await page
    .getByRole('complementary', { name: '管理员导航' })
    .getByRole('button', { name: '退出登录' })
    .click()
  await login(page, memberEmail, memberPassword)
  const managementRequests: string[] = []
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname
    if (request.method() === 'GET' && path === '/api/control/users') managementRequests.push(path)
  })
  await page.goto('/members')
  expect(managementRequests).toEqual([])
  const forbidden = await page.request.get('/api/control/users')
  expect(forbidden.status()).toBe(403)
  expect(problemCode(await forbidden.json())).toBe('forbidden')
})

async function verifyRestoredIdentities(page: Page): Promise<void> {
  await page.goto('/login')
  await login(page, administratorEmail, administratorPassword)
  await page
    .getByRole('complementary', { name: '管理员导航' })
    .getByRole('button', { name: '退出登录' })
    .click()
  await login(page, memberEmail, memberPassword)
  const forbidden = await page.request.get('/api/control/users')
  expect(forbidden.status()).toBe(403)
}

async function login(page: Page, email: string, password: string): Promise<void> {
  await page.goto('/login')
  await page.getByLabel('邮箱').fill(email)
  await page.getByLabel('密码').fill(password)
  const responsePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/session') && response.request().method() === 'POST',
  )
  await page.getByRole('button', { name: '登录' }).click()
  expect((await responsePromise).status()).toBe(200)
}
