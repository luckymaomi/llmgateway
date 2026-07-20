export type Role = 'administrator' | 'operator' | 'member'

export type Capability =
  | 'overview:read'
  | 'providers:read'
  | 'providers:write'
  | 'credentials:read'
  | 'credentials:write'
  | 'access:read'
  | 'access:write'
  | 'ledger:read'
  | 'ledger:write'
  | 'operations:read'
  | 'audit:read'
  | 'content:read'
  | 'playground:use'
  | 'settings:read'
  | 'settings:write'
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

export interface MetricPoint {
  timestamp: string
  requests: number
  inputTokens: number
  outputTokens: number
  errorRate: number
}

export interface OverviewAlert {
  id: string
  severity: 'info' | 'warning' | 'critical'
  title: string
  summary: string
  occurredAt: string
  requestId?: string
}

export interface PoolCapacity {
  resourceDomain: ResourceDomain
  readyCredentials: number
  busyCredentials: number
  coolingCredentials: number
  queuedRequests: number
}

export interface Overview {
  health: 'healthy' | 'degraded' | 'unavailable'
  requests24h: number
  successRate: number
  p95LatencyMs: number
  queuedRequests: number
  series: MetricPoint[]
  pools: PoolCapacity[]
  alerts: OverviewAlert[]
}

export interface ProviderRecord {
  id: string
  slug: string
  name: string
  kind: 'openai-compatible' | 'zhipu' | 'deepseek' | 'agnes'
  baseUrl: string
  status: 'enabled' | 'disabled'
  verifiedAt?: string
  updatedAt: string
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
  contextTokens?: number
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
}

export interface Credential {
  id: string
  providerId: string
  providerName: string
  label: string
  maskedSecret: string
  resourceDomain: ResourceDomain
  status: EntityStatus
  authorizedModels: string[]
  rpmLimit?: number
  tpmLimit?: number
  concurrencyLimit?: number
  fixedProxy?: string
  cooldownUntil?: string
  lastCheckedAt?: string
  recentSuccessRate?: number
}

export interface CredentialInput {
  providerId: string
  label: string
  secret: string
  resourceDomain: ResourceDomain
  authorizedModels: string[]
  rpmLimit?: number
  tpmLimit?: number
  concurrencyLimit?: number
  fixedProxy?: string
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
  role: Role
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
  modelAliases: string[]
  tokenLimit?: number
  usedTokens: number
  rpmLimit?: number
  tpmLimit?: number
  concurrencyLimit: number
  startsAt: string
  expiresAt: string
  status: 'scheduled' | 'active' | 'expired' | 'revoked'
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
  modelAliases: string[]
  tokenLimit?: number
  rpmLimit?: number
  tpmLimit?: number
  concurrencyLimit: number
  startsAt: string
  expiresAt: string
}

export type RequestState =
  | 'queued'
  | 'admitted'
  | 'dispatching'
  | 'streaming'
  | 'completed'
  | 'failed'
  | 'canceled'
  | 'uncertain'

export interface ProviderAttempt {
  id: string
  sequence: number
  providerName: string
  credentialLabel: string
  state: 'selected' | 'sending' | 'committed' | 'succeeded' | 'failed' | 'uncertain'
  exclusionReasons: string[]
  errorCode?: string
  latencyMs?: number
  inputTokens?: number
  outputTokens?: number
  startedAt: string
  completedAt?: string
}

export interface GatewayRequest {
  id: string
  requestId: string
  createdAt: string
  completedAt?: string
  userName: string
  keyPrefix: string
  modelAlias: string
  resourceDomain: ResourceDomain
  state: RequestState
  statusCode?: number
  latencyMs?: number
  ttftMs?: number
  inputTokens?: number
  outputTokens?: number
  errorCode?: string
  errorMessage?: string
  configurationRevisionId: string
  attempts?: ProviderAttempt[]
}

export interface AuditEvent {
  id: string
  occurredAt: string
  actorName: string
  action: string
  objectType: string
  objectLabel: string
  summary: string
  requestId: string
}

export interface ContentRecord {
  id: string
  requestId: string
  ownerName: string
  modelAlias: string
  capturedAt: string
  expiresAt: string
  status: 'retained' | 'deletion_scheduled' | 'deleted'
  accessReasonRequired: boolean
  content?: {
    request: unknown
    response: unknown
  }
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

export interface SettingsDocument {
  section: 'security' | 'network' | 'observability' | 'backups' | 'revisions'
  revisionId: string
  values: Record<string, string | number | boolean>
  updatedAt: string
  updatedBy: string
}
