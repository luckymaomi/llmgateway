import { ApiProblem } from '@/api'

export interface ProviderOperation<TVariables> {
  idempotencyKey: string
  variables: TVariables
}

export function createProviderOperation<TVariables>(
  variables: TVariables,
): ProviderOperation<TVariables> {
  return { idempotencyKey: crypto.randomUUID(), variables }
}

export function hasUnknownProviderOutcome(error: unknown): boolean {
  return (
    error instanceof ApiProblem &&
    (error.code === 'network_unavailable' || error.code === 'operation_outcome_unknown')
  )
}
