import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { useMemo } from 'react'

import { ledgerApi, type LedgerEntry } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatSignedTokens } from '@/lib/format'

export function EntriesPage() {
  const { state, setPage, setSearch } = useListSearch()
  const query = useQuery({
    queryKey: ['ledger-entries', state],
    queryFn: ({ signal }) => ledgerApi.entries(state, signal),
    placeholderData: keepPreviousData,
  })
  const columns = useMemo<ColumnDef<LedgerEntry, unknown>[]>(
    () => [
      {
        accessorKey: 'occurredAt',
        header: '时间',
        cell: ({ row }) => formatDateTime(row.original.occurredAt),
      },
      { accessorKey: 'ownerName', header: '用户' },
      { accessorKey: 'servicePlanName', header: '套餐' },
      { accessorKey: 'kind', header: '事件', cell: ({ row }) => entryLabel[row.original.kind] },
      {
        accessorKey: 'tokenDelta',
        header: 'Token 变化',
        cell: ({ row }) => (
          <strong className={row.original.tokenDelta >= 0 ? 'number-positive' : 'number-negative'}>
            {formatSignedTokens(row.original.tokenDelta)}
          </strong>
        ),
        meta: { align: 'right' },
      },
      { accessorKey: 'reason', header: '原因' },
      { accessorKey: 'actorName', header: '操作者' },
      {
        accessorKey: 'requestId',
        header: 'Request ID',
        cell: ({ row }) => (row.original.requestId ? <code>{row.original.requestId}</code> : '—'),
      },
    ],
    [],
  )
  return (
    <Page>
      <PageHeader title="额度记录" />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索用户、原因或 Request ID"
        />
        <DataTable
          ariaLabel="额度变更记录"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(entry) => entry.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="暂无额度变更记录"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
        />
      </PageSection>
    </Page>
  )
}

const entryLabel = {
  grant: '发放',
  reservation: '预留',
  settlement: '结算',
  release: '释放',
  compensation: '补偿',
} as const
