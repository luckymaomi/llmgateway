import { CheckCheck, RotateCcw, Send } from 'lucide-react'

import type { ConfigurationRevision } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { formatDateTime } from '@/lib/format'

export type RevisionAction = 'validate' | 'publish' | 'rollback'

interface RevisionTableProps {
  revisions: ConfigurationRevision[]
  loading: boolean
  fetching: boolean
  error: unknown
  page: number
  pageSize: number
  total: number
  canPublish: boolean
  actionUnavailable: boolean
  activeUnavailable: boolean
  onRetry: () => void
  onPageChange: (page: number) => void
  onAction: (revision: ConfigurationRevision, action: RevisionAction) => void
}

export function RevisionTable({
  revisions,
  loading,
  fetching,
  error,
  page,
  pageSize,
  total,
  canPublish,
  actionUnavailable,
  activeUnavailable,
  onRetry,
  onPageChange,
  onAction,
}: RevisionTableProps) {
  const columns: ColumnDef<ConfigurationRevision, unknown>[] = [
    {
      accessorKey: 'sequence',
      header: '版本',
      cell: ({ row }) => <strong>{row.original.sequence}</strong>,
    },
    { accessorKey: 'summary', header: '摘要' },
    {
      accessorKey: 'status',
      header: '状态',
      cell: ({ row }) => <StatusBadge status={row.original.status} />,
    },
    { accessorKey: 'validationIssueCount', header: '校验问题' },
    { accessorKey: 'createdBy', header: '创建人' },
    {
      accessorKey: 'createdAt',
      header: '创建时间',
      cell: ({ row }) => formatDateTime(row.original.createdAt),
    },
    {
      id: 'actions',
      header: '操作',
      cell: ({ row }) =>
        canPublish ? (
          <div className="row-actions">
            <Button
              size="sm"
              variant="quiet"
              icon={<CheckCheck size={15} />}
              disabled={actionUnavailable}
              onClick={() => onAction(row.original, 'validate')}
            >
              校验
            </Button>
            {row.original.status === 'draft' ? (
              <Button
                size="sm"
                variant="quiet"
                icon={<Send size={15} />}
                disabled={actionUnavailable || activeUnavailable}
                onClick={() => onAction(row.original, 'publish')}
              >
                发布
              </Button>
            ) : null}
            {row.original.status === 'superseded' ? (
              <Button
                size="sm"
                variant="quiet"
                icon={<RotateCcw size={15} />}
                disabled={actionUnavailable || activeUnavailable}
                onClick={() => onAction(row.original, 'rollback')}
              >
                回滚
              </Button>
            ) : null}
          </div>
        ) : null,
    },
  ]

  return (
    <DataTable
      ariaLabel="配置版本列表"
      data={revisions}
      columns={columns}
      getRowId={(revision) => revision.id}
      loading={loading}
      fetching={fetching}
      error={error}
      onRetry={onRetry}
      emptyLabel="没有配置版本"
      page={page}
      pageSize={pageSize}
      total={total}
      onPageChange={onPageChange}
      renderMobile={(revision) => (
        <div className="mobile-summary">
          <div>
            <strong>{revision.summary}</strong>
            <StatusBadge status={revision.status} />
          </div>
          <span>
            版本 {revision.sequence} · {formatDateTime(revision.createdAt)} · {revision.createdBy}
          </span>
          {revision.validationIssueCount > 0 ? (
            <span className="mobile-summary__warning">
              有 {revision.validationIssueCount} 个校验问题
            </span>
          ) : null}
        </div>
      )}
    />
  )
}
