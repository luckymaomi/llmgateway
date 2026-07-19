import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { operationsApi, type AuditEvent } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime } from '@/lib/format'

import { OperationsTabs } from './operations-tabs'

export function AuditPage() {
  const { state, setPage, setSearch } = useListSearch()
  const query = useQuery({
    queryKey: ['audit-events', state],
    queryFn: ({ signal }) => operationsApi.auditEvents(state, signal),
    placeholderData: keepPreviousData,
  })
  const columns = useMemo<ColumnDef<AuditEvent, unknown>[]>(
    () => [
      {
        accessorKey: 'occurredAt',
        header: '时间',
        cell: ({ row }) => formatDateTime(row.original.occurredAt),
      },
      { accessorKey: 'actorName', header: '操作者' },
      {
        accessorKey: 'action',
        header: '动作',
        cell: ({ row }) => <strong>{row.original.action}</strong>,
      },
      { accessorKey: 'objectType', header: '对象类型' },
      { accessorKey: 'objectLabel', header: '对象' },
      { accessorKey: 'summary', header: '变更摘要' },
      {
        accessorKey: 'requestId',
        header: 'Request ID',
        cell: ({ row }) => <code>{row.original.requestId}</code>,
      },
    ],
    [],
  )
  return (
    <Page>
      <PageHeader title="请求与审计" description="请求、路由 attempt、错误与管理操作" />
      <OperationsTabs />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索操作者、对象或 Request ID"
        />
        <DataTable
          ariaLabel="管理审计列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(event) => event.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的审计事件"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          renderMobile={(event) => (
            <div className="mobile-summary">
              <div>
                <strong>{event.action}</strong>
                <code>{event.requestId}</code>
              </div>
              <span>
                {event.actorName} · {event.objectType} / {event.objectLabel}
              </span>
              <span>{formatDateTime(event.occurredAt)}</span>
            </div>
          )}
        />
      </PageSection>
    </Page>
  )
}
