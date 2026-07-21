import { z } from 'zod'

import { ApiProblem, type EntitlementInput } from '@/api'
import {
  clearPendingEntitlementOperation,
  loadPendingEntitlementOperation,
} from '@/app/pending-operations'

const optionalInteger = z
  .number()
  .int()
  .positive()
  .optional()
  .or(z.nan().transform(() => undefined))

export const entitlementSchema = z
  .object({
    ownerId: z.string().uuid('请选择用户'),
    planKind: z.enum(['token', 'coding']),
    resourceDomain: z.enum(['free', 'professional']),
    modelId: z.string().uuid().or(z.literal('')),
    grantedTokens: z.number().int().positive('请输入大于 0 的 Token 额度'),
    rpmLimit: optionalInteger,
    tpmLimit: optionalInteger,
    concurrencyLimit: z.number().int().positive('请输入大于 0 的并发上限'),
    startsAt: z.string().min(1, '请选择开始时间'),
    expiresAt: z.string().min(1, '请选择到期时间'),
    reason: z.string().trim().min(1, '请输入分配原因').max(500, '分配原因不能超过 500 个字符'),
  })
  .refine((value) => new Date(value.expiresAt) > new Date(value.startsAt), {
    path: ['expiresAt'],
    message: '到期时间必须晚于开始时间',
  })

export type EntitlementValues = z.infer<typeof entitlementSchema>
export type EntitlementSubmission = { input: EntitlementInput; idempotencyKey: string }

const pendingSubmissionSchema = z.object({
  input: z.object({
    ownerId: z.string().uuid(),
    planKind: z.enum(['token', 'coding']),
    resourceDomain: z.enum(['free', 'professional']),
    modelId: z.string().uuid().optional(),
    grantedTokens: z.number().int().positive(),
    rpmLimit: z.number().int().positive().optional(),
    tpmLimit: z.number().int().positive().optional(),
    concurrencyLimit: z.number().int().positive(),
    startsAt: z.string(),
    expiresAt: z.string(),
    reason: z.string().min(1),
  }),
  idempotencyKey: z.string().uuid(),
})

export function defaultEntitlementValues(): EntitlementValues {
  const now = new Date()
  return {
    ownerId: '',
    planKind: 'token',
    resourceDomain: 'free',
    modelId: '',
    grantedTokens: 100_000,
    concurrencyLimit: 1,
    startsAt: localDateTime(now.toISOString()),
    expiresAt: localDateTime(new Date(now.getTime() + 30 * 24 * 60 * 60 * 1_000).toISOString()),
    reason: '',
  }
}

export function readPendingEntitlementSubmission(
  userId: string,
): EntitlementSubmission | undefined {
  const parsed = pendingSubmissionSchema.safeParse(loadPendingEntitlementOperation(userId))
  if (!parsed.success) {
    clearPendingEntitlementOperation(userId)
    return undefined
  }
  const restored = parsed.data
  return {
    input: {
      ownerId: restored.input.ownerId,
      planKind: restored.input.planKind,
      resourceDomain: restored.input.resourceDomain,
      grantedTokens: restored.input.grantedTokens,
      concurrencyLimit: restored.input.concurrencyLimit,
      startsAt: restored.input.startsAt,
      expiresAt: restored.input.expiresAt,
      reason: restored.input.reason,
      ...(restored.input.modelId ? { modelId: restored.input.modelId } : {}),
      ...(restored.input.rpmLimit !== undefined ? { rpmLimit: restored.input.rpmLimit } : {}),
      ...(restored.input.tpmLimit !== undefined ? { tpmLimit: restored.input.tpmLimit } : {}),
    },
    idempotencyKey: restored.idempotencyKey,
  }
}

export function entitlementValuesFromPending(pending?: EntitlementSubmission): EntitlementValues {
  if (!pending) return defaultEntitlementValues()
  return {
    ownerId: pending.input.ownerId,
    planKind: pending.input.planKind,
    resourceDomain: pending.input.resourceDomain,
    modelId: pending.input.modelId ?? '',
    grantedTokens: pending.input.grantedTokens,
    concurrencyLimit: pending.input.concurrencyLimit,
    startsAt: localDateTime(pending.input.startsAt),
    expiresAt: localDateTime(pending.input.expiresAt),
    reason: pending.input.reason,
    ...(pending.input.rpmLimit !== undefined ? { rpmLimit: pending.input.rpmLimit } : {}),
    ...(pending.input.tpmLimit !== undefined ? { tpmLimit: pending.input.tpmLimit } : {}),
  }
}

export function isEntitlementOutcomeUnknown(error: unknown): boolean {
  return (
    error instanceof ApiProblem &&
    (error.code === 'operation_outcome_unknown' || error.code === 'network_unavailable')
  )
}

function localDateTime(value: string): string {
  const date = new Date(value)
  return new Date(date.getTime() - date.getTimezoneOffset() * 60_000).toISOString().slice(0, 16)
}
