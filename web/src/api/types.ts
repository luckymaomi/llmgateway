export type Role = 'administrator' | 'member'

export type Capability =
  | 'providers:read'
  | 'providers:write'
  | 'credentials:read'
  | 'credentials:write'
  | 'access:read'
  | 'access:write'
  | 'ledger:read'
  | 'ledger:write'
  | 'playground:use'
  | 'revisions:publish'

export type ResourceDomain = 'free' | 'professional'
export type EntityStatus = 'active' | 'disabled' | 'pending' | 'cooling' | 'unknown'

export interface Session {
  userId: string
  displayName: string
  role: Role
  capabilities: Capability[]
  csrfToken: string
  expiresAt: string
}

export interface Page<T> {
  items: T[]
  page: number
  pageSize: number
  total: number
}

export interface ListQuery {
  page?: number
  pageSize?: number
  search?: string
  status?: string
  sort?: string
  order?: 'asc' | 'desc'
  providerId?: string
  resourceDomain?: ResourceDomain
}

export interface ProviderRecord {
  id: string
  slug: string
  name: string
  kind: string
  baseUrl: string
  status: 'enabled' | 'disabled'
  verifiedAt?: string
  updatedAt: string
}

export interface ProviderKind {
  kind: string
  displayName: string
}

export interface Provider extends ProviderRecord {
  modelCount: number
  credentialCount: number
}

export interface ProviderCreateInput {
  slug: string
  name: string
  kind: Provider['kind']
  baseUrl: string
}

export interface ProviderUpdateInput {
  name: string
  kind: Provider['kind']
  baseUrl: string
  expectedUpdatedAt: string
}

export type ModelCapability = 'streaming' | 'tools' | 'reasoning' | 'structured_output'

export interface Model {
  id: string
  providerId: string
  providerName: string
  alias: string
  upstreamModelId: string
  resourceDomain: ResourceDomain
  capabilities: ModelCapability[]
  contextTokens: number
  status: EntityStatus
  verifiedAt?: string
}

export interface ModelInput {
  providerId: string
  alias: string
  upstreamModelId: string
  resourceDomain: ResourceDomain
  capabilities: ModelCapability[]
  contextTokens?: number
}

export interface ConfigurationRevision {
  id: string
  sequence: number
  status: 'draft' | 'validating' | 'published' | 'superseded' | 'invalid'
  createdBy: string
  createdAt: string
  publishedAt?: string
  summary: string
  validationIssueCount: number
  providerCount: number
  modelCount: number
  credentialCount: number
  routeCount: number
}

export interface ActiveConfigurationModel {
  id: string
  alias: string
  displayName: string
  providerId: string
  providerName: string
  resourceDomain: ResourceDomain
}

export interface ActiveConfiguration {
  revisionId: string | null
  sequence: number
  version: number
  updatedAt: string | null
  models: ActiveConfigurationModel[]
}

export interface Credential {
  id: string
  providerId: string
  providerName: string
  label: string
  maskedSecret: string
  resourceDomain: ResourceDomain
  status: EntityStatus
  authorizedModelIds: string[]
  authorizedModels: string[]
  rpmLimit?: number
  tpmLimit?: number
  concurrencyLimit?: number
  cooldownUntil?: string
  lastCheckedAt?: string
  recentSuccessRate?: number
  lastProbeAt?: string
  lastProbeLatencyMs?: number
  lastProbeKind?: string
  lastProbeStatus?: 'succeeded' | 'failed' | 'unavailable'
  lastProbeErrorKind?: string
  updatedAt: string
}

export interface CredentialInput {
  providerId: string
  label: string
  secret: string
  resourceDomain: ResourceDomain
  authorizedModelIds: string[]
  rpmLimit?: number
  tpmLimit?: number
  concurrencyLimit?: number
}

export interface CredentialUpdateInput extends Omit<CredentialInput, 'providerId' | 'secret'> {
  secret?: string
  expectedUpdatedAt: string
}

export interface CredentialProbeResult {
  credential: Credential
  kind: string
  status: 'succeeded' | 'failed' | 'unavailable'
  errorKind?: string
  retryable: boolean
  mayUseTokens: boolean
  latencyMillis: number
  requestId: string
}

export interface UserAccount {
  id: string
  displayName: string
  email: string
  role: Role
  status: 'pending_review' | 'active' | 'suspended'
  modelCount: number
  keyCount: number
  quotaRemainingTokens?: number
  createdAt: string
  lastActiveAt?: string
}

export interface Invitation {
  id: string
  codePrefix: string
  status: 'issued' | 'claimed' | 'approved' | 'expired' | 'revoked'
  expiresAt: string
  createdBy: string
  claimedBy?: string
}

export interface GatewayKey {
  id: string
  ownerId: string
  ownerName: string
  name: string
  prefix: string
  status: 'active' | 'revoked' | 'expired'
  authorizedModelIds: string[]
  authorizedModels: string[]
  expiresAt?: string
  createdAt: string
  lastUsedAt?: string
}

export interface CreatedGatewayKey {
  key: GatewayKey
  secret: string
}

export interface UsageRecord {
  id: string
  occurredAt: string
  userName: string
  keyPrefix: string
  modelAlias: string
  resourceDomain: ResourceDomain
  inputTokens: number
  outputTokens: number
  usageSource: 'authoritative' | 'estimated'
  requestId: string
}

export interface LedgerEntry {
  id: string
  occurredAt: string
  ownerName: string
  kind: 'grant' | 'reserve' | 'settle' | 'release' | 'adjust' | 'expire' | 'compensate'
  tokenDelta: number
  resourceDomain: ResourceDomain
  reason: string
  requestId?: string
  actorName: string
}

export interface Entitlement {
  id: string
  ownerId: string
  ownerName: string
  planKind: 'token' | 'coding'
  resourceDomain: ResourceDomain
  modelId?: string
  modelAlias?: string
  grantedTokens: number
  balanceTokens: number
  rpmLimit?: number
  tpmLimit?: number
  concurrencyLimit: number
  startsAt: string
  expiresAt: string
  status: 'scheduled' | 'active' | 'expired'
}

export interface LedgerAdjustmentInput {
  ownerId: string
  resourceDomain: ResourceDomain
  tokenDelta: number
  reason: string
  idempotencyKey: string
}

export interface EntitlementInput {
  ownerId: string
  planKind: Entitlement['planKind']
  resourceDomain: ResourceDomain
  modelId?: string
  grantedTokens: number
  rpmLimit?: number
  tpmLimit?: number
  concurrencyLimit: number
  startsAt: string
  expiresAt: string
  reason: string
}

export type OperationPhase =
  | 'submitted'
  | 'validating'
  | 'queued'
  | 'running'
  | 'streaming'
  | 'waiting'
  | 'completed'
  | 'failed'
  | 'canceled'
  | 'uncertain'

export interface OperationSnapshot<TResult = unknown> {
  id: string
  kind: string
  phase: OperationPhase
  step: string
  progress?: number
  requestId: string
  createdAt: string
  updatedAt: string
  canCancel: boolean
  result?: TResult
  error?: ApiProblemShape
}

export interface ApiProblemShape {
  status: number
  code: string
  message: string
  stage?: string
  requestId?: string
  retryable: boolean
  fieldErrors?: Record<string, string>
}

export interface PlaygroundModel {
  id: string
  alias: string
  providerName: string
  capabilities: ModelCapability[]
}

export interface PlaygroundMessage {
  id: string
  role: 'system' | 'user' | 'assistant' | 'tool'
  content: string
  reasoning?: string
  toolCall?: {
    name: string
    arguments: string
  }
}

export interface PlaygroundRunInput {
  gatewayKeyId: string
  model: string
  stream: boolean
  messages: Array<Pick<PlaygroundMessage, 'role' | 'content'>>
  tools?: Array<{ name: string; description: string; parameters: unknown }>
  reasoningEffort?: 'low' | 'medium' | 'high'
}

export type PlaygroundEvent =
  | { type: 'phase'; phase: OperationPhase; step: string; requestId: string }
  | { type: 'reasoning'; delta: string }
  | { type: 'content'; delta: string }
  | { type: 'tool_call'; name: string; argumentsDelta: string }
  | {
      type: 'usage'
      inputTokens: number
      outputTokens: number
      source: 'authoritative' | 'estimated'
    }
  | { type: 'completed'; requestId: string }
  | { type: 'error'; problem: ApiProblemShape }
