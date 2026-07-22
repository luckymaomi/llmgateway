import { apiClient } from './client'
import type { OperationsOverview } from './types'

export const operationsApi = {
  overview: (signal?: AbortSignal) =>
    apiClient.request<OperationsOverview>('/api/control/overview', {
      ...(signal ? { signal } : {}),
    }),
}
