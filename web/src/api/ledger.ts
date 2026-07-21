import { apiClient, listQuery } from './client'
import type {
  Entitlement,
  EntitlementInput,
  LedgerAdjustmentInput,
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
  adjust: (input: LedgerAdjustmentInput) =>
    apiClient.request<LedgerEntry, LedgerAdjustmentInput>(`${base}/ledger/adjustments`, {
      method: 'POST',
      body: input,
      headers: { 'Idempotency-Key': input.idempotencyKey },
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
