import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { server } from '@/test/server'

import { accessApi } from './access'
import { authApi } from './auth'
import { streamPlaygroundRun } from './operations'
import type { CreatedGatewayKey, GatewayKey, PlaygroundEvent, Session } from './types'

const session: Session = {
  userId: 'user-admin',
  displayName: 'Admin',
  role: 'administrator',
  capabilities: ['access:read', 'access:write', 'playground:use'],
  csrfToken: 'csrf-session-token',
  expiresAt: '2026-07-20T00:00:00.000Z',
}

describe('same-origin control API', () => {
  it('adopts the session CSRF fact for authenticated writes', async () => {
    let receivedBody: unknown
    let receivedCsrf = ''
    const created: CreatedGatewayKey = {
      key: gatewayKey,
      secret: 'lgw_live_once',
    }
    server.use(
      http.get('/api/control/session', () => HttpResponse.json({ data: session })),
      http.post('/api/control/keys', async ({ request }) => {
        receivedCsrf = request.headers.get('x-csrf-token') ?? ''
        receivedBody = await request.json()
        return HttpResponse.json({ data: created })
      }),
    )

    await authApi.session()
    const result = await accessApi.createKey({
      ownerId: 'user-admin',
      name: 'Automation',
      authorizedModels: ['gpt-main'],
    })

    expect(receivedCsrf).toBe('csrf-session-token')
    expect(receivedBody).toEqual({
      ownerId: 'user-admin',
      name: 'Automation',
      authorizedModels: ['gpt-main'],
    })
    expect(result).toEqual(created)
  })

  it('preserves typed list filters in the request URL', async () => {
    let receivedQuery = ''
    server.use(
      http.get('/api/control/keys', ({ request }) => {
        receivedQuery = new URL(request.url).search
        return HttpResponse.json({
          data: { items: [gatewayKey], page: 2, pageSize: 20, total: 21 },
        })
      }),
    )

    const result = await accessApi.keys({
      page: 2,
      pageSize: 20,
      search: 'automation',
      status: 'active',
    })

    expect(Object.fromEntries(new URLSearchParams(receivedQuery))).toEqual({
      page: '2',
      pageSize: '20',
      search: 'automation',
      status: 'active',
    })
    expect(result.items).toEqual([gatewayKey])
  })

  it('decodes ordered Playground facts from an SSE response', async () => {
    const events: PlaygroundEvent[] = [
      {
        type: 'phase',
        phase: 'streaming',
        step: 'Receiving upstream response',
        requestId: 'req-stream-1',
      },
      { type: 'content', delta: 'Hello' },
      { type: 'usage', inputTokens: 8, outputTokens: 3, source: 'authoritative' },
      { type: 'completed', requestId: 'req-stream-1' },
    ]
    const body = events.map((event) => `data: ${JSON.stringify(event)}\n\n`).join('')
    server.use(
      http.post(
        '/api/control/playground/runs',
        () => new HttpResponse(body, { headers: { 'Content-Type': 'text/event-stream' } }),
      ),
    )
    const received: PlaygroundEvent[] = []

    await streamPlaygroundRun(
      {
        model: 'gpt-main',
        stream: true,
        messages: [{ role: 'user', content: 'Hello' }],
      },
      new AbortController().signal,
      (event) => received.push(event),
    )

    expect(received).toEqual(events)
  })
})

const gatewayKey: GatewayKey = {
  id: 'key-1',
  ownerId: 'user-admin',
  ownerName: 'Admin',
  name: 'Automation',
  prefix: 'lgw_live',
  status: 'active',
  authorizedModels: ['gpt-main'],
  createdAt: '2026-07-19T00:00:00.000Z',
}
