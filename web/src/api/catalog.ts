import { apiClient, listQuery } from './client'
import type {
  ConfigurationRevision,
  Credential,
  CredentialInput,
  ListQuery,
  Model,
  ModelInput,
  OperationSnapshot,
  Page,
  Provider,
  ProviderCreateInput,
  ProviderRecord,
  ProviderUpdateInput,
} from './types'

const base = '/api/control'
const item = (path: string, id: string) => `${base}/${path}/${encodeURIComponent(id)}`

export const catalogApi = {
  providers: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<Provider>>(`${base}/providers`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createProvider: (input: ProviderCreateInput) =>
    apiClient.request<ProviderRecord, ProviderCreateInput>(`${base}/providers`, {
      method: 'POST',
      body: input,
    }),
  updateProvider: (id: string, input: ProviderUpdateInput) =>
    apiClient.request<ProviderRecord, ProviderUpdateInput>(item('providers', id), {
      method: 'PUT',
      body: input,
    }),
  setProviderEnabled: (id: string, enabled: boolean, expectedUpdatedAt: string) =>
    apiClient.request<ProviderRecord, { enabled: boolean; expectedUpdatedAt: string }>(
      `${item('providers', id)}/status`,
      {
        method: 'PUT',
        body: { enabled, expectedUpdatedAt },
      },
    ),
  models: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<Model>>(`${base}/models`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createModel: (input: ModelInput) =>
    apiClient.request<Model, ModelInput>(`${base}/models`, { method: 'POST', body: input }),
  updateModel: (id: string, input: ModelInput) =>
    apiClient.request<Model, ModelInput>(item('models', id), { method: 'PUT', body: input }),

  revisions: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<ConfigurationRevision>>(`${base}/configuration/revisions`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  validateRevision: (id: string) =>
    apiClient.request<OperationSnapshot>(`${item('configuration/revisions', id)}/validate`, {
      method: 'POST',
    }),
  publishRevision: (id: string, expectedActiveRevisionId: string) =>
    apiClient.request<OperationSnapshot, { expectedActiveRevisionId: string }>(
      `${item('configuration/revisions', id)}/publish`,
      { method: 'POST', body: { expectedActiveRevisionId } },
    ),
  rollbackRevision: (id: string, expectedActiveRevisionId: string) =>
    apiClient.request<OperationSnapshot, { expectedActiveRevisionId: string }>(
      `${item('configuration/revisions', id)}/rollback`,
      { method: 'POST', body: { expectedActiveRevisionId } },
    ),

  credentials: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<Credential>>(`${base}/credentials`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createCredential: (input: CredentialInput) =>
    apiClient.request<Credential, CredentialInput>(`${base}/credentials`, {
      method: 'POST',
      body: input,
    }),
  updateCredential: (id: string, input: CredentialInput) =>
    apiClient.request<Credential, CredentialInput>(item('credentials', id), {
      method: 'PUT',
      body: input,
    }),
  setCredentialEnabled: (id: string, enabled: boolean) =>
    apiClient.request<Credential, { enabled: boolean }>(`${item('credentials', id)}/status`, {
      method: 'PUT',
      body: { enabled },
    }),
  testCredential: (id: string, mode: 'connection' | 'generation') =>
    apiClient.request<OperationSnapshot, { mode: 'connection' | 'generation' }>(
      `${item('credentials', id)}/tests`,
      { method: 'POST', body: { mode } },
    ),
}
