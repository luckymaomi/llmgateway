import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { ledgerApi, type UsageRecord } from '@/api'
import { useSession } from '@/app/session'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatNumber } from '@/lib/format'

import { LedgerTabs } from './ledger-tabs'

export function UsagePage() {
  const session = useSession()
  const { state, setPage, setSearch } = useListSearch()
  const query = useQuery({
    queryKey: ['usage', state],
    queryFn: ({ signal }) => ledgerApi.usage(state, signal),
    placeholderData: keepPreviousData,
  })
  const columns = useMemo<ColumnDef<UsageRecord, unknown>[]>(
    () => [
      {
        accessorKey: 'occurredAt',
        header: '时间',
        cell: ({ row }) => formatDateTime(row.original.occurredAt),
      },
      ...(session.role === 'member'
        ? []
        : [{ accessorKey: 'userName', header: '用户' } as ColumnDef<UsageRecord, unknown>]),
      {
        accessorKey: 'keyPrefix',
        header: 'Key',
        cell: ({ row }) => <code>{row.original.keyPrefix}…</code>,
      },
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
        accessorKey: 'inputTokens',
        header: '输入 Token',
        cell: ({ row }) => formatNumber(row.original.inputTokens),
      },
      {
        accessorKey: 'outputTokens',
        header: '输出 Token',
        cell: ({ row }) => formatNumber(row.original.outputTokens),
      },
      {
        accessorKey: 'usageSource',
        header: '来源',
        cell: ({ row }) => (
          <Badge tone={row.original.usageSource === 'authoritative' ? 'positive' : 'warning'}>
            {row.original.usageSource === 'authoritative' ? '上游权威' : '本地估算'}
          </Badge>
        ),
      },
      {
        accessorKey: 'requestId',
        header: 'Request ID',
        cell: ({ row }) => <code>{row.original.requestId}</code>,
      },
    ],
    [session.role],
  )
  return (
    <Page>
      <PageHeader
        title={session.role === 'member' ? '我的用量' : '用量与账本'}
        description="权威 usage、估算与额度事件"
      />
      <LedgerTabs />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索用户、模型或 Request ID"
        />
        <DataTable
          ariaLabel="请求用量列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(record) => record.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的用量记录"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          renderMobile={(record) => (
            <div className="mobile-summary">
              <div>
                <strong>{record.modelAlias}</strong>
                <Badge tone={record.usageSource === 'authoritative' ? 'positive' : 'warning'}>
                  {record.usageSource === 'authoritative' ? '权威' : '估算'}
                </Badge>
              </div>
              <span>
                {formatDateTime(record.occurredAt)} · {record.keyPrefix}…
              </span>
              <span>
                输入 {formatNumber(record.inputTokens)} · 输出 {formatNumber(record.outputTokens)}
              </span>
            </div>
          )}
        />
      </PageSection>
    </Page>
  )
}
