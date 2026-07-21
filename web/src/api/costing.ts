import { apiClient, listQuery } from './client'
import type { CostSummary, ListQuery, ModelPriceInput, ModelPriceVersion, Page } from './types'

const base = '/api/control'

export const costingApi = {
  prices: (query: ListQuery & { modelId?: string }, signal?: AbortSignal) =>
    apiClient.request<Page<ModelPriceVersion>>(`${base}/model-prices`, {
      query: { ...listQuery(query), modelId: query.modelId },
      ...(signal ? { signal } : {}),
    }),
  createPrice: (input: ModelPriceInput, idempotencyKey: string) =>
    apiClient.request<ModelPriceVersion, ModelPriceInput>(`${base}/model-prices`, {
      method: 'POST',
      body: input,
      headers: { 'Idempotency-Key': idempotencyKey },
    }),
  summaries: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<CostSummary>>(`${base}/costs`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
}
