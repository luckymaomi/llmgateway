import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useMemo, useState } from 'react'

import { operationsApi, type GatewayRequest } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatDuration, formatNumber } from '@/lib/format'

import { OperationsTabs } from './operations-tabs'
import { RequestDrawer } from './request-drawer'

export function RequestsPage() {
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [selected, setSelected] = useState<string | null>(null)
  const query = useQuery({
    queryKey: ['requests', state],
    queryFn: ({ signal }) => operationsApi.requests(state, signal),
    placeholderData: keepPreviousData,
    refetchInterval: 10_000,
  })
  const columns = useMemo<ColumnDef<GatewayRequest, unknown>[]>(
    () => [
      {
        accessorKey: 'createdAt',
        header: '时间',
        cell: ({ row }) => formatDateTime(row.original.createdAt),
      },
      {
        accessorKey: 'requestId',
        header: 'Request ID',
        cell: ({ row }) => <code>{row.original.requestId}</code>,
      },
      { accessorKey: 'userName', header: '用户' },
      { accessorKey: 'modelAlias', header: '模型' },
      {
        accessorKey: 'resourceDomain',
        header: '资源域',
        cell: ({ row }) => (
          <Badge tone={row.original.resourceDomain === 'free' ? 'positive' : 'info'}>
            {row.original.resourceDomain === 'free' ? '免费' : '专业'}
          </Badge>
        ),
      },
      {
        accessorKey: 'state',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.state} />,
      },
      {
        accessorKey: 'latencyMs',
        header: '耗时',
        cell: ({ row }) => formatDuration(row.original.latencyMs),
      },
      {
        id: 'tokens',
        header: 'Token',
        cell: ({ row }) =>
          row.original.inputTokens === undefined && row.original.outputTokens === undefined
            ? '未知'
            : formatNumber((row.original.inputTokens ?? 0) + (row.original.outputTokens ?? 0)),
      },
      {
        accessorKey: 'errorCode',
        header: '错误',
        cell: ({ row }) => (row.original.errorCode ? <code>{row.original.errorCode}</code> : '—'),
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
          searchLabel="搜索 Request ID、用户或模型"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'streaming', label: '流式返回' },
            { value: 'completed', label: '已完成' },
            { value: 'failed', label: '失败' },
            { value: 'uncertain', label: '待确认' },
          ]}
        />
        <DataTable
          ariaLabel="请求列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(request) => request.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的请求"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          onRowClick={(request) => setSelected(request.id)}
          renderMobile={(request) => (
            <div className="mobile-summary">
              <div>
                <code>{request.requestId}</code>
                <StatusBadge status={request.state} />
              </div>
              <span>
                {request.userName} · {request.modelAlias}
              </span>
              <span>
                {formatDateTime(request.createdAt)} · {formatDuration(request.latencyMs)}
              </span>
            </div>
          )}
        />
      </PageSection>
      <RequestDrawer
        requestId={selected}
        onOpenChange={(open) => {
          if (!open) setSelected(null)
        }}
      />
    </Page>
  )
}
