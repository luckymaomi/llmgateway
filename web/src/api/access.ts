import { apiClient, listQuery } from './client'
import type {
  CreatedGatewayKey,
  GatewayKey,
  Invitation,
  ListQuery,
  Page,
  SessionRevocation,
  UserAccount,
} from './types'

const base = '/api/control'
const item = (path: string, id: string) => `${base}/${path}/${encodeURIComponent(id)}`

export interface InvitationInput {
  expiresAt: string
}

export interface CreatedInvitation {
  invitation: Invitation
  code: string
}

export interface GatewayKeyInput {
  ownerId: string
  name: string
  authorizedModelIds: string[]
  expiresAt?: string
}

export const accessApi = {
  users: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<UserAccount>>(`${base}/users`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  reviewUser: (id: string, decision: 'approve' | 'suspend' | 'activate') =>
    apiClient.request<UserAccount, { decision: string }>(`${item('users', id)}/review`, {
      method: 'POST',
      body: { decision },
    }),
  resetMemberPassword: (id: string, newPassword: string, idempotencyKey: string) =>
    apiClient.request<SessionRevocation, { newPassword: string }>(`${item('users', id)}/password`, {
      method: 'POST',
      body: { newPassword },
      headers: { 'Idempotency-Key': idempotencyKey },
    }),
  revokeUserSessions: (id: string) =>
    apiClient.request<SessionRevocation>(`${item('users', id)}/sessions/revoke`, {
      method: 'POST',
    }),

  invitations: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<Invitation>>(`${base}/invitations`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createInvitation: (input: InvitationInput, idempotencyKey: string) =>
    apiClient.request<CreatedInvitation, InvitationInput>(`${base}/invitations`, {
      method: 'POST',
      body: input,
      headers: { 'Idempotency-Key': idempotencyKey },
    }),
  revokeInvitation: (id: string) =>
    apiClient.request<Invitation>(`${item('invitations', id)}/revoke`, { method: 'POST' }),

  keys: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<GatewayKey>>(`${base}/keys`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createKey: (input: GatewayKeyInput, idempotencyKey: string) =>
    apiClient.request<CreatedGatewayKey, GatewayKeyInput>(`${base}/keys`, {
      method: 'POST',
      body: input,
      headers: { 'Idempotency-Key': idempotencyKey },
    }),
  revokeKey: (id: string) =>
    apiClient.request<GatewayKey>(`${item('keys', id)}/revoke`, { method: 'POST' }),
  replaceKey: (id: string, idempotencyKey: string) =>
    apiClient.request<CreatedGatewayKey>(`${item('keys', id)}/replacement`, {
      method: 'POST',
      headers: { 'Idempotency-Key': idempotencyKey },
    }),
}
