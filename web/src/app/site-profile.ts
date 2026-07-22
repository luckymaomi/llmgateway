import { queryOptions } from '@tanstack/react-query'

import { siteProfileApi } from '@/api'

export const siteProfileQuery = queryOptions({
  queryKey: ['site-profile'],
  queryFn: ({ signal }) => siteProfileApi.get(signal),
  staleTime: 60_000,
})
