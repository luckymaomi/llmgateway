import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { Tooltip } from 'radix-ui'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { Button } from '@/components/ui/button'
import { server } from '@/test/server'

import { InvitationForm } from './invitation-form'

const completeCode = 'invite_once_complete_secret'

describe('InvitationForm one-time invitation result', () => {
  afterEach(() => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: undefined,
    })
  })

  it('keeps the complete code only in the acknowledgement dialog and clears it on close', async () => {
    const user = userEvent.setup()
    const writeText = vi.fn().mockResolvedValue(undefined)
    setClipboard(writeText)
    let createRequests = 0
    server.use(
      http.post('/api/control/invitations', async ({ request }) => {
        createRequests += 1
        const input = (await request.json()) as Record<string, unknown>
        expect(input.role).toBe('member')
        expect(typeof input.expiresAt).toBe('string')
        return HttpResponse.json({ data: createdInvitation() }, { status: 201 })
      }),
    )
    const queryClient = renderInvitationForm()

    await user.click(
      within(await screen.findByRole('dialog')).getByRole('button', { name: /^创建$/ }),
    )

    const code = await screen.findByTestId('created-invitation-code')
    expect(code).toHaveTextContent(completeCode)
    expect(screen.getByText('完整邀请码仅在本次创建结果中显示，关闭后无法再次查看。')).toBeVisible()
    expect(createRequests).toBe(1)
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0)
    expect(JSON.stringify(queryClient.getQueriesData({}))).not.toContain(completeCode)

    await user.click(screen.getByRole('button', { name: '复制邀请码' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(completeCode))
    expect(screen.getByRole('button', { name: '已复制' })).toBeVisible()

    await user.click(screen.getByRole('button', { name: '完成' }))
    expect(screen.queryByTestId('created-invitation-code')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: '重新打开邀请表单' }))
    expect(await screen.findByRole('heading', { name: '创建邀请' })).toBeVisible()
    expect(screen.queryByText(completeCode)).not.toBeInTheDocument()
  })

  it('keeps the code available and supports retry after clipboard failure', async () => {
    const user = userEvent.setup()
    const writeText = vi
      .fn()
      .mockRejectedValueOnce(new DOMException('Clipboard denied', 'NotAllowedError'))
      .mockResolvedValueOnce(undefined)
    setClipboard(writeText)
    server.use(
      http.post('/api/control/invitations', () =>
        HttpResponse.json({ data: createdInvitation() }, { status: 201 }),
      ),
    )
    renderInvitationForm()
    await user.click(
      within(await screen.findByRole('dialog')).getByRole('button', { name: /^创建$/ }),
    )
    await screen.findByTestId('created-invitation-code')

    await user.click(screen.getByRole('button', { name: '复制邀请码' }))
    expect(await screen.findByRole('alert')).toHaveTextContent(
      '无法写入剪贴板。请手动选择并复制上方邀请码，然后重试。',
    )
    expect(screen.getByTestId('created-invitation-code')).toHaveTextContent(completeCode)

    await user.click(screen.getByRole('button', { name: '重新复制' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledTimes(2))
    expect(screen.getByRole('button', { name: '已复制' })).toBeVisible()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  })

  it('admits only one create request while the first submission is pending', async () => {
    const user = userEvent.setup()
    let createRequests = 0
    let releaseResponse: (() => void) | undefined
    const responseBarrier = new Promise<void>((resolve) => {
      releaseResponse = resolve
    })
    server.use(
      http.post('/api/control/invitations', async () => {
        createRequests += 1
        await responseBarrier
        return HttpResponse.json({ data: createdInvitation() }, { status: 201 })
      }),
    )
    renderInvitationForm()
    const create = within(await screen.findByRole('dialog')).getByRole('button', {
      name: /^创建$/,
    })

    await user.dblClick(create)
    await waitFor(() => expect(createRequests).toBe(1))
    expect(screen.getByRole('button', { name: '创建中' })).toBeDisabled()
    expect(screen.queryByRole('button', { name: '关闭' })).not.toBeInTheDocument()

    releaseResponse?.()
    expect(await screen.findByTestId('created-invitation-code')).toHaveTextContent(completeCode)
    expect(createRequests).toBe(1)
  })
})

function renderInvitationForm(): QueryClient {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <Tooltip.Provider>
        <InvitationHarness />
      </Tooltip.Provider>
    </QueryClientProvider>,
  )
  return queryClient
}

function InvitationHarness() {
  const [open, setOpen] = useState(true)
  return (
    <>
      {!open ? <Button onClick={() => setOpen(true)}>重新打开邀请表单</Button> : null}
      <InvitationForm open={open} onOpenChange={setOpen} />
    </>
  )
}

function setClipboard(writeText: (text: string) => Promise<void>): void {
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText },
  })
}

function createdInvitation() {
  return {
    id: 'invitation-created',
    codePrefix: 'invite_once_c',
    code: completeCode,
    role: 'member',
    status: 'issued',
    expiresAt: '2026-07-27T00:00:00Z',
    createdBy: 'Administrator',
  }
}
