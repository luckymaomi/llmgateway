import { apiClient, listQuery } from './client'
import type {
  Entitlement,
  EntitlementInput,
  LedgerEntry,
  ListQuery,
  Page,
  RequestLog,
  RequestLogDetail,
} from './types'

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
  entitlements: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<Entitlement>>(`${base}/entitlements`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createEntitlement: (input: EntitlementInput, idempotencyKey: string) =>
    apiClient.request<Entitlement, EntitlementInput>(`${base}/entitlements`, {
      method: 'POST',
      body: input,
      headers: { 'Idempotency-Key': idempotencyKey },
    }),
}
