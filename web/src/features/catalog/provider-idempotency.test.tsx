import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { Tooltip } from 'radix-ui'
import { describe, expect, it, vi } from 'vitest'

import type { ProviderRecord } from '@/api'
import { server } from '@/test/server'

import { ProviderForm } from './provider-form'

interface Attempt {
  key: string
  body: unknown
}

describe('ProviderForm idempotent recovery', () => {
  it('does not replay automatically and retries the exact update with the same key', async () => {
    const user = userEvent.setup()
    const attempts: Attempt[] = []
    const onOpenChange = vi.fn()
    server.use(
      http.put('/api/control/providers/:providerID', async ({ request }) => {
        attempts.push(await attemptFrom(request))
        if (attempts.length === 1) return HttpResponse.error()
        return HttpResponse.json({
          data: { ...provider, name: 'Uncertain Rename', updatedAt: '2026-07-20T10:01:00.000Z' },
        })
      }),
    )
    renderProviderForm(onOpenChange)
    const dialog = await screen.findByRole('dialog')

    await replaceValue(user, within(dialog).getByLabelText('名称'), 'Uncertain Rename')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    const retry = await within(dialog).findByRole('button', { name: '重试原操作' })
    expect(attempts).toHaveLength(1)
    expect(attempts[0]?.key).toMatch(uuidPattern)
    expect(within(dialog).getByRole('button', { name: '保存修改为新操作' })).toBeDisabled()

    await user.click(retry)

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(attempts).toHaveLength(2)
    expect(attempts[1]).toEqual(attempts[0])
  })

  it('uses a new key only after the operator changes and explicitly saves a new operation', async () => {
    const user = userEvent.setup()
    const attempts: Attempt[] = []
    const onOpenChange = vi.fn()
    server.use(
      http.put('/api/control/providers/:providerID', async ({ request }) => {
        attempts.push(await attemptFrom(request))
        if (attempts.length === 1) return outcomeUnknownResponse()
        return HttpResponse.json({
          data: { ...provider, name: 'Revised Rename', updatedAt: '2026-07-20T10:01:00.000Z' },
        })
      }),
    )
    renderProviderForm(onOpenChange)
    const dialog = await screen.findByRole('dialog')

    await replaceValue(user, within(dialog).getByLabelText('名称'), 'Uncertain Rename')
    await user.click(within(dialog).getByRole('button', { name: '保存' }))

    await within(dialog).findByRole('button', { name: '重试原操作' })
    const saveNew = within(dialog).getByRole('button', { name: '保存修改为新操作' })
    expect(attempts).toHaveLength(1)
    expect(saveNew).toBeDisabled()

    await replaceValue(user, within(dialog).getByLabelText('名称'), 'Revised Rename')
    expect(saveNew).toBeEnabled()
    await user.click(saveNew)

    await waitFor(() => expect(onOpenChange).toHaveBeenCalledWith(false))
    expect(attempts).toHaveLength(2)
    expect(attempts[0]?.key).toMatch(uuidPattern)
    expect(attempts[1]?.key).toMatch(uuidPattern)
    expect(attempts[1]?.key).not.toBe(attempts[0]?.key)
    expect(attempts.map((attempt) => attempt.body)).toEqual([
      {
        name: 'Uncertain Rename',
        kind: provider.kind,
        baseUrl: provider.baseUrl,
        expectedUpdatedAt: provider.updatedAt,
      },
      {
        name: 'Revised Rename',
        kind: provider.kind,
        baseUrl: provider.baseUrl,
        expectedUpdatedAt: provider.updatedAt,
      },
    ])
  })
})

function renderProviderForm(onOpenChange: (open: boolean) => void): void {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={queryClient}>
      <Tooltip.Provider>
        <ProviderForm open provider={provider} onOpenChange={onOpenChange} />
      </Tooltip.Provider>
    </QueryClientProvider>,
  )
}

async function replaceValue(
  user: ReturnType<typeof userEvent.setup>,
  field: HTMLElement,
  value: string,
): Promise<void> {
  await user.clear(field)
  await user.type(field, value)
}

async function attemptFrom(request: Request): Promise<Attempt> {
  return {
    key: request.headers.get('idempotency-key') ?? '',
    body: await request.json(),
  }
}

function outcomeUnknownResponse() {
  return HttpResponse.json(
    {
      error: {
        status: 503,
        code: 'operation_outcome_unknown',
        message: 'The Provider operation may have committed.',
        retryable: true,
        requestId: 'request-outcome-unknown',
      },
    },
    { status: 503 },
  )
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

const provider: ProviderRecord = {
  id: 'provider-1',
  slug: 'fixture-provider',
  name: 'Fixture Provider',
  kind: 'openai-compatible',
  baseUrl: 'https://provider.example.test/v1',
  status: 'disabled',
  updatedAt: '2026-07-20T10:00:00.000Z',
}
