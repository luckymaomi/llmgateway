import { apiClient } from './client'
import type { Role, Session } from './types'

const base = '/api/control'

export interface SetupStatus {
  required: boolean
}

export interface BootstrapInput {
  displayName: string
  email: string
  password: string
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

function adoptSession(session: Session): Session {
  apiClient.setCsrfToken(session.csrfToken)
  return session
}

export const authApi = {
  setupStatus: () => apiClient.request<SetupStatus>(`${base}/setup/status`),
  bootstrap: (input: BootstrapInput) =>
    apiClient
      .request<Session, BootstrapInput>(`${base}/setup`, { method: 'POST', body: input })
      .then(adoptSession),
  login: (input: LoginInput) =>
    apiClient
      .request<Session, LoginInput>(`${base}/session`, { method: 'POST', body: input })
      .then(adoptSession),
  logout: () => apiClient.request<void>(`${base}/session`, { method: 'DELETE' }),
  session: () => apiClient.request<Session>(`${base}/session`).then(adoptSession),
  register: (input: RegistrationInput) =>
    apiClient.request<RegistrationResult, RegistrationInput>(`${base}/registrations`, {
      method: 'POST',
      body: input,
    }),
}
