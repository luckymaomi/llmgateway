import { queryOptions, useSuspenseQuery } from '@tanstack/react-query'

import { authApi, type Capability, type Session } from '@/api'

export const sessionQuery = queryOptions({
  queryKey: ['session'],
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
