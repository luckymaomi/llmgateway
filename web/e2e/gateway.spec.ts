import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

const fixtureBaseUrl = 'http://127.0.0.1:4174'

interface BrowserObservation {
  problems: string[]
}

const browserObservations = new WeakMap<Page, BrowserObservation>()

test.beforeEach(async ({ page, request }) => {
  const observation: BrowserObservation = { problems: [] }
  browserObservations.set(page, observation)
  page.on('console', (message) => {
    if (message.type() !== 'error' || message.text().startsWith('Failed to load resource:')) return
    observation.problems.push(`console: ${message.text()}`)
  })
  page.on('pageerror', (error) => observation.problems.push(`pageerror: ${error.message}`))
  page.on('response', (response) => {
    if (response.status() < 400) return
    const path = new URL(response.url()).pathname
    if (response.status() === 401 && path === '/api/control/session') return
    observation.problems.push(
      `http: ${response.request().method()} ${path} -> ${response.status()}`,
    )
  })
  await resetApi(request, { authenticated: true })
})

test.afterEach(({ page }) => {
  expect(browserObservations.get(page)?.problems ?? [], 'browser errors').toEqual([])
})

test('enters the application and follows capability navigation', async ({ page, request }) => {
  await resetApi(request, { authenticated: false, setupRequired: true })
  await page.goto('/setup')
  await page.getByLabel('管理员名称').fill('Gateway Admin')
  await page.getByLabel('邮箱').fill('admin@example.com')
  await page.getByLabel('密码', { exact: true }).fill('correct-horse-battery')
  await page.getByLabel('确认密码').fill('correct-horse-battery')
  await page.getByRole('button', { name: '创建管理员' }).click()
  await expect(page).toHaveURL(/\/providers\/providers$/)
  await expect(responsiveCollection(page, 'Provider 列表')).toContainText('Primary Provider')

  await navigateFromShell(page, '上游凭据池')
  await expect(page).toHaveURL(/\/credentials$/)
  await expect(responsiveCollection(page, '上游凭据列表')).toBeVisible()

  await navigateFromShell(page, 'Playground')
  await expect(page.getByRole('heading', { name: 'Playground' })).toBeVisible()
  await expectPageWidthToFit(page)
})

test('clears privileged facts before a member reaches a management URL', async ({
  page,
  request,
}) => {
  await page.goto('/access/users')
  await expect(responsiveCollection(page, '用户列表')).toContainText('admin@example.com')
  await logoutFromShell(page)

  await resetApi(request, { authenticated: false, role: 'member' })
  await page.getByLabel('邮箱').fill('member@example.com')
  await page.getByLabel('密码').fill('member-password')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page.getByRole('heading', { name: '我的网关 Key' })).toBeVisible()

  let managementRequests = 0
  page.on('request', (candidate) => {
    if (
      candidate.method() === 'GET' &&
      new URL(candidate.url()).pathname === '/api/control/users'
    ) {
      managementRequests += 1
    }
  })
  await page.goto('/access/users')
  await expect(page.getByRole('heading', { name: '当前会话无权执行此任务' })).toBeVisible()
  await expect(page.getByText('admin@example.com')).toHaveCount(0)
  expect(managementRequests).toBe(0)
  await expectPageWidthToFit(page)
})

async function resetApi(
  request: APIRequestContext,
  options: { authenticated: boolean; setupRequired?: boolean; role?: 'administrator' | 'member' },
) {
  const response = await request.post(`${fixtureBaseUrl}/__test/reset`, { data: options })
  expect(response.ok()).toBe(true)
}

async function navigateFromShell(page: Page, label: string) {
  const desktopLink = page.locator('.sidebar').getByRole('link', { name: label })
  if (await desktopLink.isVisible()) {
    await desktopLink.click()
    return
  }
  await page.getByRole('button', { name: '打开导航' }).click()
  await page.getByRole('dialog').getByRole('link', { name: label }).click()
}

async function logoutFromShell(page: Page) {
  const desktopLogout = page.locator('.sidebar').getByRole('button', { name: '退出登录' })
  if (await desktopLogout.isVisible()) {
    await desktopLogout.click()
    return
  }
  await page.getByRole('button', { name: '打开导航' }).click()
  await page.getByRole('dialog').getByRole('button', { name: '退出登录' }).click()
}

function responsiveCollection(page: Page, label: string) {
  return page.getByRole('table', { name: label }).or(page.getByRole('list', { name: label }))
}

async function expectPageWidthToFit(page: Page) {
  await expect
    .poll(() =>
      page.evaluate(
        () => document.documentElement.scrollWidth <= document.documentElement.clientWidth + 1,
      ),
    )
    .toBe(true)
}
