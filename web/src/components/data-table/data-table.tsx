import { flexRender, getCoreRowModel, useReactTable, type ColumnDef } from '@tanstack/react-table'
import { ChevronLeft, ChevronRight, LoaderCircle } from 'lucide-react'

import { cn } from '@/lib/cn'

import { Button } from '../ui/button'
import { EmptyState, ErrorState } from '../ui/state'

export type { ColumnDef }

interface DataTableProps<T> {
  data: T[]
  columns: ColumnDef<T, unknown>[]
  getRowId: (row: T) => string
  loading?: boolean
  fetching?: boolean
  error?: unknown
  onRetry?: (() => void) | undefined
  emptyLabel: string
  page: number
  pageSize: number
  total: number
  onPageChange: (page: number) => void
  onRowClick?: ((row: T) => void) | undefined
  ariaLabel: string
}

export function DataTable<T>({
  data,
  columns,
  getRowId,
  loading,
  fetching,
  error,
  onRetry,
  emptyLabel,
  page,
  pageSize,
  total,
  onPageChange,
  onRowClick,
  ariaLabel,
}: DataTableProps<T>) {
  // TanStack Table intentionally owns a mutable table instance.
  // eslint-disable-next-line react-hooks/incompatible-library
  const table = useReactTable({
    data,
    columns,
    getRowId,
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
  })
  const pageCount = Math.max(1, Math.ceil(total / pageSize))

  if (error && data.length === 0) {
    return <ErrorState error={error} {...(onRetry ? { onRetry } : {})} />
  }

  return (
    <div className="data-table-stack">
      <div className={cn('data-table-frame', fetching && !loading && 'data-table-frame--fetching')}>
        {fetching && !loading ? (
          <div className="data-table-refresh" role="status" aria-live="polite">
            <LoaderCircle className="spin" size={15} />
            正在刷新
          </div>
        ) : null}
        <div className="data-table-desktop">
          <table aria-label={ariaLabel}>
            <thead>
              {table.getHeaderGroups().map((headerGroup) => (
                <tr key={headerGroup.id}>
                  {headerGroup.headers.map((header) => (
                    <th
                      key={header.id}
                      scope="col"
                      className={alignmentClass(header.column.columnDef.meta)}
                    >
                      {header.isPlaceholder
                        ? null
                        : flexRender(header.column.columnDef.header, header.getContext())}
                    </th>
                  ))}
                </tr>
              ))}
            </thead>
            <tbody>
              {loading ? (
                <TableSkeleton columnCount={columns.length} />
              ) : table.getRowModel().rows.length > 0 ? (
                table.getRowModel().rows.map((row) => (
                  <tr
                    key={row.id}
                    className={onRowClick ? 'data-table-row--interactive' : undefined}
                    tabIndex={onRowClick ? 0 : undefined}
                    onClick={() => onRowClick?.(row.original)}
                    onKeyDown={(event) => {
                      if (onRowClick && (event.key === 'Enter' || event.key === ' ')) {
                        event.preventDefault()
                        onRowClick(row.original)
                      }
                    }}
                  >
                    {row.getVisibleCells().map((cell) => (
                      <td key={cell.id} className={alignmentClass(cell.column.columnDef.meta)}>
                        {flexRender(cell.column.columnDef.cell, cell.getContext())}
                      </td>
                    ))}
                  </tr>
                ))
              ) : (
                <tr>
                  <td colSpan={columns.length}>
                    <EmptyState title={emptyLabel} />
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="pagination" aria-label="分页">
        <span>
          第 {Math.min(page, pageCount)} / {pageCount} 页，共 {total} 条
        </span>
        <div className="pagination__actions">
          <Button
            type="button"
            variant="secondary"
            size="sm"
            aria-label="上一页"
            disabled={page <= 1 || loading}
            onClick={() => onPageChange(page - 1)}
          >
            <ChevronLeft size={16} />
          </Button>
          <Button
            type="button"
            variant="secondary"
            size="sm"
            aria-label="下一页"
            disabled={page >= pageCount || loading}
            onClick={() => onPageChange(page + 1)}
          >
            <ChevronRight size={16} />
          </Button>
        </div>
      </div>
    </div>
  )
}

function alignmentClass(meta: unknown): string | undefined {
  if (!meta || typeof meta !== 'object' || !('align' in meta)) return undefined
  const align = (meta as { align?: unknown }).align
  return align === 'right' || align === 'center' ? `table-align-${align}` : undefined
}

function TableSkeleton({ columnCount }: { columnCount: number }) {
  return Array.from({ length: 6 }, (_, row) => (
    <tr key={row} aria-hidden="true">
      {Array.from({ length: columnCount }, (_, column) => (
        <td key={column}>
          <span className="skeleton-line" />
        </td>
      ))}
    </tr>
  ))
}
