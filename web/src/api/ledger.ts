import { apiClient, listQuery } from './client'
import type { LedgerEntry, ListQuery, Page, RequestLog, RequestLogDetail } from './types'

const base = '/api/control'

export const ledgerApi = {
  requestLogs: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<RequestLog>>(`${base}/requests`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  requestLog: (requestId: string, signal?: AbortSignal) =>
    apiClient.request<RequestLogDetail>(`${base}/requests/${encodeURIComponent(requestId)}`, {
      ...(signal ? { signal } : {}),
    }),
  entries: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<LedgerEntry>>(`${base}/ledger/entries`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
}
