import { apiClient, listQuery } from './client'
import type {
  Entitlement,
  EntitlementInput,
  LedgerEntry,
  ListQuery,
  Page,
  UsageRecord,
} from './types'

const base = '/api/control'

export const ledgerApi = {
  usage: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<UsageRecord>>(`${base}/usage`, {
      query: listQuery(query),
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
