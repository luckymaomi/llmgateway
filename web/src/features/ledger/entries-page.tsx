import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useMemo, useState } from 'react'

import { ledgerApi, type LedgerEntry } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatSignedTokens } from '@/lib/format'

import { AdjustmentForm } from './adjustment-form'
import { LedgerTabs } from './ledger-tabs'

export function EntriesPage() {
  const { state, setPage, setSearch } = useListSearch()
  const [adjusting, setAdjusting] = useState(false)
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
      { accessorKey: 'kind', header: '事件', cell: ({ row }) => entryLabel[row.original.kind] },
      {
        accessorKey: 'tokenDelta',
        header: 'Token 变化',
        cell: ({ row }) => (
          <strong className={row.original.tokenDelta >= 0 ? 'number-positive' : 'number-negative'}>
            {formatSignedTokens(row.original.tokenDelta)}
          </strong>
        ),
      },
      {
        accessorKey: 'resourceDomain',
        header: '资源域',
        cell: ({ row }) => (
          <Badge tone={row.original.resourceDomain === 'free' ? 'positive' : 'info'}>
            {row.original.resourceDomain === 'free' ? '免费' : '专业'}
          </Badge>
        ),
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
      <PageHeader
        title="用量与账本"
        description="权威 usage、估算与额度事件"
        actions={
          <Button icon={<Plus size={16} />} onClick={() => setAdjusting(true)}>
            人工调整
          </Button>
        }
      />
      <LedgerTabs />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索用户、原因或 Request ID"
        />
        <DataTable
          ariaLabel="账本事件列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(entry) => entry.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的账本事件"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          renderMobile={(entry) => (
            <div className="mobile-summary">
              <div>
                <strong>{entry.ownerName}</strong>
                <span className={entry.tokenDelta >= 0 ? 'number-positive' : 'number-negative'}>
                  {formatSignedTokens(entry.tokenDelta)}
                </span>
              </div>
              <span>
                {entryLabel[entry.kind]} · {entry.reason}
              </span>
              <span>
                {formatDateTime(entry.occurredAt)} · {entry.actorName}
              </span>
            </div>
          )}
        />
      </PageSection>
      <AdjustmentForm open={adjusting} onOpenChange={setAdjusting} />
    </Page>
  )
}

const entryLabel = {
  grant: '发放',
  reserve: '预留',
  settle: '结算',
  release: '释放',
  adjust: '调整',
  expire: '到期',
  compensate: '补偿',
} as const
