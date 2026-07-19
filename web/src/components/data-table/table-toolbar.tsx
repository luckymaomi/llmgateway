import { Search } from 'lucide-react'
import type { ReactNode } from 'react'

import { Input, NativeSelect } from '../ui/field'

interface TableToolbarProps {
  search: string
  onSearchChange: (value: string) => void
  searchLabel: string
  status?: string
  onStatusChange?: (value: string) => void
  statusOptions?: Array<{ value: string; label: string }>
  actions?: ReactNode
  filters?: ReactNode
}

export function TableToolbar({
  search,
  onSearchChange,
  searchLabel,
  status,
  onStatusChange,
  statusOptions,
  actions,
  filters,
}: TableToolbarProps) {
  return (
    <div className="table-toolbar">
      <div className="table-toolbar__filters">
        <label className="search-field">
          <Search size={16} aria-hidden="true" />
          <span className="sr-only">{searchLabel}</span>
          <Input
            type="search"
            value={search}
            placeholder={searchLabel}
            onChange={(event) => onSearchChange(event.target.value)}
          />
        </label>
        {statusOptions && onStatusChange ? (
          <NativeSelect
            aria-label="状态筛选"
            value={status ?? ''}
            onChange={(event) => onStatusChange(event.target.value)}
          >
            <option value="">全部状态</option>
            {statusOptions.map((option) => (
              <option key={option.value} value={option.value}>
                {option.label}
              </option>
            ))}
          </NativeSelect>
        ) : null}
        {filters}
      </div>
      {actions ? <div className="table-toolbar__actions">{actions}</div> : null}
    </div>
  )
}
