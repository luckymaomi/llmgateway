import { QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import type { Provider, Session } from '@/api'
import { createQueryClient } from '@/app/query-client'
import { createAppRouter } from '@/app/router'
import { establishAuthenticatedSession } from '@/app/session'
import { server } from '@/test/server'

interface Attempt {
  key: string
  body: unknown
}

describe('Provider status idempotent recovery', () => {
  it('does not start another status operation and explicitly retries the original key', async () => {
    const user = userEvent.setup()
    const attempts: Attempt[] = []
    let currentProvider = disabledProvider
    server.use(
      http.get('/api/control/providers', () =>
        HttpResponse.json({
          data: { items: [currentProvider], page: 1, pageSize: 20, total: 1 },
        }),
      ),
      http.put('/api/control/providers/:providerID/status', async ({ request }) => {
        attempts.push({
          key: request.headers.get('idempotency-key') ?? '',
          body: await request.json(),
        })
        if (attempts.length === 1) {
          currentProvider = enabledProvider
          return outcomeUnknownResponse()
        }
        return HttpResponse.json({ data: enabledProvider })
      }),
    )
    const queryClient = createQueryClient()
    establishAuthenticatedSession(queryClient, administratorSession)
    const router = createAppRouter(queryClient, ['/providers/providers'])
    render(
      <QueryClientProvider client={queryClient}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    )

    await screen.findByRole('heading', { name: 'Provider 与模型' })
    const enableButtons = await screen.findAllByRole('button', { name: '启用 Provider' })
    await user.click(enableButtons[0]!)

    const retry = await screen.findByRole('button', { name: '重试原操作' })
    expect(attempts).toHaveLength(1)
    expect(attempts[0]?.key).toMatch(uuidPattern)
    await waitFor(() =>
      expect(screen.getAllByRole('button', { name: '停用 Provider' })[0]).toBeDisabled(),
    )

    await user.click(retry)

    await waitFor(() => expect(screen.queryByRole('button', { name: '重试原操作' })).toBeNull())
    expect(attempts).toHaveLength(2)
    expect(attempts[1]).toEqual(attempts[0])
    expect(attempts[0]?.body).toEqual({
      enabled: true,
      expectedUpdatedAt: disabledProvider.updatedAt,
    })
    expect(
      await within(screen.getByRole('table', { name: 'Provider 列表' })).findByText('已启用'),
    ).toBeVisible()
  })
})

function outcomeUnknownResponse() {
  return HttpResponse.json(
    {
      error: {
        status: 503,
        code: 'operation_outcome_unknown',
        message: 'The Provider operation may have committed.',
        retryable: true,
        requestId: 'request-status-outcome-unknown',
      },
    },
    { status: 503 },
  )
}

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/

const disabledProvider: Provider = {
  id: 'provider-1',
  slug: 'fixture-provider',
  name: 'Fixture Provider',
  kind: 'openai-compatible',
  baseUrl: 'https://provider.example.test/v1',
  status: 'disabled',
  modelCount: 0,
  credentialCount: 0,
  updatedAt: '2026-07-20T10:00:00.000Z',
}

const enabledProvider: Provider = {
  ...disabledProvider,
  status: 'enabled',
  updatedAt: '2026-07-20T10:01:00.000Z',
}

const administratorSession: Session = {
  userId: 'administrator-id',
  displayName: 'Administrator',
  role: 'administrator',
  capabilities: ['providers:read', 'providers:write'],
  csrfToken: 'administrator-csrf',
  expiresAt: '2026-07-21T00:00:00Z',
}
