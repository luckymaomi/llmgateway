import { createParser } from 'eventsource-parser'

import { ApiProblem, apiClient } from './client'
import type { GatewayKeyTestEvent, GatewayKeyTestInput, GatewayKeyTestModel } from './types'

const base = '/api/control/gateway-key-test'

export const gatewayKeyTestApi = {
  models: (gatewayKeyId: string, signal?: AbortSignal) =>
    apiClient.request<GatewayKeyTestModel[]>(`${base}/models`, {
      query: { gatewayKeyId },
      ...(signal ? { signal } : {}),
    }),
}

export async function streamGatewayKeyTest(
  input: GatewayKeyTestInput,
  signal: AbortSignal,
  onEvent: (event: GatewayKeyTestEvent) => void,
): Promise<void> {
  const response = await apiClient.stream(`${base}/runs`, input, signal, {
    'Idempotency-Key': crypto.randomUUID(),
  })
  if (!response.body) {
    throw new ApiProblem({
      status: 502,
      code: 'stream_unavailable',
      message: '上游没有返回流',
      retryable: true,
    })
  }

  const parser = createParser({
    onEvent(event) {
      try {
        onEvent(JSON.parse(event.data) as GatewayKeyTestEvent)
      } catch {
        throw new ApiProblem({
          status: 502,
          code: 'invalid_stream_event',
          message: '收到无法解析的流事件',
          stage: 'streaming',
          retryable: false,
        })
      }
    },
  })

  const reader = response.body.getReader()
  const decoder = new TextDecoder()
  while (true) {
    const chunk = await reader.read()
    if (chunk.done) break
    parser.feed(decoder.decode(chunk.value, { stream: true }))
  }
  parser.feed(decoder.decode())
}
