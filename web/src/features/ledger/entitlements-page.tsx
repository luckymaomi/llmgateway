import { keepPreviousData, useQuery } from '@tanstack/react-query'
import { Plus } from 'lucide-react'
import { useMemo, useState } from 'react'

import { ledgerApi, type Entitlement } from '@/api'
import { DataTable, type ColumnDef } from '@/components/data-table/data-table'
import { TableToolbar } from '@/components/data-table/table-toolbar'
import { Page, PageHeader, PageSection } from '@/components/layout'
import { Badge, StatusBadge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useListSearch } from '@/hooks/use-list-search'
import { formatDateTime, formatNumber, formatTokens } from '@/lib/format'

import { EntitlementForm } from './entitlement-form'
import { LedgerTabs } from './ledger-tabs'

export function EntitlementsPage() {
  const { state, setPage, setSearch, setStatus } = useListSearch()
  const [creating, setCreating] = useState(false)
  const query = useQuery({
    queryKey: ['entitlements', state],
    queryFn: ({ signal }) => ledgerApi.entitlements(state, signal),
    placeholderData: keepPreviousData,
  })
  const columns = useMemo<ColumnDef<Entitlement, unknown>[]>(
    () => [
      {
        accessorKey: 'ownerName',
        header: '用户',
        cell: ({ row }) => <strong>{row.original.ownerName}</strong>,
      },
      {
        accessorKey: 'planKind',
        header: '类型',
        cell: ({ row }) => (row.original.planKind === 'token' ? 'Token Plan' : 'Coding Plan'),
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
      {
        accessorKey: 'modelAliases',
        header: '模型',
        cell: ({ row }) => `${row.original.modelAliases.length} 个`,
      },
      {
        accessorKey: 'usedTokens',
        header: '用量',
        cell: ({ row }) =>
          row.original.tokenLimit
            ? `${formatTokens(row.original.usedTokens)} / ${formatTokens(row.original.tokenLimit)}`
            : formatTokens(row.original.usedTokens),
      },
      { accessorKey: 'concurrencyLimit', header: '并发' },
      {
        accessorKey: 'rpmLimit',
        header: 'RPM',
        cell: ({ row }) => (row.original.rpmLimit ? formatNumber(row.original.rpmLimit) : '—'),
      },
      {
        accessorKey: 'expiresAt',
        header: '到期',
        cell: ({ row }) => formatDateTime(row.original.expiresAt),
      },
      {
        accessorKey: 'status',
        header: '状态',
        cell: ({ row }) => <StatusBadge status={row.original.status} />,
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
          <Button icon={<Plus size={16} />} onClick={() => setCreating(true)}>
            分配套餐
          </Button>
        }
      />
      <LedgerTabs />
      <PageSection>
        <TableToolbar
          search={state.search}
          onSearchChange={setSearch}
          searchLabel="搜索用户或模型"
          status={state.status}
          onStatusChange={setStatus}
          statusOptions={[
            { value: 'active', label: '生效中' },
            { value: 'scheduled', label: '待生效' },
            { value: 'expired', label: '已过期' },
          ]}
        />
        <DataTable
          ariaLabel="额度与套餐列表"
          data={query.data?.items ?? []}
          columns={columns}
          getRowId={(entitlement) => entitlement.id}
          loading={query.isLoading}
          fetching={query.isFetching}
          error={query.error}
          onRetry={() => void query.refetch()}
          emptyLabel="没有符合条件的额度或套餐"
          page={query.data?.page ?? state.page}
          pageSize={query.data?.pageSize ?? state.pageSize}
          total={query.data?.total ?? 0}
          onPageChange={setPage}
          renderMobile={(entitlement) => (
            <div className="mobile-summary">
              <div>
                <strong>{entitlement.ownerName}</strong>
                <StatusBadge status={entitlement.status} />
              </div>
              <span>
                {entitlement.planKind === 'token' ? 'Token Plan' : 'Coding Plan'} ·{' '}
                {entitlement.modelAliases.length} 个模型
              </span>
              <span>
                {formatTokens(entitlement.usedTokens)} · 到期{' '}
                {formatDateTime(entitlement.expiresAt)}
              </span>
            </div>
          )}
        />
      </PageSection>
      <EntitlementForm open={creating} onOpenChange={setCreating} />
    </Page>
  )
}
