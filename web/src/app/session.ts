import { queryOptions, useSuspenseQuery, type QueryClient } from '@tanstack/react-query'

import { apiClient, authApi, type Capability, type Session } from '@/api'

export const sessionQuery = queryOptions({
  queryKey: ['session'] as const,
  queryFn: authApi.session,
  retry: false,
  staleTime: 30_000,
})

export function useSession(): Session {
  return useSuspenseQuery(sessionQuery).data
}

export function hasCapability(session: Session, capability: Capability): boolean {
  return session.capabilities.includes(capability)
}

export function establishAuthenticatedSession(queryClient: QueryClient, session: Session): void {
  queryClient.clear()
  apiClient.setCsrfToken(session.csrfToken)
  queryClient.setQueryData(sessionQuery.queryKey, session)
}

export function clearAuthenticatedSession(queryClient: QueryClient): void {
  queryClient.clear()
  apiClient.setCsrfToken('')
}
