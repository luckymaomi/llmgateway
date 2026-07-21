import { createParser } from 'eventsource-parser'

import { ApiProblem, apiClient } from './client'
import type { PlaygroundEvent, PlaygroundModel, PlaygroundRunInput } from './types'

const base = '/api/control/playground'

export const playgroundApi = {
  models: (gatewayKeyId: string, signal?: AbortSignal) =>
    apiClient.request<PlaygroundModel[]>(`${base}/models`, {
      query: { gatewayKeyId },
      ...(signal ? { signal } : {}),
    }),
}

export async function streamPlaygroundRun(
  input: PlaygroundRunInput,
  signal: AbortSignal,
  onEvent: (event: PlaygroundEvent) => void,
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
        onEvent(JSON.parse(event.data) as PlaygroundEvent)
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
