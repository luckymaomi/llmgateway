import { apiClient } from './client'
import type { SiteProfile, SiteProfileInput } from './types'

const path = '/api/control/site-profile'

export const siteProfileApi = {
  get: (signal?: AbortSignal) =>
    apiClient.request<SiteProfile>(path, { ...(signal ? { signal } : {}) }),
  update: (input: SiteProfileInput) =>
    apiClient.request<SiteProfile, SiteProfileInput>(path, { method: 'PUT', body: input }),
}
