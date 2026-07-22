import { apiClient } from './client'
import type { Role, Session } from './types'

const base = '/api/control'

export interface SetupStatus {
  required: boolean
}

export interface BootstrapInput {
  email: string
}

export interface BootstrapResult extends Session {
  initialPassword: string
}

export interface LoginInput {
  email: string
  password: string
}

export interface RegistrationInput {
  invitation: string
  displayName: string
  email: string
  password: string
}

export interface RegistrationResult {
  userId: string
  role: Role
  status: 'pending_review' | 'active'
}

function adoptSession<T extends Session>(session: T): T {
  apiClient.setCsrfToken(session.csrfToken)
  return session
}

export const authApi = {
  setupStatus: () => apiClient.request<SetupStatus>(`${base}/setup/status`),
  bootstrap: (input: BootstrapInput) =>
    apiClient
      .request<BootstrapResult, BootstrapInput>(`${base}/setup`, { method: 'POST', body: input })
      .then(adoptSession),
  login: (input: LoginInput) =>
    apiClient
      .request<Session, LoginInput>(`${base}/session`, { method: 'POST', body: input })
      .then(adoptSession),
  logout: () => apiClient.request<void>(`${base}/session`, { method: 'DELETE' }),
  session: () => apiClient.request<Session>(`${base}/session`).then(adoptSession),
  changePassword: (currentPassword: string, replacementPassword: string) =>
    apiClient.request<
      { revokedSessions: number },
      { currentPassword: string; replacementPassword: string }
    >(`${base}/password`, { method: 'POST', body: { currentPassword, replacementPassword } }),
  register: (input: RegistrationInput) =>
    apiClient.request<RegistrationResult, RegistrationInput>(`${base}/registrations`, {
      method: 'POST',
      body: input,
    }),
}
