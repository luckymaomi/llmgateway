import { expect, type Page, type Request } from '@playwright/test'

import { expectPageWidthToFit, isRecord, uuidPattern } from './acceptance-helpers'

const runPath = '/api/control/gateway-key-test/runs'

export async function verifyGatewayKeyRequest(page: Page, keyName: string): Promise<void> {
  const keyRow = page
    .getByRole('table', { name: 'Gateway Key 列表' })
    .getByRole('row')
    .filter({ hasText: keyName })
  await keyRow.getByRole('button', { name: '测试请求' }).click()
  const dialog = page.getByRole('dialog', { name: '测试 Gateway Key' })

  const completedResponse = page.waitForResponse((response) => isGatewayKeyTest(response.request()))
  await dialog.getByRole('button', { name: '开始测试' }).click()
  const completed = await completedResponse
  expect(completed.status()).toBe(200)
  const completedEvents = parseServerSentEvents(await completed.text())
  const completedEvent = completedEvents.find((event) => event.type === 'completed')
  expect(completedEvent?.requestId).toEqual(expect.stringMatching(uuidPattern))
  expect(completedEvents).toEqual(
    expect.arrayContaining([
      expect.objectContaining({ type: 'content', delta: 'fixture stream' }),
      expect.objectContaining({ type: 'usage', inputTokens: 4, outputTokens: 2 }),
    ]),
  )

  await expectPageWidthToFit(page)
  await dialog.getByText('关闭', { exact: true }).click()
}

function parseServerSentEvents(body: string): Record<string, unknown>[] {
  return body
    .split(/\r?\n/)
    .filter((line) => line.startsWith('data: '))
    .map((line) => JSON.parse(line.slice(6)) as unknown)
    .filter(isRecord)
}

function isGatewayKeyTest(request: Request): boolean {
  return request.method() === 'POST' && new URL(request.url()).pathname === runPath
}
