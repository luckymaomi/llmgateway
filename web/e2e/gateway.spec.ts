import { expect, test, type APIRequestContext, type Page } from '@playwright/test'

const fixtureBaseUrl = 'http://127.0.0.1:4174'

test.beforeEach(async ({ request }) => {
  await resetApi(request, { authenticated: true })
})

test('completes setup and login, then follows capability navigation', async ({ page, request }) => {
  await resetApi(request, { authenticated: false, setupRequired: true })
  await page.goto('/setup')
  await expect(page.getByRole('heading', { name: '初始化 LLMGateway' })).toBeVisible()

  await page.getByLabel('管理员名称').fill('Gateway Admin')
  await page.getByLabel('邮箱').fill('admin@example.com')
  await page.getByLabel('密码', { exact: true }).fill('correct-horse-battery')
  await page.getByLabel('确认密码').fill('correct-horse-battery')
  const setupRequestPromise = page.waitForRequest(
    (candidate) => candidate.url().endsWith('/api/control/setup') && candidate.method() === 'POST',
  )
  await page.getByRole('button', { name: '创建管理员' }).click()
  const setupRequest = await setupRequestPromise
  expect(setupRequest.postDataJSON()).toEqual({
    displayName: 'Gateway Admin',
    email: 'admin@example.com',
    password: 'correct-horse-battery',
  })
  await expect(page).toHaveURL(/\/overview$/)
  await expect(page.getByRole('heading', { name: '总览' })).toBeVisible()
  await expectPageWidthToFit(page)

  await resetApi(request, { authenticated: false })
  await page.goto('/login')
  await page.getByLabel('邮箱').fill('admin@example.com')
  await page.getByLabel('密码').fill('correct-horse-battery')
  await page.getByRole('button', { name: '登录' }).click()
  await expect(page).toHaveURL(/\/overview$/)

  await navigateFromShell(page, 'Provider 与模型')
  await expect(page).toHaveURL(/\/providers\/providers$/)
  await expect(responsiveCollection(page, 'Provider 列表')).toContainText('Primary Provider')
  await expectPageWidthToFit(page)
})

test('keeps a new gateway key inside its one-time acknowledgement flow', async ({ page }) => {
  await page.goto('/access/keys')
  await expect(page.getByRole('heading', { name: '用户与网关 Key' })).toBeVisible()
  await page.getByRole('button', { name: '创建 Key' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByLabel('所属用户').selectOption('user-admin')
  await dialog.getByLabel('名称').fill('Automation Key')
  await dialog.getByLabel('授权模型').fill('gpt-main')

  const creationRequestPromise = page.waitForRequest(
    (candidate) => candidate.url().endsWith('/api/control/keys') && candidate.method() === 'POST',
  )
  await dialog.getByRole('button', { name: '创建', exact: true }).click()
  const creationRequest = await creationRequestPromise
  expect(creationRequest.headers()['x-csrf-token']).toBe('csrf-e2e-token')
  expect(creationRequest.postDataJSON()).toEqual({
    ownerId: 'user-admin',
    name: 'Automation Key',
    authorizedModels: ['gpt-main'],
  })
  await expect(page.getByTestId('created-key-secret')).toHaveText('lgw_live_7F2A_once_secret')
  await page.getByRole('button', { name: '完成' }).click()
  await expect(responsiveCollection(page, '网关 Key 列表')).toContainText('Automation Key')

  await page.getByRole('button', { name: '创建 Key' }).click()
  await expect(
    page.getByRole('dialog').getByRole('heading', { name: '创建网关 Key' }),
  ).toBeVisible()
  await expect(page.getByRole('dialog').getByLabel('名称')).toHaveValue('')
  await expectPageWidthToFit(page)
})

test('publishes a validated configuration revision with optimistic concurrency', async ({
  page,
}) => {
  await page.goto('/providers/revisions')
  await expect(responsiveCollection(page, '配置版本列表')).toContainText('Add primary route')
  const publishRequestPromise = page.waitForRequest(
    (candidate) =>
      candidate.url().endsWith('/configuration/revisions/revision-42/publish') &&
      candidate.method() === 'POST',
  )
  await page.getByRole('button', { name: '发布', exact: true }).click()
  const publishRequest = await publishRequestPromise
  expect(publishRequest.headers()['x-csrf-token']).toBe('csrf-e2e-token')
  expect(publishRequest.postDataJSON()).toEqual({ expectedActiveRevisionId: 'revision-41' })
  const operation = page.getByRole('region', { name: '操作进度' })
  await expect(operation).toContainText('配置已发布')
  await expect(operation).toContainText('100%')
  await expectPageWidthToFit(page)
})

test('streams Playground facts, presents an API problem, and cancels an active run', async ({
  page,
}) => {
  await page.goto('/playground')
  await expect(page.getByRole('heading', { name: 'Playground' })).toBeVisible()

  const prompt = page.getByLabel('消息')
  await prompt.fill('给出流式结果')
  await page.getByRole('button', { name: '运行' }).click()
  await expect(page.getByText('这是流式响应')).toBeVisible()
  const facts = page.getByRole('complementary', { name: '运行事实' })
  await expect(facts).toContainText('响应完成')
  await expect(facts).toContainText('req-stream')
  await expect(facts).toContainText('上游权威')

  await prompt.fill('触发错误')
  await page.getByRole('button', { name: '运行' }).click()
  await expect(page.getByRole('alert')).toContainText('上游当前繁忙')
  await expect(page.getByRole('alert')).toContainText('provider_busy')

  await prompt.fill('等待取消')
  await page.getByRole('button', { name: '运行' }).click()
  await page.getByRole('button', { name: '取消' }).click()
  await expect(facts).toContainText('请求已取消')
  await expectPageWidthToFit(page)
})

async function resetApi(
  request: APIRequestContext,
  options: { authenticated: boolean; setupRequired?: boolean },
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
