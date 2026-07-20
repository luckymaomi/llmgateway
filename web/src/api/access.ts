import { apiClient, listQuery } from './client'
import type {
  CreatedGatewayKey,
  GatewayKey,
  Invitation,
  ListQuery,
  Page,
  Role,
  UserAccount,
} from './types'

const base = '/api/control'
const item = (path: string, id: string) => `${base}/${path}/${encodeURIComponent(id)}`

export interface InvitationInput {
  role: Role
  expiresAt: string
}

export interface CreatedInvitation extends Invitation {
  code: string
}

export interface GatewayKeyInput {
  ownerId: string
  name: string
  authorizedModels: string[]
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

  invitations: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<Invitation>>(`${base}/invitations`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createInvitation: (input: InvitationInput) =>
    apiClient.request<CreatedInvitation, InvitationInput>(`${base}/invitations`, {
      method: 'POST',
      body: input,
    }),
  revokeInvitation: (id: string) =>
    apiClient.request<Invitation>(`${item('invitations', id)}/revoke`, { method: 'POST' }),

  keys: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<GatewayKey>>(`${base}/keys`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createKey: (input: GatewayKeyInput) =>
    apiClient.request<CreatedGatewayKey, GatewayKeyInput>(`${base}/keys`, {
      method: 'POST',
      body: input,
    }),
  revokeKey: (id: string) =>
    apiClient.request<GatewayKey>(`${item('keys', id)}/revoke`, { method: 'POST' }),
}
