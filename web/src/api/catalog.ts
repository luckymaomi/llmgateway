import { apiClient, listQuery } from './client'
import type {
  ActiveConfiguration,
  ConfigurationRevision,
  Credential,
  CredentialInput,
  CredentialProbeResult,
  CredentialUpdateInput,
  ListQuery,
  Model,
  ModelInput,
  OperationSnapshot,
  Page,
  Provider,
  ProviderCreateInput,
  ProviderKind,
  ProviderPreset,
  ProviderPresetInstallation,
  ProviderRecord,
  ProviderUpdateInput,
} from './types'

const base = '/api/control'
const item = (path: string, id: string) => `${base}/${path}/${encodeURIComponent(id)}`
const mutationHeaders = (idempotencyKey: string) => ({
  'Idempotency-Key': idempotencyKey,
})

export const catalogApi = {
  providerKinds: (signal?: AbortSignal) =>
    apiClient.request<ProviderKind[]>(`${base}/provider-kinds`, {
      ...(signal ? { signal } : {}),
    }),
  providerPresets: (signal?: AbortSignal) =>
    apiClient.request<ProviderPreset[]>(`${base}/provider-presets`, {
      ...(signal ? { signal } : {}),
    }),
  installProviderPreset: (id: string, idempotencyKey: string) =>
    apiClient.request<ProviderPresetInstallation>(
      `${base}/provider-presets/${encodeURIComponent(id)}/install`,
      { method: 'POST', headers: mutationHeaders(idempotencyKey) },
    ),
  providers: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<Provider>>(`${base}/providers`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  provider: (id: string, signal?: AbortSignal) =>
    apiClient.request<ProviderRecord>(item('providers', id), {
      ...(signal ? { signal } : {}),
    }),
  createProvider: (input: ProviderCreateInput, idempotencyKey: string) =>
    apiClient.request<ProviderRecord, ProviderCreateInput>(`${base}/providers`, {
      method: 'POST',
      body: input,
      headers: mutationHeaders(idempotencyKey),
    }),
  updateProvider: (id: string, input: ProviderUpdateInput, idempotencyKey: string) =>
    apiClient.request<ProviderRecord, ProviderUpdateInput>(item('providers', id), {
      method: 'PUT',
      body: input,
      headers: mutationHeaders(idempotencyKey),
    }),
  setProviderEnabled: (
    id: string,
    enabled: boolean,
    expectedUpdatedAt: string,
    idempotencyKey: string,
  ) =>
    apiClient.request<ProviderRecord, { enabled: boolean; expectedUpdatedAt: string }>(
      `${item('providers', id)}/status`,
      {
        method: 'PUT',
        body: { enabled, expectedUpdatedAt },
        headers: mutationHeaders(idempotencyKey),
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
  activeConfiguration: (signal?: AbortSignal) =>
    apiClient.request<ActiveConfiguration>(`${base}/configuration/active`, {
      ...(signal ? { signal } : {}),
    }),
  captureRevision: (idempotencyKey: string) =>
    apiClient.request<ConfigurationRevision>(`${base}/configuration/revisions`, {
      method: 'POST',
      headers: mutationHeaders(idempotencyKey),
    }),
  validateRevision: (id: string) =>
    apiClient.request<OperationSnapshot>(`${item('configuration/revisions', id)}/validate`, {
      method: 'POST',
    }),
  publishRevision: (id: string, expectedActiveVersion: number, idempotencyKey: string) =>
    apiClient.request<OperationSnapshot, { expectedActiveVersion: number }>(
      `${item('configuration/revisions', id)}/publish`,
      {
        method: 'POST',
        body: { expectedActiveVersion },
        headers: mutationHeaders(idempotencyKey),
      },
    ),
  rollbackRevision: (id: string, expectedActiveVersion: number, idempotencyKey: string) =>
    apiClient.request<OperationSnapshot, { expectedActiveVersion: number }>(
      `${item('configuration/revisions', id)}/rollback`,
      {
        method: 'POST',
        body: { expectedActiveVersion },
        headers: mutationHeaders(idempotencyKey),
      },
    ),

  credentials: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<Credential>>(`${base}/credentials`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createCredential: (input: CredentialInput, idempotencyKey: string) =>
    apiClient.request<Credential, CredentialInput>(`${base}/credentials`, {
      method: 'POST',
      body: input,
      headers: mutationHeaders(idempotencyKey),
    }),
  updateCredential: (id: string, input: CredentialUpdateInput, idempotencyKey: string) =>
    apiClient.request<Credential, CredentialUpdateInput>(item('credentials', id), {
      method: 'PUT',
      body: input,
      headers: mutationHeaders(idempotencyKey),
    }),
  setCredentialEnabled: (
    id: string,
    enabled: boolean,
    expectedUpdatedAt: string,
    idempotencyKey: string,
  ) =>
    apiClient.request<Credential, { enabled: boolean; expectedUpdatedAt: string }>(
      `${item('credentials', id)}/status`,
      {
        method: 'PUT',
        body: { enabled, expectedUpdatedAt },
        headers: mutationHeaders(idempotencyKey),
      },
    ),
  probeCredential: (id: string, modelId: string, signal?: AbortSignal) =>
    apiClient.request<CredentialProbeResult, { modelId: string }>(
      `${item('credentials', id)}/probe`,
      {
        method: 'POST',
        body: { modelId },
        ...(signal ? { signal } : {}),
      },
    ),
}
