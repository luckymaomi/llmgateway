import { apiClient, listQuery } from './client'
import type {
  ListQuery,
  Page,
  PlanInput,
  PlanStatus,
  ServicePlan,
  Subscription,
  SubscriptionInput,
  SubscriptionStatus,
  SubscriptionUpdateInput,
} from './types'

const base = '/api/control'
const mutationHeaders = (idempotencyKey: string) => ({ 'Idempotency-Key': idempotencyKey })

export const subscriptionsApi = {
  plans: (includeArchived = false, signal?: AbortSignal) =>
    apiClient
      .request<ServicePlanWire[]>(`${base}/plans`, {
        query: { includeArchived },
        ...(signal ? { signal } : {}),
      })
      .then((items) => items.map(mapPlan)),
  publishPlan: (input: PlanInput, idempotencyKey: string, id?: string) =>
    apiClient
      .request<ServicePlanWire>(id ? `${base}/plans/${encodeURIComponent(id)}` : `${base}/plans`, {
        method: id ? 'PUT' : 'POST',
        body: input,
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapPlan),
  setPlanStatus: (id: string, status: PlanStatus, idempotencyKey: string) =>
    apiClient
      .request<ServicePlanWire>(`${base}/plans/${encodeURIComponent(id)}/status`, {
        method: 'PUT',
        body: { status },
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapPlan),
  subscriptions: (query: ListQuery, signal?: AbortSignal) =>
    apiClient
      .request<Page<SubscriptionWire>>(`${base}/subscriptions`, {
        query: listQuery(query),
        ...(signal ? { signal } : {}),
      })
      .then((page) => ({ ...page, items: page.items.map(mapSubscription) })),
  createSubscription: (input: SubscriptionInput, idempotencyKey: string) =>
    apiClient
      .request<SubscriptionWire>(`${base}/subscriptions`, {
        method: 'POST',
        body: input,
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapSubscription),
  updateSubscription: (id: string, input: SubscriptionUpdateInput, idempotencyKey: string) =>
    apiClient
      .request<SubscriptionWire>(`${base}/subscriptions/${encodeURIComponent(id)}`, {
        method: 'PUT',
        body: input,
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapSubscription),
  setSubscriptionStatus: (
    id: string,
    status: SubscriptionStatus,
    expectedUpdatedAt: string,
    idempotencyKey: string,
  ) =>
    apiClient
      .request<SubscriptionWire>(`${base}/subscriptions/${encodeURIComponent(id)}/status`, {
        method: 'PUT',
        body: { status, expectedUpdatedAt },
        headers: mutationHeaders(idempotencyKey),
      })
      .then(mapSubscription),
}

interface PlanRouteWire {
  model_id: string
  model_name?: string
  resource_pool_id: string
  resource_pool_name?: string
  resource_pool_slug?: string
  provider_name?: string
}

interface PlanVersionWire {
  id: string
  version: number
  token_quota: number
  validity_days: number
  concurrency_limit: number
  rpm_limit?: number
  tpm_limit?: number
  routes: PlanRouteWire[]
  created_at: string
}

interface ServicePlanWire {
  id: string
  slug: string
  name: string
  description: string
  kind: ServicePlan['kind']
  status: ServicePlan['status']
  current_version?: PlanVersionWire
  active_subscription_count: number
  created_at: string
  updated_at: string
}

interface SubscriptionWire {
  id: string
  user_id: string
  member_email: string
  member_name: string
  service_plan_id: string
  service_plan_version_id: string
  service_plan_name: string
  plan_kind: Subscription['planKind']
  plan_version: number
  status: Subscription['status']
  granted_tokens: number
  balance_tokens: number
  starts_at: string
  expires_at: string
  notes: string
  concurrency_limit: number
  rpm_limit?: number
  tpm_limit?: number
  routes: PlanRouteWire[]
  suspended_at?: string
  canceled_at?: string
  created_at: string
  updated_at: string
}

function mapPlan(plan: ServicePlanWire): ServicePlan {
  return {
    id: plan.id,
    slug: plan.slug,
    name: plan.name,
    description: plan.description,
    kind: plan.kind,
    status: plan.status,
    ...(plan.current_version
      ? {
          currentVersion: {
            id: plan.current_version.id,
            version: plan.current_version.version,
            tokenQuota: plan.current_version.token_quota,
            validityDays: plan.current_version.validity_days,
            concurrencyLimit: plan.current_version.concurrency_limit,
            ...(plan.current_version.rpm_limit !== undefined
              ? { rpmLimit: plan.current_version.rpm_limit }
              : {}),
            ...(plan.current_version.tpm_limit !== undefined
              ? { tpmLimit: plan.current_version.tpm_limit }
              : {}),
            routes: plan.current_version.routes.map((route) => ({
              modelId: route.model_id,
              modelName: route.model_name ?? route.model_id,
              resourcePoolId: route.resource_pool_id,
              resourcePoolName: route.resource_pool_name ?? route.resource_pool_id,
              resourcePoolSlug: route.resource_pool_slug ?? '',
              providerName: route.provider_name ?? '',
            })),
            createdAt: plan.current_version.created_at,
          },
        }
      : {}),
    activeSubscriptionCount: plan.active_subscription_count,
    createdAt: plan.created_at,
    updatedAt: plan.updated_at,
  }
}

function mapSubscription(value: SubscriptionWire): Subscription {
  return {
    id: value.id,
    userId: value.user_id,
    memberEmail: value.member_email,
    memberName: value.member_name,
    servicePlanId: value.service_plan_id,
    servicePlanVersionId: value.service_plan_version_id,
    servicePlanName: value.service_plan_name,
    planKind: value.plan_kind,
    planVersion: value.plan_version,
    status: value.status,
    grantedTokens: value.granted_tokens,
    balanceTokens: value.balance_tokens,
    startsAt: value.starts_at,
    expiresAt: value.expires_at,
    notes: value.notes,
    concurrencyLimit: value.concurrency_limit,
    ...(value.rpm_limit !== undefined ? { rpmLimit: value.rpm_limit } : {}),
    ...(value.tpm_limit !== undefined ? { tpmLimit: value.tpm_limit } : {}),
    routes: value.routes.map((route) => ({
      modelId: route.model_id,
      modelName: route.model_name ?? route.model_id,
      resourcePoolId: route.resource_pool_id,
      resourcePoolName: route.resource_pool_name ?? route.resource_pool_id,
      resourcePoolSlug: route.resource_pool_slug ?? '',
      providerName: route.provider_name ?? '',
    })),
    ...(value.suspended_at ? { suspendedAt: value.suspended_at } : {}),
    ...(value.canceled_at ? { canceledAt: value.canceled_at } : {}),
    createdAt: value.created_at,
    updatedAt: value.updated_at,
  }
}
