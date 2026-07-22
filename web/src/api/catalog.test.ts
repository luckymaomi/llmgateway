import { http, HttpResponse } from 'msw'
import { describe, expect, it } from 'vitest'

import { server } from '@/test/server'

import { catalogApi } from './catalog'
import type { ProviderRecord } from './types'

describe('Provider control API idempotency', () => {
  it('sends the caller-owned Idempotency-Key for create, update, and status writes', async () => {
    const received: Array<{ path: string; key: string; body: unknown }> = []
    server.use(
      http.post('http://llmgateway.test/api/control/providers', async ({ request }) => {
        received.push(await requestFacts(request))
        return HttpResponse.json({ data: provider }, { status: 201 })
      }),
      http.put('http://llmgateway.test/api/control/providers/:providerID', async ({ request }) => {
        received.push(await requestFacts(request))
        return HttpResponse.json({ data: provider })
      }),
      http.put(
        'http://llmgateway.test/api/control/providers/:providerID/status',
        async ({ request }) => {
          received.push(await requestFacts(request))
          return HttpResponse.json({ data: { ...provider, status: 'enabled' } })
        },
      ),
    )

    await catalogApi.createProvider(
      {
        slug: provider.slug,
        name: provider.name,
        kind: provider.kind,
        baseUrl: provider.baseUrl,
      },
      createKey,
    )
    await catalogApi.updateProvider(
      provider.id,
      {
        name: 'Updated Provider',
        kind: provider.kind,
        baseUrl: provider.baseUrl,
        expectedUpdatedAt: provider.updatedAt,
      },
      updateKey,
    )
    await catalogApi.setProviderEnabled(provider.id, true, provider.updatedAt, statusKey)

    expect(received).toEqual([
      {
        path: '/api/control/providers',
        key: createKey,
        body: {
          slug: provider.slug,
          name: provider.name,
          kind: provider.kind,
          baseUrl: provider.baseUrl,
        },
      },
      {
        path: `/api/control/providers/${provider.id}`,
        key: updateKey,
        body: {
          name: 'Updated Provider',
          kind: provider.kind,
          baseUrl: provider.baseUrl,
          expectedUpdatedAt: provider.updatedAt,
        },
      },
      {
        path: `/api/control/providers/${provider.id}/status`,
        key: statusKey,
        body: { enabled: true, expectedUpdatedAt: provider.updatedAt },
      },
    ])
  })
})

async function requestFacts(
  request: Request,
): Promise<{ path: string; key: string; body: unknown }> {
  const body: unknown = await request.json()
  return {
    path: new URL(request.url).pathname,
    key: request.headers.get('idempotency-key') ?? '',
    body,
  }
}

const createKey = '11111111-1111-4111-8111-111111111111'
const updateKey = '22222222-2222-4222-8222-222222222222'
const statusKey = '33333333-3333-4333-8333-333333333333'

const provider: ProviderRecord = {
  id: 'provider-1',
  slug: 'fixture-provider',
  name: 'Fixture Provider',
  kind: 'openai-compatible',
  baseUrl: 'https://provider.example.test/v1',
  status: 'disabled',
  updatedAt: '2026-07-20T10:00:00.000Z',
}
