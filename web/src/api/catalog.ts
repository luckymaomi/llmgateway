import { apiClient } from './client'
import type {
  Credential,
  CredentialBatchInput,
  CredentialBatchResult,
  CredentialProbeResult,
  CredentialStatus,
  CredentialUpdateInput,
  Model,
  Provider,
  ResourcePool,
  ResourcePoolInput,
  ResourcePoolStatus,
} from './types'

const base = '/api/control'
const mutationHeaders = (idempotencyKey: string) => ({ 'Idempotency-Key': idempotencyKey })

export const catalogApi = {
  providers: (signal?: AbortSignal) =>
    apiClient
      .request<ProviderWire[]>(`${base}/providers`, { ...(signal ? { signal } : {}) })
      .then((items) => items.map(mapProvider)),
  models: (signal?: AbortSignal) =>
    apiClient
      .request<ModelWire[]>(`${base}/models`, { ...(signal ? { signal } : {}) })
      .then((items) => items.map(mapModel)),
  resourcePools: (includeRetired = false, signal?: AbortSignal) =>
    apiClient
      .request<ResourcePoolWire[]>(`${base}/resource-pools`, {
        query: { includeRetired },
        ...(signal ? { signal } : {}),
      })
      .then((items) => items.map(mapResourcePool)),
  createResourcePool: (input: ResourcePoolInput, idempotencyKey: string) =>
    apiClient
      .request<ResourcePoolWire>(`${base}/resource-pools`, {
        method: 'POST',
        body: input,
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapResourcePool),
  updateResourcePool: (
    id: string,
    input: { name: string; expectedUpdatedAt: string },
    idempotencyKey: string,
  ) =>
    apiClient
      .request<ResourcePoolWire>(`${base}/resource-pools/${encodeURIComponent(id)}`, {
        method: 'PUT',
        body: input,
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapResourcePool),
  setResourcePoolStatus: (
    id: string,
    status: ResourcePoolStatus,
    expectedUpdatedAt: string,
    idempotencyKey: string,
  ) =>
    apiClient
      .request<ResourcePoolWire>(`${base}/resource-pools/${encodeURIComponent(id)}/status`, {
        method: 'PUT',
        body: { status, expectedUpdatedAt },
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapResourcePool),
  credentials: (includeRetired = false, signal?: AbortSignal) =>
    apiClient
      .request<CredentialWire[]>(`${base}/credentials`, {
        query: { includeRetired },
        ...(signal ? { signal } : {}),
      })
      .then((items) => items.map(mapCredential)),
  updateCredential: (id: string, input: CredentialUpdateInput, idempotencyKey: string) =>
    apiClient
      .request<CredentialWire>(`${base}/credentials/${encodeURIComponent(id)}`, {
        method: 'PUT',
        body: credentialBody(input),
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapCredential),
  importCredentials: (input: CredentialBatchInput, idempotencyKey: string) =>
    apiClient
      .request<CredentialBatchResultWire[]>(`${base}/credentials/batch`, {
        method: 'POST',
        body: {
          ...input,
          modelBindings: input.modelBindings.map(bindingBody),
        },
        headers: mutationHeaders(idempotencyKey),
      })
      .then((items) => items.map(mapBatchResult)),
  setCredentialStatus: (
    id: string,
    status: CredentialStatus,
    expectedUpdatedAt: string,
    idempotencyKey: string,
  ) =>
    apiClient
      .request<CredentialWire>(`${base}/credentials/${encodeURIComponent(id)}/status`, {
        method: 'PUT',
        body: { status, expectedUpdatedAt },
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapCredential),
  retireCredential: (id: string, expectedUpdatedAt: string, idempotencyKey: string) =>
    apiClient
      .request<CredentialWire>(`${base}/credentials/${encodeURIComponent(id)}`, {
        method: 'DELETE',
        query: { expectedUpdatedAt },
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapCredential),
  probeCredential: (id: string, modelId: string, signal?: AbortSignal) =>
    apiClient
      .request<CredentialProbeWire>(`${base}/credentials/${encodeURIComponent(id)}/probe`, {
        method: 'POST',
        body: { modelId },
        ...(signal ? { signal } : {}),
      })
      .then(mapProbeResult),
}

interface ModelWire {
  id: string
  provider_id: string
  provider_slug?: string
  provider_name?: string
  public_name: string
  upstream_name: string
  display_name: string
  capabilities: {
    chat: boolean
    streaming: boolean
    tools: boolean
    reasoning: boolean
    reasoning_mode?: 'toggle' | 'effort' | 'hybrid'
    structured_output: boolean
    context_tokens: number
    output_tokens: number
  }
  created_at: string
  updated_at: string
}

interface ProviderWire {
  id: string
  catalog_id: string
  slug: string
  name: string
  kind: string
  base_url: string
  source_url: string
  verified_at: string
  contract: Provider['contract']
  resource_pool_count: number
  active_credential_count: number
  created_at: string
  updated_at: string
}

interface ResourcePoolWire {
  id: string
  provider_id: string
  provider_catalog_id: string
  provider_slug: string
  provider_name: string
  provider_kind: string
  provider_base_url: string
  slug: string
  name: string
  status: ResourcePoolStatus
  models: ModelWire[]
  model_count: number
  credential_count: number
  active_credential_count: number
  retired_at?: string
  created_at: string
  updated_at: string
}

interface CredentialWire {
  id: string
  resource_pool_id: string
  resource_pool_name: string
  resource_pool_slug: string
  provider_id: string
  provider_name: string
  provider_kind: string
  provider_base_url: string
  name: string
  status: CredentialStatus
  rpm_limit?: number
  tpm_limit?: number
  concurrency_limit?: number
  cooldown_until?: string
  consecutive_failures: number
  last_success_at?: string
  last_error_kind?: string
  last_probe_at?: string
  last_probe_latency_ms?: number
  last_probe_kind?: string
  last_probe_status?: string
  last_probe_error_kind?: string
  last_checked_at?: string
  recent_success_rate?: number
  first_byte_p95_ms?: number
  total_latency_p95_ms?: number
  retired_at?: string
  created_at: string
  updated_at: string
  model_bindings: Array<{
    model_id: string
    model_name?: string
    priority: number
    weight: number
  }>
}

interface CredentialBatchResultWire {
  line: number
  name: string
  status: 'created' | 'skipped' | 'rejected'
  credential?: CredentialWire
  error_kind?: string
}

interface CredentialProbeWire {
  credential: CredentialWire
  execution: {
    kind: string
    status: CredentialProbeResult['status']
    error_kind?: string
    retryable: boolean
    may_use_tokens: boolean
    latency_ms: number
    model_id: string
    model_name: string
    input_tokens?: number
    output_tokens?: number
    request_id: string
  }
}

function mapModel(model: ModelWire): Model {
  return {
    id: model.id,
    providerId: model.provider_id,
    providerSlug: model.provider_slug ?? '',
    providerName: model.provider_name ?? '',
    publicName: model.public_name,
    upstreamName: model.upstream_name,
    displayName: model.display_name,
    capabilities: {
      chat: model.capabilities.chat,
      streaming: model.capabilities.streaming,
      tools: model.capabilities.tools,
      reasoning: model.capabilities.reasoning,
      ...(model.capabilities.reasoning_mode
        ? { reasoningMode: model.capabilities.reasoning_mode }
        : {}),
      structuredOutput: model.capabilities.structured_output,
      contextTokens: model.capabilities.context_tokens,
      outputTokens: model.capabilities.output_tokens,
    },
    createdAt: model.created_at,
    updatedAt: model.updated_at,
  }
}

function mapProvider(provider: ProviderWire): Provider {
  return {
    id: provider.id,
    catalogId: provider.catalog_id,
    slug: provider.slug,
    name: provider.name,
    kind: provider.kind,
    baseUrl: provider.base_url,
    sourceUrl: provider.source_url,
    verifiedAt: provider.verified_at,
    contract: provider.contract,
    resourcePoolCount: provider.resource_pool_count,
    activeCredentialCount: provider.active_credential_count,
    createdAt: provider.created_at,
    updatedAt: provider.updated_at,
  }
}

function mapResourcePool(pool: ResourcePoolWire): ResourcePool {
  return {
    id: pool.id,
    providerId: pool.provider_id,
    providerCatalogId: pool.provider_catalog_id,
    providerSlug: pool.provider_slug,
    providerName: pool.provider_name,
    providerKind: pool.provider_kind,
    providerBaseUrl: pool.provider_base_url,
    slug: pool.slug,
    name: pool.name,
    status: pool.status,
    models: pool.models.map(mapModel),
    modelCount: pool.model_count,
    credentialCount: pool.credential_count,
    activeCredentialCount: pool.active_credential_count,
    ...(pool.retired_at ? { retiredAt: pool.retired_at } : {}),
    createdAt: pool.created_at,
    updatedAt: pool.updated_at,
  }
}

function mapCredential(credential: CredentialWire): Credential {
  return {
    id: credential.id,
    resourcePoolId: credential.resource_pool_id,
    resourcePoolName: credential.resource_pool_name,
    resourcePoolSlug: credential.resource_pool_slug,
    providerId: credential.provider_id,
    providerName: credential.provider_name,
    providerKind: credential.provider_kind,
    providerBaseUrl: credential.provider_base_url,
    name: credential.name,
    status: credential.status,
    ...(credential.rpm_limit !== undefined ? { rpmLimit: credential.rpm_limit } : {}),
    ...(credential.tpm_limit !== undefined ? { tpmLimit: credential.tpm_limit } : {}),
    ...(credential.concurrency_limit !== undefined
      ? { concurrencyLimit: credential.concurrency_limit }
      : {}),
    ...(credential.cooldown_until ? { cooldownUntil: credential.cooldown_until } : {}),
    consecutiveFailures: credential.consecutive_failures,
    ...(credential.last_success_at ? { lastSuccessAt: credential.last_success_at } : {}),
    ...(credential.last_error_kind ? { lastErrorKind: credential.last_error_kind } : {}),
    ...(credential.last_probe_at ? { lastProbeAt: credential.last_probe_at } : {}),
    ...(credential.last_probe_latency_ms !== undefined
      ? { lastProbeLatencyMs: credential.last_probe_latency_ms }
      : {}),
    ...(credential.last_probe_kind ? { lastProbeKind: credential.last_probe_kind } : {}),
    ...(credential.last_probe_status ? { lastProbeStatus: credential.last_probe_status } : {}),
    ...(credential.last_probe_error_kind
      ? { lastProbeErrorKind: credential.last_probe_error_kind }
      : {}),
    ...(credential.last_checked_at ? { lastCheckedAt: credential.last_checked_at } : {}),
    ...(credential.recent_success_rate !== undefined
      ? { recentSuccessRate: credential.recent_success_rate }
      : {}),
    ...(credential.first_byte_p95_ms !== undefined
      ? { firstByteP95Ms: credential.first_byte_p95_ms }
      : {}),
    ...(credential.total_latency_p95_ms !== undefined
      ? { totalLatencyP95Ms: credential.total_latency_p95_ms }
      : {}),
    ...(credential.retired_at ? { retiredAt: credential.retired_at } : {}),
    createdAt: credential.created_at,
    updatedAt: credential.updated_at,
    modelBindings: credential.model_bindings.map((binding) => ({
      modelId: binding.model_id,
      modelName: binding.model_name ?? binding.model_id,
      priority: binding.priority,
      weight: binding.weight,
    })),
  }
}

function bindingBody(binding: Omit<Credential['modelBindings'][number], 'modelName'>) {
  return { model_id: binding.modelId, priority: binding.priority, weight: binding.weight }
}

function credentialBody(input: CredentialUpdateInput) {
  return { ...input, modelBindings: input.modelBindings.map(bindingBody) }
}

function mapBatchResult(result: CredentialBatchResultWire): CredentialBatchResult {
  return {
    line: result.line,
    name: result.name,
    status: result.status,
    ...(result.credential ? { credential: mapCredential(result.credential) } : {}),
    ...(result.error_kind ? { errorKind: result.error_kind } : {}),
  }
}

function mapProbeResult(result: CredentialProbeWire): CredentialProbeResult {
  const execution = result.execution
  return {
    credential: mapCredential(result.credential),
    kind: execution.kind,
    status: execution.status,
    ...(execution.error_kind ? { errorKind: execution.error_kind } : {}),
    retryable: execution.retryable,
    mayUseTokens: execution.may_use_tokens,
    latencyMillis: execution.latency_ms,
    modelId: execution.model_id,
    modelName: execution.model_name,
    ...(execution.input_tokens !== undefined ? { inputTokens: execution.input_tokens } : {}),
    ...(execution.output_tokens !== undefined ? { outputTokens: execution.output_tokens } : {}),
    requestId: execution.request_id,
  }
}
