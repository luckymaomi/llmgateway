import { keepPreviousData, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCheck, RotateCcw, Send } from 'lucide-react'
import { useMemo, useState } from 'react'

import { catalogApi, type ConfigurationRevision, type OperationSnapshot } from '@/api'
import { hasCapability, useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { OperationPanel } from '@/components/operations/operation-panel'
import { StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { DialogFrame } from '@/components/ui/dialog'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime } from '@/lib/format'

import { CatalogTabs } from './catalog-tabs'

export function RevisionsPage() {
  const session = useSession()
  const canPublish = hasCapability(session, 'revisions:publish')
  const { state, setPage } = useListSearch()
  const queryClient = useQueryClient()
  const [operation, setOperation] = useState<OperationSnapshot | null>(null)
  const query = useQuery({
    queryKey: ['configuration-revisions', state.page, state.pageSize],
    queryFn: ({ signal }) =>
      catalogApi.revisions({ page: state.page, pageSize: state.pageSize }, signal),
    placeholderData: keepPreviousData,
  })
  const active = query.data?.items.find((revision) => revision.status === 'published')
  const action = useMutation({
    mutationFn: ({
      revision,
      kind,
    }: {
      revision: ConfigurationRevision
      kind: 'validate' | 'publish' | 'rollback'
    }) => {
      if (kind === 'validate') return catalogApi.validateRevision(revision.id)
      const activeRevisionId = active?.id ?? ''
      return kind === 'publish'
        ? catalogApi.publishRevision(revision.id, activeRevisionId)
        : catalogApi.rollbackRevision(revision.id, activeRevisionId)
    },
    onSuccess(snapshot) {
      setOperation(snapshot)
      void queryClient.invalidateQueries({ queryKey: ['configuration-revisions'] })
    },
  })
  const columns = useMemo<ColumnDef<ConfigurationRevision, unknown>[]>(
    () => [
      {
        accessorKey: 'sequence',
        header: '版本',
        cell: ({ row }) => <strong>#{row.original.sequence}</strong>,
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
                disabled={action.isPending}
                onClick={() => action.mutate({ revision: row.original, kind: 'validate' })}
              >
                校验
              </Button>
              {row.original.status === 'draft' ? (
                <Button
                  size="sm"
                  variant="quiet"
                  icon={<Send size={15} />}
                  disabled={action.isPending}
                  onClick={() => action.mutate({ revision: row.original, kind: 'publish' })}
                >
                  发布
                </Button>
              ) : null}
              {row.original.status === 'superseded' ? (
                <Button
                  size="sm"
                  variant="quiet"
                  icon={<RotateCcw size={15} />}
                  disabled={action.isPending}
                  onClick={() => action.mutate({ revision: row.original, kind: 'rollback' })}
                >
                  回滚
                </Button>
              ) : null}
            </div>
          ) : null,
      },
    ],
    [action, canPublish],
  )

  return (
    <Page>
      <PageHeader title="Provider 与模型" description="上游端点、模型能力与可发布配置" />
      <CatalogTabs />
      <div className="revision-bar" aria-label="当前生效配置">
        <div>
          <span>当前生效</span>
          <strong>{active ? `#${active.sequence}` : '尚未发布'}</strong>
        </div>
        <div>
          <span>Revision ID</span>
          <code>{active?.id ?? 'none'}</code>
        </div>
        <div>
          <span>发布时间</span>
          <strong>{formatDateTime(active?.publishedAt)}</strong>
        </div>
      </div>
      <PageSection>
        <DataTable
          ariaLabel="配置版本列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(revision) => revision.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error ?? action.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有配置版本"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          renderMobile={(revision) => (
            <div className="mobile-summary">
              <div>
                <strong>
                  #{revision.sequence} · {revision.summary}
                </strong>
                <StatusBadge status={revision.status} />
              </div>
              <span>
                {formatDateTime(revision.createdAt)} · {revision.createdBy}
              </span>
            </div>
          )}
        />
      </PageSection>
      <DialogFrame
        open={operation !== null}
        onOpenChange={(open) => {
          if (!open) setOperation(null)
        }}
        title="配置操作"
        width="lg"
      >
        {operation ? <OperationPanel initial={operation} /> : null}
      </DialogFrame>
    </Page>
  )
}
