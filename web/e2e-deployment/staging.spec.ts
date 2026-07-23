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
  await page.goto('/')

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

  await page.goto('/members')
  await page.getByRole('button', { name: '创建成员' }).click()
  const memberDialog = page.getByRole('dialog', { name: '创建成员' })
  await memberDialog.getByLabel('显示名称').fill('Deployment Member')
  await memberDialog.getByLabel('邮箱').fill(memberEmail)
  const memberResponsePromise = page.waitForResponse(
    (response) =>
      response.url().endsWith('/api/control/members') && response.request().method() === 'POST',
  )
  await memberDialog.getByRole('button', { name: '保存' }).click()
  expect((await memberResponsePromise).status()).toBe(201)
  const initialMemberPassword =
    (await page.getByRole('dialog').locator('.secret-reveal code').textContent()) ?? ''
  expect(initialMemberPassword).toMatch(/^\S{40,}$/)
  await page.getByRole('dialog').getByRole('button', { name: '完成' }).click()
  const memberRow = page.getByRole('row').filter({ hasText: memberEmail })
  await expect(memberRow).toBeVisible()

  await logout(page, '管理员导航')
  await login(page, memberEmail, initialMemberPassword)
  await changePassword(page, '控制台导航', initialMemberPassword, memberPassword)
  const managementRequests: string[] = []
  page.on('request', (request) => {
    const path = new URL(request.url()).pathname
    if (request.method() === 'GET' && path === '/api/control/members') managementRequests.push(path)
  })
  await page.goto('/members')
  expect(managementRequests).toEqual([])
  const forbidden = await page.request.get('/api/control/members')
  expect(forbidden.status()).toBe(403)
  expect(problemCode(await forbidden.json())).toBe('forbidden')
})

async function verifyRestoredIdentities(page: Page): Promise<void> {
  await page.goto('/login')
  await login(page, administratorEmail, administratorPassword)
  await logout(page, '管理员导航')
  await login(page, memberEmail, memberPassword)
  const forbidden = await page.request.get('/api/control/members')
  expect(forbidden.status()).toBe(403)
}

async function changePassword(
  page: Page,
  navigationName: string,
  currentPassword: string,
  replacementPassword: string,
): Promise<void> {
  await page
    .getByRole('complementary', { name: navigationName })
    .getByRole('button', { name: '更换密码' })
    .click()
  const dialog = page.getByRole('dialog', { name: '更换密码' })
  await dialog.getByLabel('当前密码').fill(currentPassword)
  await dialog.getByLabel('新密码', { exact: true }).fill(replacementPassword)
  await dialog.getByLabel('确认新密码').fill(replacementPassword)
  const response = page.waitForResponse(
    (candidate) =>
      candidate.url().endsWith('/api/control/password') && candidate.request().method() === 'POST',
  )
  await dialog.getByRole('button', { name: '确认更换' }).click()
  expect((await response).status()).toBe(200)
  await page
    .getByRole('dialog', { name: '密码已更换' })
    .getByRole('button', { name: '完成' })
    .click()
}

async function logout(page: Page, navigationName: string): Promise<void> {
  const response = page.waitForResponse(
    (candidate) =>
      candidate.url().endsWith('/api/control/session') && candidate.request().method() === 'DELETE',
  )
  await page
    .getByRole('complementary', { name: navigationName })
    .getByRole('button', { name: '退出登录' })
    .click()
  expect((await response).status()).toBe(204)
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
