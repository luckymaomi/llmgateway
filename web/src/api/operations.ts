import { createParser } from 'eventsource-parser'

import { ApiProblem, apiClient, listQuery } from './client'
import type {
  AuditEvent,
  ContentRecord,
  GatewayRequest,
  ListQuery,
  OperationSnapshot,
  Page,
  PlaygroundEvent,
  PlaygroundModel,
  PlaygroundRunInput,
} from './types'

const base = '/api/control'

export const operationsApi = {
  requests: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<GatewayRequest>>(`${base}/requests`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  request: (id: string, signal?: AbortSignal) =>
    apiClient.request<GatewayRequest>(`${base}/requests/${encodeURIComponent(id)}`, {
      ...(signal ? { signal } : {}),
    }),
  auditEvents: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<AuditEvent>>(`${base}/audit/events`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  contentRecords: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<ContentRecord>>(`${base}/content-records`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  revealContent: (id: string, reason: string) =>
    apiClient.request<ContentRecord, { reason: string }>(
      `${base}/content-records/${encodeURIComponent(id)}/access`,
      { method: 'POST', body: { reason } },
    ),
  scheduleContentDeletion: (id: string) =>
    apiClient.request<OperationSnapshot>(
      `${base}/content-records/${encodeURIComponent(id)}/deletion`,
      { method: 'POST' },
    ),
  operation: (id: string, signal?: AbortSignal) =>
    apiClient.request<OperationSnapshot>(`${base}/operations/${encodeURIComponent(id)}`, {
      ...(signal ? { signal } : {}),
    }),
  cancelOperation: (id: string) =>
    apiClient.request<OperationSnapshot>(`${base}/operations/${encodeURIComponent(id)}/cancel`, {
      method: 'POST',
    }),
  playgroundModels: (signal?: AbortSignal) =>
    apiClient.request<PlaygroundModel[]>(`${base}/playground/models`, {
      ...(signal ? { signal } : {}),
    }),
}

export async function streamPlaygroundRun(
  input: PlaygroundRunInput,
  signal: AbortSignal,
  onEvent: (event: PlaygroundEvent) => void,
): Promise<void> {
  const response = await apiClient.stream(`${base}/playground/runs`, input, signal)
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
        const parsed = JSON.parse(event.data) as PlaygroundEvent
        onEvent(parsed)
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
