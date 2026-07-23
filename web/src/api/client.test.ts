import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { server } from '@/test/server'

import { streamGatewayKeyTest } from './gateway-key-test'
import type { GatewayKeyTestEvent } from './types'

describe('same-origin control API', () => {
  it('decodes ordered API key test facts from an SSE response', async () => {
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
        'http://llmgateway.test/api/control/api-key-test/runs',
        () => new HttpResponse(body, { headers: { 'Content-Type': 'text/event-stream' } }),
      ),
    )
    const received: GatewayKeyTestEvent[] = []

    await streamGatewayKeyTest(
      {
        apiKeyId: 'key-1',
        model: 'gpt-main',
        message: 'Hello',
      },
      new AbortController().signal,
      (event) => received.push(event),
    )

    expect(received).toEqual(events)
  })
})
