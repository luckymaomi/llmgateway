import { apiClient } from './client'
import type { OperationSnapshot, SettingsDocument } from './types'

const base = '/api/control/settings'

export const settingsApi = {
  get: (section: SettingsDocument['section'], signal?: AbortSignal) =>
    apiClient.request<SettingsDocument>(`${base}/${section}`, {
      ...(signal ? { signal } : {}),
    }),
  update: (
    section: SettingsDocument['section'],
    revisionId: string,
    values: SettingsDocument['values'],
  ) =>
    apiClient.request<SettingsDocument, { revisionId: string; values: SettingsDocument['values'] }>(
      `${base}/${section}`,
      { method: 'PUT', body: { revisionId, values } },
    ),
  runBackup: () => apiClient.request<OperationSnapshot>(`${base}/backups/runs`, { method: 'POST' }),
}
