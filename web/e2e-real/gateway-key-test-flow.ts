import { expect, type Page, type Request } from '@playwright/test'

import { expectPageWidthToFit, uuidPattern } from './acceptance-helpers'
import type { BrowserProblems } from './runtime'

const runPath = '/api/control/gateway-key-test/runs'

export async function verifyGatewayKeyRequest(
  page: Page,
  browserProblems: BrowserProblems,
  keyName: string,
): Promise<void> {
  const keyRow = page
    .getByRole('table', { name: 'API Key 列表' })
    .getByRole('row')
    .filter({ hasText: keyName })
  await keyRow.getByRole('button', { name: '测试请求' }).click()
  const dialog = page.getByRole('dialog', { name: '测试 Gateway Key' })
  await expect(dialog.getByLabel('模型')).toHaveValue('browser-chat')

  const completedResponse = page.waitForResponse((response) => isGatewayKeyTest(response.request()))
  await dialog.getByRole('button', { name: '开始测试' }).click()
  expect((await completedResponse).status()).toBe(200)
  await expect(dialog).toContainText('上游回复：fixture stream')
  const requestIDText = (await dialog.getByText(/Request ID：/).textContent()) ?? ''
  expect(requestIDText.replace('Request ID：', '')).toMatch(uuidPattern)
  await expect(dialog.getByRole('button', { name: '重新测试' })).toBeVisible()

  await dialog.getByLabel('测试消息').fill('hold stream then cancel')
  await dialog.getByRole('button', { name: '重新测试' }).click()
  await expect(dialog).toContainText('上游回复：fixture stream')
  const failedRequest = page
    .waitForEvent('requestfailed', {
      predicate: isGatewayKeyTest,
      timeout: 5_000,
    })
    .catch(() => undefined)
  await dialog.getByRole('button', { name: '停止等待' }).click()
  const canceledRequest = await failedRequest
  if (canceledRequest) allowExpectedCancellation(browserProblems, canceledRequest)
  await expect(dialog).toContainText('已停止等待，服务端结果待确认')
  await expectPageWidthToFit(page)
  await dialog.getByText('关闭', { exact: true }).click()
}

function isGatewayKeyTest(request: Request): boolean {
  return request.method() === 'POST' && new URL(request.url()).pathname === runPath
}

function allowExpectedCancellation(browserProblems: BrowserProblems, request: Request): void {
  const errorText = request.failure()?.errorText ?? 'unknown'
  expect(['net::ERR_ABORTED', 'net::ERR_FAILED']).toContain(errorText)
  browserProblems.allowRequestFailure('POST', runPath, errorText)
}
