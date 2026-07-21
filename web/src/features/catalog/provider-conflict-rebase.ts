import type { ProviderRecord } from '@/api'

export interface ProviderDraft {
  slug: string
  name: string
  kind: ProviderRecord['kind']
  baseUrl: string
}

export type ProviderEditableField = 'name' | 'kind' | 'baseUrl'
export type ProviderConflictChoice = 'draft' | 'latest'

export interface ProviderConflictState {
  opened: ProviderRecord
  draft: ProviderDraft
  latest?: ProviderRecord
  choices: Partial<Record<ProviderEditableField, ProviderConflictChoice>>
}

export interface ProviderFieldDifference {
  field: ProviderEditableField
  label: string
  draftValue: string
  latestValue: string
  selectedSource: ProviderConflictChoice
  reason: 'draft-only' | 'latest-only' | 'overlap' | 'routing-locked'
  choice?: ProviderConflictChoice
}

export interface ProviderRebase {
  values: ProviderDraft
  differences: ProviderFieldDifference[]
  unresolvedFields: ProviderEditableField[]
}

const fieldDefinitions = [
  { field: 'name', label: '名称' },
  { field: 'kind', label: '类型' },
  { field: 'baseUrl', label: 'Base URL' },
] as const

export function buildProviderRebase(
  opened: ProviderRecord,
  draft: ProviderDraft,
  latest: ProviderRecord,
  choices: ProviderConflictState['choices'],
): ProviderRebase {
  const differences = fieldDefinitions.flatMap(({ field, label }) => {
    const draftValue = draft[field]
    const latestValue = latest[field]
    if (draftValue === latestValue) return []

    const draftChanged = draftValue !== opened[field]
    const latestChanged = latestValue !== opened[field]
    const routingLocked = latest.status === 'enabled' && field !== 'name'
    const choice = choices[field]
    let reason: ProviderFieldDifference['reason']
    let selectedSource: ProviderConflictChoice

    if (routingLocked) {
      reason = 'routing-locked'
      selectedSource = 'latest'
    } else if (!draftChanged) {
      reason = 'latest-only'
      selectedSource = 'latest'
    } else if (!latestChanged) {
      reason = 'draft-only'
      selectedSource = 'draft'
    } else {
      reason = 'overlap'
      selectedSource = choice ?? 'draft'
    }

    return [
      {
        field,
        label,
        draftValue,
        latestValue,
        selectedSource,
        reason,
        ...(choice ? { choice } : {}),
      },
    ]
  })

  const selectedValue = (field: ProviderEditableField): string => {
    const difference = differences.find((candidate) => candidate.field === field)
    if (!difference) return latest[field]
    return difference.selectedSource === 'draft' ? difference.draftValue : difference.latestValue
  }

  return {
    values: {
      slug: latest.slug,
      name: selectedValue('name'),
      kind: selectedValue('kind'),
      baseUrl: selectedValue('baseUrl'),
    },
    differences,
    unresolvedFields: differences
      .filter((difference) => difference.reason === 'overlap' && !difference.choice)
      .map((difference) => difference.field),
  }
}
