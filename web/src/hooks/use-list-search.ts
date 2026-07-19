import { useRouter, useSearch } from '@tanstack/react-router'

export interface ListSearchState {
  page: number
  pageSize: number
  search: string
  status: string
}

function numberValue(value: unknown, fallback: number): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? value : fallback
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value : ''
}

export function useListSearch(): {
  state: ListSearchState
  setPage: (page: number) => void
  setSearch: (search: string) => void
  setStatus: (status: string) => void
} {
  const raw = useSearch({ strict: false })
  const router = useRouter()
  const state: ListSearchState = {
    page: numberValue(raw.page, 1),
    pageSize: numberValue(raw.pageSize, 20),
    search: stringValue(raw.search),
    status: stringValue(raw.status),
  }
  const update = (patch: Partial<ListSearchState>) => {
    const next = {
      ...raw,
      ...patch,
      page:
        patch.search !== undefined || patch.status !== undefined ? 1 : (patch.page ?? state.page),
    }
    const search = new URLSearchParams()
    for (const [key, value] of Object.entries(next)) {
      if (value !== undefined && value !== '') search.set(key, String(value))
    }
    const query = search.toString()
    router.history.replace(`${router.state.location.pathname}${query ? `?${query}` : ''}`)
  }
  return {
    state,
    setPage: (page) => update({ page }),
    setSearch: (search) => update({ search }),
    setStatus: (status) => update({ status }),
  }
}
