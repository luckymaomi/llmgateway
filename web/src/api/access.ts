import { apiClient, listQuery } from './client'
import type {
  CreatedGatewayKey,
  CreatedMember,
  GatewayKey,
  ListQuery,
  Page,
  SessionRevocation,
  UserAccount,
} from './types'

const base = '/api/control'
const item = (path: string, id: string) => `${base}/${path}/${encodeURIComponent(id)}`
const mutationHeaders = (idempotencyKey: string) => ({ 'Idempotency-Key': idempotencyKey })

export interface GatewayKeyInput {
  ownerId: string
  name: string
  authorizedModelIds: string[]
  expiresAt?: string
}

export interface MemberInput {
  email: string
  displayName: string
}

export const accessApi = {
  members: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<UserAccount>>(`${base}/members`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createMember: (input: MemberInput, idempotencyKey: string) =>
    apiClient.request<CreatedMember, MemberInput>(`${base}/members`, {
      method: 'POST',
      body: input,
      headers: mutationHeaders(idempotencyKey),
    }),
  updateMember: (
    id: string,
    input: MemberInput & { expectedUpdatedAt: string },
    idempotencyKey: string,
  ) =>
    apiClient.request<UserAccount>(item('members', id), {
      method: 'PUT',
      body: input,
      headers: mutationHeaders(idempotencyKey),
    }),
  setMemberStatus: (id: string, status: 'active' | 'disabled', idempotencyKey: string) =>
    apiClient.request<UserAccount>(`${item('members', id)}/status`, {
      method: 'PUT',
      body: { status },
      headers: mutationHeaders(idempotencyKey),
    }),
  deleteMember: (id: string, idempotencyKey: string) =>
    apiClient.request<UserAccount>(item('members', id), {
      method: 'DELETE',
      headers: mutationHeaders(idempotencyKey),
    }),
  resetMemberPassword: (id: string, newPassword: string, idempotencyKey: string) =>
    apiClient.request<SessionRevocation>(`${item('members', id)}/password`, {
      method: 'POST',
      body: { newPassword },
      headers: mutationHeaders(idempotencyKey),
    }),
  keys: (query: ListQuery, signal?: AbortSignal) =>
    apiClient.request<Page<GatewayKey>>(`${base}/keys`, {
      query: listQuery(query),
      ...(signal ? { signal } : {}),
    }),
  createKey: (input: GatewayKeyInput, idempotencyKey: string) =>
    apiClient.request<CreatedGatewayKey, GatewayKeyInput>(`${base}/keys`, {
      method: 'POST',
      body: input,
      headers: mutationHeaders(idempotencyKey),
    }),
  revokeKey: (id: string) =>
    apiClient.request<GatewayKey>(`${item('keys', id)}/revoke`, { method: 'POST' }),
  replaceKey: (id: string, idempotencyKey: string) =>
    apiClient.request<CreatedGatewayKey>(`${item('keys', id)}/replacement`, {
      method: 'POST',
      headers: mutationHeaders(idempotencyKey),
    }),
}
