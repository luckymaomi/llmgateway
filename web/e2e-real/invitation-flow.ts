import { expect, type Page } from '@playwright/test'

import {
  clearClipboard,
  clearClipboardBestEffort,
  dataRecord,
  isRecord,
  uuidPattern,
} from './acceptance-helpers'
import type { BrowserProblems, GatewayRuntime } from './runtime'

export interface InvitationFacts {
  id: string
  prefix: string
  code: string
}

export async function createInvitationAfterLostResponse(
  page: Page,
  browserProblems: BrowserProblems,
  gateway: GatewayRuntime,
): Promise<InvitationFacts> {
  const invitationPath = '/api/control/invitations'
  const routePattern = '**' + invitationPath
  let interrupted = false
  let originalKey = ''
  let originalBody = ''
  let committedInvitationID = ''
  let committedInvitationCode = ''
  await page.getByRole('button', { name: '创建邀请' }).click()
  const dialog = page.getByRole('dialog')
  await page.route(routePattern, async (route) => {
    const request = route.request()
    if (interrupted || request.method() !== 'POST') {
      await route.continue()
      return
    }
    interrupted = true
    originalKey = request.headers()['idempotency-key'] ?? ''
    originalBody = request.postData() ?? ''
    const committed = await route.fetch()
    expect(committed.status()).toBe(201)
    const committedData = dataRecord(await committed.json())
    const committedInvitation = isRecord(committedData?.invitation)
      ? committedData.invitation
      : undefined
    committedInvitationID =
      typeof committedInvitation?.id === 'string' ? committedInvitation.id : ''
    committedInvitationCode = typeof committedData?.code === 'string' ? committedData.code : ''
    expect(committedInvitationID).toMatch(uuidPattern)
    expect(/^invite_[A-Za-z0-9_-]{20,}$/.test(committedInvitationCode)).toBe(true)
    await route.abort('failed')
  })
  browserProblems.allowRequestFailure('POST', invitationPath, 'net::ERR_FAILED')
  try {
    const failedRequest = page.waitForEvent(
      'requestfailed',
      (request) =>
        new URL(request.url()).pathname === invitationPath && request.method() === 'POST',
    )
    await dialog.getByRole('button', { name: '创建', exact: true }).click()
    await failedRequest
    expect(originalKey).toMatch(uuidPattern)
    const originalInput = JSON.parse(originalBody) as { expiresAt?: unknown }
    expect(typeof originalInput.expiresAt).toBe('string')
    expect(new Date(String(originalInput.expiresAt)).toISOString()).toBe(originalInput.expiresAt)
    const pendingOperation = await page.evaluate(() => {
      for (let index = 0; index < sessionStorage.length; index += 1) {
        const storageKey = sessionStorage.key(index)
        if (storageKey?.startsWith('llmgateway:pending-invitation:')) {
          return { storageKey, encoded: sessionStorage.getItem(storageKey) }
        }
      }
      return null
    })
    expect(pendingOperation).not.toBeNull()
    expect(pendingOperation?.storageKey.slice('llmgateway:pending-invitation:'.length)).toMatch(
      uuidPattern,
    )
    expect(pendingOperation?.encoded?.includes('invite_')).toBe(false)
    const pendingData = JSON.parse(pendingOperation?.encoded ?? '{}') as {
      idempotencyKey?: unknown
      input?: unknown
    }
    expect(pendingData.idempotencyKey).toBe(originalKey)
    expect(pendingData.input).toEqual(originalInput)
    await gateway.restart()
    await page.reload()
    const recoveryDialog = page.getByRole('dialog')
    const replayResponse = page.waitForResponse(
      (response) =>
        new URL(response.url()).pathname === invitationPath &&
        response.request().method() === 'POST',
    )
    await recoveryDialog.getByRole('button', { name: '确认原操作' }).click()
    const replayed = await replayResponse
    expect(replayed.status()).toBe(201)
    expect(replayed.request().headers()['idempotency-key']).toBe(originalKey)
    expect(replayed.request().postData()).toBe(originalBody)
    const replayedData = dataRecord(await replayed.json())
    const replayedInvitation = isRecord(replayedData?.invitation)
      ? replayedData.invitation
      : undefined
    expect(typeof replayedInvitation?.id === 'string' ? replayedInvitation.id : '').toBe(
      committedInvitationID,
    )
    expect(typeof replayedData?.code === 'string' ? replayedData.code : '').toBe(
      committedInvitationCode,
    )
    const acknowledgement = page.getByRole('dialog')
    expect(
      await page.evaluate(
        (expected) =>
          document.querySelector('[data-testid="created-invitation-code"]')?.textContent?.trim() ===
          expected,
        committedInvitationCode,
      ),
    ).toBe(true)
    await acknowledgement.getByRole('button', { name: '复制邀请码' }).click()
    expect(
      await page.evaluate(async () => {
        const displayed = document
          .querySelector('[data-testid="created-invitation-code"]')
          ?.textContent?.trim()
        return Boolean(displayed) && (await navigator.clipboard.readText()) === displayed
      }),
    ).toBe(true)
    await acknowledgement.getByRole('button', { name: '完成' }).click()
    await expect(page.getByTestId('created-invitation-code')).toHaveCount(0)
    expect(
      await page.evaluate(
        () =>
          Object.keys(sessionStorage).filter((key) =>
            key.startsWith('llmgateway:pending-invitation:'),
          ).length,
      ),
    ).toBe(0)
    const prefix = committedInvitationCode.slice(0, 13)
    expect(
      await page.evaluate(
        (code) => !document.body.innerText.includes(code),
        committedInvitationCode,
      ),
    ).toBe(true)
    await clearClipboard(page)
    return { id: committedInvitationID, prefix, code: committedInvitationCode }
  } finally {
    await clearClipboardBestEffort(page)
    await page.unroute(routePattern)
  }
}
