import { QueryClient } from '@tanstack/react-query'

import { ApiProblem } from '@/api'

export function createQueryClient(): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        staleTime: 15_000,
        refetchOnWindowFocus: false,
        retry(failureCount, error) {
          return error instanceof ApiProblem && error.retryable && failureCount < 1
        },
      },
      mutations: {
        retry: false,
      },
    },
  })
}
