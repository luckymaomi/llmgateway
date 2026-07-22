import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { server } from '@/test/server'

import { accessApi } from './access'
import { authApi } from './auth'
import { streamGatewayKeyTest } from './gateway-key-test'
import type { CreatedGatewayKey, GatewayKey, GatewayKeyTestEvent, Session } from './types'

const session: Session = {
  userId: 'user-admin',
  displayName: 'Admin',
  role: 'administrator',
  capabilities: ['access:read', 'access:write', 'gateway-key:test'],
  csrfToken: 'csrf-session-token',
  expiresAt: '2026-07-20T00:00:00.000Z',
}

describe('same-origin control API', () => {
  it('adopts the session CSRF fact for authenticated writes', async () => {
    let receivedBody: unknown
    let receivedCsrf = ''
    let receivedIdempotencyKey = ''
    const created: CreatedGatewayKey = {
      key: gatewayKey,
      secret: 'lgw_live_once',
    }
    server.use(
      http.get('http://llmgateway.test/api/control/session', () =>
        HttpResponse.json({ data: session }),
      ),
      http.post('http://llmgateway.test/api/control/keys', async ({ request }) => {
        receivedCsrf = request.headers.get('x-csrf-token') ?? ''
        receivedIdempotencyKey = request.headers.get('idempotency-key') ?? ''
        receivedBody = await request.json()
        return HttpResponse.json({ data: created })
      }),
    )

    await authApi.session()
    const result = await accessApi.createKey(
      {
        ownerId: 'user-admin',
        name: 'Automation',
        authorizedModelIds: ['11111111-1111-4111-8111-111111111111'],
      },
      '22222222-2222-4222-8222-222222222222',
    )

    expect(receivedCsrf).toBe('csrf-session-token')
    expect(receivedIdempotencyKey).toBe('22222222-2222-4222-8222-222222222222')
    expect(receivedBody).toEqual({
      ownerId: 'user-admin',
      name: 'Automation',
      authorizedModelIds: ['11111111-1111-4111-8111-111111111111'],
    })
    expect(result).toEqual(created)
  })

  it('decodes ordered Gateway Key test facts from an SSE response', async () => {
    const events: GatewayKeyTestEvent[] = [
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
        'http://llmgateway.test/api/control/gateway-key-test/runs',
        () => new HttpResponse(body, { headers: { 'Content-Type': 'text/event-stream' } }),
      ),
    )
    const received: GatewayKeyTestEvent[] = []

    await streamGatewayKeyTest(
      {
        gatewayKeyId: 'key-1',
        model: 'gpt-main',
        message: 'Hello',
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
  authorizedModelIds: ['11111111-1111-4111-8111-111111111111'],
  authorizedModels: ['gpt-main'],
  createdAt: '2026-07-19T00:00:00.000Z',
}
