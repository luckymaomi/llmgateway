import { devices, expect, type Browser, type Page, type Request } from '@playwright/test'

import { expectPageWidthToFit, uuidPattern } from './acceptance-helpers'
import type { BrowserProblems } from './runtime'

const runPath = '/api/control/playground/runs'

export async function verifyRealPlayground(
  page: Page,
  browser: Browser,
  browserProblems: BrowserProblems,
): Promise<void> {
  await page.goto('/playground')
  await expect(page.getByRole('heading', { name: 'Playground' })).toBeVisible()
  await expect(page.getByLabel('模型')).toHaveValue('browser-chat')

  await runCompletedStream(page, '桌面真实流式请求')

  await page.getByLabel('消息').fill('hold stream then cancel')
  await page.getByRole('button', { name: '运行', exact: true }).click()
  await expect(page.getByRole('article').last()).toContainText('fixture stream')
  const failedRequest = page
    .waitForEvent('requestfailed', {
      predicate: isPlaygroundRun,
      timeout: 5_000,
    })
    .catch(() => undefined)
  await page.getByRole('button', { name: '取消', exact: true }).click()
  const canceledRequest = await failedRequest
  if (canceledRequest) allowExpectedCancellation(browserProblems, canceledRequest)
  const desktopFacts = page.getByRole('complementary', { name: '运行事实' })
  await expect(desktopFacts).toContainText('客户端已停止等待，服务端结果待确认')
  await expect(desktopFacts.locator('code')).toHaveText(uuidPattern)
  await expectPageWidthToFit(page)

  const mobileContext = await browser.newContext({
    ...devices['Pixel 7'],
    baseURL: new URL(page.url()).origin,
    storageState: await page.context().storageState(),
  })
  try {
    const mobilePage = await mobileContext.newPage()
    browserProblems.observe(mobilePage)
    await mobilePage.goto('/playground')
    await expect(mobilePage.getByRole('heading', { name: 'Playground' })).toBeVisible()
    await runCompletedStream(mobilePage, '移动端真实流式请求', false)
    await mobilePage.getByRole('button', { name: '运行事实', exact: true }).click()
    const mobileFacts = mobilePage.getByRole('complementary', { name: '运行事实' })
    await expect(mobileFacts).toContainText('响应完成')
    await expect(mobileFacts.locator('code')).toHaveText(uuidPattern)
    await expectPageWidthToFit(mobilePage)
  } finally {
    await mobileContext.close()
  }
}

async function runCompletedStream(
  page: Page,
  prompt: string,
  assertVisibleFacts = true,
): Promise<void> {
  await page.getByLabel('消息').fill(prompt)
  const responsePromise = page.waitForResponse((response) => isPlaygroundRun(response.request()))
  await page.getByRole('button', { name: '运行', exact: true }).click()
  const response = await responsePromise
  expect(response.status()).toBe(200)
  try {
    await expect(page.getByRole('article').last()).toContainText('fixture stream')
  } catch (error) {
    throw new Error(`real Playground stream did not produce content: ${await response.text()}`, {
      cause: error,
    })
  }
  if (assertVisibleFacts) {
    const facts = page.getByRole('complementary', { name: '运行事实' })
    await expect(facts).toContainText('响应完成')
    await expect(facts.locator('code')).toHaveText(uuidPattern)
  }
}

function isPlaygroundRun(request: Request): boolean {
  return request.method() === 'POST' && new URL(request.url()).pathname === runPath
}

function allowExpectedCancellation(browserProblems: BrowserProblems, request: Request): void {
  const errorText = request.failure()?.errorText ?? 'unknown'
  expect(['net::ERR_ABORTED', 'net::ERR_FAILED']).toContain(errorText)
  browserProblems.allowRequestFailure('POST', runPath, errorText)
}
